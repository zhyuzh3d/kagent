package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"

	"kagent/pkg/toolproto"
)

type Service struct {
	config      Config
	projectRoot string
	runtimeRoot string
	serviceID   string
	instanceID  string
	addr        string
	startedAtMS int64
	shutdownNow func(string)
	rootCtx     context.Context

	mu       sync.RWMutex
	browsers map[string]*BrowserSession
}

func NewService(rootCtx context.Context, projectRoot string, runtimeRoot string, cfg Config, instanceID string, addr string, shutdownNow func(string)) *Service {
	return &Service{
		config:      cfg,
		projectRoot: projectRoot,
		runtimeRoot: runtimeRoot,
		serviceID:   "chrome_control",
		instanceID:  instanceID,
		addr:        addr,
		startedAtMS: nowMS(),
		shutdownNow: shutdownNow,
		rootCtx:     rootCtx,
		browsers:    map[string]*BrowserSession{},
	}
}

func (s *Service) StartupCleanup() error {
	if s.config.CleanupStaleBrowsersOnStart {
		_ = os.RemoveAll(filepath.Join(s.runtimeRoot, "profiles"))
		_ = os.RemoveAll(filepath.Join(s.runtimeRoot, "downloads"))
	}
	return os.MkdirAll(s.runtimeRoot, 0o755)
}

func (s *Service) Execute(req toolproto.CallRequest) (map[string]any, error) {
	switch req.ToolID {
	case "service.lifecycle.health", "service.lifecycle.state.get":
		return map[string]any{
			"service_id":    s.serviceID,
			"instance_id":   s.instanceID,
			"endpoint":      "http://" + strings.TrimSpace(s.addr),
			"healthy":       true,
			"status":        "ready",
			"started_at_ms": s.startedAtMS,
			"browser_count": s.browserCount(),
		}, nil
	case "service.lifecycle.shutdown":
		go func() {
			time.Sleep(100 * time.Millisecond)
			if s.shutdownNow != nil {
				s.shutdownNow("shutdown requested via tool")
			}
		}()
		return map[string]any{"shutting_down": true}, nil
	case "chrome.browser.launch":
		return s.launchBrowser(req.Args)
	case "chrome.browser.list":
		return s.listBrowsers(), nil
	case "chrome.browser.state.get":
		return s.browserState(req.Args)
	case "chrome.browser.close":
		return s.closeBrowser(req.Args)
	case "chrome.tab.open":
		return s.openTab(req.Args)
	case "chrome.tab.list":
		return s.listTabs(req.Args)
	case "chrome.tab.activate":
		return s.activateTab(req.Args)
	case "chrome.tab.close":
		return s.closeTab(req.Args)
	case "chrome.tab.navigate":
		return s.navigateTab(req.Args)
	case "chrome.tab.reload":
		return s.reloadTab(req.Args)
	case "chrome.tab.stop":
		return s.stopTab(req.Args)
	case "chrome.page.info.get":
		return s.pageInfo(req.Args)
	case "chrome.page.html.get":
		return s.pageHTML(req.Args)
	case "chrome.page.dom.snapshot":
		return s.pageDOMSnapshot(req.Args)
	case "chrome.page.node.query":
		return s.pageNodeQuery(req.Args)
	case "chrome.page.screenshot":
		return s.pageScreenshot(req.Args)
	case "chrome.page.eval":
		return s.pageEval(req.Args)
	case "chrome.page.viewport.set":
		return s.setViewport(req.Args)
	case "chrome.page.user_agent.set":
		return s.setUserAgent(req.Args)
	case "chrome.page.headers.set":
		return s.setHeaders(req.Args)
	case "chrome.page.timezone.set":
		return s.setTimezone(req.Args)
	case "chrome.page.permission.set":
		return s.setPermission(req.Args)
	case "chrome.action.click":
		return s.actionClick(req.Args, false)
	case "chrome.action.context.click":
		return s.actionClick(req.Args, true)
	case "chrome.action.hover":
		return s.actionHover(req.Args)
	case "chrome.action.input":
		return s.actionInput(req.Args)
	case "chrome.action.press":
		return s.actionPress(req.Args)
	case "chrome.action.scroll":
		return s.actionScroll(req.Args)
	case "chrome.action.select":
		return s.actionSelect(req.Args)
	case "chrome.action.drag":
		return s.actionDrag(req.Args)
	case "chrome.wait.selector":
		return s.waitSelector(req.Args)
	case "chrome.wait.text":
		return s.waitText(req.Args)
	case "chrome.wait.navigation":
		return s.waitNavigation(req.Args)
	case "chrome.wait.network.idle":
		return s.waitNetworkIdle(req.Args)
	case "chrome.download.dir.set":
		return s.setDownloadDir(req.Args)
	case "chrome.download.wait":
		return s.waitDownload(req.Args)
	case "chrome.download.list":
		return s.listDownloads(req.Args)
	case "chrome.storage.cookies.get":
		return s.getCookies(req.Args)
	case "chrome.storage.cookies.set":
		return s.setCookies(req.Args)
	case "chrome.storage.local.get":
		return s.storageGet(req.Args, "localStorage")
	case "chrome.storage.local.set":
		return s.storageSet(req.Args, "localStorage")
	case "chrome.storage.session.get":
		return s.storageGet(req.Args, "sessionStorage")
	case "chrome.storage.session.set":
		return s.storageSet(req.Args, "sessionStorage")
	case "chrome.debug.console.list":
		return s.listConsole(req.Args)
	case "chrome.debug.network.list":
		return s.listNetwork(req.Args)
	default:
		return nil, fmt.Errorf("tool not found: %s", req.ToolID)
	}
}

func (s *Service) HandleWS(ctx context.Context, toolID string, firstPayload []byte, push func([]byte) error) error {
	switch toolID {
	case "chrome.debug.console.subscribe":
		return s.subscribeDebugStream(ctx, firstPayload, push, true)
	case "chrome.debug.network.subscribe":
		return s.subscribeDebugStream(ctx, firstPayload, push, false)
	default:
		return fmt.Errorf("unsupported ws tool: %s", toolID)
	}
}

func (s *Service) subscribeDebugStream(ctx context.Context, firstPayload []byte, push func([]byte) error, console bool) error {
	reqArgs := map[string]any{}
	if len(firstPayload) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(firstPayload, &raw); err == nil {
			if args, ok := raw["args"].(map[string]any); ok {
				reqArgs = args
			} else {
				reqArgs = raw
			}
		}
	}
	browserSession, _, err := s.mustBrowserAndMaybeTab(reqArgs)
	if err != nil {
		return err
	}
	subID := newID("sub")
	ch := make(chan []byte, 32)
	browserSession.mu.Lock()
	if console {
		if browserSession.ConsoleSubs == nil {
			browserSession.ConsoleSubs = map[string]chan []byte{}
		}
		browserSession.ConsoleSubs[subID] = ch
	} else {
		if browserSession.NetworkSubs == nil {
			browserSession.NetworkSubs = map[string]chan []byte{}
		}
		browserSession.NetworkSubs[subID] = ch
	}
	browserSession.mu.Unlock()
	defer func() {
		browserSession.mu.Lock()
		delete(browserSession.ConsoleSubs, subID)
		delete(browserSession.NetworkSubs, subID)
		browserSession.mu.Unlock()
	}()
	initial := map[string]any{
		"type":        "subscribed",
		"tool_id":     map[bool]string{true: "chrome.debug.console.subscribe", false: "chrome.debug.network.subscribe"}[console],
		"browser_id":  browserSession.ID,
		"instance_id": s.instanceID,
	}
	if err := push([]byte(compactJSON(initial))); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case payload := <-ch:
			if err := push(payload); err != nil {
				return err
			}
		}
	}
}

func (s *Service) browserCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.browsers)
}

func (s *Service) listBrowsers() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]map[string]any, 0, len(s.browsers))
	for _, browserSession := range s.browsers {
		items = append(items, s.browserSummary(browserSession))
	}
	return map[string]any{"items": items}
}

