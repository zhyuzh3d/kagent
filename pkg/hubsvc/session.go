package hubsvc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"kagent/pkg/toolproto"
)

const (
	HeaderServiceID             = "X-Service-Id"
	HeaderServiceInstanceID     = "X-Service-Instance-Id"
	HeaderServiceAuth           = "X-Service-Auth"
	HeaderHubServiceID          = "X-Hub-Service-Id"
	HeaderHubInstanceID         = "X-Hub-Service-Instance-Id"
	HeaderHubAuth               = "X-Hub-Auth"
	HeaderCallerType            = "X-Caller-Type"
	HeaderCallerUserID          = "X-Caller-User-Id"
	HeaderCallerServiceID       = "X-Caller-Service-Id"
	HeaderCallerSurfaceID       = "X-Caller-Surface-Id"
	HeaderOriginCallerType      = "X-Origin-Caller-Type"
	HeaderOriginCallerUserID    = "X-Origin-Caller-User-Id"
	HeaderOriginCallerServiceID = "X-Origin-Caller-Service-Id"
	HeaderOriginCallerSurfaceID = "X-Origin-Caller-Surface-Id"
	HeaderOriginCallerToken     = "X-Origin-Caller-Token"
)

type originCallerContextKey struct{}
type originCallerTokenContextKey struct{}

type OriginCallerTokenClaims struct {
	OriginCaller       toolproto.Caller `json:"origin_caller"`
	IssuedAtMS         int64            `json:"issued_at_ms"`
	ExpiresAtMS        int64            `json:"expires_at_ms"`
	IssuedForServiceID string           `json:"issued_for_service_id,omitempty"`
	RequestID          string           `json:"request_id,omitempty"`
	TraceID            string           `json:"trace_id,omitempty"`
}

type ServiceIdentity struct {
	ServiceID  string
	InstanceID string
}

type BootstrapSecret struct {
	ServiceID      string `json:"service_id"`
	InstanceID     string `json:"instance_id"`
	HubRegisterURL string `json:"hub_register_url"`
	S2HToken       string `json:"s2h_token"`
	H2SToken       string `json:"h2s_token"`
	IssuedAtMS     int64  `json:"issued_at_ms"`
	ExpiresAtMS    int64  `json:"expires_at_ms"`
}

func LoadBootstrapSecret(path string) (BootstrapSecret, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return BootstrapSecret{}, err
	}
	secret, err := parseBootstrapSecret(raw)
	if err != nil {
		return BootstrapSecret{}, err
	}
	return secret, secret.Validate()
}

func DecodeSupervisorRegisterResult(responseBody []byte) (toolproto.SupervisorRegisterResult, error) {
	var callResp toolproto.CallResponse
	if err := json.Unmarshal(responseBody, &callResp); err != nil {
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("decode register response: %w", err)
	}
	if !callResp.Ok {
		msg := "register rejected"
		if callResp.Error != nil && strings.TrimSpace(callResp.Error.Message) != "" {
			msg = strings.TrimSpace(callResp.Error.Message)
		}
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("%s", msg)
	}
	rawResult, err := json.Marshal(callResp.Result)
	if err != nil {
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("marshal register result: %w", err)
	}
	var out toolproto.SupervisorRegisterResult
	if err := json.Unmarshal(rawResult, &out); err != nil {
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("decode register result: %w", err)
	}
	return out, nil
}

func BuildServiceRuntimeManifest(result toolproto.SupervisorRegisterResult) (toolproto.ServiceRuntimeManifest, error) {
	if result.RegisteredService == nil {
		return toolproto.ServiceRuntimeManifest{}, fmt.Errorf("registered service info missing")
	}
	item := result.RegisteredService
	return toolproto.ServiceRuntimeManifest{
		ServiceName:        strings.TrimSpace(item.ServiceName),
		ServiceID:          strings.TrimSpace(item.ServiceID),
		Version:            strings.TrimSpace(item.Version),
		BuildHash:          strings.TrimSpace(item.BuildHash),
		Reliability:        strings.TrimSpace(item.Reliability),
		Visibility:         strings.TrimSpace(item.Visibility),
		Registered:         strings.TrimSpace(item.ServiceID) != "",
		Active:             strings.EqualFold(strings.TrimSpace(item.Status), "active"),
		InstanceID:         strings.TrimSpace(item.InstanceID),
		PID:                item.PID,
		Endpoint:           strings.TrimSpace(item.Endpoint),
		Status:             strings.TrimSpace(item.Status),
		Healthy:            item.Healthy,
		ManifestHash:       strings.TrimSpace(item.ManifestHash),
		ToolCount:          item.ToolCount,
		RegisteredAtMS:     item.RegisteredAtMS,
		LastSeenAtMS:       item.LastSeenAtMS,
		RegisteredManifest: toolproto.NormalizeServiceManifest(item.RegisteredManifest),
	}, nil
}

