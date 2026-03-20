package app

import "kagent/pkg/toolproto"

type ServiceToolDescriptor = toolproto.ServiceTool
type ServiceManifest = toolproto.ServiceManifest

func ChatServerServiceManifest() ServiceManifest {
	return toolproto.NormalizeServiceManifest(ServiceManifest{
		ServiceID:   "chat_server",
		ServiceName: "chat_server",
		Version:     "1.0.0",
		Reliability: "trusted",
		Visibility:  "public",
		Provides: []ServiceToolDescriptor{
			{ToolID: "app.chat.project_list", Category: "app", Type: "chat", Tool: "project_list", Description: "list projects", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.project_create", Category: "app", Type: "chat", Tool: "project_create", Description: "create project", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.project_update", Category: "app", Type: "chat", Tool: "project_update", Description: "update project", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.project_delete", Category: "app", Type: "chat", Tool: "project_delete", Description: "delete project", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.thread_list", Category: "app", Type: "chat", Tool: "thread_list", Description: "list threads", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.thread_create", Category: "app", Type: "chat", Tool: "thread_create", Description: "create thread", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.thread_update", Category: "app", Type: "chat", Tool: "thread_update", Description: "update thread", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.thread_delete", Category: "app", Type: "chat", Tool: "thread_delete", Description: "delete thread", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.config.get", Category: "app", Type: "chat", Tool: "config_get", Description: "get effective chat runtime config", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.config.update", Category: "app", Type: "chat", Tool: "config_update", Description: "update chat runtime config", AllowedCallerTypes: []string{"user"}},
			{ToolID: "app.chat.stream", Category: "app", Type: "chat", Tool: "stream", Description: "chat stream websocket", Streaming: true, StreamingMode: "ws", WSPath: "/service/tool/ws", AllowedCallerTypes: []string{"user"}},
			{ToolID: "service.lifecycle.health", Category: "service", Type: "lifecycle", Tool: "health", Description: "service health probe", AllowedCallerTypes: []string{"service"}},
			{ToolID: "service.lifecycle.state.get", Category: "service", Type: "lifecycle", Tool: "state.get", Description: "service lifecycle state snapshot", AllowedCallerTypes: []string{"service"}},
			{ToolID: "service.lifecycle.shutdown", Category: "service", Type: "lifecycle", Tool: "shutdown", Description: "service shutdown", AllowedCallerTypes: []string{"service"}},
		},
		Requires: []string{"ai.llm.stream", "ai.speech.asr", "ai.speech.tts"},
	})
}
