# Core

本文件合并项目核心理念与开发规范。它回答的是：这个项目的基本不变量是什么，agent 在修改前应遵守什么边界。

## 1. 项目概览

`kagent` 是一个基于 `Hub + 多独立 Service` 架构的本地多进程 AI 交互与工具平台。当前实现仍然遵循“交互在前端，治理在 Hub，执行在 Service”的分层，同时把 Hub 的治理面和系统面尽量统一到工具平面：

- `Page` 负责用户交互、录音/打断等高频仲裁和工具调用编排。
- `Hub` 负责统一入口、鉴权、路由治理、审计、生命周期与服务调度，其中治理/系统能力已主要以 `hub.*` 工具暴露。
- `Service` 负责原子能力执行或业务协作执行，通过统一工具协议接受 Hub 调度，并以 `service.lifecycle.*` 作为生命周期正式契约。

这个分层不是风格偏好，而是当前仓库的运行事实。新增能力、服务或 Surface 时，应尽量保持逻辑路径、治理边界和数据边界不被打穿。

## 2. 核心理念

### 2.1 逻辑路径优先（Logic > Physics）

跨模块协作优先依赖 `tool_id` 等逻辑标识，而不是固定端口、进程名或私有 URL。端口和进程只是运行时载体，不应成为业务契约。

- 实现落点：`hub/internal/gateway/tool_handler.go` 的 `POST /api/tool/call` 和 `GET /api/tool/ws?tool_id=...`，以及 `hub/internal/routing/*` 的实例选择与记录。
- 相关协议：`pkg/toolproto/v1.go` 中的 `CallRequest.ToolID`、`Context`、`Caller`。

### 2.2 Client-Driven 交互模型

录音开始/结束、打断/继续、何时触发调用等高频节奏，应由 `Page` 作为单一事实源仲裁，后端承担被动执行和可观测返回。这样可以避免把交互时序绑死在后端进程和网络抖动上。

- 实现落点：`webui/page/chat/` 与 `webui/page/surface/` 的页面逻辑，Hub 的静态资源托管与默认跳转。

### 2.3 治理权收敛于 Hub

服务发现、路由选择、审计、身份/权限、生命周期编排都应收敛在 Hub。Service 不应绕过 Hub 与其他 Service 直接互连，除非实现层明确允许且仍保留治理记录。

- 实现落点：`hub/internal/supervisor/*`、`hub/internal/routing/*`、`hub/internal/observability/*`、`hub/internal/gateway/*`。

### 2.4 工具平面统一

平台对外能力收敛为统一工具平面：原子工具和流式工具都由 Hub 暴露，并由 Hub 统一做路由、caller 识别、头部清洗与副作用落地。Hub 的治理、管理和系统能力也已经以工具形式注册，但仍保留少量 HTTP/WS 承载面作为兼容或静态资源入口。

- 原子工具入口：`POST /api/tool/call`
- 流式工具入口：`GET /api/tool/ws?tool_id=...`
- Service 侧入口：`POST /service/tool/exec`、`GET /service/tool/ws`

当前 `toolproto.ServiceTool` 已支持 `allowed_caller_types`、`streaming`、`ws_path`、`capabilities_required` 等声明，说明工具层已经成为真实的运行契约；Hub 侧的 `hub.governance.service.*`、`hub.admin.*`、`hub.system.*` 也都通过 tool 注册暴露。

### 2.5 零信任与头部卫生（Header Hygiene）

身份不能靠端口、`RemoteAddr` 或“看起来像”来推断，必须依赖互信 header + 严格校验。Hub 还需要在转发前清洗 protected headers，避免浏览器侧伪造内部身份字段影响后端。

- 实现落点：`hub/internal/security/headers.go`
- 互信通道：`pkg/hubsvc/session.go` 以及各 Service 的 `/.service_secret` 启动期秘密
- 当前身份模型：`hub/internal/app/identity.go`

### 2.6 生命周期与编排收敛

多进程系统的启动、停止、冲突清理、Ready 判定、可用性探测和冒烟验证，应该尽量由 Hub 收敛管理。脚本只负责构建和拉起最小入口，不应承担复杂治理逻辑。

- 实现落点：`hub/internal/supervisor/lifecycle.go`、`hub/internal/supervisor/process_control.go`
- Hub 入口：`hub/cmd/hub/main.go`
- 启动脚本：`scripts/deploy.sh`

Hub 侧当前的正式生命周期调用已经优先走 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，而 `/healthz` 仅作为兼容 fallback 参与存活探测；`hub.governance.service.register` 与 `hub.governance.service.heartbeat` 则作为服务注册和心跳的正式工具入口。

### 2.7 数据与文件边界

Service 的文件系统边界应尽量收敛到自身目录；对用户数据和共享存储的读写，应尽量通过 Hub 的工具平面完成，以便统一审计、能力隔离和授权闭环。

