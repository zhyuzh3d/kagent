package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/sql_db/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18085", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "SQL_DB")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	if _, err := hubsvc.LoadProjectConfig(filepath.Join(appRoot, "services", "sql_db")); err != nil {
		app.Errorf("load service config failed: %v", err)
		os.Exit(1)
	}
	dataRoot := filepath.Join(appRoot, "data")
	serviceSecretPath := filepath.Join(appRoot, "services", "sql_db", "run", ".service_secret")
	processStorePath := filepath.Join(appRoot, "services", "sql_db", "run", ".service_pid")
	runtimeManifestPath := filepath.Join(appRoot, "services", "sql_db", "run", "manifest_runtime.json")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}
	if err := hubsvc.CleanupPreviousServiceProcess(processStorePath, "sql_db"); err != nil {
		app.Errorf("cleanup previous process failed: %v", err)
		os.Exit(1)
	}

	scopedDBService, err := app.NewScopedDatabaseService(dataRoot)
	if err != nil {
		app.Errorf("init scoped database service failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("sql_db")
	if strings.TrimSpace(serviceBootstrap.ServiceID) != strings.TrimSpace(manifest.ServiceID) {
		app.Errorf("bootstrap service_id mismatch: expect=%s got=%s", strings.TrimSpace(manifest.ServiceID), strings.TrimSpace(serviceBootstrap.ServiceID))
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(serviceBootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	instance := strings.TrimSpace(serviceBootstrap.InstanceID)
	if instance == "" {
		instance = strings.TrimSpace(*instanceID)
	}
	if instance == "" {
		instance = "sql_db-" + app.NewRequestID()
	}
	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("sql_db shutdown: %s", strings.TrimSpace(reason))
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
		writeJSON(w, app.AIServiceInfo{ServiceID: manifest.ServiceID, ServiceName: manifest.ServiceName, Version: manifest.Version, Provider: "sql_db", Capabilities: caps, Transport: "http"})
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

		if err := hubsvc.VerifyHubAuthHeaders(r.Header, manifest.ServiceID, instance, serviceBootstrap.H2SToken); err != nil {
			writeToolResponse(w, http.StatusForbidden, toolproto.CallResponse{
				Ok: false,
				Error: &toolproto.Error{
					Code:    toolproto.ErrorCodeForbidden,
					Message: "invalid hub auth",
				},
				Meta: toolproto.Meta{
					RequestID: strings.TrimSpace(req.Context.RequestID),
					TraceID:   strings.TrimSpace(req.Context.TraceID),
				},
			})
			return
		}
		hubOnly := isHubOnlyContext(req.Context)
		if !hubOnly {
			delete(req.Args, "healthz")
		}

		caller := hubsvc.MergeCaller(&req, r.Header)
		hubsvc.MergeOriginCaller(&req, r.Header)

		meta := toolproto.Meta{
			RequestID:  strings.TrimSpace(req.Context.RequestID),
			TraceID:    strings.TrimSpace(req.Context.TraceID),
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
		}
		startedAt := time.Now()
		resp := toolproto.CallResponse{Ok: false, Result: nil, Error: nil, Meta: meta}
		if hubOnly && healthzRequested(req.Args) {
			resp.Ok = true
			resp.Result = map[string]any{
				"service_id": strings.TrimSpace(manifest.ServiceID),
				"hub_only":   true,
				"healthz":    true,
			}
			writeToolResponse(w, http.StatusOK, resp)
			return
		}
		if req.ToolID == "service.lifecycle.health" || req.ToolID == "service.lifecycle.state.get" {
			resp.Ok = true
			resp.Result = map[string]any{
				"service_id":   strings.TrimSpace(manifest.ServiceID),
				"instance_id":  strings.TrimSpace(instance),
				"pid":          os.Getpid(),
				"endpoint":     "http://" + strings.TrimSpace(*addr),
				"healthy":      true,
				"status":       "ready",
				"timestamp_ms": time.Now().UnixMilli(),
			}
			writeToolResponse(w, http.StatusOK, resp)
			return
		}

		resolveTargetFromCaller := func(c toolproto.Caller) (app.StorageScopeTarget, error) {
			callerType := strings.ToLower(strings.TrimSpace(c.Type))
			if callerType == "" {
				switch {
				case strings.TrimSpace(c.UserID) != "":
					callerType = toolproto.CallerTypeUser
				case strings.TrimSpace(c.ServiceID) != "":
					callerType = toolproto.CallerTypeService
				}
			}
			switch callerType {
			case toolproto.CallerTypeUser, toolproto.CallerTypeAdmin, toolproto.CallerTypePage:
				if strings.TrimSpace(c.UserID) == "" {
					return app.StorageScopeTarget{}, fmt.Errorf("missing caller user_id")
				}
				return app.StorageScopeTarget{
					Scope:  "user",
					UserID: strings.TrimSpace(c.UserID),
				}, nil
			case toolproto.CallerTypeSurface:
				if strings.TrimSpace(c.UserID) == "" || strings.TrimSpace(c.SurfaceID) == "" {
					return app.StorageScopeTarget{}, fmt.Errorf("missing caller surface context")
				}
				return app.StorageScopeTarget{
					Scope:     "surface",
					UserID:    strings.TrimSpace(c.UserID),
					SurfaceID: strings.TrimSpace(c.SurfaceID),
				}, nil
			case toolproto.CallerTypeService:
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
		resolveScopedCaller := func() toolproto.Caller {
			if strings.EqualFold(asString(req.Args["scope_source"]), "origin") {
				switch strings.ToLower(strings.TrimSpace(req.Context.OriginCaller.Type)) {
				case toolproto.CallerTypeUser, toolproto.CallerTypeSurface:
					return req.Context.OriginCaller
				}
			}
			return caller
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
			target, err := resolveTargetFromCaller(resolveScopedCaller())
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
			target, err := resolveTargetFromCaller(resolveScopedCaller())
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
			target, err := resolveTargetFromCaller(resolveScopedCaller())
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
	app.Infof("sql_db service listening=http://%s", *addr)
	ln, err := hubsvc.Listen(*addr)
	if err != nil {
		app.Errorf("server listen failed: %v", err)
		os.Exit(1)
	}
	startedAtMS := time.Now().UnixMilli()
	if err := hubsvc.RecordCurrentServiceProcess(processStorePath, manifest.ServiceID, startedAtMS); err != nil {
		app.Errorf("record current process failed: %v", err)
		os.Exit(1)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- server.Serve(ln)
	}()
	if registerURL != "" {
		healthy := true
		registerCall := toolproto.CallRequest{
			ToolID: "hub.governance.service.register",
			Args: map[string]any{
				"service_id":  strings.TrimSpace(manifest.ServiceID),
				"instance_id": strings.TrimSpace(instance),
				"version":     strings.TrimSpace(manifest.Version),
				"transport":   "tcp",
				"endpoint": map[string]any{
					"tcp_url": "http://" + strings.TrimSpace(*addr),
				},
				"tools":   toSupervisorTools(manifest),
				"healthy": &healthy,
			},
			Context: &toolproto.Context{
				RequestID: "reg-" + app.NewRequestID(),
				TraceID:   "tr-" + app.NewRequestID(),
				Caller: toolproto.Caller{
					Type:      "service",
					ServiceID: manifest.ServiceID,
				},
			},
		}
		rawResp, statusCode, err := postHubToolCall(registerURL, serviceBootstrap, registerCall)
		if err != nil {
			app.Errorf("register sql_db service to hub failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if statusCode >= 300 {
			app.Errorf("register sql_db service to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			shutdownNow("register to hub failed")
			return
		}
		registerResp, err := hubsvc.DecodeSupervisorRegisterResult(rawResp)
		if err != nil {
			app.Errorf("decode register response failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if err := hubsvc.WriteServiceRuntimeManifest(runtimeManifestPath, registerResp); err != nil {
			app.Errorf("write runtime manifest failed: %v", err)
			shutdownNow("register to hub failed")
			return
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register sql_db service to hub status=%d", statusCode)
	}
	if hubToolCallURL := buildHubToolCallURL(registerURL); hubToolCallURL != "" {
		startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	if err := <-serveErrCh; err != nil && err != http.ErrServerClosed {
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
			Description:          strings.TrimSpace(descriptor.Description),
			Protocol:             "http",
			Version:              strings.TrimSpace(manifest.Version),
			Streaming:            strings.EqualFold(strings.TrimSpace(descriptor.Streaming), "stream"),
			TimeoutMS:            descriptor.TimeoutMSDefault,
			TimeoutMSDefault:     descriptor.TimeoutMSDefault,
			InputSchema:          descriptor.InputSchema,
			OutputSchema:         descriptor.OutputSchema,
			ScopeSupport:         append([]string(nil), descriptor.ScopeSupport...),
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
			AllowedCallerTypes:   append([]string(nil), descriptor.AllowedCallerTypes...),
		})
	}
	return tools
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(&http.Client{Timeout: 5 * time.Second}, hubToolCallURL, serviceAuth, req)
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

func healthzRequested(args map[string]any) bool {
	if args == nil {
		return false
	}
	value, ok := args["healthz"]
	if !ok {
		return false
	}
	switch tv := value.(type) {
	case bool:
		return tv
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "1", "true", "yes", "on":
			return true
		}
	case int:
		return tv != 0
	case int64:
		return tv != 0
	case float64:
		return tv != 0
	}
	return false
}

func isHubOnlyContext(ctx *toolproto.Context) bool {
	if ctx == nil || ctx.Meta == nil {
		return false
	}
	value, ok := ctx.Meta["hub_only"]
	if !ok {
		return false
	}
	switch tv := value.(type) {
	case bool:
		return tv
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "1", "true", "yes", "on":
			return true
		}
	case float64:
		return tv != 0
	case int:
		return tv != 0
	case int64:
		return tv != 0
	}
	return false
}

func buildHubToolCallURL(registerURL string) string {
	return hubsvc.BuildHubToolCallURL(registerURL)
}

func startHubToolHeartbeatGuard(hubToolCallURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  strings.TrimSpace(serviceID),
					"instance_id": strings.TrimSpace(instanceID),
					"status":      "ready",
					"healthy":     true,
					"pid":         pid,
					"endpoint":    strings.TrimSpace(endpoint),
				},
				Context: &toolproto.Context{
					RequestID: "hb-" + app.NewRequestID(),
					TraceID:   "tr-" + app.NewRequestID(),
					Caller: toolproto.Caller{
						Type:      "service",
						ServiceID: strings.TrimSpace(serviceID),
					},
				},
			}
			rawResp, statusCode, err := postHubToolCall(hubToolCallURL, serviceAuth, callReq)
			if err != nil {
				return err
			}
			if statusCode >= 300 {
				return fmt.Errorf("heartbeat status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			}
			var resp toolproto.CallResponse
			if err := json.Unmarshal(rawResp, &resp); err != nil {
				return fmt.Errorf("decode heartbeat response: %w", err)
			}
			if !resp.Ok {
				if resp.Error != nil && strings.TrimSpace(resp.Error.Message) != "" {
					return fmt.Errorf("heartbeat rejected: %s", strings.TrimSpace(resp.Error.Message))
				}
				return fmt.Errorf("heartbeat rejected")
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
