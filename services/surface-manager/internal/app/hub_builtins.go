package app

import "strings"

func BuiltinServiceManifests() []ServiceManifest {
	return []ServiceManifest{
		{
			ServiceID:   "file",
			ServiceName: "File Service",
			Version:     "1.0.0",
			Reliability: "trusted",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				{ToolID: "storage.file.read", Category: "storage", Type: "file", Tool: "read", Description: "read file", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.write", Category: "storage", Type: "file", Tool: "write", Description: "write file", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.delete", Category: "storage", Type: "file", Tool: "delete", Description: "delete file", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.exists", Category: "storage", Type: "file", Tool: "exists", Description: "check file existence", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.stat", Category: "storage", Type: "file", Tool: "stat", Description: "file metadata", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.list", Category: "storage", Type: "file", Tool: "list", Description: "list dir entries", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.mkdir", Category: "storage", Type: "file", Tool: "mkdir", Description: "create dir", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.rename", Category: "storage", Type: "file", Tool: "rename", Description: "rename path", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.file.copy", Category: "storage", Type: "file", Tool: "copy", Description: "copy file", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.blob.put", Category: "storage", Type: "blob", Tool: "put", Description: "put immutable blob", ScopeSupport: []string{"user"}},
				{ToolID: "storage.blob.get", Category: "storage", Type: "blob", Tool: "get", Description: "get immutable blob", ScopeSupport: []string{"user"}},
				{ToolID: "storage.blob.sign_url", Category: "storage", Type: "blob", Tool: "sign_url", Description: "sign blob download url", ScopeSupport: []string{"user"}},
				{ToolID: "storage.blob.gc", Category: "storage", Type: "blob", Tool: "gc", Description: "gc expired blobs", ScopeSupport: []string{"service"}},
			},
		},
		{
			ServiceID:   "database",
			ServiceName: "Database Service",
			Version:     "1.0.0",
			Reliability: "verified",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				{ToolID: "storage.database.query", Category: "storage", Type: "database", Tool: "query", Description: "query rows", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.database.insert", Category: "storage", Type: "database", Tool: "insert", Description: "insert row", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.database.update", Category: "storage", Type: "database", Tool: "update", Description: "update row", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.database.delete", Category: "storage", Type: "database", Tool: "delete", Description: "delete row", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.database.execute", Category: "storage", Type: "database", Tool: "execute", Description: "execute SQL", ScopeSupport: []string{"user", "surface", "service"}},
				{ToolID: "storage.database.schema", Category: "storage", Type: "database", Tool: "schema", Description: "show schema", ScopeSupport: []string{"user", "surface", "service"}},
			},
		},
		{
			ServiceID:   "auth",
			ServiceName: "Auth Service",
			Version:     "1.0.0",
			Reliability: "trusted",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				{ToolID: "security.auth.user_register", Category: "security", Type: "auth", Tool: "user_register", Description: "register user"},
				{ToolID: "security.auth.user_login", Category: "security", Type: "auth", Tool: "user_login", Description: "login user"},
				{ToolID: "security.auth.user_logout", Category: "security", Type: "auth", Tool: "user_logout", Description: "logout user"},
				{ToolID: "security.auth.user_me", Category: "security", Type: "auth", Tool: "user_me", Description: "current user"},
				{ToolID: "security.auth.user_password_change", Category: "security", Type: "auth", Tool: "user_password_change", Description: "change password"},
				{ToolID: "security.auth.service_register", Category: "security", Type: "auth", Tool: "service_register", Description: "register service"},
				{ToolID: "security.auth.service_issue_token", Category: "security", Type: "auth", Tool: "service_issue_token", Description: "issue service token"},
				{ToolID: "security.auth.service_verify_token", Category: "security", Type: "auth", Tool: "service_verify_token", Description: "verify service token"},
				{ToolID: "security.auth.service_revoke_token", Category: "security", Type: "auth", Tool: "service_revoke_token", Description: "revoke service token"},
				{ToolID: "security.auth.surface_session_issue", Category: "security", Type: "auth", Tool: "surface_session_issue", Description: "issue surface session token"},
				{ToolID: "security.auth.surface_capability_issue", Category: "security", Type: "auth", Tool: "surface_capability_issue", Description: "issue surface capability"},
				{ToolID: "security.auth.surface_verify", Category: "security", Type: "auth", Tool: "surface_verify", Description: "verify surface token"},
				{ToolID: "security.auth.audit_query", Category: "security", Type: "auth", Tool: "audit_query", Description: "query auth audit"},
			},
		},
		{
			ServiceID:   "surface-manager",
			ServiceName: "Surface Manager",
			Version:     "1.0.0",
			Reliability: "verified",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				{ToolID: "ui.surface.catalog_list", Category: "ui", Type: "surface", Tool: "catalog_list", Description: "list surface catalog"},
				{ToolID: "ui.surface.get", Category: "ui", Type: "surface", Tool: "get", Description: "get one surface"},
				{ToolID: "ui.surface.enable_set", Category: "ui", Type: "surface", Tool: "enable_set", Description: "set enabled"},
				{ToolID: "ui.surface.session_issue", Category: "ui", Type: "surface", Tool: "session_issue", Description: "issue session token"},
				{ToolID: "ui.surface.capability_issue", Category: "ui", Type: "surface", Tool: "capability_issue", Description: "issue capability token"},
				{ToolID: "ui.surface.runtime_status", Category: "ui", Type: "surface", Tool: "runtime_status", Description: "runtime status"},
				{ToolID: "ui.surface.logs_query", Category: "ui", Type: "surface", Tool: "logs_query", Description: "query surface logs"},
				{ToolID: "ui.surface.rescan", Category: "ui", Type: "surface", Tool: "rescan", Description: "rescan packages"},
				{ToolID: "ui.surface.rebind", Category: "ui", Type: "surface", Tool: "rebind", Description: "rebind manifest"},
				{ToolID: "ui.surface.fs_read", Category: "ui", Type: "surface", Tool: "fs_read", Description: "surface fs read"},
				{ToolID: "ui.surface.fs_write", Category: "ui", Type: "surface", Tool: "fs_write", Description: "surface fs write"},
				{ToolID: "ui.surface.fs_list", Category: "ui", Type: "surface", Tool: "fs_list", Description: "surface fs list"},
				{ToolID: "ui.surface.fs_delete", Category: "ui", Type: "surface", Tool: "fs_delete", Description: "surface fs delete"},
				{ToolID: "ui.surface.fs_sign_static", Category: "ui", Type: "surface", Tool: "fs_sign_static", Description: "surface fs sign static url"},
				{ToolID: "ui.surface.db_roundtrip", Category: "ui", Type: "surface", Tool: "db_roundtrip", Description: "service caller db roundtrip via hub"},
			},
		},
	}
}

