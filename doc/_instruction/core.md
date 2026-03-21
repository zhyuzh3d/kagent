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

补充：

1. `hub` 与每个 `service` 的内部配置文件应完全隔离在各自项目目录内。
2. 内部配置应优先收敛到各自项目内的 `config/`；密钥、密码等敏感字段统一存放在 `configx.json`，并配套 `configx.json.example` 作为样例。
3. Hub 可读写的 service 运行态文件统一落在各自 `run/`，不再共享仓库根 `config/`。
4. 配置读取必须局部封装：`hub` 只读 `hub/` 自己的配置，各 `service` 只读各自项目目录内的配置，不向上层目录或其他 `service` 目录跨边界读取配置。
5. 平台级标准配置只保留 service / hub 进程级统一配置，不再定义通用的用户级 config 覆盖机制。
6. 若某个 `service` 需要按用户差异化行为处理，应在其内部业务数据或业务逻辑中自行实现，而不是改变该 `service` 的项目级 `config.json` / `configx.json` 语义。

### 2.4 数据库存储边界

数据库访问按基础设施边界收敛：

1. 除 `sql_db` 以及经明确批准的 Hub 核心基础设施外，所有 `service` 不得直接使用 sqlite 驱动。
2. 业务 `service` 的数据库读写必须统一通过 `services/sql_db` 暴露的工具能力完成，不在各自进程内直接 `sql.Open(...)`、私自持有 sqlite 文件或新增私有驱动封装。
3. `sql_db` 作为数据库服务实现，本身可以直接持有 sqlite 驱动与底层数据库打开逻辑。
4. `hub` 允许保留少量核心本地数据库直连能力，但仅限启动、认证、治理快照等经明确批准且不能对 `sql_db` 形成启动依赖环的基础设施路径。
5. 数据库驱动封装属于子项目内部工具，不属于默认共享公共包；原则上仅 `hub` 与 `sql_db` 可以在各自目录内维护这类内部封装。

### 2.5 共享包边界

跨项目可稳定复用的共享包当前只保留少数协议基础设施：

1. `pkg/hubsvc` 与 `pkg/toolproto` 是面向 Hub <-> Service 通信的统一基础，属于允许被各 `service` 调用的共享外部包。
2. 除这类通信协议与互信基础设施外，其他实现性工具不应默认放入 `pkg/` 供所有 `service` 依赖。
3. sqlite 驱动注册、数据库打开、底层存储适配等实现性能力不属于共享协议面，应收敛在 `hub` 或 `sql_db` 自身项目目录内。

### 2.6 工具平面统一

平台对外能力收敛到统一工具平面：

1. Hub 原子工具入口：`POST /api/tool/call`
2. Hub 流式工具入口：`GET /api/tool/ws?tool_id=...`
3. Service 原子工具入口：`POST /service/tool/exec`
4. Service 流式工具入口：`GET /service/tool/ws`

`hub.governance.service.*`、`hub.admin.*`、`hub.system.*` 与业务工具共享同一工具平面；少量 HTTP/WS 路由仍作为静态资源、兼容入口或 transport 承载面存在，但不应替代标准 tool 契约。

补充：

1. 首选推荐路径是 `web -> hub -> service -> web`，也就是由 Web/Page 直接调用业务 service tool，再经 Hub 统一转发与治理。
2. `service -> hub -> service` 的二次调用链路当前已被正式支持，但它应视为补充路径，而不是默认主路径。
3. 当业务可以直接走 `web -> hub -> service` 时，不应优先设计成某个 service 再间接调用另一个 service。

### 2.7 平台安全与业务安全分离

平台安全由 `Hub` 主导，业务安全由 `service` 主导。

1. `Hub` 负责 caller 识别、header 清洗、前置分发门槛、路由、运行统计和治理视图。
2. `service` 负责用户身份鉴权、对象级权限、领域规则、内部限额和最终执行判断。
3. 需要信任 `Hub` 来源的 tool，应通过 `.service_secret` 与互信 header 建立可信接入；本项目当前不要求每次调用都做请求级签名。

