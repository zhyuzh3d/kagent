package app

import "kagent/pkg/toolproto"

type ServiceManifest = toolproto.ServiceManifest
type ServiceTool = toolproto.ServiceTool

func ChromeControlManifest() ServiceManifest {
	return toolproto.NormalizeServiceManifest(ServiceManifest{
		ServiceID:   "chrome_control",
		ServiceName: "chrome_control",
		Version:     "1.0.0",
		Visibility:  "public",
		Provides:    chromeToolDescriptors(),
	})
}

func chromeToolDescriptors() []ServiceTool {
	add := func(toolID string, desc string, allowed []string, risk int, timeout int, input map[string]any, output map[string]any) ServiceTool {
		return toolproto.NormalizeServiceTool(ServiceTool{
			ToolID:             toolID,
			Description:        desc,
			AllowedCallerTypes: allowed,
			RiskLV:             risk,
			TimeoutMSDefault:   timeout,
			InputSchema:        input,
			OutputSchema:       output,
		})
	}
	ws := func(toolID string, desc string) ServiceTool {
		return toolproto.NormalizeServiceTool(ServiceTool{
			ToolID:             toolID,
			Description:        desc,
			AllowedCallerTypes: []string{"user", "service", "surface"},
			Streaming:          true,
			StreamingMode:      "ws",
			WSPath:             "/service/tool/ws",
			Protocol:           "ws",
			TimeoutMSDefault:   120000,
			RiskLV:             2,
		})
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	locator := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"strategy":   map[string]any{"type": "string"},
			"value":      map[string]any{"type": "string"},
			"state":      map[string]any{"type": "string"},
			"timeout_ms": map[string]any{"type": "integer"},
			"nth":        map[string]any{"type": "integer"},
		},
	}
	tools := []ServiceTool{
		add("chrome.browser.launch", "Launch a new Chrome instance owned by chrome_control.", []string{"user", "service", "surface"}, 2, 30000, obj(map[string]any{
			"mode":                 map[string]any{"type": "string"},
			"start_url":            map[string]any{"type": "string"},
			"executable_path":      map[string]any{"type": "string"},
			"window":               map[string]any{"type": "object"},
			"profile_mode":         map[string]any{"type": "string"},
			"user_data_dir":        map[string]any{"type": "string"},
			"lang":                 map[string]any{"type": "string"},
			"timezone":             map[string]any{"type": "string"},
			"user_agent":           map[string]any{"type": "string"},
			"extra_headers":        map[string]any{"type": "object"},
			"download_dir":         map[string]any{"type": "string"},
			"default_timeout_ms":   map[string]any{"type": "integer"},
			"allow_insecure_certs": map[string]any{"type": "boolean"},
		}), obj(map[string]any{"browser_id": map[string]any{"type": "string"}})),
		add("chrome.browser.list", "List owned Chrome browser sessions.", []string{"user", "service", "surface"}, 1, 5000, obj(map[string]any{}), obj(map[string]any{"items": map[string]any{"type": "array"}})),
		add("chrome.browser.state.get", "Get a Chrome browser session snapshot.", []string{"user", "service", "surface"}, 1, 5000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}}, "browser_id"), obj(map[string]any{"browser_id": map[string]any{"type": "string"}})),
		add("chrome.browser.close", "Close an owned Chrome browser session.", []string{"user", "service", "surface"}, 3, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}}, "browser_id"), obj(map[string]any{"closed": map[string]any{"type": "boolean"}})),
		add("chrome.tab.open", "Open a new tab inside a browser session.", []string{"user", "service", "surface"}, 2, 15000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "url": map[string]any{"type": "string"}, "new_window": map[string]any{"type": "boolean"}}, "browser_id"), obj(map[string]any{"tab_id": map[string]any{"type": "string"}})),
		add("chrome.tab.list", "List tabs inside a browser session.", []string{"user", "service", "surface"}, 1, 5000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}}, "browser_id"), obj(map[string]any{"items": map[string]any{"type": "array"}})),
		add("chrome.tab.activate", "Activate a specific tab.", []string{"user", "service", "surface"}, 2, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}}, "browser_id", "tab_id"), obj(map[string]any{"activated": map[string]any{"type": "boolean"}})),
		add("chrome.tab.close", "Close a specific tab.", []string{"user", "service", "surface"}, 3, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}}, "browser_id", "tab_id"), obj(map[string]any{"closed": map[string]any{"type": "boolean"}})),
		add("chrome.tab.navigate", "Navigate a tab to a new URL.", []string{"user", "service", "surface"}, 2, 30000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "url": map[string]any{"type": "string"}}, "browser_id", "tab_id", "url"), obj(map[string]any{"url": map[string]any{"type": "string"}})),
		add("chrome.tab.reload", "Reload a tab.", []string{"user", "service", "surface"}, 2, 20000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}}, "browser_id", "tab_id"), obj(map[string]any{"reloaded": map[string]any{"type": "boolean"}})),
		add("chrome.tab.stop", "Stop loading on a tab.", []string{"user", "service", "surface"}, 2, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}}, "browser_id", "tab_id"), obj(map[string]any{"stopped": map[string]any{"type": "boolean"}})),
		add("chrome.page.info.get", "Get URL, title, readyState and viewport for a tab.", []string{"user", "service", "surface"}, 1, 5000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}}, "browser_id", "tab_id"), obj(map[string]any{"url": map[string]any{"type": "string"}})),
		add("chrome.page.html.get", "Get document HTML or a target node outerHTML.", []string{"user", "service", "surface"}, 1, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "locator": locator}, "browser_id", "tab_id"), obj(map[string]any{"html": map[string]any{"type": "string"}})),
		add("chrome.page.dom.snapshot", "Get a normalized DOM snapshot.", []string{"user", "service", "surface"}, 1, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "locator": locator, "depth": map[string]any{"type": "integer"}, "include_text": map[string]any{"type": "boolean"}, "include_attributes": map[string]any{"type": "boolean"}}, "browser_id", "tab_id"), obj(map[string]any{"tree": map[string]any{"type": "object"}})),
		add("chrome.page.node.query", "Query DOM nodes by locator.", []string{"user", "service", "surface"}, 1, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "locator": locator}, "browser_id", "tab_id", "locator"), obj(map[string]any{"count": map[string]any{"type": "integer"}})),
		add("chrome.page.screenshot", "Capture viewport, full page or element screenshot.", []string{"user", "service", "surface"}, 2, 20000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "locator": locator, "full_page": map[string]any{"type": "boolean"}, "quality": map[string]any{"type": "integer"}}, "browser_id", "tab_id"), obj(map[string]any{"png_base64": map[string]any{"type": "string"}})),
		add("chrome.page.eval", "Evaluate JavaScript in page context.", []string{"user", "service", "surface"}, 3, 15000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "expression": map[string]any{"type": "string"}}, "browser_id", "tab_id", "expression"), obj(map[string]any{"value": map[string]any{}})),
		add("chrome.page.viewport.set", "Set tab viewport emulation.", []string{"user", "service", "surface"}, 2, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "width": map[string]any{"type": "integer"}, "height": map[string]any{"type": "integer"}, "mobile": map[string]any{"type": "boolean"}}, "browser_id", "tab_id"), obj(map[string]any{"updated": map[string]any{"type": "boolean"}})),
		add("chrome.page.user_agent.set", "Override tab user agent.", []string{"user", "service", "surface"}, 2, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "user_agent": map[string]any{"type": "string"}}, "browser_id", "tab_id", "user_agent"), obj(map[string]any{"updated": map[string]any{"type": "boolean"}})),
		add("chrome.page.headers.set", "Set extra HTTP headers for a tab.", []string{"user", "service", "surface"}, 3, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "headers": map[string]any{"type": "object"}}, "browser_id", "tab_id", "headers"), obj(map[string]any{"updated": map[string]any{"type": "boolean"}})),
		add("chrome.page.timezone.set", "Set tab timezone emulation.", []string{"user", "service", "surface"}, 2, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "timezone": map[string]any{"type": "string"}}, "browser_id", "tab_id", "timezone"), obj(map[string]any{"updated": map[string]any{"type": "boolean"}})),
		add("chrome.page.permission.set", "Set page permission override.", []string{"user", "service", "surface"}, 3, 10000, obj(map[string]any{"browser_id": map[string]any{"type": "string"}, "tab_id": map[string]any{"type": "string"}, "permission": map[string]any{"type": "string"}, "setting": map[string]any{"type": "string"}, "origin": map[string]any{"type": "string"}}, "browser_id", "tab_id", "permission", "setting"), obj(map[string]any{"updated": map[string]any{"type": "boolean"}})),
	}
	for _, name := range []struct {
		Tool string
		Desc string
		Risk int
	}{
		{"chrome.action.click", "Locate an element and click it.", 2},
		{"chrome.action.input", "Fill an element with text or send key input.", 2},
		{"chrome.action.press", "Send keyboard key or modifiers.", 3},
		{"chrome.action.hover", "Move the virtual mouse over an element.", 2},
		{"chrome.action.scroll", "Scroll page or element.", 2},
		{"chrome.action.select", "Select option in a select element.", 2},
		{"chrome.action.context.click", "Right click an element.", 2},
		{"chrome.action.drag", "Drag from source locator to destination locator or offset.", 3},
		{"chrome.wait.selector", "Wait for selector state.", 1},
		{"chrome.wait.text", "Wait for text on page or element.", 1},
		{"chrome.wait.navigation", "Wait for navigation conditions.", 1},
		{"chrome.wait.network.idle", "Wait until tab network becomes idle.", 1},
		{"chrome.download.dir.set", "Set download directory for a browser.", 3},
		{"chrome.download.wait", "Wait for a download to finish.", 2},
		{"chrome.download.list", "List recent downloads for a browser.", 1},
		{"chrome.storage.cookies.get", "Get cookies for current tab or URL.", 1},
		{"chrome.storage.cookies.set", "Set cookies for current tab or URL.", 3},
		{"chrome.storage.local.get", "Get localStorage keys.", 1},
		{"chrome.storage.local.set", "Set localStorage values.", 2},
		{"chrome.storage.session.get", "Get sessionStorage keys.", 1},
		{"chrome.storage.session.set", "Set sessionStorage values.", 2},
		{"chrome.debug.console.list", "List recent console events.", 1},
		{"chrome.debug.network.list", "List recent network events.", 1},
	} {
		tools = append(tools, add(name.Tool, name.Desc, []string{"user", "service", "surface"}, name.Risk, 20000, obj(map[string]any{
			"browser_id": map[string]any{"type": "string"},
			"tab_id":     map[string]any{"type": "string"},
			"locator":    locator,
			"limit":      map[string]any{"type": "integer"},
			"args":       map[string]any{"type": "object"},
			"value":      map[string]any{},
		}), obj(map[string]any{"ok": map[string]any{"type": "boolean"}})))
	}
	tools = append(tools,
		ws("chrome.debug.console.subscribe", "Subscribe console events for a browser or tab."),
		ws("chrome.debug.network.subscribe", "Subscribe network events for a browser or tab."),
		add("service.lifecycle.health", "service health probe", []string{"service"}, 1, 3000, obj(map[string]any{}), obj(map[string]any{"ok": map[string]any{"type": "boolean"}})),
		add("service.lifecycle.state.get", "service lifecycle state snapshot", []string{"service"}, 1, 3000, obj(map[string]any{}), obj(map[string]any{"status": map[string]any{"type": "string"}})),
		add("service.lifecycle.shutdown", "service shutdown", []string{"service"}, 4, 3000, obj(map[string]any{"reason": map[string]any{"type": "string"}}), obj(map[string]any{"shutting_down": map[string]any{"type": "boolean"}})),
	)
	return tools
}
