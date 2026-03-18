package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime"
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
	app "kagent/services/surface-manager/internal/app"
)

func main() {
	sqlitePath := flag.String("sqlite-path", "data/kagent.db", "path to sqlite db")
	userID := flag.String("user-id", "default", "runtime user id")
	projectID := flag.String("project-id", "project-default", "runtime project id")
	threadID := flag.String("thread-id", "chat-default", "runtime thread id")
	addr := flag.String("addr", "127.0.0.1:18086", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "SURFACE-MGR")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	sqlitePathResolved := app.ResolvePathFromRoot(appRoot, *sqlitePath)
	dataRoot := filepath.Join(appRoot, "data")
	webuiRoot := filepath.Join(appRoot, "webui")
	surfaceRoot := filepath.Join(webuiRoot, "surface")
	serviceSecretPath := filepath.Join(appRoot, "services", "surface-manager", "run", ".service_secret")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}

	sqliteStore, err := app.NewSQLiteStore(sqlitePathResolved, *userID, *projectID, *threadID)
	if err != nil {
		app.Errorf("init sqlite store failed: %v", err)
		os.Exit(1)
	}
	defer sqliteStore.Close()
	if err := app.SyncSurfaceCatalog(sqliteStore, surfaceRoot); err != nil {
		app.Warnf("surface catalog scan skipped: %v", err)
	}
	surfaceFS, err := app.NewSurfaceFSService(dataRoot)
	if err != nil {
		app.Errorf("init surfacefs failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("surface-manager")
	if strings.TrimSpace(serviceBootstrap.ServiceID) != strings.TrimSpace(manifest.ServiceID) {
		app.Errorf("bootstrap service_id mismatch: expect=%s got=%s", strings.TrimSpace(manifest.ServiceID), strings.TrimSpace(serviceBootstrap.ServiceID))
		os.Exit(1)
	}
	registerURL := strings.TrimSpace(serviceBootstrap.HubRegisterURL)
	if registerURL == "" {
		registerURL = strings.TrimSpace(*hubRegisterURL)
	}
	hubToolCallURL := buildHubToolCallURL(registerURL)
	instance := strings.TrimSpace(serviceBootstrap.InstanceID)
	if instance == "" {
		instance = strings.TrimSpace(*instanceID)
	}
	if instance == "" {
		instance = "surface-manager-" + app.NewRequestID()
	}
	if registerURL != "" {
		healthy := true
		registerPayload := toolproto.SupervisorRegisterRequest{
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
			Version:    strings.TrimSpace(manifest.Version),
			Transport:  "tcp",
			Endpoint: toolproto.Endpoint{
				TCPURL: "http://" + strings.TrimSpace(*addr),
			},
			Tools:   toSupervisorTools(manifest),
			Healthy: &healthy,
		}
		raw, _ := json.Marshal(registerPayload)
		req, _ := http.NewRequest(http.MethodPost, registerURL, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		hubsvc.ApplyServiceAuthHeaders(req.Header, serviceBootstrap)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			app.Errorf("register surface-manager to hub failed: %v", err)
			os.Exit(1)
		}
		rawResp, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register surface-manager to hub status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register surface-manager to hub status=%d", resp.StatusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("surface-manager shutdown: %s", strings.TrimSpace(reason))
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
		writeJSON(w, app.AIServiceInfo{ServiceID: manifest.ServiceID, ServiceName: manifest.ServiceName, Version: manifest.Version, Provider: "surface-manager", Capabilities: caps, Transport: "http"})
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
		requireCallerUser := func() (string, error) {
			uid := strings.TrimSpace(caller.UserID)
			if uid == "" {
				return "", fmt.Errorf("missing caller user_id")
			}
			return uid, nil
		}
		requireSurfaceID := func() (string, error) {
			sid := asString(req.Args["surface_id"])
			if sid == "" {
				return "", fmt.Errorf("surface_id is required")
			}
			return sid, nil
		}

		switch req.ToolID {
		case "ui.surface.catalog_list":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			items, err := sqliteStore.ListSurfacesForUser(userID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "list surfaces failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"user_id": userID, "total": len(items), "items": items}
		case "ui.surface.get":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			entry, ok, err := sqliteStore.GetSurfaceForUser(userID, surfaceID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "query surface failed: "+err.Error(), false)
				break
			}
			if !ok {
				resp = toErrResp(toolproto.ErrorCodeToolNotFound, "surface not found", false)
				break
			}
			resp.Ok = true
			resp.Result = entry
		case "ui.surface.enable_set":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			enabled, ok := asBool(req.Args["enabled"])
			if !ok {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "enabled must be boolean", false)
				break
			}
			if err := sqliteStore.SetSurfaceEnabled(userID, surfaceID, enabled); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "set surface enabled failed: "+err.Error(), false)
				break
			}
			entry, exists, err := sqliteStore.GetSurfaceForUser(userID, surfaceID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "query surface failed: "+err.Error(), false)
				break
			}
			if !exists {
				resp = toErrResp(toolproto.ErrorCodeToolNotFound, "surface not found", false)
				break
			}
			resp.Ok = true
			resp.Result = entry
		case "ui.surface.session_issue":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			entry, ok, err := sqliteStore.GetSurfaceForUser(userID, surfaceID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "query surface failed: "+err.Error(), false)
				break
			}
			if !ok {
				resp = toErrResp(toolproto.ErrorCodeToolNotFound, "surface not found", false)
				break
			}
			if !entry.Available {
				resp = toErrResp(toolproto.ErrorCodeForbidden, "surface is not available", false)
				break
			}
			ttl := 30 * time.Minute
			ttlSec := asInt(req.Args["ttl_seconds"], 0)
			if ttlSec > 0 {
				ttl = time.Duration(ttlSec) * time.Second
			}
			token, expMS, err := surfaceFS.IssueSurfaceSessionToken(userID, surfaceID, ttl)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "issue session token failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "surface_session_token": token, "exp_ms": expMS}
		case "ui.surface.capability_issue":
			sessionToken := asString(req.Args["surface_session_token"])
			scope := asString(req.Args["scope"])
			if sessionToken == "" || scope == "" {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surface_session_token and scope are required", false)
				break
			}
			pathPrefix := asString(req.Args["path_prefix"])
			ttl := 5 * time.Minute
			ttlSec := asInt(req.Args["ttl_seconds"], 0)
			if ttlSec > 0 {
				ttl = time.Duration(ttlSec) * time.Second
			}
			token, expMS, err := surfaceFS.IssueCapabilityTokenFromSession(sessionToken, scope, pathPrefix, ttl)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "issue capability failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"capability_token": token, "exp_ms": expMS, "scope": scope, "path_prefix": pathPrefix}
		case "ui.surface.runtime_status":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			entry, ok, err := sqliteStore.GetSurfaceForUser(userID, surfaceID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "query surface failed: "+err.Error(), false)
				break
			}
			if !ok {
				resp = toErrResp(toolproto.ErrorCodeToolNotFound, "surface not found", false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface": entry, "runtime": surfaceFS.RuntimeStatus(surfaceID)}
		case "ui.surface.logs_query":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			limit := asInt(req.Args["limit"], 80)
			if limit <= 0 {
				limit = 80
			}
			if limit > 200 {
				limit = 200
			}
			logs, err := sqliteStore.LoadRecentSurfaceMessages(userID, surfaceID, limit)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "query surface logs failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "count": len(logs), "items": logs}
		case "ui.surface.rescan":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			if err := app.SyncSurfaceCatalog(sqliteStore, surfaceRoot); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "rescan surface failed: "+err.Error(), false)
				break
			}
			items, err := sqliteStore.ListSurfacesForUser(userID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "list surfaces failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true, "total": len(items), "items": items}
		case "ui.surface.rebind":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			if err := app.SyncSurfaceCatalog(sqliteStore, surfaceRoot); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "rebind surface failed: "+err.Error(), false)
				break
			}
			items, err := sqliteStore.ListSurfacesForUser(userID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "list surfaces failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true, "total": len(items), "items": items}
		case "ui.surface.fs_read":
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			raw, err := surfaceFS.ReadFile(capabilityToken, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surfacefs read failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"surface_id":  surfaceID,
				"path":        pathValue,
				"size_bytes":  len(raw),
				"data_base64": base64.StdEncoding.EncodeToString(raw),
			}
		case "ui.surface.fs_write":
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			dataBase64 := asString(req.Args["data_base64"])
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "invalid data_base64", false)
				break
			}
			size, err := surfaceFS.WriteFile(capabilityToken, surfaceID, pathValue, raw)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surfacefs write failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "path": pathValue, "size_bytes": size, "ok": true}
		case "ui.surface.fs_list":
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			items, err := surfaceFS.ListDir(capabilityToken, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surfacefs list failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "path": pathValue, "items": items}
		case "ui.surface.fs_delete":
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			recursive, _ := asBool(req.Args["recursive"])
			if err := surfaceFS.DeletePath(capabilityToken, surfaceID, pathValue, recursive); err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surfacefs delete failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "path": pathValue, "ok": true}
		case "ui.surface.fs_sign_static":
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			raw, err := surfaceFS.ReadFile(capabilityToken, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surfacefs read failed: "+err.Error(), false)
				break
			}
			mimeType := strings.TrimSpace(mime.TypeByExtension(filepath.Ext(pathValue)))
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			encoded := base64.StdEncoding.EncodeToString(raw)
			resp.Ok = true
			resp.Result = map[string]any{
				"surface_id": surfaceID,
				"path":       pathValue,
				"size_bytes": len(raw),
				"mime":       mimeType,
				"url":        "data:" + mimeType + ";base64," + encoded,
			}
		case "ui.surface.db_roundtrip":
			if strings.TrimSpace(hubToolCallURL) == "" {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub register url is not configured", false)
				break
			}
			dbName := asString(req.Args["db_name"])
			if dbName == "" {
				dbName = "surface-manager.db"
			}
			recordKey := asString(req.Args["key"])
			if recordKey == "" {
				recordKey = "default"
			}
			recordValue := asString(req.Args["value"])
			if recordValue == "" {
				recordValue = "v-" + app.NewRequestID()
			}
			probeRequestID := strings.TrimSpace(req.Context.RequestID)
			if probeRequestID == "" {
				probeRequestID = "req_" + app.NewRequestID()
			}
			probeTraceID := strings.TrimSpace(req.Context.TraceID)
			if probeTraceID == "" {
				probeTraceID = "tr_" + app.NewRequestID()
			}
			createResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, probeRequestID+"-create", probeTraceID, "storage.database.execute", map[string]any{
				"db_name": dbName,
				"query":   "CREATE TABLE IF NOT EXISTS surface_probe (probe_key TEXT PRIMARY KEY, probe_value TEXT NOT NULL, updated_at_ms INTEGER NOT NULL)",
				"args":    []any{},
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "call hub create table failed: "+err.Error(), true)
				break
			}
			if !createResp.Ok {
				msg := "create table failed"
				if createResp.Error != nil {
					msg = strings.TrimSpace(createResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			writeResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, probeRequestID+"-write", probeTraceID, "storage.database.execute", map[string]any{
				"db_name": dbName,
				"query":   "INSERT INTO surface_probe(probe_key, probe_value, updated_at_ms) VALUES(?, ?, ?) ON CONFLICT(probe_key) DO UPDATE SET probe_value=excluded.probe_value, updated_at_ms=excluded.updated_at_ms",
				"args":    []any{recordKey, recordValue, time.Now().UnixMilli()},
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "call hub write failed: "+err.Error(), true)
				break
			}
			if !writeResp.Ok {
				msg := "write failed"
				if writeResp.Error != nil {
					msg = strings.TrimSpace(writeResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			queryResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, probeRequestID+"-query", probeTraceID, "storage.database.query", map[string]any{
				"db_name": dbName,
				"query":   "SELECT probe_key, probe_value, updated_at_ms FROM surface_probe WHERE probe_key=? LIMIT 1",
				"args":    []any{recordKey},
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "call hub query failed: "+err.Error(), true)
				break
			}
			if !queryResp.Ok {
				msg := "query failed"
				if queryResp.Error != nil {
					msg = strings.TrimSpace(queryResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"db_name":       dbName,
				"key":           recordKey,
				"value":         recordValue,
				"query_result":  queryResp.Result,
				"caller_type":   "service",
				"caller_id":     manifest.ServiceID,
				"hub_tool_call": true,
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
	if hbURL := buildHubHeartbeatURL(registerURL); hbURL != "" {
		startHubHeartbeatGuard(hbURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow)
	}
	app.Infof("surface-manager listening=http://%s", *addr)
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

func asBool(v any) (bool, bool) {
	switch tv := v.(type) {
	case bool:
		return tv, true
	case string:
		switch strings.ToLower(strings.TrimSpace(tv)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		}
	case int:
		if tv == 0 {
			return false, true
		}
		if tv == 1 {
			return true, true
		}
	case int64:
		if tv == 0 {
			return false, true
		}
		if tv == 1 {
			return true, true
		}
	case float64:
		if tv == 0 {
			return false, true
		}
		if tv == 1 {
			return true, true
		}
	}
	return false, false
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

func callHubToolAsService(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, serviceID string, requestID string, traceID string, toolID string, args map[string]any) (toolproto.CallResponse, error) {
	callReq := toolproto.CallRequest{
		ToolID: strings.TrimSpace(toolID),
		Args:   args,
		Context: &toolproto.Context{
			RequestID: strings.TrimSpace(requestID),
			TraceID:   strings.TrimSpace(traceID),
			Caller: toolproto.Caller{
				Type:      "service",
				ServiceID: strings.TrimSpace(serviceID),
			},
		},
	}
	rawReq, _ := json.Marshal(callReq)
	req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(hubToolCallURL), bytes.NewReader(rawReq))
	req.Header.Set("Content-Type", "application/json")
	hubsvc.ApplyServiceAuthHeaders(req.Header, serviceAuth)
	client := &http.Client{Timeout: 8 * time.Second}
	httpResp, err := client.Do(req)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	defer httpResp.Body.Close()
	rawResp, _ := io.ReadAll(httpResp.Body)
	var callResp toolproto.CallResponse
	if err := json.Unmarshal(rawResp, &callResp); err != nil {
		return toolproto.CallResponse{}, fmt.Errorf("decode hub tool response failed: %w", err)
	}
	if httpResp.StatusCode >= 300 && callResp.Error == nil {
		return toolproto.CallResponse{}, fmt.Errorf("hub tool call status=%d", httpResp.StatusCode)
	}
	return callResp, nil
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

func buildHubToolCallURL(registerURL string) string {
	raw := strings.TrimSpace(registerURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.Path = "/api/tool/call"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func startHubHeartbeatGuard(heartbeatURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string)) {
	if strings.TrimSpace(heartbeatURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			body := map[string]any{
				"service_id":  strings.TrimSpace(serviceID),
				"instance_id": strings.TrimSpace(instanceID),
				"status":      "ready",
				"healthy":     true,
				"pid":         pid,
				"endpoint":    strings.TrimSpace(endpoint),
			}
			raw, _ := json.Marshal(body)
			req, _ := http.NewRequest(http.MethodPost, strings.TrimSpace(heartbeatURL), bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			hubsvc.ApplyServiceAuthHeaders(req.Header, serviceAuth)
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
