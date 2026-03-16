package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Endpoint struct {
	Transport string `json:"transport"`
	UDSPath   string `json:"uds_path,omitempty"`
	TCPURL    string `json:"tcp_url,omitempty"`
}

type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type Client struct {
	mu sync.RWMutex

	allowTCPFallback bool
	tcpClient        *http.Client
	udsClients       map[string]*http.Client
}

func NewClient(allowTCPFallback bool) *Client {
	return &Client{
		allowTCPFallback: allowTCPFallback,
		tcpClient: &http.Client{
			Timeout: 70 * time.Second,
		},
		udsClients: map[string]*http.Client{},
	}
}

func (c *Client) Call(ctx context.Context, endpoint Endpoint, method string, path string, headers http.Header, body []byte, timeout time.Duration) (Response, error) {
	if c == nil {
		return Response{}, fmt.Errorf("transport client is nil")
	}
	if timeout <= 0 {
		timeout = 70 * time.Second
	}
	switch strings.ToLower(strings.TrimSpace(endpoint.Transport)) {
	case "uds":
		response, err := c.callUDS(ctx, endpoint, method, path, headers, body, timeout)
		if err == nil {
			return response, nil
		}
		if c.allowTCPFallback && strings.TrimSpace(endpoint.TCPURL) != "" {
			return c.callTCP(ctx, endpoint.TCPURL, method, path, headers, body, timeout)
		}
		return Response{}, err
	case "tcp", "":
		return c.callTCP(ctx, endpoint.TCPURL, method, path, headers, body, timeout)
	default:
		return Response{}, fmt.Errorf("unsupported transport: %s", endpoint.Transport)
	}
}

func (c *Client) callTCP(ctx context.Context, baseURL string, method string, path string, headers http.Header, body []byte, timeout time.Duration) (Response, error) {
	targetURL, err := buildTargetURL(baseURL, path)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header = headers.Clone()
	client := *c.tcpClient
	client.Timeout = timeout
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       raw,
	}, nil
}

func (c *Client) callUDS(ctx context.Context, endpoint Endpoint, method string, path string, headers http.Header, body []byte, timeout time.Duration) (Response, error) {
	socketPath := strings.TrimSpace(endpoint.UDSPath)
	if socketPath == "" {
		return Response{}, fmt.Errorf("uds_path is empty")
	}
	client := c.getUDSClient(socketPath, timeout)
	targetURL := "http://unix" + ensurePrefixSlash(path)
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header = headers.Clone()
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return Response{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header.Clone(),
		Body:       raw,
	}, nil
}

func (c *Client) getUDSClient(socketPath string, timeout time.Duration) *http.Client {
	c.mu.RLock()
	cached, ok := c.udsClients[socketPath]
	c.mu.RUnlock()
	if ok {
		client := *cached
		client.Timeout = timeout
		return &client
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	c.mu.Lock()
	c.udsClients[socketPath] = client
	c.mu.Unlock()
	return client
}

func buildTargetURL(baseURL string, path string) (string, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return "", fmt.Errorf("tcp_url is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid tcp_url: %w", err)
	}
	parsed.Path = joinURLPath(parsed.Path, path)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func joinURLPath(basePath string, extraPath string) string {
	left := strings.TrimSuffix(strings.TrimSpace(basePath), "/")
	right := strings.TrimPrefix(strings.TrimSpace(extraPath), "/")
	switch {
	case left == "" && right == "":
		return "/"
	case left == "":
		return "/" + right
	case right == "":
		return left
	default:
		return left + "/" + right
	}
}

func ensurePrefixSlash(path string) string {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "/"
	}
	if strings.HasPrefix(clean, "/") {
		return clean
	}
	return "/" + clean
}