func (s *Service) browserSummary(browserSession *BrowserSession) map[string]any {
	browserSession.mu.RLock()
	defer browserSession.mu.RUnlock()
	items := make([]map[string]any, 0, len(browserSession.Tabs))
	for _, tabSession := range browserSession.Tabs {
		items = append(items, tabSnapshot(tabSession))
	}
	return map[string]any{
		"browser_id":         browserSession.ID,
		"mode":               browserSession.Mode,
		"chrome_pid":         browserSession.PID,
		"debug_ws_url":       browserSession.DebugWSURL,
		"download_root":      browserSession.DownloadRoot,
		"browser_context_id": string(browserSession.BrowserContextID),
		"created_at_ms":      browserSession.CreatedAtMS,
		"last_seen_at_ms":    browserSession.LastSeenAtMS,
		"active_tab_id":      browserSession.ActiveTabID,
		"tabs":               items,
	}
}

func tabSnapshot(tabSession *TabSession) map[string]any {
	tabSession.Lock.Lock()
	defer tabSession.Lock.Unlock()
	return map[string]any{
		"tab_id":           tabSession.ID,
		"url":              tabSession.URL,
		"title":            tabSession.Title,
		"ready_state":      tabSession.ReadyState,
		"created_at_ms":    tabSession.CreatedAtMS,
		"last_activity_at": tabSession.LastActivityMS,
		"pending_network":  len(tabSession.PendingNetwork),
		"viewport":         tabSession.Viewport,
		"user_agent":       tabSession.UserAgent,
		"timezone":         tabSession.Timezone,
		"extra_headers":    cloneStringMap(tabSession.ExtraHeaders),
	}
}

func (s *Service) launchBrowser(args map[string]any) (map[string]any, error) {
	s.mu.Lock()
	if len(s.browsers) >= s.config.MaxBrowsers {
		s.mu.Unlock()
		return nil, fmt.Errorf("max browsers reached")
	}
	s.mu.Unlock()

	mode := firstNonEmpty(asString(args["mode"]), s.config.DefaultMode)
	if mode != "headed" {
		mode = "headless"
	}
	browserID := newID("browser")
	profileDir := filepath.Join(s.runtimeRoot, "profiles", browserID)
	downloadRoot := filepath.Join(s.runtimeRoot, "downloads", browserID)
	if value := strings.TrimSpace(asString(args["download_dir"])); value != "" {
		downloadRoot = value
	}
	downloadRoot, err := s.resolveDownloadRoot(downloadRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(downloadRoot, 0o755); err != nil {
		return nil, err
	}
	executable, err := s.detectChromeExecutable(asString(args["executable_path"]))
	if err != nil {
		return nil, err
	}
	window := s.config.DefaultWindow
	if raw, ok := args["window"].(map[string]any); ok {
		if asInt(raw["width"], 0) > 0 {
			window.Width = asInt(raw["width"], window.Width)
		}
		if asInt(raw["height"], 0) > 0 {
			window.Height = asInt(raw["height"], window.Height)
		}
		if asInt(raw["left"], 0) != 0 {
			window.Left = asInt(raw["left"], window.Left)
		}
		if asInt(raw["top"], 0) != 0 {
			window.Top = asInt(raw["top"], window.Top)
		}
		window.State = firstNonEmpty(asString(raw["state"]), window.State)
	}
	lang := firstNonEmpty(asString(args["lang"]), s.config.DefaultLang)
	userAgent := firstNonEmpty(asString(args["user_agent"]), s.config.DefaultUserAgent)
	timezoneID := firstNonEmpty(asString(args["timezone"]), s.config.DefaultTimezone)
	defaultTimeoutMS := asInt(args["default_timeout_ms"], s.config.DefaultTimeoutMS)
	allowInsecure := asBool(args["allow_insecure_certs"], s.config.AllowInsecureCerts)
	headers := mapString(args["extra_headers"])
	startURL := firstNonEmpty(asString(args["start_url"]), "about:blank")

	cmd, wsURL, err := startChromeProcess(s.rootCtx, executable, profileDir, mode, window, lang, allowInsecure)
	if err != nil {
		return nil, err
	}
	allocCtx, allocCancel := chromedp.NewRemoteAllocator(s.rootCtx, wsURL)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithNewBrowserContext())
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("attach browser failed: %w", err)
	}

	browserContextID := chromedp.FromContext(browserCtx).BrowserContextID
	browserSession := &BrowserSession{
		ID:                  browserID,
		Mode:                mode,
		ExecutablePath:      executable,
		PID:                 cmd.Process.Pid,
		DebugWSURL:          wsURL,
		CreatedAtMS:         nowMS(),
		LastSeenAtMS:        nowMS(),
		ProfileDir:          profileDir,
		DownloadRoot:        downloadRoot,
		BrowserContextID:    browserContextID,
		DefaultTimeoutMS:    defaultTimeoutMS,
		DefaultNavigationMS: s.config.DefaultNavigationTimeoutMS,
		AllowInsecureCerts:  allowInsecure,
		WindowState:         window,
		CancelAll: func() {
			browserCancel()
			allocCancel()
		},
		ProcessKill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
		ConsoleBuffer:  NewRingBuffer[*ConsoleEntry](s.config.EventBufferSize),
		NetworkBuffer:  NewRingBuffer[*NetworkEntry](s.config.EventBufferSize),
		DownloadBuffer: NewRingBuffer[*DownloadEntry](s.config.EventBufferSize),
		ConsoleSubs:    map[string]chan []byte{},
		NetworkSubs:    map[string]chan []byte{},
		Tabs:           map[string]*TabSession{},
	}

	if err := s.configureBrowserDownload(browserCtx, browserSession); err != nil {
		browserSession.CancelAll()
		_ = browserSession.ProcessKill()
		return nil, err
	}
	s.listenBrowser(browserCtx, browserSession)
	initialCtx, initialCancel := chromedp.NewContext(browserCtx, chromedp.WithExistingBrowserContext(browserContextID))
	if err := chromedp.Run(initialCtx); err != nil {
		initialCancel()
		browserSession.CancelAll()
		_ = browserSession.ProcessKill()
		return nil, err
	}
	initialTab, err := s.attachTab(browserSession, initialCtx, initialCancel, chromedp.FromContext(initialCtx).Target.TargetID)
	if err != nil {
		browserSession.CancelAll()
		_ = browserSession.ProcessKill()
		return nil, err
	}
	if userAgent != "" {
		if _, err := s.setUserAgent(map[string]any{"browser_id": browserID, "tab_id": initialTab.ID, "user_agent": userAgent}); err != nil {
			return nil, err
		}
	}
	if len(headers) > 0 {
		if _, err := s.setHeaders(map[string]any{"browser_id": browserID, "tab_id": initialTab.ID, "headers": headers}); err != nil {
			return nil, err
		}
	}
	if timezoneID != "" {
		if _, err := s.setTimezone(map[string]any{"browser_id": browserID, "tab_id": initialTab.ID, "timezone": timezoneID}); err != nil {
			return nil, err
		}
	}
	if _, err := s.setViewport(map[string]any{"browser_id": browserID, "tab_id": initialTab.ID, "width": s.config.DefaultViewport.Width, "height": s.config.DefaultViewport.Height, "mobile": s.config.DefaultViewport.Mobile}); err != nil {
		return nil, err
	}
	if startURL != "" && startURL != "about:blank" {
		if _, err := s.navigateTab(map[string]any{"browser_id": browserID, "tab_id": initialTab.ID, "url": startURL}); err != nil {
			return nil, err
		}
	}

	s.mu.Lock()
	s.browsers[browserID] = browserSession
	s.mu.Unlock()
	return map[string]any{
		"browser_id":         browserSession.ID,
		"chrome_pid":         browserSession.PID,
		"mode":               browserSession.Mode,
		"browser_context_id": string(browserSession.BrowserContextID),
		"initial_tab_id":     initialTab.ID,
		"debug_ws_url":       browserSession.DebugWSURL,
		"effective_config": map[string]any{
			"download_dir":       browserSession.DownloadRoot,
			"default_timeout_ms": browserSession.DefaultTimeoutMS,
			"timezone":           timezoneID,
			"user_agent":         userAgent,
		},
	}, nil
}

