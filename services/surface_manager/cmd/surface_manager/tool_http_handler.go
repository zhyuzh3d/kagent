package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/surface_manager/internal/app"
)

func handleSurfaceToolExec(w http.ResponseWriter, r *http.Request, manifest app.ServiceManifest, instance string, addr string, surfaceRoot string, hubToolCallURL string, serviceBootstrap hubsvc.BootstrapSecret, surfaceFS *app.SurfaceFSService, currentHealthy func() bool, currentStatus func() string, lastInitError func() string, storeGetter func() *app.HubStore) {
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
			"service_id":      strings.TrimSpace(manifest.ServiceID),
			"instance_id":     strings.TrimSpace(instance),
			"pid":             os.Getpid(),
			"endpoint":        "http://" + strings.TrimSpace(addr),
			"healthy":         currentHealthy(),
			"status":          currentStatus(),
			"ready":           currentStatus() == "ready",
			"initialized":     currentStatus() == "ready",
			"last_init_error": strings.TrimSpace(lastInitError()),
			"timestamp_ms":    time.Now().UnixMilli(),
		}
		writeToolResponse(w, http.StatusOK, resp)
		return
	}
	if currentStatus() != "ready" {
		writeToolResponse(w, http.StatusServiceUnavailable, toErrResp(toolproto.ErrorCodeServiceUnavailable, "service not initialized", true))
		return
	}
	store := storeGetter()
	if store == nil {
		writeToolResponse(w, http.StatusServiceUnavailable, toErrResp(toolproto.ErrorCodeServiceUnavailable, "service store unavailable", true))
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
	case "ui.surface.catalog_cleanup":
		if _, err := requireCallerUser(); err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		deletedIDs, err := store.CleanupDuplicateCatalogEntries(reqCtx)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "cleanup surface catalog failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"deleted_ids":   deletedIDs,
			"deleted_count": len(deletedIDs),
			"cleaned":       len(deletedIDs) > 0,
		}
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
}
