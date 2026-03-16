package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/database/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18085", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "DATABASE")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	dataRoot := filepath.Join(appRoot, "data")
	serviceSecret, err := hubsvc.LoadServiceSecret(filepath.Join(dataRoot, ".service_secret"))
	if err != nil {
		app.Errorf("load service secret failed: %v", err)
		os.Exit(1)
	}

	scopedDBService, err := app.NewScopedDatabaseService(dataRoot)
	if err != nil {
		app.Errorf("init scoped database service failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("database")
	instance := strings.TrimSpace(*instanceID)
	if instance == "" {
		instance = "database-" + app.NewRequestID()
	}
	if strings.TrimSpace(*hubRegisterURL) != "" {
		registerPayload := toolproto.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: toolproto.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools: toSupervisorTools(manifest),
		}
		raw, _ := json.Marshal(registerPayload)
		req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(*hubRegisterURL), bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			app.Errorf("register database service to hub failed: %v", err)
			os.Exit(1)
		}
		rawResp, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register database service to hub status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		app.Infof("register database service to hub status=%d", resp.StatusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("database-service shutdown: %s", strings.TrimSpace(reason))
			if server != nil {
				_ = server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
				_ = server.Shutdown(ctx)
				cancel()
			}
			time.Sleep(80 * time.Millisecond)
			os.Exit(0)
		})
	}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"ok": true, "timestamp_ms": time.Now().UnixMilli()})
	})
	mux.HandleFunc("/service/info", func(w http.ResponseWriter, _ *http.Request) {
		caps := make([]string, 0, len(manifest.Provides))
		for _, p := range manifest.Provides {
			if strings.TrimSpace(p.ToolID) != "" {
				caps = append(caps, p.ToolID)
			}
		}
		writeJSON(w, app.AIServiceInfo{ServiceID: manifest.ServiceID, ServiceName: manifest.ServiceName, Version: manifest.Version, Provider: "database", Capabilities: caps, Transport: "http"})
	})
	mux.HandleFunc("/service/tools", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, app.AIServiceListToolsResponse{ServiceID: manifest.ServiceID, Tools: manifestTools(manifest)})
	})
	mux.HandleFunc("/service/tool/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeToolResponse(w, http.StatusMethodNotAllowed, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "method not allowed",
				},
			})
			return
		}
		var req toolproto.CallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: "invalid request body",
				},
			})
			return
		}
		req, err := toolproto.NormalizeRequest(req)
		if err != nil {
			writeToolResponse(w, http.StatusBadRequest, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeBadRequest,
					Message: err.Error(),
				},
			})
			return
		}

		tokenClaims, err := hubsvc.VerifyServiceSessionTokenLoose(strings.TrimSpace(r.Header.Get("X-Hub-Service-Token")), serviceSecret)
		if err != nil || tokenClaims.ServiceID != strings.TrimSpace(manifest.ServiceID) {
			writeToolResponse(w, http.StatusUnauthorized, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeUnauthorized,
					Message: "invalid hub service token",
				},
				Meta: toolproto.Meta{
					RequestID: strings.TrimSpace(req.Context.RequestID),
					TraceID:   strings.TrimSpace(req.Context.TraceID),
				},
			})
			return
		}

		caller := toolproto.Caller{
			Type:      strings.ToLower(strings.TrimSpace(r.Header.Get("X-Caller-Type"))),
			UserID:    strings.TrimSpace(r.Header.Get("X-Caller-User-Id")),
			ServiceID: strings.TrimSpace(r.Header.Get("X-Caller-Service-Id")),
			SurfaceID: strings.TrimSpace(r.Header.Get("X-Caller-Surface-Id")),
		}
		if caller.Type == "" {
			caller = req.Context.Caller
		}
		req.Context.Caller = caller

		meta := toolproto.Meta{
			RequestID:  strings.TrimSpace(req.Context.RequestID),
			TraceID:    strings.TrimSpace(req.Context.TraceID),
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
		}
		startedAt := time.Now()
		resp := toolproto.CallResponse{Ok: false, Result: nil, Error: nil, Meta: meta}

		resolveTargetFromCaller := func(c toolproto.Caller) (app.StorageScopeTarget, error) {
			switch strings.ToLower(strings.TrimSpace(c.Type)) {
			case "user":
				if strings.TrimSpace(c.UserID) == "" {
					return app.StorageScopeTarget{}, fmt.Errorf("missing caller user_id")
				}
				return app.StorageScopeTarget{
					Scope:  "user",
					UserID: strings.TrimSpace(c.UserID),
				}, nil
			case "surface":
				if strings.TrimSpace(c.UserID) == "" || strings.TrimSpace(c.SurfaceID) == "" {
					return app.StorageScopeTarget{}, fmt.Errorf("missing caller surface context")
				}
				return app.StorageScopeTarget{
					Scope:     "surface",
					UserID:    strings.TrimSpace(c.UserID),
					SurfaceID: strings.TrimSpace(c.SurfaceID),
				}, nil
			case "service":
				if strings.TrimSpace(c.ServiceID) == "" {
					return app.StorageScopeTarget{}, fmt.Errorf("missing caller service_id")
				}
				return app.StorageScopeTarget{
					Scope:     "service",
					ServiceID: strings.TrimSpace(c.ServiceID),
				}, nil
			default:
				return app.StorageScopeTarget{}, fmt.Errorf("unsupported caller type")
			}
		}
		toErrResp := func(code string, msg string, retryable bool) toolproto.CallResponse {
			return toolproto.CallResponse{
				Ok:     false,
				Result: nil,
				Error: &toolproto.Error{
					Code:      code,
					Message:   msg,
					Retryable: retryable,
				},
				Meta: meta,
			}
		}
		buildShareTarget := func() app.StorageScopeTarget {
			return app.StorageScopeTarget{
				Scope:     "service",
				ServiceID: "_share",
				DBName:    "_share.db",
			}
		}
		ensureShareTable := func() error {
			shareTarget := buildShareTarget()
			_, _, err := scopedDBService.Execute(shareTarget, `
				CREATE TABLE IF NOT EXISTS share_records (
					id TEXT PRIMARY KEY,
					namespace TEXT NOT NULL,
					category TEXT NOT NULL,
					service_id TEXT NOT NULL,
					key TEXT NOT NULL,
					value_json TEXT NOT NULL,
					visibility TEXT NOT NULL DEFAULT 'public',
					created_at_ms INTEGER NOT NULL,
					updated_at_ms INTEGER NOT NULL,
					UNIQUE(namespace, category, service_id, key)
				)
			`, nil)
			return err
		}

		switch req.ToolID {
		case "storage.database.query":
			target, err := resolveTargetFromCaller(caller)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			target.DBName = asString(req.Args["db_name"])
			query := asString(req.Args["query"])
			if strings.TrimSpace(query) == "" {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "query is required", false)
				break
			}
			rows, err := scopedDBService.Query(target, query, asAnySlice(req.Args["args"]))
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "database query failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"rows":  rows,
				"count": len(rows),
			}
		case "storage.database.execute":
			target, err := resolveTargetFromCaller(caller)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			target.DBName = asString(req.Args["db_name"])
			query := asString(req.Args["query"])
			if strings.TrimSpace(query) == "" {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "query is required", false)
				break
			}
			affected, lastID, err := scopedDBService.Execute(target, query, asAnySlice(req.Args["args"]))
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "database execute failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"rows_affected":  affected,
				"last_insert_id": lastID,
			}
		case "storage.database.schema":
			target, err := resolveTargetFromCaller(caller)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			target.DBName = asString(req.Args["db_name"])
			items, err := scopedDBService.Schema(target)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "database schema failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"items": items,
				"count": len(items),
			}
		case "storage.share.write":
			if strings.ToLower(strings.TrimSpace(caller.Type)) != "service" || strings.TrimSpace(caller.ServiceID) == "" {
				resp = toErrResp(toolproto.ErrorCodeForbidden, "share.write requires service caller", false)
				break
			}
			if err := ensureShareTable(); err != nil {
				resp = toErrResp(toolproto.ErrorCodeInternalError, "ensure share table failed: "+err.Error(), true)
				break
			}
			namespace := asString(req.Args["namespace"])
			category := asString(req.Args["category"])
			key := asString(req.Args["key"])
			if strings.TrimSpace(namespace) == "" || strings.TrimSpace(category) == "" || strings.TrimSpace(key) == "" {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "namespace/category/key are required", false)
				break
			}
			valueJSON := asString(req.Args["value_json"])
			if strings.TrimSpace(valueJSON) == "" {
				raw, _ := json.Marshal(req.Args["value"])
				valueJSON = string(raw)
			}
			visibility := asString(req.Args["visibility"])
			if strings.TrimSpace(visibility) == "" {
				visibility = "public"
			}
			nowMS := time.Now().UnixMilli()
			shareTarget := buildShareTarget()
			_, _, err := scopedDBService.Execute(shareTarget, `
				INSERT INTO share_records (
					id, namespace, category, service_id, key, value_json, visibility, created_at_ms, updated_at_ms
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(namespace, category, service_id, key)
				DO UPDATE SET
					value_json=excluded.value_json,
					visibility=excluded.visibility,
					updated_at_ms=excluded.updated_at_ms
			`, []any{
				"sh_" + app.NewRequestID(),
				namespace,
				category,
				caller.ServiceID,
				key,
				valueJSON,
				visibility,
				nowMS,
				nowMS,
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "share write failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"namespace":  namespace,
				"category":   category,
				"service_id": caller.ServiceID,
				"key":        key,
				"visibility": visibility,
			}
		case "storage.share.read":
			if err := ensureShareTable(); err != nil {
				resp = toErrResp(toolproto.ErrorCodeInternalError, "ensure share table failed: "+err.Error(), true)
				break
			}
			namespace := asString(req.Args["namespace"])
			category := asString(req.Args["category"])
			serviceID := asString(req.Args["service_id"])
			key := asString(req.Args["key"])
			limit := asInt(req.Args["limit"], 100)
			if limit <= 0 {
				limit = 100
			}
			if limit > 500 {
				limit = 500
			}
			query := `SELECT namespace, category, service_id, key, value_json, visibility, created_at_ms, updated_at_ms FROM share_records WHERE 1=1`
			params := make([]any, 0, 5)
			if strings.TrimSpace(namespace) != "" {
				query += " AND namespace = ?"
				params = append(params, namespace)
			}
			if strings.TrimSpace(category) != "" {
				query += " AND category = ?"
				params = append(params, category)
			}
			if strings.TrimSpace(serviceID) != "" {
				query += " AND service_id = ?"
				params = append(params, serviceID)
			}
			if strings.TrimSpace(key) != "" {
				query += " AND key = ?"
				params = append(params, key)
			}
			query += " ORDER BY updated_at_ms DESC LIMIT ?"
			params = append(params, limit)
			rows, err := scopedDBService.Query(buildShareTarget(), query, params)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "share read failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"items": rows,
				"count": len(rows),
			}
		default:
			resp = toErrResp(toolproto.ErrorCodeToolNotFound, "tool not found", false)
		}

		resp.Meta.DurationMS = time.Since(startedAt).Milliseconds()
		statusCode := http.StatusOK
		if !resp.Ok && resp.Error != nil {
			statusCode = toolproto.HTTPStatusFromCode(resp.Error.Code)
		}
		writeToolResponse(w, statusCode, resp)
	})

	mux.HandleFunc("/admin/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			http.Error(w, "bad remote addr", http.StatusBadRequest)
			return
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "message": "shutting down"})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			shutdownNow("admin shutdown requested")
		}()
	})

	server = &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if hbURL := buildHubHeartbeatURL(strings.TrimSpace(*hubRegisterURL)); hbURL != "" {
		startHubHeartbeatGuard(hbURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), shutdownNow)
	}
	app.Infof("database service listening=http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}

func builtinManifest(serviceID string) app.ServiceManifest {
	for _, m := range app.BuiltinServiceManifests() {
		if strings.TrimSpace(m.ServiceID) == strings.TrimSpace(serviceID) {
			return m
		}
	}
	return app.ServiceManifest{ServiceID: serviceID, ServiceName: serviceID, Version: "1.0.0"}
}

func manifestTools(manifest app.ServiceManifest) []app.AIServiceToolDescriptor {
	tools := make([]app.AIServiceToolDescriptor, 0, len(manifest.Provides))
	for _, p := range manifest.Provides {
		tools = append(tools, app.AIServiceToolDescriptor{
			Name:             p.ToolID,
			Description:      p.Description,
			InputSchema:      p.InputSchema,
			OutputSchema:     p.OutputSchema,
			SideEffect:       p.SideEffect,
			TimeoutMSDefault: p.TimeoutMSDefault,
			Streaming:        p.Streaming,
		})
	}
	return tools
}

func toSupervisorTools(manifest app.ServiceManifest) []toolproto.ServiceTool {
	tools := make([]toolproto.ServiceTool, 0, len(manifest.Provides))
	for _, descriptor := range manifest.Provides {
		toolID := strings.TrimSpace(descriptor.ToolID)
		if toolID == "" {
			continue
		}
		tools = append(tools, toolproto.ServiceTool{
			ToolID:               toolID,
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
		})
	}
	return tools
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeToolResponse(w http.ResponseWriter, statusCode int, resp toolproto.CallResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}

func asString(v any) string {
	switch tv := v.(type) {
	case string:
		return strings.TrimSpace(tv)
	default:
		return ""
	}
}

func asAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	out, ok := v.([]any)
	if ok {
		return out
	}
	return nil
}

func asInt(v any, defaultValue int) int {
	switch tv := v.(type) {
	case int:
		return tv
	case int32:
		return int(tv)
	case int64:
		return int(tv)
	case float32:
		return int(tv)
	case float64:
		return int(tv)
	case json.Number:
		i, err := tv.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(tv))
		if err == nil {
			return parsed
		}
	}
	return defaultValue
}

func buildHubHeartbeatURL(registerURL string) string {
	raw := strings.TrimSpace(registerURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/service/heartbeat"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func startHubHeartbeatGuard(heartbeatURL string, serviceID string, instanceID string, pid int, endpoint string, onFailure func(reason string)) {
	if strings.TrimSpace(heartbeatURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			body := map[string]any{
				"service_id":  strings.TrimSpace(serviceID),
				"instance_id": strings.TrimSpace(instanceID),
				"pid":         pid,
				"endpoint":    strings.TrimSpace(endpoint),
			}
			raw, _ := json.Marshal(body)
			req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(heartbeatURL), bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 2200 * time.Millisecond}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d", resp.StatusCode)
			}
			return nil
		}
		if err := send(); err != nil {
			onFailure("hub heartbeat failed: " + err.Error())
			return
		}
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := send(); err != nil {
				onFailure("hub heartbeat failed: " + err.Error())
				return
			}
		}
	}()
}
