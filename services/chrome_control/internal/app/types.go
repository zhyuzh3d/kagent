package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
)

type Config struct {
	ChromeExecutableCandidates  []string     `json:"chrome_executable_candidates"`
	DefaultMode                 string       `json:"default_mode"`
	DefaultWindow               WindowConfig `json:"default_window"`
	DefaultViewport             Viewport     `json:"default_viewport"`
	DefaultLang                 string       `json:"default_lang"`
	DefaultTimezone             string       `json:"default_timezone"`
	DefaultUserAgent            string       `json:"default_user_agent"`
	DefaultTimeoutMS            int          `json:"default_timeout_ms"`
	DefaultNavigationTimeoutMS  int          `json:"default_navigation_timeout_ms"`
	DefaultDownloadRoot         string       `json:"default_download_root"`
	AllowedDownloadRoots        []string     `json:"allowed_download_roots"`
	MaxBrowsers                 int          `json:"max_browsers"`
	MaxTabsPerBrowser           int          `json:"max_tabs_per_browser"`
	EventBufferSize             int          `json:"event_buffer_size"`
	CleanupStaleBrowsersOnStart bool         `json:"cleanup_stale_browsers_on_start"`
	AllowInsecureCerts          bool         `json:"allow_insecure_certs"`
}

type WindowConfig struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Left   int    `json:"left"`
	Top    int    `json:"top"`
	State  string `json:"state"`
}

type Viewport struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
	Mobile bool    `json:"mobile"`
}

type Locator struct {
	Strategy  string `json:"strategy"`
	Value     string `json:"value"`
	State     string `json:"state"`
	TimeoutMS int    `json:"timeout_ms"`
	Nth       int    `json:"nth"`
}

type ConsoleEntry struct {
	EventID     string         `json:"event_id"`
	BrowserID   string         `json:"browser_id"`
	TabID       string         `json:"tab_id"`
	Level       string         `json:"level"`
	Type        string         `json:"type"`
	Text        string         `json:"text"`
	Args        []any          `json:"args,omitempty"`
	URL         string         `json:"url,omitempty"`
	Line        int64          `json:"line,omitempty"`
	Column      int64          `json:"column,omitempty"`
	CreatedAtMS int64          `json:"created_at_ms"`
	Raw         map[string]any `json:"raw,omitempty"`
}

type NetworkEntry struct {
	EventID        string         `json:"event_id"`
	BrowserID      string         `json:"browser_id"`
	TabID          string         `json:"tab_id"`
	RequestID      string         `json:"request_id"`
	URL            string         `json:"url"`
	Method         string         `json:"method,omitempty"`
	Status         int64          `json:"status,omitempty"`
	Type           string         `json:"type,omitempty"`
	ResourceType   string         `json:"resource_type,omitempty"`
	MimeType       string         `json:"mime_type,omitempty"`
	ErrorText      string         `json:"error_text,omitempty"`
	EncodedDataLen float64        `json:"encoded_data_length,omitempty"`
	CreatedAtMS    int64          `json:"created_at_ms"`
	Raw            map[string]any `json:"raw,omitempty"`
}

type DownloadEntry struct {
	GUID              string  `json:"guid"`
	BrowserID         string  `json:"browser_id"`
	URL               string  `json:"url"`
	SuggestedFilename string  `json:"suggested_filename"`
	State             string  `json:"state"`
	ReceivedBytes     float64 `json:"received_bytes"`
	TotalBytes        float64 `json:"total_bytes"`
	FilePath          string  `json:"file_path,omitempty"`
	StartedAtMS       int64   `json:"started_at_ms"`
	UpdatedAtMS       int64   `json:"updated_at_ms"`
	FrameID           string  `json:"frame_id,omitempty"`
	TabID             string  `json:"tab_id,omitempty"`
}

type BrowserSession struct {
	ID                  string
	Mode                string
	ExecutablePath      string
	PID                 int
	DebugWSURL          string
	CreatedAtMS         int64
	LastSeenAtMS        int64
	ProfileDir          string
	DownloadRoot        string
	BrowserContextID    cdp.BrowserContextID
	DefaultTimeoutMS    int
	DefaultNavigationMS int
	AllowInsecureCerts  bool
	ActiveTabID         string
	WindowState         WindowConfig

	CancelAll   func()
	ProcessKill func() error

	ConsoleBuffer  *RingBuffer[*ConsoleEntry]
	NetworkBuffer  *RingBuffer[*NetworkEntry]
	DownloadBuffer *RingBuffer[*DownloadEntry]

	ConsoleSubs map[string]chan []byte
	NetworkSubs map[string]chan []byte

	Tabs map[string]*TabSession
	mu   sync.RWMutex
}

