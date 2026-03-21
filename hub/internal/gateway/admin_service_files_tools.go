package gateway

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"kagent/pkg/toolproto"
)

func (h *AdminHandler) handleServiceFilesListTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	items, dir, err := lifecycle.ListServiceFiles(serviceID)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{"service_id": serviceID, "dir": dir, "items": items}}, nil
}

func (h *AdminHandler) handleServiceFilesReadTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	path, _ := req.Args["path"].(string)
	raw, resolved, err := lifecycle.ReadServiceFile(serviceID, path)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id":  serviceID,
		"path":        path,
		"resolved":    resolved,
		"data_base64": base64.StdEncoding.EncodeToString(raw),
	}}, nil
}

func (h *AdminHandler) handleServiceFilesWriteTool(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error) {
	if err := h.checkAuth(ctx); err != nil {
		return toolproto.CallResponse{}, err
	}
	lifecycle, err := h.requireLifecycle()
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	serviceID, err := h.requireServiceID(req.Args)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	path, _ := req.Args["path"].(string)
	dataBase64, _ := req.Args["data_base64"].(string)
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataBase64))
	if err != nil {
		return toolproto.CallResponse{}, fmt.Errorf("invalid data_base64")
	}
	resolved, err := lifecycle.WriteServiceFile(serviceID, path, raw)
	if err != nil {
		return toolproto.CallResponse{}, err
	}
	return toolproto.CallResponse{Ok: true, Result: map[string]any{
		"service_id": serviceID,
		"path":       path,
		"resolved":   resolved,
		"size_bytes": len(raw),
		"ok":         true,
	}}, nil
}
