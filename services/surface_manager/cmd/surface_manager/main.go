package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"mime"
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
	app "kagent/services/surface_manager/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18086", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "SURFACE_MANAGER")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	dataRoot := filepath.Join(appRoot, "data")
	webuiRoot := filepath.Join(appRoot, "webui")
	surfaceRoot := filepath.Join(webuiRoot, "surface")
	serviceSecretPath := filepath.Join(appRoot, "services", "surface_manager", "run", ".service_secret")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}

	surfaceFS, err := app.NewSurfaceFSService(dataRoot)
	if err != nil {
		app.Errorf("init surfacefs failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("surface_manager")
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
		instance = "surface_manager-" + app.NewRequestID()
	}
	var lifecycleMu sync.RWMutex
	var initialized bool
	var initializing bool
	var lastInitErr string
	var store *app.HubStore
	currentStatus := func() string {
		lifecycleMu.RLock()
		defer lifecycleMu.RUnlock()
		switch {
		case initialized:
			return "ready"
		case initializing:
			return "initializing"
		case strings.TrimSpace(lastInitErr) != "":
			return "failed"
		default:
			return "registered"
		}
	}
	currentHealthy := func() bool {
		lifecycleMu.RLock()
		defer lifecycleMu.RUnlock()
		return strings.TrimSpace(lastInitErr) == ""
	}
	runInit := func(ctx context.Context) error {
		lifecycleMu.Lock()
		if initialized {
			lifecycleMu.Unlock()
			return nil
		}
		if initializing {
			lifecycleMu.Unlock()
			return nil
		}
		initializing = true
		lastInitErr = ""
		lifecycleMu.Unlock()

		nextStore := app.NewHubStore(hubToolCallURL, serviceBootstrap, manifest.ServiceID, 8*time.Second)
		if err := nextStore.EnsureSchema(ctx); err != nil {
			lifecycleMu.Lock()
			initializing = false
			lastInitErr = err.Error()
			lifecycleMu.Unlock()
			_ = nextStore.Close()
			return fmt.Errorf("init hub-backed store failed: %w", err)
		}
		if err := app.SyncSurfaceCatalog(ctx, nextStore, surfaceRoot); err != nil {
			app.Warnf("surface catalog scan skipped: %v", err)
		}
		lifecycleMu.Lock()
		oldStore := store
		store = nextStore
		initializing = false
		initialized = true
		lastInitErr = ""
		lifecycleMu.Unlock()
		if oldStore != nil {
			_ = oldStore.Close()
		}
		return nil
	}
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
			app.Errorf("register surface_manager to hub failed: %v", err)
			os.Exit(1)
		}
		if statusCode >= 300 {
			app.Errorf("register surface_manager to hub status=%d body=%s", statusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register surface_manager to hub status=%d", statusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("surface_manager shutdown: %s", strings.TrimSpace(reason))
			lifecycleMu.RLock()
			activeStore := store
			lifecycleMu.RUnlock()
			if activeStore != nil {
				_ = activeStore.Close()
			}
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
		writeJSON(w, app.AIServiceInfo{ServiceID: manifest.ServiceID, ServiceName: manifest.ServiceName, Version: manifest.Version, Provider: "surface_manager", Capabilities: caps, Transport: "http"})
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
		reqCtx := hubsvc.ContextWithDelegation(r.Context(), req.Context.OriginCaller, req.Context.OriginToken)
		effectiveCaller := caller
		if strings.TrimSpace(req.Context.OriginCaller.UserID) != "" {
			effectiveCaller.UserID = strings.TrimSpace(req.Context.OriginCaller.UserID)
		}

		meta := toolproto.Meta{
			RequestID:  strings.TrimSpace(req.Context.RequestID),
			TraceID:    strings.TrimSpace(req.Context.TraceID),
			ServiceID:  strings.TrimSpace(manifest.ServiceID),
			InstanceID: strings.TrimSpace(instance),
		}
		startedAt := time.Now()
		resp := toolproto.CallResponse{Ok: false, Result: nil, Error: nil, Meta: meta}
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
				"service_id":  strings.TrimSpace(manifest.ServiceID),
				"instance_id": strings.TrimSpace(instance),
				"pid":         os.Getpid(),
				"endpoint":    "http://" + strings.TrimSpace(*addr),
				"healthy":     currentHealthy(),
				"status":      currentStatus(),
				"ready":       currentStatus() == "ready",
				"initialized": currentStatus() == "ready",
				"last_init_error": strings.TrimSpace(func() string {
					lifecycleMu.RLock()
					defer lifecycleMu.RUnlock()
					return lastInitErr
				}()),
				"timestamp_ms": time.Now().UnixMilli(),
			}
			writeToolResponse(w, http.StatusOK, resp)
			return
		}
		if req.ToolID == "service.lifecycle.init" {
			if !strings.EqualFold(strings.TrimSpace(caller.Type), "service") || strings.TrimSpace(caller.ServiceID) != strings.TrimSpace(manifest.ServiceID) {
				writeToolResponse(w, http.StatusForbidden, toErrResp(toolproto.ErrorCodeForbidden, "forbidden", false))
				return
			}
			if err := runInit(reqCtx); err != nil {
				writeToolResponse(w, http.StatusServiceUnavailable, toErrResp(toolproto.ErrorCodeServiceUnavailable, err.Error(), true))
				return
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true, "status": currentStatus()}
			writeToolResponse(w, http.StatusOK, resp)
			return
		}
		if currentStatus() != "ready" {
			writeToolResponse(w, http.StatusServiceUnavailable, toErrResp(toolproto.ErrorCodeServiceUnavailable, "service not initialized", true))
			return
		}
		requireCallerUser := func() (string, error) {
			uid := strings.TrimSpace(effectiveCaller.UserID)
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
		requireSurfaceEntry := func() (string, app.SurfaceCatalogEntry, error) {
			userID, err := requireCallerUser()
			if err != nil {
				return "", app.SurfaceCatalogEntry{}, err
			}
			surfaceID, err := requireSurfaceID()
			if err != nil {
				return "", app.SurfaceCatalogEntry{}, err
			}
			entry, ok, err := store.GetSurfaceForUser(reqCtx, userID, surfaceID)
			if err != nil {
				return "", app.SurfaceCatalogEntry{}, err
			}
			if !ok {
				return "", app.SurfaceCatalogEntry{}, fmt.Errorf("surface not found")
			}
			return userID, entry, nil
		}

		switch req.ToolID {
		case "ui.surface.catalog_list":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			items, err := store.ListSurfacesForUser(reqCtx, userID)
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
			entry, ok, err := store.GetSurfaceForUser(reqCtx, userID, surfaceID)
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
			if err := store.SetSurfaceEnabled(reqCtx, userID, surfaceID, enabled); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "set surface enabled failed: "+err.Error(), false)
				break
			}
			entry, exists, err := store.GetSurfaceForUser(reqCtx, userID, surfaceID)
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
			entry, ok, err := store.GetSurfaceForUser(reqCtx, userID, surfaceID)
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
			entry, ok, err := store.GetSurfaceForUser(reqCtx, userID, surfaceID)
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
			if _, err := requireCallerUser(); err != nil {
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
			logs, err := store.LoadRecentSurfaceMessages(reqCtx, surfaceID, limit)
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
			if err := app.SyncSurfaceCatalog(reqCtx, store, surfaceRoot); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "rescan surface failed: "+err.Error(), false)
				break
			}
			items, err := store.ListSurfacesForUser(reqCtx, userID)
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
			if err := app.SyncSurfaceCatalog(reqCtx, store, surfaceRoot); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "rebind surface failed: "+err.Error(), false)
				break
			}
			items, err := store.ListSurfacesForUser(reqCtx, userID)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "list surfaces failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"ok": true, "total": len(items), "items": items}
		case "ui.surface.generate":
			userID, err := requireCallerUser()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
				break
			}
			surfaceName := asString(req.Args["surface_name"])
			if surfaceName == "" {
				surfaceName = "generated_surface"
			}
			prompt := asString(req.Args["prompt"])
			dir, generatedManifest, err := app.GenerateSurfaceScaffold(surfaceRoot, userID, surfaceName, prompt)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "generate surface failed: "+err.Error(), false)
				break
			}
			if text, ok := generateSurfaceByAI(hubToolCallURL, serviceBootstrap, manifest.ServiceID, surfaceName, prompt); ok {
				if generatedFiles, parseErr := app.ParseGeneratedFilesMap(text); parseErr == nil && len(generatedFiles) > 0 {
					for relPath, content := range generatedFiles {
						target := filepath.Clean(filepath.Join(dir, relPath))
						if !strings.HasPrefix(target, filepath.Clean(dir)+string(filepath.Separator)) && target != filepath.Clean(dir) {
							continue
						}
						if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
							continue
						}
						_ = os.WriteFile(target, []byte(content), 0o644)
					}
				}
			}
			if err := app.SyncSurfaceCatalog(reqCtx, store, surfaceRoot); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "rescan generated surface failed: "+err.Error(), false)
				break
			}
			entry, _, _ := store.GetSurfaceForUser(reqCtx, userID, generatedManifest.ID)
			resp.Ok = true
			resp.Result = map[string]any{
				"surface_id": generatedManifest.ID,
				"dir":        dir,
				"manifest":   generatedManifest,
				"entry":      entry,
			}
		case "ui.surface.package_read":
			_, entry, err := requireSurfaceEntry()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			relPath := asString(req.Args["path"])
			raw, resolvedPath, err := app.ReadSurfacePackageFile(surfaceRoot, entry, relPath)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "read surface package failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{
				"surface_id":  entry.SurfaceID,
				"path":        relPath,
				"resolved":    resolvedPath,
				"data_base64": base64.StdEncoding.EncodeToString(raw),
			}
		case "ui.surface.package_write":
			_, entry, err := requireSurfaceEntry()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			relPath := asString(req.Args["path"])
			dataBase64 := asString(req.Args["data_base64"])
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "invalid data_base64", false)
				break
			}
			resolvedPath, err := app.WriteSurfacePackageFile(surfaceRoot, entry, relPath, raw)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "write surface package failed: "+err.Error(), false)
				break
			}
			if err := app.SyncSurfaceCatalog(reqCtx, store, surfaceRoot); err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "rescan surface package failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": entry.SurfaceID, "path": relPath, "resolved": resolvedPath, "ok": true}
		case "ui.surface.package_list":
			_, entry, err := requireSurfaceEntry()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			items, dir, err := app.ListSurfacePackageFiles(surfaceRoot, entry)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "list surface package failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": entry.SurfaceID, "dir": dir, "items": items}
		case "ui.surface.fs_read":
			if strings.TrimSpace(hubToolCallURL) == "" {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub register url is not configured", false)
				break
			}
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			claims, cleanPath, err := surfaceFS.ValidateCapabilityRequest(capabilityToken, app.SurfaceScopeRead, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surface capability read failed: "+err.Error(), false)
				break
			}
			storageResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, meta.RequestID+"-fs-read", meta.TraceID, "storage.file.read", map[string]any{
				"path": surfaceStoragePath(claims, cleanPath),
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub file read failed: "+err.Error(), true)
				break
			}
			if !storageResp.Ok {
				msg := "hub file read failed"
				if storageResp.Error != nil {
					msg = strings.TrimSpace(storageResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			raw, err := decodeDataBase64Result(storageResp.Result)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "decode hub file read failed: "+err.Error(), false)
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
			if strings.TrimSpace(hubToolCallURL) == "" {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub register url is not configured", false)
				break
			}
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
			claims, cleanPath, err := surfaceFS.ValidateCapabilityRequest(capabilityToken, app.SurfaceScopeWrite, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surface capability write failed: "+err.Error(), false)
				break
			}
			storageResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, meta.RequestID+"-fs-write", meta.TraceID, "storage.file.write", map[string]any{
				"path":        surfaceStoragePath(claims, cleanPath),
				"data_base64": base64.StdEncoding.EncodeToString(raw),
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub file write failed: "+err.Error(), true)
				break
			}
			if !storageResp.Ok {
				msg := "hub file write failed"
				if storageResp.Error != nil {
					msg = strings.TrimSpace(storageResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "path": pathValue, "size_bytes": len(raw), "ok": true}
		case "ui.surface.fs_list":
			if strings.TrimSpace(hubToolCallURL) == "" {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub register url is not configured", false)
				break
			}
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			claims, cleanPath, err := surfaceFS.ValidateCapabilityRequest(capabilityToken, app.SurfaceScopeList, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surface capability list failed: "+err.Error(), false)
				break
			}
			storageResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, meta.RequestID+"-fs-list", meta.TraceID, "storage.file.list", map[string]any{
				"path": surfaceStoragePath(claims, cleanPath),
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub file list failed: "+err.Error(), true)
				break
			}
			if !storageResp.Ok {
				msg := "hub file list failed"
				if storageResp.Error != nil {
					msg = strings.TrimSpace(storageResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			items, err := decodeItemsResult(storageResp.Result)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "decode hub file list failed: "+err.Error(), false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "path": pathValue, "items": items}
		case "ui.surface.fs_delete":
			if strings.TrimSpace(hubToolCallURL) == "" {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub register url is not configured", false)
				break
			}
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			recursive, _ := asBool(req.Args["recursive"])
			claims, cleanPath, err := surfaceFS.ValidateCapabilityRequest(capabilityToken, app.SurfaceScopeDelete, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surface capability delete failed: "+err.Error(), false)
				break
			}
			storageResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, meta.RequestID+"-fs-delete", meta.TraceID, "storage.file.delete", map[string]any{
				"path":      surfaceStoragePath(claims, cleanPath),
				"recursive": recursive,
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub file delete failed: "+err.Error(), true)
				break
			}
			if !storageResp.Ok {
				msg := "hub file delete failed"
				if storageResp.Error != nil {
					msg = strings.TrimSpace(storageResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			resp.Ok = true
			resp.Result = map[string]any{"surface_id": surfaceID, "path": pathValue, "ok": true}
		case "ui.surface.fs_sign_static":
			if strings.TrimSpace(hubToolCallURL) == "" {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub register url is not configured", false)
				break
			}
			capabilityToken := asString(req.Args["capability_token"])
			surfaceID, err := requireSurfaceID()
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, err.Error(), false)
				break
			}
			pathValue := asString(req.Args["path"])
			claims, cleanPath, err := surfaceFS.ValidateCapabilityRequest(capabilityToken, app.SurfaceScopeStatic, surfaceID, pathValue)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeBadRequest, "surface capability static read failed: "+err.Error(), false)
				break
			}
			storageResp, err := callHubToolAsService(hubToolCallURL, serviceBootstrap, manifest.ServiceID, meta.RequestID+"-fs-static", meta.TraceID, "storage.file.read", map[string]any{
				"path": surfaceStoragePath(claims, cleanPath),
			})
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeServiceUnavailable, "hub file static read failed: "+err.Error(), true)
				break
			}
			if !storageResp.Ok {
				msg := "hub file static read failed"
				if storageResp.Error != nil {
					msg = strings.TrimSpace(storageResp.Error.Message)
				}
				resp = toErrResp(toolproto.ErrorCodeToolExecError, msg, false)
				break
			}
			raw, err := decodeDataBase64Result(storageResp.Result)
			if err != nil {
				resp = toErrResp(toolproto.ErrorCodeToolExecError, "decode hub file static read failed: "+err.Error(), false)
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
				dbName = "surface_manager.db"
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
	if hubToolCallURL := buildHubToolCallURL(registerURL); hubToolCallURL != "" {
		startHubToolHeartbeatGuard(hubToolCallURL, manifest.ServiceID, instance, os.Getpid(), "http://"+strings.TrimSpace(*addr), serviceBootstrap, shutdownNow, currentStatus, currentHealthy)
	}
	app.Infof("surface_manager listening=http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		app.Errorf("server failed: %v", err)
		os.Exit(1)
	}
}

func generateSurfaceByAI(hubToolCallURL string, bootstrap hubsvc.BootstrapSecret, callerServiceID string, surfaceName string, prompt string) (string, bool) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(prompt) == "" {
		return "", false
	}
	systemPrompt := `你是一个 Surface scaffold 生成器。你必须只输出 JSON，格式为 {"files":{"相对路径":"文件内容"}}。
要求：
1. 只允许输出 manifest.json、index.html、README.md。
2. 必须兼容 Page -> Surface 的 postMessage/MessageChannel 握手模式。
3. surface 必须能回报 surface_ready、state_change、action_result。
4. index.html 里必须至少支持 get_state 和一个可见业务动作。
5. 不要输出 markdown 代码块，不要输出解释。`
	result, err := callHubToolAsService(hubToolCallURL, bootstrap, callerServiceID, "gen-"+app.NewRequestID(), "tr-"+app.NewRequestID(), "ai.llm.generate", map[string]any{
		"system_prompt": systemPrompt,
		"input":         fmt.Sprintf("surface_name=%s\n用户需求：%s", strings.TrimSpace(surfaceName), strings.TrimSpace(prompt)),
	})
	if err != nil || !result.Ok {
		return "", false
	}
	payload, _ := result.Result.(map[string]any)
	text, _ := payload["text"].(string)
	text = strings.TrimSpace(text)
	return text, text != ""
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
			CapabilitiesRequired: append([]string(nil), descriptor.CapabilitiesRequired...),
			AllowedCallerTypes:   append([]string(nil), descriptor.AllowedCallerTypes...),
			WSPath:               strings.TrimSpace(descriptor.WSPath),
			ScopeSupport:         append([]string(nil), descriptor.ScopeSupport...),
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

func surfaceStoragePath(claims app.SurfaceTokenClaims, cleanPath string) string {
	base := filepath.Join("surface", strings.TrimSpace(claims.UserID), strings.TrimSpace(claims.SurfaceID))
	if strings.TrimSpace(cleanPath) == "" || cleanPath == "." {
		return base
	}
	return filepath.Join(base, cleanPath)
}

func decodeDataBase64Result(result any) ([]byte, error) {
	value := decodeResultMap(result)["data_base64"]
	raw, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("data_base64 is missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func decodeItemsResult(result any) ([]map[string]any, error) {
	itemsRaw := decodeResultMap(result)["items"]
	switch tv := itemsRaw.(type) {
	case []map[string]any:
		return tv, nil
	case []any:
		out := make([]map[string]any, 0, len(tv))
		for _, item := range tv {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("items is missing")
	}
}

func decodeResultMap(result any) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	if m, ok := result.(map[string]any); ok {
		return m
	}
	raw, _ := json.Marshal(result)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
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
	httpRespBody, statusCode, err := hubsvc.PostHubToolCall(&http.Client{Timeout: 8 * time.Second}, hubToolCallURL, serviceAuth, callReq)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	var callResp toolproto.CallResponse
	if err := json.Unmarshal(httpRespBody, &callResp); err != nil {
		return toolproto.CallResponse{}, fmt.Errorf("decode hub tool response failed: %w", err)
	}
	if statusCode >= 300 && callResp.Error == nil {
		return toolproto.CallResponse{}, fmt.Errorf("hub tool call status=%d", statusCode)
	}
	return callResp, nil
}

func postHubToolCall(hubToolCallURL string, serviceAuth hubsvc.BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	return hubsvc.PostHubToolCall(&http.Client{Timeout: 5 * time.Second}, hubToolCallURL, serviceAuth, req)
}

func buildHubToolCallURL(registerURL string) string {
	return hubsvc.BuildHubToolCallURL(registerURL)
}

func startHubToolHeartbeatGuard(hubToolCallURL string, serviceID string, instanceID string, pid int, endpoint string, serviceAuth hubsvc.BootstrapSecret, onFailure func(reason string), statusFn func() string, healthyFn func() bool) {
	if strings.TrimSpace(hubToolCallURL) == "" || strings.TrimSpace(serviceID) == "" || strings.TrimSpace(instanceID) == "" || onFailure == nil {
		return
	}
	go func() {
		send := func() error {
			status := "ready"
			if statusFn != nil && strings.TrimSpace(statusFn()) != "" {
				status = strings.TrimSpace(statusFn())
			}
			healthy := true
			if healthyFn != nil {
				healthy = healthyFn()
			}
			callReq := toolproto.CallRequest{
				ToolID: "hub.governance.service.heartbeat",
				Args: map[string]any{
					"service_id":  strings.TrimSpace(serviceID),
					"instance_id": strings.TrimSpace(instanceID),
					"status":      status,
					"healthy":     healthy,
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