type TabSession struct {
	ID             string
	TargetID       target.ID
	BrowserID      string
	CreatedAtMS    int64
	LastActivityMS int64
	URL            string
	Title          string
	ReadyState     string
	Viewport       Viewport
	UserAgent      string
	ExtraHeaders   map[string]string
	Timezone       string
	Permissions    map[string]string
	PendingNetwork map[network.RequestID]time.Time
	BusyLoading    bool
	Ctx            context.Context
	Cancel         func()
	Lock           sync.Mutex
}

type RingBuffer[T any] struct {
	mu    sync.RWMutex
	items []T
	size  int
}

func NewRingBuffer[T any](size int) *RingBuffer[T] {
	if size <= 0 {
		size = 200
	}
	return &RingBuffer[T]{size: size}
}

func (r *RingBuffer[T]) Add(v T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, v)
	if len(r.items) > r.size {
		r.items = append([]T(nil), r.items[len(r.items)-r.size:]...)
	}
}

func (r *RingBuffer[T]) List(limit int) []T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 || limit > len(r.items) {
		limit = len(r.items)
	}
	start := len(r.items) - limit
	out := make([]T, limit)
	copy(out, r.items[start:])
	return out
}

func DefaultConfig(projectRoot string) Config {
	downloadRoot := filepath.Join(projectRoot, "services", "chrome_control", "run", "downloads")
	return Config{
		ChromeExecutableCandidates:  defaultChromeCandidates(),
		DefaultMode:                 "headless",
		DefaultWindow:               WindowConfig{Width: 1440, Height: 960, Left: 20, Top: 20, State: "normal"},
		DefaultViewport:             Viewport{Width: 1440, Height: 960, Scale: 1, Mobile: false},
		DefaultLang:                 "zh-CN",
		DefaultTimezone:             "Asia/Shanghai",
		DefaultUserAgent:            "",
		DefaultTimeoutMS:            15000,
		DefaultNavigationTimeoutMS:  20000,
		DefaultDownloadRoot:         downloadRoot,
		AllowedDownloadRoots:        []string{downloadRoot},
		MaxBrowsers:                 4,
		MaxTabsPerBrowser:           8,
		EventBufferSize:             200,
		CleanupStaleBrowsersOnStart: true,
		AllowInsecureCerts:          false,
	}
}

func defaultChromeCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"google-chrome",
			"chromium",
		}
	case "linux":
		return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}
	case "windows":
		return []string{
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files\Chromium\Application\chrome.exe`,
		}
	default:
		return []string{"google-chrome", "chromium"}
	}
}

func LoadConfig(path string, projectRoot string) (Config, error) {
	cfg := DefaultConfig(projectRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if cfg.DefaultMode == "" {
		cfg.DefaultMode = "headless"
	}
	if cfg.DefaultTimeoutMS <= 0 {
		cfg.DefaultTimeoutMS = 15000
	}
	if cfg.DefaultNavigationTimeoutMS <= 0 {
		cfg.DefaultNavigationTimeoutMS = 20000
	}
	if cfg.MaxBrowsers <= 0 {
		cfg.MaxBrowsers = 4
	}
	if cfg.MaxTabsPerBrowser <= 0 {
		cfg.MaxTabsPerBrowser = 8
	}
	if cfg.EventBufferSize <= 0 {
		cfg.EventBufferSize = 200
	}
	if cfg.DefaultViewport.Width <= 0 {
		cfg.DefaultViewport.Width = 1440
	}
	if cfg.DefaultViewport.Height <= 0 {
		cfg.DefaultViewport.Height = 960
	}
	if cfg.DefaultViewport.Scale <= 0 {
		cfg.DefaultViewport.Scale = 1
	}
	if len(cfg.ChromeExecutableCandidates) == 0 {
		cfg.ChromeExecutableCandidates = defaultChromeCandidates()
	}
	if cfg.DefaultDownloadRoot == "" {
		cfg.DefaultDownloadRoot = filepath.Join(projectRoot, "services", "chrome_control", "run", "downloads")
	}
	if len(cfg.AllowedDownloadRoots) == 0 {
		cfg.AllowedDownloadRoots = []string{cfg.DefaultDownloadRoot}
	}
	return cfg, nil
}

func newID(prefix string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), rand.Int63())))
	return prefix + "-" + hex.EncodeToString(sum[:6])
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func compactJSON(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeDownloadState(state browser.DownloadProgressState) string {
	switch state {
	case browser.DownloadProgressStateCompleted:
		return "completed"
	case browser.DownloadProgressStateCanceled:
		return "canceled"
	default:
		return "in_progress"
	}
}
