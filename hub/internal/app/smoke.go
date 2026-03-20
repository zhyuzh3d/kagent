package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"kagent/pkg/toolproto"
)

// SmokeTester provides end-to-end validation of the Hub and its connected services.
type SmokeTester struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewSmokeTester creates a new SmokeTester instance.
func NewSmokeTester(hubAddr string) *SmokeTester {
	addr := strings.TrimSpace(hubAddr)
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return &SmokeTester{
		BaseURL: addr,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// SmokeTestResult represents the outcome of a smoke test run.
type SmokeTestResult struct {
	Ok        bool     `json:"ok"`
	Message   string   `json:"message"`
	Stages    []string `json:"stages"`
	Username  string   `json:"username,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
	ThreadID  string   `json:"thread_id,omitempty"`
}

// Run executes a full suite of smoke tests.
func (s *SmokeTester) Run(ctx context.Context) (*SmokeTestResult, error) {
	res := &SmokeTestResult{Ok: true}

	testUser := fmt.Sprintf("smoke_%d", time.Now().UnixNano())
	testPass := "SmokePass123!"
	projectTitle := fmt.Sprintf("Smoke Project %d", time.Now().Unix())
	threadTitle := fmt.Sprintf("Smoke Thread %d", time.Now().Unix())
	var currentToken string
	var previousToken string
	var projectID string
	var threadID string
	res.Username = testUser

	if err := s.runStage("account.auth.register", func() error {
		resp, statusCode, cookies, err := s.callTool(ctx, "account.auth.register", map[string]any{
			"username": testUser,
			"password": testPass,
		}, "")
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK || !resp.Ok {
			return fmt.Errorf("register status=%d ok=%v", statusCode, resp.Ok)
		}
		currentToken = pickCookieValue(cookies, AccountTokenCookieName)
		if strings.TrimSpace(currentToken) == "" {
			return fmt.Errorf("register missing %s cookie", AccountTokenCookieName)
		}
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("account.auth.me", func() error {
		resp, statusCode, _, err := s.callTool(ctx, "account.auth.me", map[string]any{}, currentToken)
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK || !resp.Ok {
			return fmt.Errorf("me status=%d ok=%v", statusCode, resp.Ok)
		}
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("account.auth.login#1", func() error {
		resp, statusCode, cookies, err := s.callTool(ctx, "account.auth.login", map[string]any{
			"username": testUser,
			"password": testPass,
		}, "")
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK || !resp.Ok {
			return fmt.Errorf("login1 status=%d ok=%v", statusCode, resp.Ok)
		}
		token := pickCookieValue(cookies, AccountTokenCookieName)
		if strings.TrimSpace(token) == "" {
			return fmt.Errorf("login1 missing %s cookie", AccountTokenCookieName)
		}
		previousToken = token
		currentToken = token
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("account.auth.login#2", func() error {
		resp, statusCode, cookies, err := s.callTool(ctx, "account.auth.login", map[string]any{
			"username": testUser,
			"password": testPass,
		}, "")
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK || !resp.Ok {
			return fmt.Errorf("login2 status=%d ok=%v", statusCode, resp.Ok)
		}
		token := pickCookieValue(cookies, AccountTokenCookieName)
		if strings.TrimSpace(token) == "" {
			return fmt.Errorf("login2 missing %s cookie", AccountTokenCookieName)
		}
		if token == previousToken {
			return fmt.Errorf("login2 should issue a new token")
		}
		currentToken = token
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("account.sso.old-token-rejected", func() error {
		resp, statusCode, _, err := s.callTool(ctx, "account.auth.me", map[string]any{}, previousToken)
		if err != nil {
			return err
		}
		if statusCode != http.StatusUnauthorized {
			return fmt.Errorf("expected 401 for stale token, got status=%d ok=%v err=%v", statusCode, resp.Ok, resp.Error)
		}
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("tool.app.chat.project_list", func() error {
		return s.retryToolCall(ctx, "app.chat.project_list", map[string]any{}, currentToken, 20)
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("tool.app.chat.project_create", func() error {
		resp, statusCode, _, err := s.callTool(ctx, "app.chat.project_create", map[string]any{
			"title": projectTitle,
		}, currentToken)
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK || !resp.Ok {
			return fmt.Errorf("project_create status=%d ok=%v err=%v", statusCode, resp.Ok, resp.Error)
		}
		projectID = strings.TrimSpace(resultStringField(resp.Result, "project_id"))
		if projectID == "" {
			return fmt.Errorf("project_create missing project_id")
		}
		res.ProjectID = projectID
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("tool.app.chat.thread_create", func() error {
		resp, statusCode, _, err := s.callTool(ctx, "app.chat.thread_create", map[string]any{
			"project_id": projectID,
			"title":      threadTitle,
		}, currentToken)
		if err != nil {
			return err
		}
		if statusCode != http.StatusOK || !resp.Ok {
			return fmt.Errorf("thread_create status=%d ok=%v err=%v", statusCode, resp.Ok, resp.Error)
		}
		threadID = strings.TrimSpace(resultStringField(resp.Result, "thread_id"))
		if threadID == "" {
			return fmt.Errorf("thread_create missing thread_id")
		}
		res.ThreadID = threadID
		return nil
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("tool.app.chat.thread_list", func() error {
		return s.retryToolCall(ctx, "app.chat.thread_list", map[string]any{
			"project_id": projectID,
		}, currentToken, 20)
	}, res); err != nil {
		return res, nil
	}

	if err := s.runStage("tool.storage.database.schema", func() error {
		return s.retryToolCall(ctx, "storage.database.schema", map[string]any{}, currentToken, 10)
	}, res); err != nil {
		return res, nil
	}

	res.Message = "All smoke tests passed"
	return res, nil
}

func (s *SmokeTester) runStage(name string, fn func() error, res *SmokeTestResult) error {
	Infof("System:Internal:SmokeTest:Stage: %s", name)
	if err := fn(); err != nil {
		res.Ok = false
		res.Message = fmt.Sprintf("Stage %s failed: %v", name, err)
		res.Stages = append(res.Stages, fmt.Sprintf("%s: FAIL (%v)", name, err))
		Errorf("System:Internal:SmokeTest:Error: %s -> %v", name, err)
		return err
	}
	res.Stages = append(res.Stages, fmt.Sprintf("%s: OK", name))
	return nil
}

func (s *SmokeTester) retryToolCall(ctx context.Context, toolID string, args map[string]any, token string, attempts int) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		resp, statusCode, _, err := s.callTool(ctx, toolID, args, token)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if statusCode == http.StatusOK && resp.Ok {
			return nil
		}
		lastErr = fmt.Errorf("status=%d ok=%v err=%v", statusCode, resp.Ok, resp.Error)
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("after %d attempts: %v", attempts, lastErr)
}

func (s *SmokeTester) callTool(ctx context.Context, toolID string, args map[string]any, token string) (toolproto.CallResponse, int, []*http.Cookie, error) {
	payload := toolproto.CallRequest{
		ToolID: toolID,
		Args:   args,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return toolproto.CallResponse{}, 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/api/tool/call", bytes.NewReader(body))
	if err != nil {
		return toolproto.CallResponse{}, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(token) != "" {
		req.AddCookie(&http.Cookie{
			Name:  AccountTokenCookieName,
			Value: token,
			Path:  "/",
		})
	}
	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return toolproto.CallResponse{}, 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out toolproto.CallResponse
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return toolproto.CallResponse{}, resp.StatusCode, resp.Cookies(), fmt.Errorf("decode response failed: %w", err)
		}
	}
	return out, resp.StatusCode, resp.Cookies(), nil
}

func pickCookieValue(cookies []*http.Cookie, name string) string {
	target := strings.TrimSpace(name)
	if target == "" {
		return ""
	}
	for _, cookie := range cookies {
		if strings.TrimSpace(cookie.Name) == target {
			return strings.TrimSpace(cookie.Value)
		}
	}
	return ""
}

func resultStringField(result any, key string) string {
	payload, ok := result.(map[string]any)
	if !ok {
		return ""
	}
	value, _ := payload[strings.TrimSpace(key)].(string)
	return value
}