### 2.8 生命周期与治理视图收敛于 Hub

`Hub` 维护 service 接纳与运行态治理视图，并负责生命周期编排。对被接纳的 `service`，最低正式生命周期契约是：

1. `service.lifecycle.health`
2. `service.lifecycle.shutdown`
3. `service.lifecycle.state.get`

如存在排空语义，还应补充 `service.lifecycle.drain`。`Hub` 同时维护 `service session`、路由覆盖层和治理统计；`reliability`、`success_rate`、`call_count` 等字段属于 `Hub` 的治理产物，不是 `service` 的最终自声明事实。

### 2.9 服务描述与清单规范 (Single Source of Description)

服务的人类可读描述 (`description`) 必须具有唯一权威来源：

1. **唯一来源**：描述必须且仅能从该服务本地项目目录下的 `run/manifest.json` 文件中读取。
2. **禁止传输**：禁止通过 `hub.governance.service.register` 接口动态传输顶级描述字段。这确保了管辖权在本地配置中，而不是在运行时的心跳数据中。
3. **Hub 自身对齐**：Hub 自身的描述也必须遵循此原则，通过其根目录下的 `manifest.json` 定义并展示。

### 2.10 副作用与身份卫生

`service` 不应直接改写浏览器响应或外部上下文。cookie、header 等副作用应通过 `effects` 返回，再由 `Hub` 统一写回调用方。内部身份也不能靠端口或来源地址猜测，必须依赖互信 header 与显式校验；转发前应清洗 protected headers，避免外部伪造内部身份字段。

### 2.11 增强的端口自愈与重用规范
 
 为了解决本地开发与频繁重启场景下的“Address already in use”问题，项目实施了统一的监听优化：
 
 1. **统一 Listen 入口**：所有 `service` 与 `Hub` 必须放弃直接使用 `net.Listen` 或 `http.ListenAndServe`。
 2. **强制开启重用选项**：必须统一调用 `pkg/hubsvc.Listen(addr)` 助手函数。该助手在所有支持的平台上强制开启 `SO_REUSEADDR`，并在 POSIX 系统（macOS/Linux）上额外开启 `SO_REUSEPORT`。
 3. **抢占与自愈**：通过双重重用选项，允许新进程在旧进程尚未完全释放 Socket 时强势抢占端口，显著提升了生命周期编排（如 `deploy.sh` 快速重启）的成功率。
 4. **跨平台屏蔽**：具体系统调用差异（如 Windows 的 Handle 转换与 POSIX 的整型 FD 转换）由 `pkg/hubsvc` 内部屏蔽，对外提供一致的 `net.Listener` 契约。

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

### 3.6 新边界直接落地，不为旧模式保留兼容层

当结构性边界已经明确时，重构应优先直接把旧实现迁到新边界，而不是继续为旧模式增加兼容层、双写逻辑或长期过渡接口。涉及旧前端、旧工具入口或旧 service 内部模式时，应同步把调用方与使用方一起改到新方案，而不是围绕旧路径做保守兼容。

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

当前还已落地：

1. `toolproto.Context` 已扩展 `origin_caller` 与 `origin_caller_token`。
2. Hub 当前会保持现有 `caller` 语义不变，并在合法 delegation 场景下恢复 `origin_caller`。
3. `origin_caller` 表示链路起点的原始客户端身份；`caller` 仍表示当前这一次直连 Hub 的请求主体。
4. `origin_caller_token` 由 Hub 自签发和校验，不等于浏览器 JWT，也不允许 service 自行伪造。

### 4.3 当前已落地的治理工具

以下治理工具已在 Hub 侧注册：

1. `hub.governance.service.register`
2. `hub.governance.service.heartbeat`
3. `hub.governance.service.drain`
4. `hub.admin.*`
5. `hub.system.*`

