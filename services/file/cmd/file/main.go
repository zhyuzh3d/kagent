package main

import (
	"bytes"
	"context"
	"encoding/base64"
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
	app "kagent/services/file/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18084", "listen addr")
	hubRegisterURL := flag.String("hub-register-url", "", "optional hub register endpoint")
	instanceID := flag.String("instance-id", "", "optional service instance id")
	flag.Parse()

	app.InitLogger(app.LevelDebug, "FILE")

	appRoot, rootErr := app.DetectAppRoot()
	if rootErr != nil {
		app.Warnf("detect app root fallback: %v", rootErr)
	}
	dataRoot := filepath.Join(appRoot, "data")
	serviceSecretPath := filepath.Join(appRoot, "services", "file", "run", ".service_secret")
	serviceBootstrap, err := hubsvc.LoadBootstrapSecret(serviceSecretPath)
	if err != nil {
		app.Errorf("load bootstrap secret failed: %v", err)
		os.Exit(1)
	}

	scopedFileService, err := app.NewScopedFileService(dataRoot)
	if err != nil {
		app.Errorf("init scoped file service failed: %v", err)
		os.Exit(1)
	}
	blobService, err := app.NewBlobService(dataRoot)
	if err != nil {
		app.Errorf("init blob service failed: %v", err)
		os.Exit(1)
	}

	manifest := builtinManifest("file")
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
		instance = "file-" + app.NewRequestID()
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
			app.Errorf("register file service to hub failed: %v", err)
			os.Exit(1)
		}
		rawResp, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= 300 {
			app.Errorf("register file service to hub status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(rawResp)))
			os.Exit(1)
		}
		if _, err := hubsvc.DecodeSupervisorRegisterResult(rawResp); err != nil {
			app.Errorf("decode register response failed: %v", err)
			os.Exit(1)
		}
		if err := hubsvc.DeleteBootstrapSecret(serviceSecretPath); err != nil {
			app.Warnf("delete bootstrap secret failed: %v", err)
		}
		app.Infof("register file service to hub status=%d", resp.StatusCode)
	}

	mux := http.NewServeMux()
	var server *http.Server
	var shutdownOnce sync.Once
	shutdownNow := func(reason string) {
		shutdownOnce.Do(func() {
			app.Warnf("file-service shutdown: %s", strings.TrimSpace(reason))
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
		writeJSON(w, app.AIServiceInfo{ServiceID: manifest.ServiceID, ServiceName: manifest.ServiceName, Version: manifest.Version, Provider: "file", Capabilities: caps, Transport: "http"})
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
		requireCallerUser := func() (string, error) {
			uid := strings.TrimSpace(caller.UserID)
			if uid == "" {
				return "", fmt.Errorf("missing caller user_id")
			}
			return uid, nil
		}

		switch req.ToolID {
		case "storage.file.read":
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
			target, err := resolveTargetFromCaller(caller)
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
	app.Infof("file service listening=http://%s", *addr)
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