func startChromeProcess(parent context.Context, executable string, profileDir string, mode string, window WindowConfig, lang string, allowInsecure bool) (*exec.Cmd, string, error) {
	args := []string{
		"--remote-debugging-port=0",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-background-timer-throttling",
		"--disable-popup-blocking",
		"--disable-sync",
		"--enable-automation",
		"--lang=" + firstNonEmpty(lang, "en-US"),
		fmt.Sprintf("--window-size=%d,%d", maxInt(window.Width, 800), maxInt(window.Height, 600)),
		"about:blank",
	}
	if mode == "headless" {
		args = append(args, "--headless=new")
	}
	if allowInsecure {
		args = append(args, "--ignore-certificate-errors")
	}
	cmd := exec.CommandContext(parent, executable, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, "", err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, "", err
	}
	deadline := time.After(20 * time.Second)
	for {
		select {
		case <-deadline:
			_ = cmd.Process.Kill()
			return nil, "", fmt.Errorf("chrome websocket url timeout")
		default:
		}
		buf := make([]byte, 4096)
		n, err := stdout.Read(buf)
		if n > 0 {
			for _, line := range strings.Split(string(buf[:n]), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "DevTools listening on ") {
					return cmd, strings.TrimSpace(strings.TrimPrefix(line, "DevTools listening on ")), nil
				}
			}
		}
		if err != nil {
			_ = cmd.Process.Kill()
			return nil, "", fmt.Errorf("read chrome output: %w", err)
		}
	}
}

func (s *Service) detectChromeExecutable(explicit string) (string, error) {
	candidates := []string{}
	if strings.TrimSpace(explicit) != "" {
		candidates = append(candidates, strings.TrimSpace(explicit))
	}
	candidates = append(candidates, s.config.ChromeExecutableCandidates...)
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("chrome executable not found")
}

func (s *Service) configureBrowserDownload(browserCtx context.Context, browserSession *BrowserSession) error {
	execCtx := cdp.WithExecutor(browserCtx, chromedp.FromContext(browserCtx).Browser)
	return browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
		WithBrowserContextID(browserSession.BrowserContextID).
		WithDownloadPath(browserSession.DownloadRoot).
		WithEventsEnabled(true).
		Do(execCtx)
}

func (s *Service) listenBrowser(browserCtx context.Context, browserSession *BrowserSession) {
	chromedp.ListenBrowser(browserCtx, func(ev any) {
		switch evt := ev.(type) {
		case *browser.EventDownloadWillBegin:
			entry := &DownloadEntry{
				GUID:              evt.GUID,
				BrowserID:         browserSession.ID,
				URL:               evt.URL,
				SuggestedFilename: evt.SuggestedFilename,
				State:             "in_progress",
				StartedAtMS:       nowMS(),
				UpdatedAtMS:       nowMS(),
				FrameID:           string(evt.FrameID),
			}
			browserSession.DownloadBuffer.Add(entry)
		case *browser.EventDownloadProgress:
			entry := &DownloadEntry{
				GUID:          evt.GUID,
				BrowserID:     browserSession.ID,
				State:         normalizeDownloadState(evt.State),
				ReceivedBytes: evt.ReceivedBytes,
				TotalBytes:    evt.TotalBytes,
				FilePath:      evt.FilePath,
				UpdatedAtMS:   nowMS(),
			}
			prev := browserSession.DownloadBuffer.List(browserSession.DownloadBuffer.size)
			for i := len(prev) - 1; i >= 0; i-- {
				if prev[i].GUID == entry.GUID {
					entry.URL = prev[i].URL
					entry.SuggestedFilename = prev[i].SuggestedFilename
					entry.StartedAtMS = prev[i].StartedAtMS
					entry.FrameID = prev[i].FrameID
					entry.TabID = prev[i].TabID
					break
				}
			}
			if entry.StartedAtMS == 0 {
				entry.StartedAtMS = entry.UpdatedAtMS
			}
			browserSession.DownloadBuffer.Add(entry)
		}
	})
}

func (s *Service) attachTab(browserSession *BrowserSession, tabCtx context.Context, tabCancel func(), targetID target.ID) (*TabSession, error) {
	tabSession := &TabSession{
		ID:             string(targetID),
		TargetID:       targetID,
		BrowserID:      browserSession.ID,
		CreatedAtMS:    nowMS(),
		LastActivityMS: nowMS(),
		Viewport:       s.config.DefaultViewport,
		ExtraHeaders:   map[string]string{},
		Permissions:    map[string]string{},
		PendingNetwork: map[network.RequestID]time.Time{},
		Ctx:            tabCtx,
		Cancel:         tabCancel,
	}
	if err := chromedp.Run(tabCtx,
		network.Enable(),
		page.Enable(),
		page.SetLifecycleEventsEnabled(true),
		runtime.Enable(),
		log.Enable(),
	); err != nil {
		return nil, err
	}
	chromedp.ListenTarget(tabCtx, func(ev any) {
		switch evt := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			args := make([]any, 0, len(evt.Args))
			for _, item := range evt.Args {
				args = append(args, item.Value)
			}
			entry := &ConsoleEntry{
				EventID:     newID("console"),
				BrowserID:   browserSession.ID,
				TabID:       tabSession.ID,
				Level:       strings.ToLower(evt.Type.String()),
				Type:        evt.Type.String(),
				Text:        strings.TrimSpace(fmt.Sprint(args...)),
				Args:        args,
				CreatedAtMS: nowMS(),
			}
			browserSession.ConsoleBuffer.Add(entry)
			s.broadcastConsole(browserSession, entry)
		case *runtime.EventExceptionThrown:
			entry := &ConsoleEntry{
				EventID:     newID("console"),
				BrowserID:   browserSession.ID,
				TabID:       tabSession.ID,
				Level:       "error",
				Type:        "exception",
				Text:        evt.ExceptionDetails.Text,
				URL:         evt.ExceptionDetails.URL,
				Line:        evt.ExceptionDetails.LineNumber,
				Column:      evt.ExceptionDetails.ColumnNumber,
				CreatedAtMS: nowMS(),
			}
			browserSession.ConsoleBuffer.Add(entry)
			s.broadcastConsole(browserSession, entry)
		case *log.EventEntryAdded:
			entry := &ConsoleEntry{
				EventID:     newID("console"),
				BrowserID:   browserSession.ID,
				TabID:       tabSession.ID,
				Level:       strings.ToLower(evt.Entry.Level.String()),
				Type:        "log_entry",
				Text:        evt.Entry.Text,
				URL:         evt.Entry.URL,
				Line:        evt.Entry.LineNumber,
				CreatedAtMS: nowMS(),
			}
			browserSession.ConsoleBuffer.Add(entry)
			s.broadcastConsole(browserSession, entry)
		case *network.EventRequestWillBeSent:
			tabSession.Lock.Lock()
			tabSession.PendingNetwork[evt.RequestID] = time.Now()
			tabSession.BusyLoading = true
			tabSession.LastActivityMS = nowMS()
			tabSession.Lock.Unlock()
			entry := &NetworkEntry{
				EventID:      newID("network"),
				BrowserID:    browserSession.ID,
				TabID:        tabSession.ID,
				RequestID:    string(evt.RequestID),
				URL:          evt.Request.URL,
				Method:       evt.Request.Method,
				Type:         "request",
				ResourceType: evt.Type.String(),
				CreatedAtMS:  nowMS(),
			}
			browserSession.NetworkBuffer.Add(entry)
			s.broadcastNetwork(browserSession, entry)
		case *network.EventResponseReceived:
			entry := &NetworkEntry{
				EventID:      newID("network"),
				BrowserID:    browserSession.ID,
				TabID:        tabSession.ID,
				RequestID:    string(evt.RequestID),
				URL:          evt.Response.URL,
				Method:       "",
				Status:       int64(evt.Response.Status),
				Type:         "response",
				ResourceType: evt.Type.String(),
				MimeType:     evt.Response.MimeType,
				CreatedAtMS:  nowMS(),
			}
			browserSession.NetworkBuffer.Add(entry)
			s.broadcastNetwork(browserSession, entry)
		case *network.EventLoadingFinished:
			tabSession.Lock.Lock()
			delete(tabSession.PendingNetwork, evt.RequestID)
			tabSession.LastActivityMS = nowMS()
			if len(tabSession.PendingNetwork) == 0 {
				tabSession.BusyLoading = false
			}
			tabSession.Lock.Unlock()
			entry := &NetworkEntry{
				EventID:        newID("network"),
				BrowserID:      browserSession.ID,
				TabID:          tabSession.ID,
				RequestID:      string(evt.RequestID),
				Type:           "loading_finished",
				EncodedDataLen: evt.EncodedDataLength,
				CreatedAtMS:    nowMS(),
			}
			browserSession.NetworkBuffer.Add(entry)
			s.broadcastNetwork(browserSession, entry)
		case *network.EventLoadingFailed:
			tabSession.Lock.Lock()
			delete(tabSession.PendingNetwork, evt.RequestID)
			tabSession.LastActivityMS = nowMS()
			if len(tabSession.PendingNetwork) == 0 {
				tabSession.BusyLoading = false
			}
			tabSession.Lock.Unlock()
			entry := &NetworkEntry{
				EventID:     newID("network"),
				BrowserID:   browserSession.ID,
				TabID:       tabSession.ID,
				RequestID:   string(evt.RequestID),
				Type:        "loading_failed",
				ErrorText:   evt.ErrorText,
				CreatedAtMS: nowMS(),
			}
			browserSession.NetworkBuffer.Add(entry)
			s.broadcastNetwork(browserSession, entry)
		case *page.EventLifecycleEvent:
			tabSession.Lock.Lock()
			switch evt.Name {
			case "DOMContentLoaded":
				tabSession.ReadyState = "interactive"
			case "load":
				tabSession.ReadyState = "complete"
			}
			tabSession.LastActivityMS = nowMS()
			tabSession.Lock.Unlock()
		case *page.EventFrameNavigated:
			tabSession.Lock.Lock()
			tabSession.URL = evt.Frame.URL
			tabSession.LastActivityMS = nowMS()
			tabSession.ReadyState = "loading"
			tabSession.Lock.Unlock()
		}
	})
	info, _ := s.readTabInfo(tabSession)
	tabSession.URL = asString(info["url"])
	tabSession.Title = asString(info["title"])
	tabSession.ReadyState = asString(info["ready_state"])
	browserSession.mu.Lock()
	browserSession.Tabs[tabSession.ID] = tabSession
	browserSession.ActiveTabID = tabSession.ID
	browserSession.mu.Unlock()
	return tabSession, nil
}