对应地，`account`、`ai_doubao`、`chat_server`、`file_storage` 等 service 已存在通过 `hub.governance.service.register` 完成启动注册的实现。

### 4.4 当前已落地的 service 生命周期事实

当前代码已可见的正式生命周期事实包括：

1. Hub 当前以各 service `run/manifest.json` 中的 `depends_on` 为唯一依赖事实源，先做环检测，再按 DAG 分批启动。
2. Hub 当前只等待 service 完成 `hub.governance.service.register`；一旦注册成功，Hub 直接把该实例视为 `ready`，不再维护 Hub 侧后置 `init` 阶段。
3. 各 service 当前必须在自己进程内先完成旧实例自清理、内部初始化与监听，再对 Hub 发起 register；Hub 不再统一执行 `kill_old` 或反向调用 `service.lifecycle.init`。
4. Hub 停机或治理停机时仍优先调用 `service.lifecycle.shutdown`；`/healthz` 与部分 `/admin/shutdown` 只作为兼容 fallback 保留。
5. `ai_doubao` 仍保留 `ai_doubao.system.health`、`ai_doubao.system.shutdown` 兼容别名。

### 4.5 当前已落地的工具字段事实

`pkg/toolproto` 与多处 service manifest/协议结构已体现以下字段：

1. `allowed_caller_types`
2. `capabilities_required`
3. `streaming`
4. `ws_path`
5. `protocol`
6. `timeout_ms_default`
7. `scope_support`

这说明 tool 元数据已经是运行态真实契约，不只是文档约定。

### 4.6 当前已落地的协议与生命周期收敛事实

本轮重构后，以下事实已可由代码直接确认：

1. `pkg/toolproto` 已扩展为共享的 tool/service 元数据与 lifecycle 基础结构来源，`chat_server` 与 `ai_doubao` 不再各自维护完整私有 `CallRequest / ServiceTool / SupervisorRegisterRequest` 定义。
2. `account`、`ai_doubao`、`chat_server`、`file_storage`、`sql_db`、`surface_manager` 当前都已补齐 `service.lifecycle.state.get`。
3. `hub` 当前已注册 `hub.system.state.get`，可直接返回 Hub 运行治理视图快照。
4. 多个 service 的 Hub 工具调用样板已开始收敛到 `pkg/hubsvc`，减少重复的 register/tool-call/auth header 拼装逻辑。
5. Chat 页面运行配置已从 Hub 自身逻辑下沉到 `chat_server`，当前通过 `app.chat.config.get` 与 `app.chat.config.update` 经 Hub 工具网关访问。

### 4.7 account 的当前边界

`account` 当前负责账号、token、会话与登录态相关工具输出，并通过 `effects.set_cookies` 协同 Hub 完成外部副作用写回。Hub 会在适当时机同步 account 状态，但这不意味着业务授权被上收给 Hub；业务安全边界仍应由具体 `service` 自己负责。

### 4.8 当前仍未完全收敛的边界

以下问题在本轮后仍未完全解决：

1. Hub 当前仍保留 `user_store` 与 `startup_snapshot_store` 本地 sqlite 基础设施路径；后续重点不是继续下沉到 `sql_db`，而是防止其边界继续扩张到业务持久化。
2. `service -> hub -> service` 已可保留 `origin_caller`，但各业务 service 仍需继续清理“明明可由 Web 直接调用，却绕行 service 二次调 service”的旧路径。

---

**文档更新时间**：2026-03-21 12:29:00 CST

**本轮修改范围**：补充平台级配置边界，明确 `config.json` / `configx.json` / `configx.json.example` 的职责，统一 service 级配置读取标准，并移除平台级用户覆盖配置作为默认模式。

**信息来源**：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/hubsvc/project_config.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/account/internal/app/app.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/cmd/ai_doubao/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/cmd/chat_server/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/internal/app/runtime_config.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/file_storage/cmd/file_storage/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/cmd/sql_db/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/surface_manager/cmd/surface_manager/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/cmd/autogui/main.go`。
