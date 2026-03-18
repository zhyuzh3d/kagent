package hubsvc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"kagent/pkg/toolproto"
)

const (
	HeaderServiceID         = "X-Service-Id"
	HeaderServiceInstanceID = "X-Service-Instance-Id"
	HeaderServiceAuth       = "X-Service-Auth"
	HeaderHubServiceID      = "X-Hub-Service-Id"
	HeaderHubInstanceID     = "X-Hub-Service-Instance-Id"
	HeaderHubAuth           = "X-Hub-Auth"
)

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
