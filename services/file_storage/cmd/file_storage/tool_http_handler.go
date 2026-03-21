package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
	app "kagent/services/file_storage/internal/app"
)

func handleFileStorageToolExec(w http.ResponseWriter, r *http.Request, manifest app.ServiceManifest, instance string, addr string, serviceBootstrap hubsvc.BootstrapSecret, scopedFileService *app.ScopedFileService, blobService *app.BlobService, shutdownNow func(string)) {
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
	if req.ToolID == "service.lifecycle.health" {
		resp.Ok = true
		resp.Result = map[string]any{
			"service_id":   strings.TrimSpace(manifest.ServiceID),
			"instance_id":  strings.TrimSpace(instance),
			"pid":          os.Getpid(),
			"endpoint":     "http://" + strings.TrimSpace(addr),
			"healthy":      true,
			"status":       "ready",
			"timestamp_ms": time.Now().UnixMilli(),
		}
		writeToolResponse(w, http.StatusOK, resp)
		return
	}
	if req.ToolID == "service.lifecycle.state.get" {
		resp.Ok = true
		resp.Result = map[string]any{
			"service_id":   strings.TrimSpace(manifest.ServiceID),
			"instance_id":  strings.TrimSpace(instance),
			"pid":          os.Getpid(),
			"endpoint":     "http://" + strings.TrimSpace(addr),
			"healthy":      true,
			"status":       "ready",
			"timestamp_ms": time.Now().UnixMilli(),
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
	requireCallerUser := func() (string, error) {
		uid := strings.TrimSpace(caller.UserID)
		if uid == "" {
			return "", fmt.Errorf("missing caller user_id")
		}
		return uid, nil
	}

	switch req.ToolID {
	case "storage.file.read":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		raw, err := scopedFileService.ReadFile(target, pathValue)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file read failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"scope":       target.Scope,
			"path":        pathValue,
			"size_bytes":  len(raw),
			"data_base64": base64.StdEncoding.EncodeToString(raw),
		}
	case "storage.file.write":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		dataBase64 := asString(req.Args["data_base64"])
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeBadRequest, "invalid data_base64", false)
			break
		}
		size, err := scopedFileService.WriteFile(target, pathValue, raw)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file write failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"ok":      true,
			"scope":   target.Scope,
			"path":    pathValue,
			"written": size,
		}
	case "storage.file.list":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		if pathValue == "" {
			pathValue = "."
		}
		items, err := scopedFileService.List(target, pathValue)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file list failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"scope": target.Scope,
			"path":  pathValue,
			"items": items,
			"count": len(items),
		}
	case "storage.file.delete":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		recursive, _ := asBool(req.Args["recursive"])
		if err := scopedFileService.Delete(target, pathValue, recursive); err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file delete failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"ok":        true,
			"scope":     target.Scope,
			"path":      pathValue,
			"recursive": recursive,
		}
	case "storage.file.exists":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		exists, err := scopedFileService.Exists(target, pathValue)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file exists failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"scope":  target.Scope,
			"path":   pathValue,
			"exists": exists,
		}
	case "storage.file.stat":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		info, err := scopedFileService.Stat(target, pathValue)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file stat failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"scope":       target.Scope,
			"path":        pathValue,
			"name":        info.Name(),
			"is_dir":      info.IsDir(),
			"size":        info.Size(),
			"mod_time_ms": info.ModTime().UnixMilli(),
		}
	case "storage.file.mkdir":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		if err := scopedFileService.Mkdir(target, pathValue); err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file mkdir failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"ok":    true,
			"scope": target.Scope,
			"path":  pathValue,
		}
	case "storage.file.rename":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		newPathValue := asString(req.Args["new_path"])
		if newPathValue == "" {
			resp = toErrResp(toolproto.ErrorCodeBadRequest, "new_path is required", false)
			break
		}
		if err := scopedFileService.Rename(target, pathValue, newPathValue); err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file rename failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"ok":       true,
			"scope":    target.Scope,
			"old_path": pathValue,
			"new_path": newPathValue,
		}
	case "storage.file.copy":
		target, err := resolveTargetFromCaller(resolveScopedCaller())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		pathValue := asString(req.Args["path"])
		newPathValue := asString(req.Args["new_path"])
		if newPathValue == "" {
			resp = toErrResp(toolproto.ErrorCodeBadRequest, "new_path is required", false)
			break
		}
		size, err := scopedFileService.Copy(target, pathValue, newPathValue)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "storage file copy failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"ok":       true,
			"scope":    target.Scope,
			"old_path": pathValue,
			"new_path": newPathValue,
			"copied":   size,
		}
	case "storage.blob.put":
		userID, err := requireCallerUser()
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		dataBase64 := asString(req.Args["data_base64"])
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeBadRequest, "invalid data_base64", false)
			break
		}
		ttl := 24 * time.Hour
		ttlSec := asInt(req.Args["ttl_seconds"], 0)
		if ttlSec > 0 {
			ttl = time.Duration(ttlSec) * time.Second
		}
		meta, err := blobService.Put(userID, asString(req.Args["mime"]), raw, ttl)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "blob put failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"blob_id":       meta.BlobID,
			"size":          meta.Size,
			"sha256":        meta.SHA256,
			"mime":          meta.MIME,
			"expires_at_ms": meta.ExpiresAtMS,
		}
	case "storage.blob.get":
		userID, err := requireCallerUser()
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		blobID := asString(req.Args["blob_id"])
		raw, meta, err := blobService.Get(userID, blobID)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "blob get failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"blob_id":       meta.BlobID,
			"size":          meta.Size,
			"sha256":        meta.SHA256,
			"mime":          meta.MIME,
			"expires_at_ms": meta.ExpiresAtMS,
			"data_base64":   base64.StdEncoding.EncodeToString(raw),
		}
	case "storage.blob.sign_url":
		userID, err := requireCallerUser()
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeUnauthorized, err.Error(), false)
			break
		}
		blobID := asString(req.Args["blob_id"])
		ttl := 5 * time.Minute
		ttlSec := asInt(req.Args["ttl_seconds"], 0)
		if ttlSec > 0 {
			ttl = time.Duration(ttlSec) * time.Second
		}
		token, expMS, err := blobService.SignDownloadURL(userID, blobID, ttl)
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "blob sign url failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"blob_id":        blobID,
			"expires_at_ms":  expMS,
			"download_token": token,
		}
	case "storage.blob.gc":
		if strings.ToLower(strings.TrimSpace(caller.Type)) != "service" || strings.TrimSpace(caller.ServiceID) == "" {
			resp = toErrResp(toolproto.ErrorCodeForbidden, "blob gc requires service caller", false)
			break
		}
		deleted, err := blobService.GC(time.Now())
		if err != nil {
			resp = toErrResp(toolproto.ErrorCodeToolExecError, "blob gc failed: "+err.Error(), false)
			break
		}
		resp.Ok = true
		resp.Result = map[string]any{"ok": true, "deleted": deleted}
	case "service.lifecycle.shutdown":
		reason := asString(req.Args["reason"])
		if reason == "" {
			reason = "hub requested lifecycle shutdown"
		}
		resp.Ok = true
		resp.Result = map[string]any{
			"ok":      true,
			"message": "shutting down",
			"reason":  reason,
		}
		go func() {
			time.Sleep(20 * time.Millisecond)
			shutdownNow(reason)
		}()
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