func (s *Service) readTabInfo(tabSession *TabSession) (map[string]any, error) {
	ctx, cancel := s.tabTimeout(tabSession, 5000)
	defer cancel()
	result := map[string]any{}
	err := runJSFunction(ctx, `
function(input) {
  return {
    url: location.href,
    title: document.title,
    ready_state: document.readyState,
    viewport: {
      width: window.innerWidth,
      height: window.innerHeight,
      device_pixel_ratio: window.devicePixelRatio || 1
    },
    document_lang: document.documentElement && document.documentElement.lang ? document.documentElement.lang : ""
  };
}`, map[string]any{}, &result)
	return result, err
}

func (s *Service) broadcastConsole(browserSession *BrowserSession, entry *ConsoleEntry) {
	payload, _ := json.Marshal(map[string]any{"type": "console", "entry": entry})
	browserSession.mu.RLock()
	defer browserSession.mu.RUnlock()
	for _, ch := range browserSession.ConsoleSubs {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (s *Service) broadcastNetwork(browserSession *BrowserSession, entry *NetworkEntry) {
	payload, _ := json.Marshal(map[string]any{"type": "network", "entry": entry})
	browserSession.mu.RLock()
	defer browserSession.mu.RUnlock()
	for _, ch := range browserSession.NetworkSubs {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (s *Service) browserState(args map[string]any) (map[string]any, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, err
	}
	return s.browserSummary(browserSession), nil
}

func (s *Service) closeBrowser(args map[string]any) (map[string]any, error) {
	browserID := strings.TrimSpace(asString(args["browser_id"]))
	if browserID == "" {
		return nil, fmt.Errorf("browser_id is required")
	}
	s.mu.Lock()
	browserSession, ok := s.browsers[browserID]
	if ok {
		delete(s.browsers, browserID)
	}
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("browser not found: %s", browserID)
	}
	var anyTab *TabSession
	browserSession.mu.RLock()
	for _, item := range browserSession.Tabs {
		anyTab = item
		break
	}
	browserSession.mu.RUnlock()
	if anyTab != nil {
		execCtx := cdp.WithExecutor(anyTab.Ctx, chromedp.FromContext(anyTab.Ctx).Browser)
		_ = browser.Close().Do(execCtx)
	}
	browserSession.CancelAll()
	_ = browserSession.ProcessKill()
	return map[string]any{"closed": true, "browser_id": browserID}, nil
}

func (s *Service) openTab(args map[string]any) (map[string]any, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, err
	}
	browserSession.mu.RLock()
	tabCount := len(browserSession.Tabs)
	browserSession.mu.RUnlock()
	if tabCount >= s.config.MaxTabsPerBrowser {
		return nil, fmt.Errorf("max tabs reached")
	}
	blankCtx := cdp.WithExecutor(browserSession.Tabs[browserSession.ActiveTabID].Ctx, chromedp.FromContext(browserSession.Tabs[browserSession.ActiveTabID].Ctx).Browser)
	params := target.CreateTarget(firstNonEmpty(asString(args["url"]), "about:blank")).WithBrowserContextID(browserSession.BrowserContextID)
	if asBool(args["new_window"], false) {
		params = params.WithNewWindow(true)
	}
	targetID, err := params.Do(blankCtx)
	if err != nil {
		return nil, err
	}
	tabCtx, tabCancel := chromedp.NewContext(browserSession.Tabs[browserSession.ActiveTabID].Ctx, chromedp.WithTargetID(targetID))
	if err := chromedp.Run(tabCtx); err != nil {
		tabCancel()
		return nil, err
	}
	tabSession, err := s.attachTab(browserSession, tabCtx, tabCancel, targetID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tab_id": tabSession.ID, "browser_id": browserSession.ID}, nil
}

func (s *Service) listTabs(args map[string]any) (map[string]any, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, err
	}
	browserSession.mu.RLock()
	defer browserSession.mu.RUnlock()
	items := make([]map[string]any, 0, len(browserSession.Tabs))
	for _, tabSession := range browserSession.Tabs {
		items = append(items, tabSnapshot(tabSession))
	}
	return map[string]any{"items": items, "active_tab_id": browserSession.ActiveTabID}, nil
}

func (s *Service) activateTab(args map[string]any) (map[string]any, error) {
	browserSession, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	execCtx := cdp.WithExecutor(tabSession.Ctx, chromedp.FromContext(tabSession.Ctx).Browser)
	if err := target.ActivateTarget(tabSession.TargetID).Do(execCtx); err != nil {
		return nil, err
	}
	browserSession.mu.Lock()
	browserSession.ActiveTabID = tabSession.ID
	browserSession.mu.Unlock()
	return map[string]any{"activated": true, "tab_id": tabSession.ID}, nil
}

func (s *Service) closeTab(args map[string]any) (map[string]any, error) {
	browserSession, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	execCtx := cdp.WithExecutor(tabSession.Ctx, chromedp.FromContext(tabSession.Ctx).Browser)
	if err := target.CloseTarget(tabSession.TargetID).Do(execCtx); err != nil {
		return nil, err
	}
	tabSession.Cancel()
	browserSession.mu.Lock()
	delete(browserSession.Tabs, tabSession.ID)
	if browserSession.ActiveTabID == tabSession.ID {
		browserSession.ActiveTabID = ""
		for id := range browserSession.Tabs {
			browserSession.ActiveTabID = id
			break
		}
	}
	browserSession.mu.Unlock()
	return map[string]any{"closed": true, "tab_id": tabSession.ID}, nil
}

func (s *Service) navigateTab(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.navTimeoutMS(tabSession))
	defer cancel()
	urlValue := strings.TrimSpace(asString(args["url"]))
	if urlValue == "" {
		return nil, fmt.Errorf("url is required")
	}
	tabSession.Lock.Lock()
	tabSession.ReadyState = "loading"
	tabSession.Lock.Unlock()
	if err := chromedp.Run(ctx, chromedp.Navigate(urlValue)); err != nil {
		return nil, err
	}
	_, _ = s.waitNavigation(map[string]any{"browser_id": tabSession.BrowserID, "tab_id": tabSession.ID, "url_contains": urlValue, "event": "load", "timeout_ms": s.navTimeoutMS(tabSession)})
	info, _ := s.readTabInfo(tabSession)
	return info, nil
}