func ChatServerServiceManifest() ServiceManifest {
	return ServiceManifest{
		ServiceID:   "chat-server",
		ServiceName: "Chat Server",
		Version:     "1.0.0",
		Reliability: "trusted",
		Visibility:  "public",
		Provides: []ServiceToolDescriptor{
			{ToolID: "app.chat.project_list", Category: "app", Type: "chat", Tool: "project_list", Description: "list projects"},
			{ToolID: "app.chat.project_create", Category: "app", Type: "chat", Tool: "project_create", Description: "create project"},
			{ToolID: "app.chat.project_update", Category: "app", Type: "chat", Tool: "project_update", Description: "update project"},
			{ToolID: "app.chat.project_delete", Category: "app", Type: "chat", Tool: "project_delete", Description: "delete project"},
			{ToolID: "app.chat.thread_list", Category: "app", Type: "chat", Tool: "thread_list", Description: "list threads"},
			{ToolID: "app.chat.thread_create", Category: "app", Type: "chat", Tool: "thread_create", Description: "create thread"},
			{ToolID: "app.chat.thread_update", Category: "app", Type: "chat", Tool: "thread_update", Description: "update thread"},
			{ToolID: "app.chat.thread_delete", Category: "app", Type: "chat", Tool: "thread_delete", Description: "delete thread"},
			{ToolID: "app.chat.thread_move", Category: "app", Type: "chat", Tool: "thread_move", Description: "move thread across project"},
			{ToolID: "app.chat.history_fetch", Category: "app", Type: "chat", Tool: "history_fetch", Description: "fetch chat history"},
			{ToolID: "app.chat.stream_start", Category: "app", Type: "chat", Tool: "stream_start", Description: "start chat stream"},
			{ToolID: "app.chat.stream_stop", Category: "app", Type: "chat", Tool: "stream_stop", Description: "stop chat stream"},
			{ToolID: "app.chat.turn_start_listen", Category: "app", Type: "chat", Tool: "turn_start_listen", Description: "mark turn listen start"},
			{ToolID: "app.chat.turn_commit", Category: "app", Type: "chat", Tool: "turn_commit", Description: "commit turn"},
			{ToolID: "app.chat.turn_interrupt", Category: "app", Type: "chat", Tool: "turn_interrupt", Description: "interrupt running turn"},
			{ToolID: "app.chat.action_report", Category: "app", Type: "chat", Tool: "action_report", Description: "append action report"},
			{ToolID: "app.chat.surface_state_change", Category: "app", Type: "chat", Tool: "surface_state_change", Description: "append surface state change"},
			{ToolID: "app.chat.config_change", Category: "app", Type: "chat", Tool: "config_change", Description: "record config change"},
		},
		Requires: []string{"ai.llm.stream", "ai.speech.asr", "ai.speech.tts"},
	}
}

func BuildAIServiceManifest(info *AIServiceInfo, tools []AIServiceToolDescriptor, healthy bool) ServiceManifest {
	serviceID := "ai-doubao"
	serviceName := "AI Doubao"
	version := "unknown"
	if info != nil {
		serviceID = firstNonEmpty(strings.TrimSpace(info.ServiceID), serviceID)
		serviceName = firstNonEmpty(strings.TrimSpace(info.ServiceName), serviceName)
		version = firstNonEmpty(strings.TrimSpace(info.Version), version)
	}
	provides := make([]ServiceToolDescriptor, 0, len(tools))
	for _, t := range tools {
		toolID := strings.TrimSpace(t.Name)
		if toolID == "" {
			continue
		}
		td := ServiceToolDescriptor{
			ToolID:               toolID,
			Description:          strings.TrimSpace(t.Description),
			InputSchema:          cloneAnyMap(t.InputSchema),
			OutputSchema:         cloneAnyMap(t.OutputSchema),
			SideEffect:           strings.TrimSpace(t.SideEffect),
			CapabilitiesRequired: uniqueNonEmpty(t.CapabilitiesRequired),
			TimeoutMSDefault:     t.TimeoutMSDefault,
			Streaming:            strings.TrimSpace(t.Streaming),
		}
		parts := strings.Split(toolID, ".")
		if len(parts) >= 3 {
			td.Category = parts[0]
			td.Type = parts[1]
			td.Tool = strings.Join(parts[2:], ".")
		}
		provides = append(provides, td)
	}
	reliability := "verified"
	if !healthy {
		reliability = "unverified"
	}
	return ServiceManifest{
		ServiceID:   serviceID,
		ServiceName: serviceName,
		Version:     version,
		Reliability: reliability,
		Visibility:  "public",
		Provides:    provides,
	}
}
