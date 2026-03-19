# Core

本文件只保留项目核心理念、默认边界与开发规范。它回答的是：`kagent` 当前按什么原则运行，agent 修改前应先守住什么不变量。

## 1. 项目定位

`kagent` 是一个基于 `Hub + 多独立 Service` 的本地多进程 AI 交互与工具平台。当前实现遵循三层分工：

1. `Page` 负责交互节奏、页面状态和高频仲裁。
2. `Hub` 负责统一入口、接纳治理、caller 识别、路由、可观测与生命周期编排。
3. `Service` 负责能力执行，并通过标准 `tool` 协议向外提供能力。

这个分层是当前仓库的运行事实，不是风格偏好。新增能力、页面或 service 时，应优先保持逻辑路径、治理边界和数据边界稳定。

## 2. 核心边界

### 2.1 Logic > Physics

跨模块协作应优先依赖 `tool_id`、`caller`、`capabilities_required` 等逻辑标识，而不是固定端口、进程名或私有 URL。端口和进程只是运行时载体，不应成为长期业务契约。

### 2.2 Hub 是治理入口，不是业务替身

`Hub` 负责接纳、路由、前置筛选、展示、统计、审计和生命周期编排；`Hub` 不负责替 `service` 完成业务鉴权、对象级权限和内部限额，也不负责证明 `service` 内部一定正确消费了 caller / capability / 身份结果。

因此：

1. 被 `Hub` 接纳，表示进入统一治理平面。
2. 被 `Hub` 接纳，不等于内部实现已被 `Hub` 背书。
3. `Hub` 的 caller / capability 筛选不是完整业务授权系统。

### 2.3 Service 是自治能力模块

`Service` 是独立进程、能力模块和工具提供者。只有兼容 `Hub` 的 `tool + lifecycle` 协议，并能通过 `manifest / register / heartbeat` 完成标准接入的程序，才会被 `Hub` 接纳。

因此：

1. `service` 可以物理独立运行。
2. `Hub` 对外统一提供其标准入口。
3. `service` 内部是否信任 `Hub`、是否拒绝非 `Hub` 请求、是否消费注入的 caller / capability，仍属于自治范围。

### 2.4 工具平面统一

平台对外能力收敛到统一工具平面：

1. Hub 原子工具入口：`POST /api/tool/call`
2. Hub 流式工具入口：`GET /api/tool/ws?tool_id=...`
3. Service 原子工具入口：`POST /service/tool/exec`
4. Service 流式工具入口：`GET /service/tool/ws`

`hub.governance.service.*`、`hub.admin.*`、`hub.system.*` 与业务工具共享同一工具平面；少量 HTTP/WS 路由仍作为静态资源、兼容入口或 transport 承载面存在，但不应替代标准 tool 契约。

### 2.5 平台安全与业务安全分离

平台安全由 `Hub` 主导，业务安全由 `service` 主导。

1. `Hub` 负责 caller 识别、header 清洗、前置分发门槛、路由、运行统计和治理视图。
2. `service` 负责用户身份鉴权、对象级权限、领域规则、内部限额和最终执行判断。
3. 需要信任 `Hub` 来源的 tool，应通过 `.service_secret` 与互信 header 建立可信接入；本项目当前不要求每次调用都做请求级签名。

### 2.6 生命周期与治理视图收敛于 Hub

`Hub` 维护 service 接纳与运行态治理视图，并负责生命周期编排。对被接纳的 `service`，最低正式生命周期契约是：

1. `service.lifecycle.health`
2. `service.lifecycle.shutdown`
3. `service.lifecycle.state.get`

如存在排空语义，还应补充 `service.lifecycle.drain`。`Hub` 同时维护 `service session`、路由覆盖层和治理统计；`reliability`、`success_rate`、`call_count` 等字段属于 `Hub` 的治理产物，不是 `service` 的最终自声明事实。

### 2.7 副作用与身份卫生

`service` 不应直接改写浏览器响应或外部上下文。cookie、header 等副作用应通过 `effects` 返回，再由 `Hub` 统一写回调用方。内部身份也不能靠端口或来源地址猜测，必须依赖互信 header 与显式校验；转发前应清洗 protected headers，避免外部伪造内部身份字段。

## 3. 开发规范

### 3.1 先核验再判断