func (s *Service) reloadTab(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.navTimeoutMS(tabSession))
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Reload()); err != nil {
		return nil, err
	}
	return map[string]any{"reloaded": true}, nil
}

func (s *Service) stopTab(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, 5000)
	defer cancel()
	if err := page.StopLoading().Do(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"stopped": true}, nil
}

func (s *Service) pageInfo(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	return s.readTabInfo(tabSession)
}

func (s *Service) pageHTML(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	locator := parseLocator(args["locator"])
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	var html string
	if locator.Value == "" {
		err = runJSFunction(ctx, `function(input){ return document.documentElement ? document.documentElement.outerHTML : ""; }`, map[string]any{}, &html)
	} else {
		err = runJSFunction(ctx, nodeOuterHTMLScript, locatorToInput(locator), &html)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"html": html}, nil
}

func (s *Service) pageDOMSnapshot(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	inputValue := locatorToInput(parseLocator(args["locator"]))
	inputValue["depth"] = asInt(args["depth"], 4)
	inputValue["include_text"] = asBool(args["include_text"], true)
	inputValue["include_attributes"] = asBool(args["include_attributes"], true)
	tree := map[string]any{}
	if err := runJSFunction(ctx, domSnapshotScript, inputValue, &tree); err != nil {
		return nil, err
	}
	return map[string]any{"tree": tree}, nil
}

func (s *Service) pageNodeQuery(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	out := map[string]any{}
	if err := runJSFunction(ctx, nodeQueryScript, locatorToInput(parseLocator(args["locator"])), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) pageScreenshot(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, 20000)
	defer cancel()
	var buf []byte
	locator := parseLocator(args["locator"])
	if locator.Value != "" {
		clip := map[string]any{}
		if err := runJSFunction(ctx, nodeClipScript, locatorToInput(locator), &clip); err != nil {
			return nil, err
		}
		buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).WithClip(&page.Viewport{
			X:      asFloat(clip["x"]),
			Y:      asFloat(clip["y"]),
			Width:  asFloat(clip["width"]),
			Height: asFloat(clip["height"]),
			Scale:  1,
		}).Do(ctx)
	} else if asBool(args["full_page"], false) {
		err = chromedp.Run(ctx, chromedp.FullScreenshot(&buf, asInt(args["quality"], 90)))
	} else {
		err = chromedp.Run(ctx, chromedp.CaptureScreenshot(&buf))
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"png_base64": base64.StdEncoding.EncodeToString(buf),
		"size_bytes": len(buf),
	}, nil
}

func (s *Service) pageEval(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	expression := strings.TrimSpace(asString(args["expression"]))
	if expression == "" {
		return nil, fmt.Errorf("expression is required")
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	var value any
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &value)); err != nil {
		return nil, err
	}
	return map[string]any{"value": value}, nil
}

func (s *Service) setViewport(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	width := asInt(args["width"], s.config.DefaultViewport.Width)
	height := asInt(args["height"], s.config.DefaultViewport.Height)
	mobile := asBool(args["mobile"], s.config.DefaultViewport.Mobile)
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	if err := emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1, mobile).Do(ctx); err != nil {
		return nil, err
	}
	tabSession.Lock.Lock()
	tabSession.Viewport = Viewport{Width: width, Height: height, Scale: 1, Mobile: mobile}
	tabSession.Lock.Unlock()
	return map[string]any{"updated": true, "viewport": tabSession.Viewport}, nil
}

func (s *Service) setUserAgent(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	userAgent := strings.TrimSpace(asString(args["user_agent"]))
	if userAgent == "" {
		return nil, fmt.Errorf("user_agent is required")
	}
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	if err := emulation.SetUserAgentOverride(userAgent).Do(ctx); err != nil {
		return nil, err
	}
	tabSession.Lock.Lock()
	tabSession.UserAgent = userAgent
	tabSession.Lock.Unlock()
	return map[string]any{"updated": true}, nil
}

func (s *Service) setHeaders(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	headers := mapString(args["headers"])
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	networkHeaders := network.Headers{}
	for k, v := range headers {
		networkHeaders[k] = v
	}
	if err := network.SetExtraHTTPHeaders(networkHeaders).Do(ctx); err != nil {
		return nil, err
	}
	tabSession.Lock.Lock()
	tabSession.ExtraHeaders = headers
	tabSession.Lock.Unlock()
	return map[string]any{"updated": true}, nil
}

func (s *Service) setTimezone(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	timezoneID := strings.TrimSpace(asString(args["timezone"]))
	if timezoneID == "" {
		return nil, fmt.Errorf("timezone is required")
	}
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	if err := emulation.SetTimezoneOverride(timezoneID).Do(ctx); err != nil {
		return nil, err
	}
	tabSession.Lock.Lock()
	tabSession.Timezone = timezoneID
	tabSession.Lock.Unlock()
	return map[string]any{"updated": true}, nil
}

func (s *Service) setPermission(args map[string]any) (map[string]any, error) {
	browserSession, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	permissionName := strings.TrimSpace(asString(args["permission"]))
	settingText := strings.TrimSpace(asString(args["setting"]))
	if permissionName == "" || settingText == "" {
		return nil, fmt.Errorf("permission and setting are required")
	}
	var setting browser.PermissionSetting
	switch strings.ToLower(settingText) {
	case "granted", "allow":
		setting = browser.PermissionSettingGranted
	case "denied", "deny":
		setting = browser.PermissionSettingDenied
	default:
		setting = browser.PermissionSettingPrompt
	}
	origin := firstNonEmpty(asString(args["origin"]), tabSession.URL)
	execCtx := cdp.WithExecutor(tabSession.Ctx, chromedp.FromContext(tabSession.Ctx).Browser)
	desc := &browser.PermissionDescriptor{Name: permissionName}
	if err := browser.SetPermission(desc, setting).WithBrowserContextID(browserSession.BrowserContextID).WithOrigin(origin).Do(execCtx); err != nil {
		return nil, err
	}
	tabSession.Lock.Lock()
	tabSession.Permissions[permissionName] = setting.String()
	tabSession.Lock.Unlock()
	return map[string]any{"updated": true}, nil
}

func (s *Service) actionClick(args map[string]any, rightClick bool) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	locator := parseLocator(args["locator"])
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	point := map[string]any{}
	if err := runJSFunction(ctx, nodePointScript, locatorToInput(locator), &point); err != nil {
		return nil, err
	}
	button := input.Left
	if rightClick {
		button = input.Right
	}
	if err := input.DispatchMouseEvent(input.MouseMoved, asFloat(point["x"]), asFloat(point["y"])).Do(ctx); err != nil {
		return nil, err
	}
	if err := input.DispatchMouseEvent(input.MousePressed, asFloat(point["x"]), asFloat(point["y"])).WithButton(button).WithClickCount(1).Do(ctx); err != nil {
		return nil, err
	}
	if err := input.DispatchMouseEvent(input.MouseReleased, asFloat(point["x"]), asFloat(point["y"])).WithButton(button).WithClickCount(1).Do(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "x": point["x"], "y": point["y"], "button": button.String()}, nil
}

