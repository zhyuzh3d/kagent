package hubsvc

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"kagent/pkg/toolproto"
)

type ServiceSessionClaims struct {
	ServiceID  string `json:"service_id"`
	InstanceID string `json:"instance_id"`
	ExpMS      int64  `json:"exp_ms"`
	Nonce      string `json:"nonce"`
}

func LoadServiceSecret(path string) ([]byte, error) {
	raw, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	if len(decoded) < 32 {
		return nil, fmt.Errorf("invalid service secret length")
	}
	return decoded, nil
}

func VerifyServiceSessionTokenLoose(token string, serviceSecret []byte) (ServiceSessionClaims, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return ServiceSessionClaims{}, fmt.Errorf("invalid service token")
	}
	raw := parts[0]
	sig := parts[1]
	expectSig, err := signRawToken(raw, serviceSecret)
	if err != nil {
		return ServiceSessionClaims{}, err
	}
	if sig != expectSig {
		return ServiceSessionClaims{}, fmt.Errorf("invalid service token signature")
	}
	payload, err := decodeRawToken(raw)
	if err != nil {
		return ServiceSessionClaims{}, err
	}
	if payload.ExpMS <= time.Now().UnixMilli() {
		return ServiceSessionClaims{}, fmt.Errorf("service token expired")
	}
	if strings.TrimSpace(payload.ServiceID) == "" || strings.TrimSpace(payload.InstanceID) == "" {
		return ServiceSessionClaims{}, fmt.Errorf("service token missing claims")
	}
	return payload, nil
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
	if strings.TrimSpace(out.ServiceSessionToken) == "" {
		return toolproto.SupervisorRegisterResult{}, fmt.Errorf("missing service_session_token")
	}
	return out, nil
}

func signRawToken(raw string, serviceSecret []byte) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("empty service token payload")
	}
	if len(serviceSecret) == 0 {
		return "", fmt.Errorf("empty service secret")
	}
	mac := hmac.New(sha256.New, serviceSecret)
	_, _ = mac.Write([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeRawToken(raw string) (ServiceSessionClaims, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return ServiceSessionClaims{}, fmt.Errorf("decode service token payload: %w", err)
	}
	var claims ServiceSessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ServiceSessionClaims{}, fmt.Errorf("decode service token claims: %w", err)
	}
	return claims, nil
}
