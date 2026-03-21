package gateway

import "kagent/pkg/toolproto"

func hubTool(
	toolID string,
	description string,
	inputSchema map[string]any,
	outputSchema map[string]any,
	allowedCallerTypes []string,
	timeoutMS int,
	hubOnly bool,
	hasEffects bool,
	riskLV int,
	sideEffect string,
) toolproto.ServiceTool {
	return toolproto.NormalizeServiceTool(toolproto.ServiceTool{
		ToolID:             toolID,
		Description:        description,
		InputSchema:        inputSchema,
		OutputSchema:       outputSchema,
		Protocol:           "http",
		Version:            "1.0.0",
		HubOnly:            hubOnly,
		HasEffects:         hasEffects,
		RiskLV:             riskLV,
		SideEffect:         sideEffect,
		TimeoutMSDefault:   timeoutMS,
		AllowedCallerTypes: allowedCallerTypes,
	})
}

func hubObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// HubManifest returns the manifest of the Hub itself as a virtual service.
func HubManifest() toolproto.SupervisorRegisterRequest {
	healthy := true
	return toolproto.SupervisorRegisterRequest{
		ServiceID:  "hub",
		InstanceID: "builtin-hub",
		Version:    "1.0.0",
		Transport:  "internal",
		Healthy:    &healthy,
		Tools: []toolproto.ServiceTool{
			hubTool(
				"hub.admin.services.list",
				"列出 Hub 当前托管服务、注册服务和完整工具目录。",
				hubObjectSchema(map[string]any{}),
				hubObjectSchema(map[string]any{
					"active_provider": map[string]any{"type": "string"},
					"services":        map[string]any{"type": "array"},
					"managed":         map[string]any{"type": "array"},
					"tools":           map[string]any{"type": "array"},
				}),
				[]string{"user"}, 5000, true, false, 1, "read",
			),
			hubTool(
				"hub.admin.routes.get",
				"查看当前工具路由、绑定关系和审计摘要。",
				hubObjectSchema(map[string]any{
					"audit_limit": map[string]any{"type": "integer", "default": 200},
				}),
				hubObjectSchema(map[string]any{
					"bindings":       map[string]any{"type": "array"},
					"routing_schema": map[string]any{"type": "object"},
				}),
				[]string{"user"}, 5000, true, false, 1, "read",
			),
			hubTool(
				"hub.admin.routes.bind",
				"为指定工具设置人工路由绑定。",
				hubObjectSchema(map[string]any{
					"tool_id":    map[string]any{"type": "string"},
					"service_id": map[string]any{"type": "string"},
				}, "tool_id", "service_id"),
				hubObjectSchema(map[string]any{
					"ok":       map[string]any{"type": "boolean"},
					"bindings": map[string]any{"type": "array"},
				}),
				[]string{"user"}, 5000, true, true, 4, "routing",
			),
			hubTool(
				"hub.admin.audits.list",
				"查询 Hub 路由审计与治理事件。",
				hubObjectSchema(map[string]any{
					"limit": map[string]any{"type": "integer", "default": 100},
				}),
				hubObjectSchema(map[string]any{
					"tool_call_audits": map[string]any{"type": "array"},
					"events":           map[string]any{"type": "array"},
				}),
				[]string{"user"}, 5000, true, false, 1, "read",
			),
			hubTool(
				"hub.admin.tool.probe",
				"绕过页面直探一个服务工具，用于治理诊断。",
				hubObjectSchema(map[string]any{
					"service_id": map[string]any{"type": "string"},
					"tool_id":    map[string]any{"type": "string"},
					"args":       map[string]any{"type": "object"},
					"timeout_ms": map[string]any{"type": "integer"},
				}, "service_id", "tool_id"),
				hubObjectSchema(map[string]any{
					"ok":     map[string]any{"type": "boolean"},
					"result": map[string]any{},
				}),
				[]string{"user"}, 15000, true, false, 2, "read",
			),
			hubTool(
				"hub.admin.service.get",
				"查看单个托管服务的运行、配置、治理和工具详情。",
				hubObjectSchema(map[string]any{
					"service_id": map[string]any{"type": "string"},
				}, "service_id"),
				hubObjectSchema(map[string]any{
					"service":          map[string]any{"type": "object"},
					"runtime_manifest": map[string]any{"type": "object"},
					"config":           map[string]any{"type": "object"},
					"state":            map[string]any{"type": "object"},
					"instances":        map[string]any{"type": "array"},
					"audits":           map[string]any{"type": "array"},
					"tool_views":       map[string]any{"type": "array"},
				}),
				[]string{"user"}, 5000, true, false, 1, "read",
			),
			hubTool("hub.admin.service.start", "启动一个托管服务实例。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}), []string{"user"}, 30000, true, true, 4, "process"),
			hubTool("hub.admin.service.stop", "停止一个托管服务实例。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "stopped": map[string]any{"type": "boolean"}}), []string{"user"}, 15000, true, true, 4, "process"),
			hubTool("hub.admin.service.restart", "重启一个托管服务实例。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}), []string{"user"}, 45000, true, true, 4, "process"),
			hubTool("hub.admin.service.drain", "排空一个托管服务实例。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "draining": map[string]any{"type": "boolean"}}), []string{"user"}, 10000, true, true, 4, "process"),
			hubTool("hub.admin.service.rebind", "在服务变更后重新计算工具绑定。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "bindings": map[string]any{"type": "array"}}), []string{"user"}, 5000, true, true, 3, "routing"),
			hubTool("hub.admin.service.disable", "停止服务并将其从人工治理中移除。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "disabled": map[string]any{"type": "boolean"}}), []string{"user"}, 15000, true, true, 4, "process"),
			hubTool("hub.admin.service.governance.update", "更新 Hub 侧 service 治理配置（enabled / reliability）。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "enabled": map[string]any{"type": "boolean"}, "reliability": map[string]any{"type": "string", "enum": []string{"trusted", "verified", "unverified", "risky", "high_risk"}}}, "service_id", "enabled", "reliability"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "enabled": map[string]any{"type": "boolean"}, "reliability": map[string]any{"type": "string"}}), []string{"user"}, 5000, true, true, 4, "config"),
			hubTool("hub.admin.service.manifest.get", "读取托管服务运行时 manifest。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "manifest_path": map[string]any{"type": "string"}, "manifest": map[string]any{"type": "object"}}), []string{"user"}, 5000, true, false, 1, "read"),
			hubTool("hub.admin.service.manifest.update", "更新托管服务运行时 manifest。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "manifest": map[string]any{"type": "object"}}, "service_id", "manifest"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "manifest": map[string]any{"type": "object"}}), []string{"user"}, 5000, true, true, 4, "config"),
			hubTool("hub.admin.service.config.get", "读取托管服务 config.json。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "config_path": map[string]any{"type": "string"}, "config_json": map[string]any{"type": "object"}}), []string{"user"}, 5000, true, false, 1, "read"),
			hubTool("hub.admin.service.config.update", "更新托管服务配置，并同步写入项目 config/ 与 run/config/。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "config_json": map[string]any{"type": "object"}}, "service_id", "config_json"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "config_path": map[string]any{"type": "string"}}), []string{"user"}, 5000, true, true, 4, "config"),
			hubTool("hub.admin.service.files.list", "列出托管服务工作区可编辑文件。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "items": map[string]any{"type": "array"}}), []string{"user"}, 5000, true, false, 1, "read"),
			hubTool("hub.admin.service.files.read", "读取托管服务工作区文件。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "service_id", "path"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "data_base64": map[string]any{"type": "string"}}), []string{"user"}, 5000, true, false, 2, "read"),
			hubTool("hub.admin.service.files.write", "写入托管服务工作区文件。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "data_base64": map[string]any{"type": "string"}}, "service_id", "path", "data_base64"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}, "written": map[string]any{"type": "boolean"}}), []string{"user"}, 5000, true, true, 5, "filesystem"),
			hubTool("hub.admin.service.build", "构建托管服务可执行文件。", hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}}, "service_id"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "exec_path": map[string]any{"type": "string"}, "output": map[string]any{"type": "string"}}), []string{"user"}, 120000, true, true, 4, "build"),
			hubTool("hub.admin.service.generate", "在用户工作区下生成一个新的受管服务骨架。", hubObjectSchema(map[string]any{"service_name": map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"}}, "service_name", "prompt"), hubObjectSchema(map[string]any{"service_id": map[string]any{"type": "string"}, "service_dir": map[string]any{"type": "string"}}), []string{"user"}, 120000, true, true, 5, "filesystem"),

			hubTool(
				"hub.governance.service.register",
				"注册服务实例和它的完整工具声明。",
				hubObjectSchema(map[string]any{
					"service_id":  map[string]any{"type": "string"},
					"instance_id": map[string]any{"type": "string"},
					"version":     map[string]any{"type": "string"},
					"transport":   map[string]any{"type": "string"},
					"endpoint":    map[string]any{"type": "object"},
					"tools":       map[string]any{"type": "array"},
					"healthy":     map[string]any{"type": "boolean"},
				}, "service_id", "instance_id", "endpoint"),
				hubObjectSchema(map[string]any{
					"heartbeat_interval_sec":             map[string]any{"type": "integer"},
					"inverse_heartbeat_interval_sec":     map[string]any{"type": "integer"},
					"inverse_heartbeat_failures_to_exit": map[string]any{"type": "integer"},
					"drain_grace_period_sec":             map[string]any{"type": "integer"},
				}),
				[]string{"service", "anonymous"}, 5000, true, true, 3, "governance",
			),
			hubTool(
				"hub.governance.service.heartbeat",
				"更新服务实例心跳、状态和健康信息。",
				hubObjectSchema(map[string]any{
					"service_id":  map[string]any{"type": "string"},
					"instance_id": map[string]any{"type": "string"},
					"status":      map[string]any{"type": "string"},
					"healthy":     map[string]any{"type": "boolean"},
					"pid":         map[string]any{"type": "integer"},
					"endpoint":    map[string]any{"type": "string"},
				}, "service_id", "instance_id"),
				hubObjectSchema(map[string]any{
					"status":       map[string]any{"type": "string"},
					"last_seen_ms": map[string]any{"type": "integer"},
				}),
				[]string{"service"}, 3000, true, true, 2, "governance",
			),
			hubTool(
				"hub.governance.service.drain",
				"协调服务实例排空，准备下线或路由摘除。",
				hubObjectSchema(map[string]any{
					"service_id":       map[string]any{"type": "string"},
					"instance_id":      map[string]any{"type": "string"},
					"reason":           map[string]any{"type": "string"},
					"grace_period_sec": map[string]any{"type": "integer"},
				}, "service_id"),
				hubObjectSchema(map[string]any{
					"draining": map[string]any{"type": "boolean"},
				}),
				[]string{"service", "user"}, 5000, true, true, 3, "governance",
			),

			hubTool("hub.system.version.get", "返回 Hub 版本信息。", hubObjectSchema(map[string]any{}), hubObjectSchema(map[string]any{"backend": map[string]any{"type": "string"}, "webui": map[string]any{"type": "string"}}), []string{"anonymous", "user", "service"}, 3000, false, false, 0, "read"),
			hubTool("hub.system.state.get", "返回 Hub 当前服务、工具和治理状态快照。", hubObjectSchema(map[string]any{}), hubObjectSchema(map[string]any{"status": map[string]any{"type": "string"}, "services": map[string]any{"type": "array"}, "tools": map[string]any{"type": "array"}}), []string{"user", "service"}, 3000, false, false, 1, "read"),
			hubTool("hub.system.smoke.test", "执行 Hub 核心治理与鉴权烟测。", hubObjectSchema(map[string]any{}), hubObjectSchema(map[string]any{"ok": map[string]any{"type": "boolean"}, "checks": map[string]any{"type": "array"}}), []string{"user", "anonymous"}, 15000, false, false, 2, "read"),
			hubTool("hub.system.report_log", "向 Hub 观测流水线上报一条结构化日志。", hubObjectSchema(map[string]any{"level": map[string]any{"type": "string"}, "module": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "page": map[string]any{"type": "string"}}, "content"), hubObjectSchema(map[string]any{"ok": map[string]any{"type": "boolean"}}), []string{"user", "service", "surface", "anonymous"}, 3000, false, false, 0, "log"),
			hubTool("hub.system.shutdown", "优雅停止 Hub 进程。", hubObjectSchema(map[string]any{}), hubObjectSchema(map[string]any{"shutting_down": map[string]any{"type": "boolean"}}), []string{"user", "anonymous"}, 5000, true, true, 5, "process"),
			hubTool("hub.system.health", "返回 Hub 健康状态与核心摘要。", hubObjectSchema(map[string]any{}), hubObjectSchema(map[string]any{"ok": map[string]any{"type": "boolean"}, "service_count": map[string]any{"type": "integer"}}), []string{"anonymous", "user", "service"}, 3000, false, false, 0, "read"),
		},
	}
}