涉及项目状态、目录结构、接口契约、运行行为或最近改动时，优先用真实文件、Git 历史与可复现命令核验，不靠记忆补全。

### 3.2 最小可行变更

默认先做最小可行变更，避免无关重构、风格化扩散和跨模块顺手修。只有在结构性问题已被证实时，才扩大修改范围。

### 3.3 不把推测写成事实

无法从代码、配置或 Git 证据直接确认的内容，应标为“待确认”，不要把推测写成稳定结论。

### 3.4 文档分工保持单一

1. `doc/_instruction.md` 只做入口与阅读路由。
2. 本文件只描述当前理念、边界和规范。
3. `doc/_instruction/structure.md` 负责结构、职责和接口。
4. `doc/_instruction/glossary.md` 负责术语。
5. `doc/_devlog.md` 只追加开发增量，不在说明文档里重复维护。

### 3.5 高风险动作先确认

大规模删除、不可逆重写、可能导致数据丢失或服务中断的操作，先征求用户确认。

## 4. 当前实现事实

### 4.1 Hub 主链路

Hub 当前通过 `hub/cmd/hub/main.go` 启动，统一装配：

1. `IdentityMiddleware`
2. `ToolHandler`
3. `SupervisorHandler`
4. `AdminHandler`
5. `SystemHandler`

主工具入口是 `/api/tool/call` 与 `/api/tool/ws`；静态资源和少量兼容路由仍然存在，但不改变“工具平面是正式主契约”这一事实。

### 4.2 身份与 caller 注入

`hub/internal/app/identity.go` 当前按以下顺序识别请求来源：

1. Hub <-> Service 互信 header
2. `X-Surface-Token`
3. 用户 JWT cookie

Hub 识别后会在转发前注入统一 `X-Caller-*` 与 `X-Hub-*` header。这里的 caller 是路由与治理上下文，不等于 `service` 内部必须采纳的最终业务授权结论。

### 4.3 当前已落地的治理工具

以下治理工具已在 Hub 侧注册：

1. `hub.governance.service.register`
2. `hub.governance.service.heartbeat`
3. `hub.governance.service.drain`
4. `hub.admin.*`
5. `hub.system.*`

对应地，`account`、`ai-doubao`、`chat-server`、`file_storage` 等 service 已存在通过 `hub.governance.service.register` 完成启动注册的实现。

### 4.4 当前已落地的 service 生命周期事实

当前代码已可见的正式生命周期事实包括：

1. Hub 优先调用 `service.lifecycle.health`
2. Hub 停机时优先调用 `service.lifecycle.shutdown`
3. `/healthz` 与部分 `/admin/shutdown` 仍作为兼容 fallback 保留
4. `ai-doubao` 仍保留 `ai-doubao.system.health`、`ai-doubao.system.shutdown` 兼容别名

### 4.5 当前已落地的工具字段事实

`pkg/toolproto` 与多处 service manifest/协议结构已体现以下字段：

1. `allowed_caller_types`
2. `capabilities_required`
3. `streaming`
4. `ws_path`

这说明 tool 元数据已经是运行态真实契约，不只是文档约定。

### 4.6 account 的当前边界

`account` 当前负责账号、token、会话与登录态相关工具输出，并通过 `effects.set_cookies` 协同 Hub 完成外部副作用写回。Hub 会在适当时机同步 account 状态，但这不意味着业务授权被上收给 Hub；业务安全边界仍应由具体 `service` 自己负责。

### 4.7 仍待确认的单一事实源

`config/services.json` 与 `hub/config/services.json` 当前内容一致，但长期哪个文件应作为单一事实源，仍待确认。

---

**文档更新时间**：2026-03-19 20:04:10 CST

**本轮修改范围**：按 `_service_standard.md` 对齐 `core.md` 的边界表述，压缩冗余实现细节，纠正对 `Hub`、`service`、caller、业务授权和生命周期职责的过强或失真描述，并补入治理视图、最低生命周期契约与副作用边界。

**信息来源**：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`、`hub/cmd/hub/main.go`、`hub/internal/app/identity.go`、`hub/internal/gateway/tool_handler.go`、`hub/internal/supervisor/handler.go`、`hub/internal/supervisor/process_control.go`、`pkg/toolproto/supervisor.go`，以及 `services/account`、`services/ai_doubao`、`services/chat_server`、`services/file_storage` 中与注册、生命周期和工具字段相关的实现检索结果。
