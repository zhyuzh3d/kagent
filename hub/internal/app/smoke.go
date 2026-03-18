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
)

// SmokeTester provides end-to-end validation of the Hub and its connected services.
type SmokeTester struct {
	BaseURL     string
	HTTPClient  *http.Client
	AuthService *AuthService
}

// NewSmokeTester creates a new SmokeTester instance.
func NewSmokeTester(hubAddr string, authSvc *AuthService) *SmokeTester {
	addr := strings.TrimSpace(hubAddr)
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return &SmokeTester{
		BaseURL: addr,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		AuthService: authSvc,
	}
}

// SmokeTestResult represents the outcome of a smoke test run.
type SmokeTestResult struct {
	Ok      bool     `json:"ok"`
	Message string   `json:"message"`
	Stages  []string `json:"stages"`
}

// Run executes a full suite of smoke tests.
func (s *SmokeTester) Run(ctx context.Context) (*SmokeTestResult, error) {
	res := &SmokeTestResult{Ok: true}
	
	// 1. Auth Register
	testUser := fmt.Sprintf("smoke_%d", time.Now().Unix())
	testPass := "SmokePass123!"
	
	if err := s.runStage(ctx, "auth.register", func() error {
		payload := map[string]string{
			"username": testUser,
			"password": testPass,
		}
		data, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/auth/register", bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
		
		resp, err := s.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("register status: %d", resp.StatusCode)
		}
		return nil
	}, res); err != nil {
		return res, nil
	}

	// 2. Auth Login (to get cookie)
	var cookies []*http.Cookie
	if err := s.runStage(ctx, "auth.login", func() error {
		payload := map[string]string{
			"username": testUser,
			"password": testPass,
		}
		data, _ := json.Marshal(payload)
		req, _ := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/auth/login", bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
		
		resp, err := s.HTTPClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("login status: %d", resp.StatusCode)
		}
		cookies = resp.Cookies()
		return nil
	}, res); err != nil {
		return res, nil
	}

	// 3. Tool Call: chat.project_list (verify service routing)
	if err := s.runStage(ctx, "tool.app.chat.project_list", func() error {
		return s.retryToolCall(ctx, "app.chat.project_list", cookies, 20)
	}, res); err != nil {
		return res, nil
	}

	// 4. Tool Call: database.schema (verify database routing)
	if err := s.runStage(ctx, "tool.storage.database.schema", func() error {
		return s.retryToolCall(ctx, "storage.database.schema", cookies, 10)
	}, res); err != nil {
		return res, nil
	}

	res.Message = "All smoke tests passed"
	return res, nil
}

func (s *SmokeTester) runStage(_ context.Context, name string, fn func() error, res *SmokeTestResult) error {
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

func (s *SmokeTester) retryToolCall(ctx context.Context, toolID string, cookies []*http.Cookie, attempts int) error {
	payload := map[string]any{
		"tool_id": toolID,
		"args":    map[string]any{},
	}
	data, _ := json.Marshal(payload)

	var lastErr error
	for i := 0; i < attempts; i++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/tool/call", bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
		for _, c := range cookies {
			req.AddCookie(c)
		}

		resp, err := s.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(200 * time.Millisecond)
			continue
		}
		
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var res struct {
				Ok bool `json:"ok"`
			}
			if err := json.Unmarshal(body, &res); err == nil && res.Ok {
				return nil
			}
			lastErr = fmt.Errorf("tool call body not ok: %s", string(body))
		} else {
			lastErr = fmt.Errorf("tool call status: %d", resp.StatusCode)
		}
		
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("after %d attempts: %v", attempts, lastErr)
}
