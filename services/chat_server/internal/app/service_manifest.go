package app

import "kagent/pkg/toolproto"

type ServiceToolDescriptor = toolproto.ServiceTool
type ServiceManifest = toolproto.ServiceManifest

func chatTool(toolID string, description string, inputSchema map[string]any, outputSchema map[string]any, allowed []string, timeoutMS int, hasEffects bool, riskLV int, sideEffect string) ServiceToolDescriptor {
	return toolproto.NormalizeServiceTool(ServiceToolDescriptor{
		ToolID:             toolID,
		Description:        description,
		InputSchema:        inputSchema,
		OutputSchema:       outputSchema,
		AllowedCallerTypes: allowed,
		TimeoutMSDefault:   timeoutMS,
		HasEffects:         hasEffects,
		RiskLV:             riskLV,
		SideEffect:         sideEffect,
	})
}

func chatObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func ChatServerServiceManifest() ServiceManifest {
	return toolproto.NormalizeServiceManifest(ServiceManifest{
		ServiceID:   "chat_server",
		ServiceName: "chat_server",
		Version:     "1.0.0",
		Visibility:  "public",
		Provides: []ServiceToolDescriptor{
			chatTool("app.chat.project_list", "列出当前用户的 chat project。", chatObjectSchema(map[string]any{}), chatObjectSchema(map[string]any{"items": map[string]any{"type": "array"}}), []string{"user"}, 5000, false, 1, "read"),
			chatTool("app.chat.project_create", "创建 chat project。", chatObjectSchema(map[string]any{"title": map[string]any{"type": "string"}}, "title"), chatObjectSchema(map[string]any{"project_id": map[string]any{"type": "string"}}), []string{"user"}, 5000, true, 2, "database"),
			chatTool("app.chat.project_update", "更新 chat project 元数据。", chatObjectSchema(map[string]any{"project_id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}}, "project_id"), chatObjectSchema(map[string]any{"updated": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 2, "database"),
			chatTool("app.chat.project_delete", "删除 chat project。", chatObjectSchema(map[string]any{"project_id": map[string]any{"type": "string"}}, "project_id"), chatObjectSchema(map[string]any{"deleted": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 3, "database"),
			chatTool("app.chat.thread_list", "列出一个 project 下的对话线程。", chatObjectSchema(map[string]any{"project_id": map[string]any{"type": "string"}}, "project_id"), chatObjectSchema(map[string]any{"items": map[string]any{"type": "array"}}), []string{"user"}, 5000, false, 1, "read"),
			chatTool("app.chat.thread_create", "创建一个对话线程。", chatObjectSchema(map[string]any{"project_id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}}, "project_id"), chatObjectSchema(map[string]any{"thread_id": map[string]any{"type": "string"}}), []string{"user"}, 5000, true, 2, "database"),
			chatTool("app.chat.thread_update", "更新线程标题或元数据。", chatObjectSchema(map[string]any{"thread_id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}}, "thread_id"), chatObjectSchema(map[string]any{"updated": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 2, "database"),
			chatTool("app.chat.thread_delete", "删除对话线程。", chatObjectSchema(map[string]any{"thread_id": map[string]any{"type": "string"}}, "thread_id"), chatObjectSchema(map[string]any{"deleted": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 3, "database"),
			chatTool("app.chat.config.get", "读取当前 chat 页面有效运行配置。", chatObjectSchema(map[string]any{}), chatObjectSchema(map[string]any{"config": map[string]any{"type": "object"}}), []string{"user"}, 5000, false, 1, "read"),
			chatTool("app.chat.config.update", "更新当前 chat 页面运行配置。", chatObjectSchema(map[string]any{"config": map[string]any{"type": "object"}}, "config"), chatObjectSchema(map[string]any{"updated": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 3, "config"),
			toolproto.NormalizeServiceTool(ServiceToolDescriptor{
				ToolID:             "app.chat.stream",
				Description:        "执行 chat 流式会话，返回 LLM 增量、动作和状态事件。",
				InputSchema:        chatObjectSchema(map[string]any{"thread_id": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "config": map[string]any{"type": "object"}}),
				OutputSchema:       chatObjectSchema(map[string]any{"type": map[string]any{"type": "string"}, "payload": map[string]any{"type": "object"}}),
				Protocol:           "ws",
				Streaming:          true,
				StreamingMode:      "ws",
				WSPath:             "/service/tool/ws",
				AllowedCallerTypes: []string{"user"},
				TimeoutMSDefault:   120000,
				HasEffects:         true,
				RiskLV:             2,
				SideEffect:         "chat",
			}),
			chatTool("service.lifecycle.health", "service health probe", chatObjectSchema(map[string]any{}), chatObjectSchema(map[string]any{"ok": map[string]any{"type": "boolean"}}), []string{"service"}, 3000, false, 1, "read"),
			chatTool("service.lifecycle.state.get", "service lifecycle state snapshot", chatObjectSchema(map[string]any{}), chatObjectSchema(map[string]any{"status": map[string]any{"type": "string"}, "healthy": map[string]any{"type": "boolean"}}), []string{"service"}, 3000, false, 1, "read"),
			chatTool("service.lifecycle.shutdown", "service shutdown", chatObjectSchema(map[string]any{"reason": map[string]any{"type": "string"}}), chatObjectSchema(map[string]any{"shutting_down": map[string]any{"type": "boolean"}}), []string{"service"}, 3000, true, 4, "process"),
		},
		Requires: []string{"ai.llm.stream", "ai.speech.asr", "ai.speech.tts", "ui.surface.catalog_list"},
	})
}