func (s *Service) actionHover(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	locator := parseLocator(args["locator"])
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	point := map[string]any{}
	if err := runJSFunction(ctx, nodePointScript, locatorToInput(locator), &point); err != nil {
		return nil, err
	}
	if err := input.DispatchMouseEvent(input.MouseMoved, asFloat(point["x"]), asFloat(point["y"])).Do(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "x": point["x"], "y": point["y"]}, nil
}

func (s *Service) actionInput(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	locator := parseLocator(args["locator"])
	text := asString(args["text"])
	mode := firstNonEmpty(asString(args["mode"]), "fill")
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	if mode == "keys" {
		if err := chromedp.Run(ctx, chromedp.SendKeys(locator.Value, text, byOption(locator))); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "mode": mode}, nil
	}
	inputValue := locatorToInput(locator)
	inputValue["text"] = text
	inputValue["clear_before"] = asBool(args["clear_before"], false)
	if err := runJSFunction(ctx, fillNodeScript, inputValue, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "mode": "fill"}, nil
}

func (s *Service) actionPress(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	key := strings.TrimSpace(asString(args["key"]))
	if key == "" {
		return nil, fmt.Errorf("key is required")
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	modifiers := parseModifiers(args["modifiers"])
	if err := chromedp.Run(ctx, chromedp.KeyEvent(key, chromedp.KeyModifiers(modifiers...))); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (s *Service) actionScroll(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	locator := parseLocator(args["locator"])
	if locator.Value != "" {
		if err := runJSFunction(ctx, scrollNodeScript, locatorToInput(locator), nil); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "mode": "element"}, nil
	}
	inputValue := map[string]any{"dx": asInt(args["dx"], 0), "dy": asInt(args["dy"], 400)}
	if err := runJSFunction(ctx, `function(input){ window.scrollBy(input.dx || 0, input.dy || 0); return true; }`, inputValue, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "mode": "window"}, nil
}

func (s *Service) actionSelect(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	inputValue := locatorToInput(parseLocator(args["locator"]))
	inputValue["value"] = asString(args["value"])
	inputValue["label"] = asString(args["label"])
	result := map[string]any{}
	if err := runJSFunction(ctx, selectNodeScript, inputValue, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) actionDrag(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, s.toolTimeoutMS(args, tabSession))
	defer cancel()
	source := map[string]any{}
	if err := runJSFunction(ctx, nodePointScript, locatorToInput(parseLocator(args["source_locator"])), &source); err != nil {
		return nil, err
	}
	destination := map[string]any{}
	if raw := parseLocator(args["target_locator"]); raw.Value != "" {
		if err := runJSFunction(ctx, nodePointScript, locatorToInput(raw), &destination); err != nil {
			return nil, err
		}
	} else {
		destination["x"] = asFloat(source["x"]) + float64(asInt(args["offset_x"], 40))
		destination["y"] = asFloat(source["y"]) + float64(asInt(args["offset_y"], 40))
	}
	if err := input.DispatchMouseEvent(input.MouseMoved, asFloat(source["x"]), asFloat(source["y"])).Do(ctx); err != nil {
		return nil, err
	}
	if err := input.DispatchMouseEvent(input.MousePressed, asFloat(source["x"]), asFloat(source["y"])).WithButton(input.Left).WithClickCount(1).Do(ctx); err != nil {
		return nil, err
	}
	if err := input.DispatchMouseEvent(input.MouseMoved, asFloat(destination["x"]), asFloat(destination["y"])).WithButton(input.Left).Do(ctx); err != nil {
		return nil, err
	}
	if err := input.DispatchMouseEvent(input.MouseReleased, asFloat(destination["x"]), asFloat(destination["y"])).WithButton(input.Left).WithClickCount(1).Do(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func (s *Service) waitSelector(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	locator := parseLocator(args["locator"])
	state := firstNonEmpty(asString(args["state"]), locator.State, "visible")
	timeoutMS := s.toolTimeoutMS(args, tabSession)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		ctx, cancel := s.tabTimeout(tabSession, 3000)
		result := map[string]any{}
		err := runJSFunction(ctx, nodeStateScript, map[string]any{"locator": locatorToInput(locator), "state": state}, &result)
		cancel()
		if err == nil && asBool(result["matched"], false) {
			return map[string]any{"ok": true, "state": state, "result": result}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait selector timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Service) waitText(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(asString(args["text"]))
	if text == "" {
		return nil, fmt.Errorf("text is required")
	}
	locator := parseLocator(args["locator"])
	timeoutMS := s.toolTimeoutMS(args, tabSession)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		ctx, cancel := s.tabTimeout(tabSession, 3000)
		result := map[string]any{}
		err := runJSFunction(ctx, textWaitScript, map[string]any{"locator": locatorToInput(locator), "text": text}, &result)
		cancel()
		if err == nil && asBool(result["matched"], false) {
			return map[string]any{"ok": true, "result": result}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait text timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Service) waitNavigation(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	urlContains := strings.TrimSpace(asString(args["url_contains"]))
	titleContains := strings.TrimSpace(asString(args["title_contains"]))
	eventName := firstNonEmpty(asString(args["event"]), "load")
	timeoutMS := s.toolTimeoutMS(args, tabSession)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		info, err := s.readTabInfo(tabSession)
		if err == nil {
			ready := true
			if urlContains != "" && !strings.Contains(asString(info["url"]), urlContains) {
				ready = false
			}
			if titleContains != "" && !strings.Contains(asString(info["title"]), titleContains) {
				ready = false
			}
			readyState := asString(info["ready_state"])
			if eventName == "domcontentloaded" && readyState == "loading" {
				ready = false
			}
			if eventName == "load" && readyState != "complete" {
				ready = false
			}
			if ready {
				return map[string]any{"ok": true, "info": info}, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait navigation timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Service) waitNetworkIdle(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	quietMS := asInt(args["quiet_ms"], 500)
	timeoutMS := s.toolTimeoutMS(args, tabSession)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		tabSession.Lock.Lock()
		pending := len(tabSession.PendingNetwork)
		lastActivity := tabSession.LastActivityMS
		tabSession.Lock.Unlock()
		if pending == 0 && (nowMS()-lastActivity) >= int64(quietMS) {
			return map[string]any{"ok": true, "quiet_ms": quietMS}, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait network idle timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (s *Service) setDownloadDir(args map[string]any) (map[string]any, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, err
	}
	targetDir, err := s.resolveDownloadRoot(asString(args["download_dir"]))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, err
	}
	browserSession.mu.RLock()
	var anyTab *TabSession
	for _, item := range browserSession.Tabs {
		anyTab = item
		break
	}
	browserSession.mu.RUnlock()
	if anyTab == nil {
		return nil, fmt.Errorf("browser has no tabs")
	}
	execCtx := cdp.WithExecutor(anyTab.Ctx, chromedp.FromContext(anyTab.Ctx).Browser)
	if err := browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
		WithBrowserContextID(browserSession.BrowserContextID).
		WithDownloadPath(targetDir).
		WithEventsEnabled(true).
		Do(execCtx); err != nil {
		return nil, err
	}
	browserSession.mu.Lock()
	browserSession.DownloadRoot = targetDir
	browserSession.mu.Unlock()
	return map[string]any{"updated": true, "download_dir": targetDir}, nil
}

func (s *Service) waitDownload(args map[string]any) (map[string]any, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, err
	}
	guid := strings.TrimSpace(asString(args["guid"]))
	filename := strings.TrimSpace(asString(args["filename_pattern"]))
	timeoutMS := asInt(args["timeout_ms"], 30000)
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		items := browserSession.DownloadBuffer.List(browserSession.DownloadBuffer.size)
		for i := len(items) - 1; i >= 0; i-- {
			item := items[i]
			if guid != "" && item.GUID != guid {
				continue
			}
			if filename != "" && !strings.Contains(item.SuggestedFilename, filename) {
				continue
			}
			if guid == "" && filename == "" && item.State == "in_progress" {
				continue
			}
			if item.State == "completed" || item.State == "canceled" {
				return map[string]any{"download": item}, nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("wait download timeout")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (s *Service) listDownloads(args map[string]any) (map[string]any, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, err
	}
	limit := asInt(args["limit"], 20)
	return map[string]any{"items": browserSession.DownloadBuffer.List(limit)}, nil
}

func (s *Service) getCookies(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	urls := []string{}
	if value := strings.TrimSpace(asString(args["url"])); value != "" {
		urls = append(urls, value)
	}
	params := network.GetCookies()
	if len(urls) > 0 {
		params = params.WithURLs(urls)
	}
	cookies, err := params.Do(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"cookies": cookies}, nil
}

func (s *Service) setCookies(args map[string]any) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	if rawList, ok := args["cookies"].([]any); ok && len(rawList) > 0 {
		items := make([]*network.CookieParam, 0, len(rawList))
		for _, item := range rawList {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			param := &network.CookieParam{Name: asString(row["name"]), Value: asString(row["value"])}
			if urlValue := asString(row["url"]); urlValue != "" {
				param.URL = urlValue
			}
			if domainValue := asString(row["domain"]); domainValue != "" {
				param.Domain = domainValue
			}
			if pathValue := asString(row["path"]); pathValue != "" {
				param.Path = pathValue
			}
			items = append(items, param)
		}
		if err := network.SetCookies(items).Do(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"updated": true}, nil
	}
	name := strings.TrimSpace(asString(args["name"]))
	value := asString(args["value"])
	if name == "" {
		return nil, fmt.Errorf("cookie name is required")
	}
	params := network.SetCookie(name, value)
	if urlValue := asString(args["url"]); urlValue != "" {
		params = params.WithURL(urlValue)
	}
	if domainValue := asString(args["domain"]); domainValue != "" {
		params = params.WithDomain(domainValue)
	}
	if pathValue := asString(args["path"]); pathValue != "" {
		params = params.WithPath(pathValue)
	}
	if err := params.Do(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

func (s *Service) storageGet(args map[string]any, storageName string) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	keys := stringSlice(args["keys"])
	result := map[string]any{}
	if err := runJSFunction(ctx, storageGetScript, map[string]any{"storage": storageName, "keys": keys}, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) storageSet(args map[string]any, storageName string) (map[string]any, error) {
	_, tabSession, err := s.mustBrowserAndTab(args)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.tabTimeout(tabSession, 10000)
	defer cancel()
	inputValue := map[string]any{"storage": storageName, "values": cloneAnyMap(asMap(args["values"]))}
	if err := runJSFunction(ctx, storageSetScript, inputValue, nil); err != nil {
		return nil, err
	}
	return map[string]any{"updated": true}, nil
}

func (s *Service) listConsole(args map[string]any) (map[string]any, error) {
	browserSession, _, err := s.mustBrowserAndMaybeTab(args)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": browserSession.ConsoleBuffer.List(asInt(args["limit"], 50))}, nil
}

func (s *Service) listNetwork(args map[string]any) (map[string]any, error) {
	browserSession, _, err := s.mustBrowserAndMaybeTab(args)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": browserSession.NetworkBuffer.List(asInt(args["limit"], 50))}, nil
}

func (s *Service) mustBrowser(args map[string]any) (*BrowserSession, error) {
	browserID := strings.TrimSpace(asString(args["browser_id"]))
	if browserID == "" {
		return nil, fmt.Errorf("browser_id is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	browserSession, ok := s.browsers[browserID]
	if !ok {
		return nil, fmt.Errorf("browser not found: %s", browserID)
	}
	return browserSession, nil
}

func (s *Service) mustBrowserAndTab(args map[string]any) (*BrowserSession, *TabSession, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, nil, err
	}
	tabID := strings.TrimSpace(asString(args["tab_id"]))
	if tabID == "" {
		browserSession.mu.RLock()
		tabID = browserSession.ActiveTabID
		browserSession.mu.RUnlock()
	}
	if tabID == "" {
		return nil, nil, fmt.Errorf("tab_id is required")
	}
	browserSession.mu.RLock()
	tabSession, ok := browserSession.Tabs[tabID]
	browserSession.mu.RUnlock()
	if !ok {
		return nil, nil, fmt.Errorf("tab not found: %s", tabID)
	}
	return browserSession, tabSession, nil
}

func (s *Service) mustBrowserAndMaybeTab(args map[string]any) (*BrowserSession, *TabSession, error) {
	browserSession, err := s.mustBrowser(args)
	if err != nil {
		return nil, nil, err
	}
	tabID := strings.TrimSpace(asString(args["tab_id"]))
	if tabID == "" {
		return browserSession, nil, nil
	}
	browserSession.mu.RLock()
	tabSession := browserSession.Tabs[tabID]
	browserSession.mu.RUnlock()
	if tabSession == nil {
		return nil, nil, fmt.Errorf("tab not found: %s", tabID)
	}
	return browserSession, tabSession, nil
}

func (s *Service) tabTimeout(tabSession *TabSession, timeoutMS int) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		timeoutMS = s.config.DefaultTimeoutMS
	}
	return context.WithTimeout(tabSession.Ctx, time.Duration(timeoutMS)*time.Millisecond)
}

func (s *Service) toolTimeoutMS(args map[string]any, tabSession *TabSession) int {
	timeoutMS := asInt(args["timeout_ms"], 0)
	if timeoutMS > 0 {
		return timeoutMS
	}
	return s.config.DefaultTimeoutMS
}

func (s *Service) navTimeoutMS(tabSession *TabSession) int {
	return s.config.DefaultNavigationTimeoutMS
}

func (s *Service) resolveDownloadRoot(targetDir string) (string, error) {
	if strings.TrimSpace(targetDir) == "" {
		targetDir = s.config.DefaultDownloadRoot
	}
	if !filepath.IsAbs(targetDir) {
		targetDir = filepath.Join(s.projectRoot, targetDir)
	}
	targetDir = filepath.Clean(targetDir)
	for _, root := range s.config.AllowedDownloadRoots {
		cleanRoot := root
		if !filepath.IsAbs(cleanRoot) {
			cleanRoot = filepath.Join(s.projectRoot, cleanRoot)
		}
		cleanRoot = filepath.Clean(cleanRoot)
		if strings.HasPrefix(targetDir, cleanRoot) {
			return targetDir, nil
		}
	}
	return "", fmt.Errorf("download directory is outside allowed roots")
}

func runJSFunction(ctx context.Context, fn string, input any, result any) error {
	raw, _ := json.Marshal(input)
	expr := fmt.Sprintf("(%s)(%s)", strings.TrimSpace(fn), string(raw))
	if result == nil {
		return chromedp.Run(ctx, chromedp.Evaluate(expr, nil))
	}
	return chromedp.Run(ctx, chromedp.Evaluate(expr, result))
}

func parseLocator(raw any) Locator {
	row, ok := raw.(map[string]any)
	if !ok {
		return Locator{Strategy: "css", State: "visible", Nth: 0}
	}
	return Locator{
		Strategy:  firstNonEmpty(asString(row["strategy"]), "css"),
		Value:     strings.TrimSpace(asString(row["value"])),
		State:     firstNonEmpty(asString(row["state"]), "visible"),
		TimeoutMS: asInt(row["timeout_ms"], 0),
		Nth:       asInt(row["nth"], 0),
	}
}

func locatorToInput(locator Locator) map[string]any {
	return map[string]any{
		"strategy": locator.Strategy,
		"value":    locator.Value,
		"state":    locator.State,
		"nth":      locator.Nth,
	}
}

func byOption(locator Locator) chromedp.QueryOption {
	if strings.EqualFold(locator.Strategy, "xpath") {
		return chromedp.BySearch
	}
	return chromedp.ByQuery
}

func parseModifiers(raw any) []input.Modifier {
	values := stringSlice(raw)
	out := make([]input.Modifier, 0, len(values))
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "alt", "option":
			out = append(out, input.ModifierAlt)
		case "ctrl", "control":
			out = append(out, input.ModifierCtrl)
		case "meta", "command":
			out = append(out, input.ModifierMeta)
		case "shift":
			out = append(out, input.ModifierShift)
		}
	}
	return out
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func asBool(value any, fallback bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return fallback
	}
}

func asInt(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

func asFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		n, err := v.Float64()
		if err == nil {
			return n
		}
	}
	return 0
}

func mapString(value any) map[string]string {
	row, ok := value.(map[string]any)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(row))
	for k, v := range row {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func asMap(value any) map[string]any {
	row, ok := value.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return row
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

const nodeQueryScript = `
function(input) {
  function selectAll(locator) {
    if (!locator || !locator.value) return [];
    if ((locator.strategy || 'css').toLowerCase() === 'xpath') {
      const result = document.evaluate(locator.value, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      const items = [];
      for (let i = 0; i < result.snapshotLength; i += 1) items.push(result.snapshotItem(i));
      return items;
    }
    return Array.from(document.querySelectorAll(locator.value));
  }
  const nodes = selectAll(input);
  const items = nodes.map((node) => {
    const rect = node.getBoundingClientRect();
    const style = window.getComputedStyle(node);
    return {
      tag_name: node.tagName ? node.tagName.toLowerCase() : '',
      text: (node.innerText || node.textContent || '').trim().slice(0, 500),
      html: node.outerHTML || '',
      attributes: Array.from(node.attributes || []).reduce((acc, item) => { acc[item.name] = item.value; return acc; }, {}),
      visible: rect.width > 0 && rect.height > 0 && style.visibility !== 'hidden' && style.display !== 'none',
      disabled: !!node.disabled,
      editable: !node.readOnly && !node.disabled && ['INPUT','TEXTAREA'].includes(node.tagName),
      bounding_box: { x: rect.left, y: rect.top, width: rect.width, height: rect.height }
    };
  });
  const nth = Number.isFinite(input.nth) ? input.nth : 0;
  return { count: items.length, items, node: items[nth] || null };
}`

const nodeOuterHTMLScript = `
function(input) {
  const result = (` + "`" + nodeQueryScript + "`" + `);
  const payload = eval(result)(input);
  return payload.node ? payload.node.html : '';
}`

const domSnapshotScript = `
function(input) {
  function selectOne(locator) {
    if (!locator || !locator.value) return document.documentElement;
    if ((locator.strategy || 'css').toLowerCase() === 'xpath') {
      return document.evaluate(locator.value, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
    }
    return document.querySelector(locator.value);
  }
  function walk(node, depth) {
    if (!node || depth < 0) return null;
    const out = { tag: node.tagName ? node.tagName.toLowerCase() : '#text' };
    if (input.include_attributes && node.attributes) {
      out.attributes = Array.from(node.attributes).reduce((acc, item) => { acc[item.name] = item.value; return acc; }, {});
    }
    if (input.include_text) {
      out.text = (node.innerText || node.textContent || '').trim().slice(0, 1000);
    }
    if (node.childNodes && depth > 0) {
      out.children = Array.from(node.childNodes).map((child) => walk(child, depth - 1)).filter(Boolean);
    }
    return out;
  }
  const root = selectOne(input);
  return walk(root, Number.isFinite(input.depth) ? input.depth : 4);
}`

const nodePointScript = `
function(input) {
  const payload = (` + "`" + nodeQueryScript + "`" + `);
  const result = eval(payload)(input);
  if (!result.node) throw new Error('node_not_found');
  const box = result.node.bounding_box;
  return { x: box.x + Math.max(box.width / 2, 1), y: box.y + Math.max(box.height / 2, 1), box };
}`

const nodeClipScript = `
function(input) {
  const payload = (` + "`" + nodeQueryScript + "`" + `);
  const result = eval(payload)(input);
  if (!result.node) throw new Error('node_not_found');
  return result.node.bounding_box;
}`

const fillNodeScript = `
function(input) {
  const payload = (` + "`" + nodeQueryScript + "`" + `);
  const result = eval(payload)(input);
  if (!result.node) throw new Error('node_not_found');
  const element = ((locator) => {
    if ((locator.strategy || 'css').toLowerCase() === 'xpath') {
      const snap = document.evaluate(locator.value, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null);
      return snap.snapshotItem(Number.isFinite(locator.nth) ? locator.nth : 0);
    }
    return document.querySelectorAll(locator.value)[Number.isFinite(locator.nth) ? locator.nth : 0];
  })(input);
  if (!element) throw new Error('node_not_found');
  element.focus();
  if (input.clear_before && typeof element.value !== 'undefined') element.value = '';
  if (typeof element.value !== 'undefined') element.value = input.text || '';
  element.dispatchEvent(new Event('input', { bubbles: true }));
  element.dispatchEvent(new Event('change', { bubbles: true }));
  return true;
}`

const scrollNodeScript = `
function(input) {
  const payload = (` + "`" + nodeQueryScript + "`" + `);
  const result = eval(payload)(input);
  if (!result.node) throw new Error('node_not_found');
  const element = ((locator) => {
    if ((locator.strategy || 'css').toLowerCase() === 'xpath') {
      return document.evaluate(locator.value, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
    }
    return document.querySelector(locator.value);
  })(input);
  if (!element) throw new Error('node_not_found');
  element.scrollIntoView({ block: 'center', inline: 'center' });
  return true;
}`

const selectNodeScript = `
function(input) {
  const element = ((locator) => {
    if ((locator.strategy || 'css').toLowerCase() === 'xpath') {
      return document.evaluate(locator.value, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
    }
    return document.querySelector(locator.value);
  })(input);
  if (!element) throw new Error('node_not_found');
  const options = Array.from(element.options || []);
  let target = null;
  if (input.value) target = options.find((item) => item.value === input.value);
  if (!target && input.label) target = options.find((item) => item.label === input.label || item.text === input.label);
  if (!target) throw new Error('option_not_found');
  element.value = target.value;
  element.dispatchEvent(new Event('input', { bubbles: true }));
  element.dispatchEvent(new Event('change', { bubbles: true }));
  return { ok: true, value: target.value, label: target.label || target.text };
}`

const nodeStateScript = `
function(input) {
  const locator = input.locator || {};
  const payload = (` + "`" + nodeQueryScript + "`" + `);
  const result = eval(payload)(locator);
  const node = result.node;
  const state = (input.state || 'visible').toLowerCase();
  if (state === 'attached') return { matched: !!node, result };
  if (state === 'detached') return { matched: !node, result };
  if (!node) return { matched: false, result };
  if (state === 'hidden') return { matched: !node.visible, result };
  return { matched: !!node.visible, result };
}`

const textWaitScript = `
function(input) {
  const source = input.locator && input.locator.value
    ? ((locator) => {
        if ((locator.strategy || 'css').toLowerCase() === 'xpath') {
          return document.evaluate(locator.value, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
        }
        return document.querySelector(locator.value);
      })(input.locator)
    : document.body;
  const text = source ? (source.innerText || source.textContent || '') : '';
  return { matched: text.includes(input.text || ''), text: text.slice(0, 2000) };
}`

const storageGetScript = `
function(input) {
  const store = window[input.storage];
  const keys = Array.isArray(input.keys) ? input.keys : [];
  const values = {};
  if (keys.length === 0) {
    for (let i = 0; i < store.length; i += 1) {
      const key = store.key(i);
      values[key] = store.getItem(key);
    }
  } else {
    keys.forEach((key) => { values[key] = store.getItem(key); });
  }
  return { storage: input.storage, values };
}`

const storageSetScript = `
function(input) {
  const store = window[input.storage];
  const values = input.values || {};
  Object.keys(values).forEach((key) => {
    const value = values[key];
    if (value === null) store.removeItem(key);
    else store.setItem(key, String(value));
  });
  return true;
}`
