package app

import "kagent/pkg/toolproto"

type ServiceToolDescriptor = toolproto.ServiceTool
type ServiceManifest = toolproto.ServiceManifest

func surfaceTool(toolID string, description string, inputSchema map[string]any, outputSchema map[string]any, allowed []string, timeoutMS int, hasEffects bool, riskLV int, sideEffect string) ServiceToolDescriptor {
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
		ScopeSupport:       []string{"user", "surface", "service"},
	})
}

func surfaceObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func BuiltinServiceManifests() []ServiceManifest {
	return []ServiceManifest{
		toolproto.NormalizeServiceManifest(ServiceManifest{
			ServiceID:   "surface_manager",
			ServiceName: "surface_manager",
			Version:     "1.0.0",
			Visibility:  "public",
			Provides: []ServiceToolDescriptor{
				surfaceTool("service.lifecycle.health", "service health probe", surfaceObjectSchema(map[string]any{}), surfaceObjectSchema(map[string]any{"ok": map[string]any{"type": "boolean"}}), []string{"service"}, 3000, false, 1, "read"),
				surfaceTool("service.lifecycle.state.get", "service lifecycle state snapshot", surfaceObjectSchema(map[string]any{}), surfaceObjectSchema(map[string]any{"status": map[string]any{"type": "string"}, "healthy": map[string]any{"type": "boolean"}}), []string{"service"}, 3000, false, 1, "read"),
				surfaceTool("service.lifecycle.shutdown", "service shutdown", surfaceObjectSchema(map[string]any{"reason": map[string]any{"type": "string"}}), surfaceObjectSchema(map[string]any{"shutting_down": map[string]any{"type": "boolean"}}), []string{"service"}, 3000, true, 4, "process"),
				surfaceTool("ui.surface.catalog_list", "列出当前可加载的 surface catalog。", surfaceObjectSchema(map[string]any{}), surfaceObjectSchema(map[string]any{"items": map[string]any{"type": "array"}}), []string{"user", "service"}, 5000, false, 1, "read"),
				surfaceTool("ui.surface.get", "读取单个 surface 的入口与元数据。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}}, "surface_id"), surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "entry_url": map[string]any{"type": "string"}}), []string{"user", "service"}, 5000, false, 1, "read"),
				surfaceTool("ui.surface.enable_set", "更新 surface 是否启用。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "enabled": map[string]any{"type": "boolean"}}, "surface_id", "enabled"), surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "enabled": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 3, "config"),
				surfaceTool("ui.surface.session_issue", "发放 surface session token。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "ttl_seconds": map[string]any{"type": "integer"}}, "surface_id"), surfaceObjectSchema(map[string]any{"surface_session_token": map[string]any{"type": "string"}, "exp_ms": map[string]any{"type": "integer"}}), []string{"user", "service"}, 5000, false, 2, "auth"),
				surfaceTool("ui.surface.capability_issue", "发放 surface capability token。", surfaceObjectSchema(map[string]any{"surface_session_token": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string"}, "path_prefix": map[string]any{"type": "string"}, "ttl_seconds": map[string]any{"type": "integer"}}, "surface_session_token", "scope"), surfaceObjectSchema(map[string]any{"capability_token": map[string]any{"type": "string"}, "exp_ms": map[string]any{"type": "integer"}}), []string{"user", "service"}, 5000, false, 2, "auth"),
				surfaceTool("ui.surface.runtime_status", "查看 surface 当前 runtime 状态。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}}, "surface_id"), surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}}), []string{"user", "service"}, 5000, false, 1, "read"),
				surfaceTool("ui.surface.logs_query", "查询 surface 运行日志。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer"}}, "surface_id"), surfaceObjectSchema(map[string]any{"items": map[string]any{"type": "array"}}), []string{"user", "service"}, 5000, false, 1, "read"),
				surfaceTool("ui.surface.rescan", "重新扫描 surface 包目录。", surfaceObjectSchema(map[string]any{}), surfaceObjectSchema(map[string]any{"rescanned": map[string]any{"type": "boolean"}}), []string{"user"}, 10000, true, 2, "filesystem"),
				surfaceTool("ui.surface.catalog_cleanup", "清理重复或陈旧的 surface catalog 记录。", surfaceObjectSchema(map[string]any{}), surfaceObjectSchema(map[string]any{"deleted_ids": map[string]any{"type": "array"}, "deleted_count": map[string]any{"type": "integer"}}), []string{"user"}, 10000, true, 2, "config"),
				surfaceTool("ui.surface.rebind", "重建 surface entry 绑定结果。", surfaceObjectSchema(map[string]any{}), surfaceObjectSchema(map[string]any{"rebound": map[string]any{"type": "boolean"}}), []string{"user"}, 10000, true, 2, "filesystem"),
				surfaceTool("ui.surface.generate", "生成一个新的 surface package。", surfaceObjectSchema(map[string]any{"surface_name": map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"}}, "surface_name", "prompt"), surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "dir": map[string]any{"type": "string"}}), []string{"user"}, 120000, true, 4, "filesystem"),
				surfaceTool("ui.surface.package_read", "读取 surface package 文件。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "surface_id", "path"), surfaceObjectSchema(map[string]any{"data_base64": map[string]any{"type": "string"}}), []string{"user", "service"}, 5000, false, 2, "read"),
				surfaceTool("ui.surface.package_write", "写入 surface package 文件。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "data_base64": map[string]any{"type": "string"}}, "surface_id", "path", "data_base64"), surfaceObjectSchema(map[string]any{"written": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, 4, "filesystem"),
				surfaceTool("ui.surface.package_list", "列出 surface package 文件列表。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}}, "surface_id"), surfaceObjectSchema(map[string]any{"items": map[string]any{"type": "array"}}), []string{"user", "service"}, 5000, false, 1, "read"),
				surfaceTool("ui.surface.fs_read", "通过 capability 读取 surface 受限文件。", surfaceObjectSchema(map[string]any{"capability_token": map[string]any{"type": "string"}, "surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "capability_token", "surface_id", "path"), surfaceObjectSchema(map[string]any{"data_base64": map[string]any{"type": "string"}}), []string{"user", "service"}, 5000, false, 2, "read"),
				surfaceTool("ui.surface.fs_write", "通过 capability 写入 surface 受限文件。", surfaceObjectSchema(map[string]any{"capability_token": map[string]any{"type": "string"}, "surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "data_base64": map[string]any{"type": "string"}}, "capability_token", "surface_id", "path", "data_base64"), surfaceObjectSchema(map[string]any{"written": map[string]any{"type": "boolean"}}), []string{"user", "service"}, 5000, true, 4, "filesystem"),
				surfaceTool("ui.surface.fs_list", "通过 capability 列出 surface 受限目录。", surfaceObjectSchema(map[string]any{"capability_token": map[string]any{"type": "string"}, "surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "capability_token", "surface_id", "path"), surfaceObjectSchema(map[string]any{"items": map[string]any{"type": "array"}}), []string{"user", "service"}, 5000, false, 2, "read"),
				surfaceTool("ui.surface.fs_delete", "通过 capability 删除 surface 受限文件。", surfaceObjectSchema(map[string]any{"capability_token": map[string]any{"type": "string"}, "surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "recursive": map[string]any{"type": "boolean"}}, "capability_token", "surface_id", "path"), surfaceObjectSchema(map[string]any{"deleted": map[string]any{"type": "boolean"}}), []string{"user", "service"}, 5000, true, 4, "filesystem"),
				surfaceTool("ui.surface.fs_sign_static", "通过 capability 为静态文件签名下载 URL。", surfaceObjectSchema(map[string]any{"capability_token": map[string]any{"type": "string"}, "surface_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "capability_token", "surface_id", "path"), surfaceObjectSchema(map[string]any{"url": map[string]any{"type": "string"}}), []string{"user", "service"}, 5000, false, 2, "read"),
				surfaceTool("ui.surface.db_roundtrip", "验证 service caller 经 Hub 访问共享数据库的链路。", surfaceObjectSchema(map[string]any{"surface_id": map[string]any{"type": "string"}}, "surface_id"), surfaceObjectSchema(map[string]any{"ok": map[string]any{"type": "boolean"}}), []string{"service"}, 10000, true, 3, "database"),
			},
		}),
	}
}
