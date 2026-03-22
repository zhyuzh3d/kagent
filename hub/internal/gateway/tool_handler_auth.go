package gateway

import (
	"fmt"
	"net/http"
	"strings"

	app "kagent/hub/internal/app"
	"kagent/pkg/hubsvc"
	"kagent/pkg/toolproto"
)

func (h *ToolHandler) resolveRequestIdentity(r *http.Request) app.Identity {
	identity := app.IdentityFromContext(r.Context())
	if identity.Type != app.IdentityAnonymous || h == nil || h.hubPlatform == nil {
		return identity
	}
	serviceID, instanceID, serviceAuth := hubsvc.ExtractServiceAuthHeaders(r.Header)
	if serviceID == "" && instanceID == "" && serviceAuth == "" {
		return identity
	}
	verified, err := h.hubPlatform.VerifyServiceAuth(serviceID, instanceID, serviceAuth)
	if err != nil {
		return identity
	}
	return app.Identity{
		Type:      app.IdentityService,
		ID:        strings.TrimSpace(verified.ServiceID),
		Name:      strings.TrimSpace(verified.ServiceID),
		ServiceID: strings.TrimSpace(verified.ServiceID),
	}
}

func (h *ToolHandler) resolveHubAuth(serviceID string, instanceID string) (string, string, error) {
	auth, ok := h.hubPlatform.ServiceHubAuth(serviceID)
	if !ok {
		return "", "", fmt.Errorf("missing service auth")
	}
	actualInstanceID := strings.TrimSpace(auth.InstanceID)
	expectedInstanceID := strings.TrimSpace(instanceID)
	if expectedInstanceID != "" && actualInstanceID != expectedInstanceID {
		return "", "", fmt.Errorf("service auth instance mismatch")
	}
	token := strings.TrimSpace(auth.H2SToken)
	if token == "" {
		return "", "", fmt.Errorf("missing hub auth token")
	}
	if actualInstanceID == "" {
		actualInstanceID = expectedInstanceID
	}
	return token, actualInstanceID, nil
}

func (h *ToolHandler) resolveOriginDelegation(caller toolproto.Caller, reqCtx *toolproto.Context, headerToken string) (toolproto.Caller, string, error) {
	origin := caller
	token := strings.TrimSpace(headerToken)
	if reqCtx != nil && strings.TrimSpace(reqCtx.OriginToken) != "" {
		token = strings.TrimSpace(reqCtx.OriginToken)
	}
	if strings.EqualFold(strings.TrimSpace(caller.Type), toolproto.CallerTypeService) && token != "" {
		claims, err := h.hubPlatform.VerifyOriginCallerToken(token, caller.ServiceID)
		if err != nil {
			return toolproto.Caller{}, "", fmt.Errorf("invalid origin caller token: %w", err)
		}
		origin = claims.OriginCaller
		token = strings.TrimSpace(token)
	}
	if strings.TrimSpace(origin.Type) == "" {
		origin = caller
	}
	return origin, token, nil
}

func findToolDescriptor(manifest app.ServiceManifest, toolID string) (app.ServiceToolDescriptor, bool) {
	target := strings.TrimSpace(toolID)
	if target == "" {
		return app.ServiceToolDescriptor{}, false
	}
	for _, tool := range manifest.Provides {
		if strings.TrimSpace(tool.ToolID) == target {
			return tool, true
		}
	}
	return app.ServiceToolDescriptor{}, false
}

func isCallerTypeAllowed(callerType string, allowedCallerTypes []string) bool {
	if len(allowedCallerTypes) == 0 {
		return true
	}
	target := strings.ToLower(strings.TrimSpace(callerType))
	for _, item := range allowedCallerTypes {
		if target == strings.ToLower(strings.TrimSpace(item)) {
			return true
		}
	}
	return false
}