- `chat-server` 通过 Hub 工具客户端访问数据库和存储能力。
- `file`、`database`、`surface-manager` 承担各自的存储/文件/Surface 相关能力。

### 2.8 可观测与审计

跨边界调用都应携带 `request_id`、`trace_id` 和 caller identity，并在 Hub 侧形成可检索审计日志。业务事件通过统一上报工具记录，避免分散打印导致链路断裂。

- 实现落点：`hub/internal/security/headers.go`、`hub/internal/observability/audit.go`
- 统一上报工具：`hub.system.report_log`

## 3. 开发规范

### 3.1 先核验再判断

涉及项目状态、目录、职责、协议或运行行为时，优先用真实文件、Git 历史和可复现命令核验，不靠记忆补全。

### 3.2 修改以最小可行变更为主

修改时优先做最小可行变更，避免无关重构和风格化扩散。只有在确实需要修复结构性问题时，才扩大改动范围。

### 3.3 无法确认的信息标为待确认

任何无法从代码或 Git 证据直接确认的内容，都应放入“待确认”而不是直接写成事实。不要把推测写成稳定事实。

### 3.4 开发记录与说明文档分离

- `doc/_devlog.md` 只记录开发增量，按追加方式维护。
- `doc/_instruction.md` 只做入口和路由，不再承载完整历史。
- 本文件只描述当前理念、边界和规范，不重复记录最近改动。

### 3.5 常用验证方式

常见验证包括构建、部署、重置环境、系统烟测和日志检查。具体采用哪一种，取决于本次变更影响面。

### 3.6 高风险动作先确认

遇到大规模删除、不可逆重写、明显可能导致数据丢失或服务中断的动作，先征求用户确认。

## 4. 当前事实边界

### 4.1 账号认证链路

`Hub` 当前通过 `svc.account.token`、`account.system.keys.get` 和活跃 `sid` 映射完成账号态校验与同步。`account` 服务负责签发 token、维护活跃会话并向 Hub 暴露公钥信息。

### 4.2 Surface 身份链路

`surface-manager` 已实现 `ui.surface.*` 的 session / capability 相关工具，但 `hub/internal/app/identity.go` 里对 `X-Surface-Token` 的识别仍带有过渡性占位逻辑，说明这条链路仍在收敛中。

### 4.3 运行配置事实

`config/services.json` 和 `hub/config/services.json` 当前内容一致，且都列出了 `ai-doubao`、`chat-server`、`account`、`file`、`database`、`surface-manager`。长期以哪个文件作为单一事实源，仍是待确认项。

### 4.4 Hub <-> Service tool 化现状

以下是当前代码里已经落地的 Hub <-> Service(tool) 事实：

- Hub 侧已把 `hub.governance.service.register`、`hub.governance.service.heartbeat`、`hub.governance.service.drain`、`hub.admin.*`、`hub.system.*` 注册为工具。
- `ai-doubao`、`file`、`chat-server` 的启动注册都通过 `POST /api/tool/call` 调用 `hub.governance.service.register`，心跳守护也通过 `hub.governance.service.heartbeat` 调用。
- `file` 和 `chat-server` 都把 `service.lifecycle.health`、`service.lifecycle.shutdown` 作为正式生命周期工具处理。
- `ai-doubao` 也已实现 `service.lifecycle.health`、`service.lifecycle.shutdown`，但仍保留 `ai-doubao.system.health`、`ai-doubao.system.shutdown` 作为兼容别名。
- `hub/internal/supervisor/process_control.go` 仍保留 `/healthz` fallback，用于兼容探测还未完全工具化的场景。
- `chat-server` 仍保留 `/admin/shutdown` 兼容入口，说明“工具化已成为主路径”，但并未做到“所有控制面 HTTP 入口都已删除”。

### 4.5 代码复用边界

部分 Service 仍复用 `pkg/*` 下的共享包，这意味着“完全独立可拷走运行”的理念尚未完全收敛，需要后续按实际代码继续判断。

---

**文档更新时间**：2026-03-19 01:18 CST

**信息来源**：`hub/cmd/hub/main.go`、`hub/internal/app/auth.go`、`hub/internal/app/identity.go`、`hub/internal/gateway/hub_manifest.go`、`hub/internal/gateway/system_handler.go`、`hub/internal/gateway/admin_handler.go`、`hub/internal/gateway/tool_handler.go`、`hub/internal/security/headers.go`、`hub/internal/supervisor/lifecycle.go`、`hub/internal/supervisor/process_control.go`、`scripts/deploy.sh`、`services/ai-doubao/cmd/ai-doubao/main.go`、`services/chat-server/cmd/chat-server/main.go`、`services/file/cmd/file/main.go`、`services/account/cmd/account/main.go`、`services/surface-manager/cmd/surface-manager/main.go`。
