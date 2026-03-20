# Glossary

本文件只维护项目内高频术语。定义应以当前代码语义为准，过时术语标记为 `deprecated`，规划中的概念标记为 `planned`。

| 术语 | 定义（本项目语境） | 状态 | 来源文件 |
| --- | --- | --- | --- |
| Hub | 系统唯一入口、治理边界与审计中心；治理/系统工具已内化到 `hub.*` 工具平面 | active | `hub/cmd/hub/main.go`、`hub/internal/gateway/*` |
| Service | 独立进程的能力/业务单元，通常通过 `/service/tool/exec` 和 `/service/tool/ws` 与 Hub 对接 | active | `services/*/cmd/*/main.go`、`services/*/manifest.json` |
| Page | 前端宿主页面，承担高频交互仲裁 | active | `webui/page/*` |
| Surface | 插件化 UI 模块 | active | `webui/surface/*`、`services/surface_manager/cmd/surface_manager/main.go` |
| Tool ID | 能力的逻辑标识，采用 `category.type.action` 风格；当前主流包括 `hub.*`、`service.lifecycle.*`、`app.*`、`storage.*`、`ai.*` | active | `pkg/toolproto/v1.go` |
| CallRequest / CallResponse | 工具调用请求/响应结构 | active | `pkg/toolproto/v1.go` |
| Context | 工具调用上下文，包含 `request_id`、`trace_id`、caller 等信息 | active | `pkg/toolproto/v1.go` |
| Caller | 调用者身份，区分 user/service/surface/anonymous | active | `pkg/toolproto/v1.go` |
| Effects | 工具响应顶层副作用描述（写 cookie / 写 header） | active | `pkg/toolproto/v1.go` |
| ServiceTool | 服务向 Hub 注册的单个工具声明，包含 `allowed_caller_types`、`streaming`、`ws_path`、`capabilities_required` 等字段 | active | `pkg/toolproto/supervisor.go` |
| allowed_caller_types | 工具声明允许哪些 caller type 访问 | active | `pkg/toolproto/supervisor.go` |
| ScopeSupport | 旧的 caller 兼容声明字段，当前主要用于 manifest 兼容输出或回填 | deprecated | `services/file_storage/internal/app/hub_builtins.go`、`services/chat_server/internal/app/service_manifest.go` |
| hub.governance.service.register | Hub 的服务注册工具 | active | `hub/internal/supervisor/handler.go`、`hub/internal/gateway/hub_manifest.go` |
| hub.governance.service.heartbeat | Hub 的服务心跳工具 | active | `hub/internal/supervisor/handler.go`、`hub/internal/gateway/hub_manifest.go` |
| service.lifecycle.health | 服务生命周期健康探测工具 | active | `services/file_storage/cmd/file_storage/main.go`、`services/chat_server/cmd/chat_server/main.go`、`services/ai_doubao/cmd/ai_doubao/main.go` |
| service.lifecycle.shutdown | 服务生命周期停机工具 | active | `services/file_storage/cmd/file_storage/main.go`、`services/chat_server/cmd/chat_server/main.go`、`services/ai_doubao/cmd/ai_doubao/main.go` |
| hub.system.smoke.test | Hub 内建的端到端烟测工具 | active | `hub/internal/gateway/system_handler.go`、`hub/internal/app/smoke.go`、`hub/internal/gateway/hub_manifest.go` |
| Bootstrap Secret | 启动期互信秘密，包含 Hub 注册地址、`S2H_TOKEN`/`H2S_TOKEN` 等 | active | `pkg/hubsvc/session.go`、`services/*/run/.service_secret` |
| `.service_secret` | 启动期秘密文件，供 Service 与 Hub 建立互信 | active | `services/*/run/.service_secret` |
| Service Manifest | Service 的运行清单，描述入口参数、生命周期、可见性和工具声明 | active | `services/*/manifest.json` |
| Runtime Manifest | Hub 拉起 service 时使用的运行态清单 | active | `services/*/run/manifest.json`、`hub/internal/supervisor/lifecycle.go` |
| LifecycleManager | Hub 内部的多服务启动/停止/重启编排器 | active | `hub/internal/supervisor/lifecycle.go` |
| X-Service-Auth | Service -> Hub 的内部调用凭证 | active | `pkg/hubsvc/session.go`、`hub/internal/security/headers.go` |
| X-Hub-Auth | Hub -> Service 的内部调用凭证 | active | `pkg/hubsvc/session.go`、`hub/internal/security/headers.go` |
| svc.account.token | account 服务登录态 cookie 名称 | active | `hub/internal/app/auth.go`、`services/account/cmd/account/main.go` |
| kagent_token | 旧版用户登录态 cookie 名称 | deprecated | `hub/internal/app/auth.go` |
| Hub SSO Map | Hub 维护的 `user_id -> sid` 活跃会话映射 | active | `hub/internal/app/auth.go` |
| hub.system.report_log | 统一业务日志上报虚拟工具 | active | `hub/internal/gateway/tool_handler.go`、`hub/internal/app/hub_platform.go` |
| hub_only | 工具调用上下文中的 Hub 内部调用标记 | active | `hub/internal/gateway/tool_handler.go`、`services/*/cmd/*/main.go` |
| Surface Session Token | Surface 相关会话令牌 | active | `services/surface_manager/cmd/surface_manager/main.go` |
| Capability Token | Surface 访问目录/API 的作用域令牌 | active | `services/surface_manager/cmd/surface_manager/main.go`、`hub/internal/app/identity.go` |
| SmokeTester | Hub 内建的端到端烟测器，当前通过工具调用串起注册、登录、旧 token 失效和跨服务工具核验 | active | `hub/internal/app/smoke.go` |
| ai_doubao.system.shutdown | `ai_doubao` 的兼容生命周期停机别名，当前与 `service.lifecycle.shutdown` 并存 | deprecated | `services/ai_doubao/cmd/ai_doubao/main.go` |
| Protected Headers | 不能从浏览器透传的内部认证/身份头 | active | `hub/internal/security/headers.go` |
| Request ID | 单次请求的追踪标识 | active | `pkg/toolproto/v1.go`、`hub/internal/security/headers.go` |
| Trace ID | 跨链路追踪标识 | active | `pkg/toolproto/v1.go`、`hub/internal/security/headers.go` |
| Internal Dispatcher | Hub 内部内存调度器，承接 `hub.*` 逻辑请求 | planned | `plan/2603182125-hub-internal-toolification-devplan.md` |

---

**文档更新时间**：2026-03-20 10:25:49 CST

**信息来源**：`pkg/toolproto/v1.go`、`pkg/toolproto/supervisor.go`、`pkg/hubsvc/session.go`、`hub/internal/app/auth.go`、`hub/internal/app/identity.go`、`hub/internal/gateway/hub_manifest.go`、`hub/internal/gateway/system_handler.go`、`hub/internal/gateway/tool_handler.go`、`hub/internal/supervisor/handler.go`、`hub/internal/app/hub_platform.go`、`hub/internal/app/smoke.go`、`services/ai_doubao/cmd/ai_doubao/main.go`、`services/chat_server/cmd/chat_server/main.go`、`services/file_storage/cmd/file_storage/main.go`、`services/account/cmd/account/main.go`、`services/surface_manager/cmd/surface_manager/main.go`。