func WriteServiceRuntimeManifest(path string, result toolproto.SupervisorRegisterResult) error {
	manifest, err := BuildServiceRuntimeManifest(result)
	if err != nil {
		return err
	}
	fullPath := strings.TrimSpace(path)
	if fullPath == "" {
		return fmt.Errorf("runtime manifest path is empty")
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, append(raw, '\n'), 0o644)
}

func BuildHubToolCallURL(raw string) string {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return ""
	}
	parsed, err := url.Parse(clean)
	if err != nil {
		return clean
	}
	parsed.Path = "/api/tool/call"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func WriteBootstrapSecret(path string, secret BootstrapSecret) error {
	clean := secret.normalize()
	if err := clean.validateRequired(); err != nil {
		return err
	}
	lines := []string{
		"SERVICE_ID=" + clean.ServiceID,
		"INSTANCE_ID=" + clean.InstanceID,
		"HUB_REGISTER_URL=" + clean.HubRegisterURL,
		"S2H_TOKEN=" + clean.S2HToken,
		"H2S_TOKEN=" + clean.H2SToken,
		"ISSUED_AT_MS=" + strconv.FormatInt(clean.IssuedAtMS, 10),
		"EXPIRES_AT_MS=" + strconv.FormatInt(clean.ExpiresAtMS, 10),
	}
	content := strings.Join(lines, "\n") + "\n"
	fullPath := strings.TrimSpace(path)
	if fullPath == "" {
		return fmt.Errorf("bootstrap secret path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(fullPath, []byte(content), 0o600)
}

func DeleteBootstrapSecret(path string) error {
	fullPath := strings.TrimSpace(path)
	if fullPath == "" {
		return nil
	}
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ApplyServiceAuthHeaders(headers http.Header, secret BootstrapSecret) {
	if headers == nil {
		return
	}
	clean := secret.normalize()
	headers.Set(HeaderServiceID, clean.ServiceID)
	headers.Set(HeaderServiceInstanceID, clean.InstanceID)
	headers.Set(HeaderServiceAuth, clean.S2HToken)
}

func ApplyHubAuthHeaders(headers http.Header, serviceID string, instanceID string, hubToken string) {
	if headers == nil {
		return
	}
	headers.Set(HeaderHubServiceID, strings.TrimSpace(serviceID))
	headers.Set(HeaderHubInstanceID, strings.TrimSpace(instanceID))
	headers.Set(HeaderHubAuth, strings.TrimSpace(hubToken))
}

func VerifyHubAuthHeaders(headers http.Header, expectedServiceID string, expectedInstanceID string, expectedHubToken string) error {
	if headers == nil {
		return fmt.Errorf("missing headers")
	}
	hubAuth := strings.TrimSpace(headers.Get(HeaderHubAuth))
	if hubAuth == "" || hubAuth != strings.TrimSpace(expectedHubToken) {
		return fmt.Errorf("invalid hub auth")
	}
	hubServiceID := strings.TrimSpace(headers.Get(HeaderHubServiceID))
	if hubServiceID == "" || hubServiceID != strings.TrimSpace(expectedServiceID) {
		return fmt.Errorf("invalid hub service id")
	}
	headerInstanceID := strings.TrimSpace(headers.Get(HeaderHubInstanceID))
	if strings.TrimSpace(expectedInstanceID) != "" && headerInstanceID != strings.TrimSpace(expectedInstanceID) {
		return fmt.Errorf("invalid hub service instance id")
	}
	return nil
}

func RequireHubAuth(headers http.Header, ident ServiceIdentity, expectedHubToken string) error {
	return VerifyHubAuthHeaders(headers, ident.ServiceID, ident.InstanceID, expectedHubToken)
}

func CallerFromHeaders(headers http.Header) toolproto.Caller {
	if headers == nil {
		return toolproto.Caller{}
	}
	return toolproto.Caller{
		Type:      strings.ToLower(strings.TrimSpace(headers.Get(HeaderCallerType))),
		UserID:    strings.TrimSpace(headers.Get(HeaderCallerUserID)),
		ServiceID: strings.TrimSpace(headers.Get(HeaderCallerServiceID)),
		SurfaceID: strings.TrimSpace(headers.Get(HeaderCallerSurfaceID)),
	}
}

func OriginCallerFromHeaders(headers http.Header) toolproto.Caller {
	if headers == nil {
		return toolproto.Caller{}
	}
	return toolproto.Caller{
		Type:      strings.ToLower(strings.TrimSpace(headers.Get(HeaderOriginCallerType))),
		UserID:    strings.TrimSpace(headers.Get(HeaderOriginCallerUserID)),
		ServiceID: strings.TrimSpace(headers.Get(HeaderOriginCallerServiceID)),
		SurfaceID: strings.TrimSpace(headers.Get(HeaderOriginCallerSurfaceID)),
	}
}

func OriginCallerTokenFromHeaders(headers http.Header) string {
	if headers == nil {
		return ""
	}
	return strings.TrimSpace(headers.Get(HeaderOriginCallerToken))
}

func MergeCaller(req *toolproto.CallRequest, headers http.Header) toolproto.Caller {
	if req == nil {
		return CallerFromHeaders(headers)
	}
	if req.Context == nil {
		req.Context = &toolproto.Context{}
	}
	caller := CallerFromHeaders(headers)
	if caller.Type == "" {
		caller = req.Context.Caller
	}
	req.Context.Caller = caller
	return caller
}

func MergeOriginCaller(req *toolproto.CallRequest, headers http.Header) (toolproto.Caller, string) {
	if req == nil {
		return OriginCallerFromHeaders(headers), OriginCallerTokenFromHeaders(headers)
	}
	if req.Context == nil {
		req.Context = &toolproto.Context{}
	}
	origin := OriginCallerFromHeaders(headers)
	if origin.Type == "" {
		origin = req.Context.OriginCaller
	}
	token := OriginCallerTokenFromHeaders(headers)
	if token == "" {
		token = req.Context.OriginToken
	}
	req.Context.OriginCaller = origin
	req.Context.OriginToken = token
	return origin, token
}

func ContextWithDelegation(ctx context.Context, origin toolproto.Caller, token string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, originCallerContextKey{}, normalizeCaller(origin))
	ctx = context.WithValue(ctx, originCallerTokenContextKey{}, strings.TrimSpace(token))
	return ctx
}

func OriginCallerFromContext(ctx context.Context) toolproto.Caller {
	if ctx == nil {
		return toolproto.Caller{}
	}
	if value, ok := ctx.Value(originCallerContextKey{}).(toolproto.Caller); ok {
		return normalizeCaller(value)
	}
	return toolproto.Caller{}
}

func OriginTokenFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if value, ok := ctx.Value(originCallerTokenContextKey{}).(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func AttachDelegationFromContext(req *toolproto.Context, ctx context.Context) {
	if req == nil {
		return
	}
	if origin := OriginCallerFromContext(ctx); origin.Type != "" && req.OriginCaller.Type == "" {
		req.OriginCaller = origin
	}
	if token := OriginTokenFromContext(ctx); token != "" && req.OriginToken == "" {
		req.OriginToken = token
	}
}

func ApplyDelegationHeaders(headers http.Header, ctx context.Context) {
	if headers == nil {
		return
	}
	origin := OriginCallerFromContext(ctx)
	headers.Set(HeaderOriginCallerType, origin.Type)
	headers.Set(HeaderOriginCallerUserID, origin.UserID)
	headers.Set(HeaderOriginCallerServiceID, origin.ServiceID)
	headers.Set(HeaderOriginCallerSurfaceID, origin.SurfaceID)
	headers.Set(HeaderOriginCallerToken, OriginTokenFromContext(ctx))
}

func SignOriginCallerToken(secret []byte, claims OriginCallerTokenClaims) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("origin caller secret is empty")
	}
	claims.OriginCaller = normalizeCaller(claims.OriginCaller)
	if strings.TrimSpace(claims.OriginCaller.Type) == "" {
		return "", fmt.Errorf("origin caller is empty")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal origin caller claims: %w", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(encodedPayload))
	encodedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + encodedSig, nil
}

func VerifyOriginCallerToken(secret []byte, token string) (OriginCallerTokenClaims, error) {
	clean := strings.TrimSpace(token)
	if len(secret) == 0 {
		return OriginCallerTokenClaims{}, fmt.Errorf("origin caller secret is empty")
	}
	if clean == "" {
		return OriginCallerTokenClaims{}, fmt.Errorf("origin caller token is empty")
	}
	parts := strings.Split(clean, ".")
	if len(parts) != 2 {
		return OriginCallerTokenClaims{}, fmt.Errorf("invalid origin caller token format")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(parts[0]))
	expectedSig := mac.Sum(nil)
	rawSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return OriginCallerTokenClaims{}, fmt.Errorf("invalid origin caller token signature")
	}
	if !hmac.Equal(rawSig, expectedSig) {
		return OriginCallerTokenClaims{}, fmt.Errorf("origin caller token signature mismatch")
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return OriginCallerTokenClaims{}, fmt.Errorf("invalid origin caller token payload")
	}
	var claims OriginCallerTokenClaims
	if err := json.Unmarshal(rawPayload, &claims); err != nil {
		return OriginCallerTokenClaims{}, fmt.Errorf("invalid origin caller token claims")
	}
	claims.OriginCaller = normalizeCaller(claims.OriginCaller)
	if claims.OriginCaller.Type == "" {
		return OriginCallerTokenClaims{}, fmt.Errorf("origin caller token missing caller")
	}
	if claims.ExpiresAtMS > 0 && claims.ExpiresAtMS < time.Now().UnixMilli() {
		return OriginCallerTokenClaims{}, fmt.Errorf("origin caller token expired")
	}
	return claims, nil
}

func normalizeCaller(caller toolproto.Caller) toolproto.Caller {
	caller.Type = strings.ToLower(strings.TrimSpace(caller.Type))
	caller.UserID = strings.TrimSpace(caller.UserID)
	caller.ServiceID = strings.TrimSpace(caller.ServiceID)
	caller.SurfaceID = strings.TrimSpace(caller.SurfaceID)
	return caller
}

func LifecycleMeta(req toolproto.CallRequest, ident ServiceIdentity) toolproto.Meta {
	ctx := req.Context
	if ctx == nil {
		ctx = &toolproto.Context{}
	}
	return toolproto.Meta{
		RequestID:  strings.TrimSpace(ctx.RequestID),
		TraceID:    strings.TrimSpace(ctx.TraceID),
		ServiceID:  strings.TrimSpace(ident.ServiceID),
		InstanceID: strings.TrimSpace(ident.InstanceID),
	}
}

func PostHubToolCall(client *http.Client, toolCallURL string, secret BootstrapSecret, req toolproto.CallRequest) ([]byte, int, error) {
	rawReq, err := json.Marshal(req)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal hub tool call: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, strings.TrimSpace(toolCallURL), bytes.NewReader(rawReq))
	if err != nil {
		return nil, 0, fmt.Errorf("build hub tool call request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	ApplyServiceAuthHeaders(httpReq.Header, secret)
	cli := client
	if cli == nil {
		cli = http.DefaultClient
	}
	httpResp, err := cli.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("execute hub tool call: %w", err)
	}
	defer httpResp.Body.Close()
	body, readErr := io.ReadAll(httpResp.Body)
	if readErr != nil {
		return nil, httpResp.StatusCode, fmt.Errorf("read hub tool call body: %w", readErr)
	}
	return body, httpResp.StatusCode, nil
}

func ExtractServiceAuthHeaders(headers http.Header) (string, string, string) {
	if headers == nil {
		return "", "", ""
	}
	return strings.TrimSpace(headers.Get(HeaderServiceID)),
		strings.TrimSpace(headers.Get(HeaderServiceInstanceID)),
		strings.TrimSpace(headers.Get(HeaderServiceAuth))
}

func (s BootstrapSecret) normalize() BootstrapSecret {
	return BootstrapSecret{
		ServiceID:      strings.TrimSpace(s.ServiceID),
		InstanceID:     strings.TrimSpace(s.InstanceID),
		HubRegisterURL: strings.TrimSpace(s.HubRegisterURL),
		S2HToken:       strings.TrimSpace(s.S2HToken),
		H2SToken:       strings.TrimSpace(s.H2SToken),
		IssuedAtMS:     s.IssuedAtMS,
		ExpiresAtMS:    s.ExpiresAtMS,
	}
}

func (s BootstrapSecret) Validate() error {
	clean := s.normalize()
	if err := clean.validateRequired(); err != nil {
		return err
	}
	if clean.ExpiresAtMS <= clean.IssuedAtMS {
		return fmt.Errorf("bootstrap secret expired_at is invalid")
	}
	if clean.ExpiresAtMS <= nowMS() {
		return fmt.Errorf("bootstrap secret is expired")
	}
	return nil
}

func (s BootstrapSecret) validateRequired() error {
	if strings.TrimSpace(s.ServiceID) == "" {
		return fmt.Errorf("bootstrap secret missing SERVICE_ID")
	}
	if strings.TrimSpace(s.InstanceID) == "" {
		return fmt.Errorf("bootstrap secret missing INSTANCE_ID")
	}
	if strings.TrimSpace(s.HubRegisterURL) == "" {
		return fmt.Errorf("bootstrap secret missing HUB_REGISTER_URL")
	}
	if strings.TrimSpace(s.S2HToken) == "" {
		return fmt.Errorf("bootstrap secret missing S2H_TOKEN")
	}
	if strings.TrimSpace(s.H2SToken) == "" {
		return fmt.Errorf("bootstrap secret missing H2S_TOKEN")
	}
	if strings.TrimSpace(s.S2HToken) == strings.TrimSpace(s.H2SToken) {
		return fmt.Errorf("bootstrap secret requires distinct S2H/H2S tokens")
	}
	if s.IssuedAtMS <= 0 || s.ExpiresAtMS <= 0 {
		return fmt.Errorf("bootstrap secret missing issue/expire time")
	}
	return nil
}

func parseBootstrapSecret(raw []byte) (BootstrapSecret, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return BootstrapSecret{}, fmt.Errorf("invalid bootstrap secret line: %q", line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		values[key] = val
	}
	if err := scanner.Err(); err != nil {
		return BootstrapSecret{}, err
	}

	issuedAtMS, err := parseInt64Field(values["ISSUED_AT_MS"], "ISSUED_AT_MS")
	if err != nil {
		return BootstrapSecret{}, err
	}
	expiresAtMS, err := parseInt64Field(values["EXPIRES_AT_MS"], "EXPIRES_AT_MS")
	if err != nil {
		return BootstrapSecret{}, err
	}

	return BootstrapSecret{
		ServiceID:      values["SERVICE_ID"],
		InstanceID:     values["INSTANCE_ID"],
		HubRegisterURL: values["HUB_REGISTER_URL"],
		S2HToken:       values["S2H_TOKEN"],
		H2SToken:       values["H2S_TOKEN"],
		IssuedAtMS:     issuedAtMS,
		ExpiresAtMS:    expiresAtMS,
	}.normalize(), nil
}

func parseInt64Field(raw string, field string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("bootstrap secret missing %s", field)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bootstrap secret invalid %s: %w", field, err)
	}
	return parsed, nil
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

// Listen creates a TCP listener with SO_REUSEADDR (and SO_REUSEPORT if available) enabled.
func Listen(addr string) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var err error
			c.Control(func(fd uintptr) {
				err = setReuseAddrPort(fd)
			})
			return err
		},
	}
	return lc.Listen(context.Background(), "tcp", addr)
}
