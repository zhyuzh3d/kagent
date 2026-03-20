# Service Standard

> 版本：草案 v0.8
> 起草时间：2026-03-19 CST
> 文档性质：面向 AI 开发、service 接入、service 重构与代码审查的约束性标准

## 1. 文档目的

本文件定义 `kagent` 中 `service` 的标准接入形态、`Hub` 的治理边界、`tool` 协议要求、tool 元数据、生命周期要求和推荐安全模式。

本文件不试图规范系统中一切可运行进程，只定义：

1. 什么样的程序可以被视为符合 `service` 规范。
2. 什么样的 `service` 可以被 `Hub` 接纳并纳入统一治理。
3. `Hub` 接纳后会做什么，不会做什么。
4. `service` 在自治前提下，应如何通过 `tool` 协议向外提供能力。

本文件是规范，不是实现说明。会随实现变化的细节应进入后续正式版本或专题文档；当前版本只保留框架、职责边界、核心字段与最低约束。

## 2. 总体定位

### 2.1 系统定位

`kagent` 的目标不是传统通用微服务治理，而是形成一套适合 AI 理解、调用、组合和扩展的 `AI tool runtime + governed service protocol`。在这个目标下：

1. `Service` 是能力模块，负责执行某一领域的动作、协作和结果产出。
2. `Tool` 是 `service` 对外表达能力的标准语义单元。
3. `Hub` 是标准治理入口、统一展示面和默认接入面，用于接纳符合规范的 `service`，并统一提供其 `tool`。

### 2.2 Hub 的角色

`Hub` 是管理者，不是管制者。这意味着：

1. `Hub` 负责接纳、治理、路由、标记、展示和统计。
2. `Hub` 不负责强制控制系统中所有可运行进程。
3. `Hub` 不负责证明 `service` 内部每个 `tool` 都正确消费了 `Hub` 注入的 caller、capability 或身份结果。
4. `Hub` 不负责替 `service` 完成业务鉴权、对象级权限和内部限额。

### 2.3 Service 的角色

`Service` 是可被用户定制、可由 AI 撰写、可动态接入的能力模块。这意味着：

1. 任何程序都可以自运行并提供服务。
2. 只有满足本标准要求并兼容 `Hub` 协议的程序，才会被 `Hub` 接纳和治理。
3. 被 `Hub` 接纳，不等于被 `Hub` 证明其内部实现完全安全或完全正确。
4. `Service` 是否消费 `Hub` 注入的 caller / capability / 身份结果，属于 `service` 自治范围。

## 3. 适用范围与非目标

### 3.1 本标准的适用范围

本标准只约束以下对象：

1. 希望被 `Hub` 接纳的 `service`。
2. `Hub` 对已接纳 `service` 的治理接口、路由接口和运行态管理。
3. `service` 对外暴露的标准 `tool` 协议。

### 3.2 本标准明确不做的事

本标准不尝试解决以下问题：

1. 强制系统中一切进程都改造为 `service`。
2. 强制所有 `service` 内部必须采用某一种业务鉴权实现。
3. 强制 `service` 内部一定使用 `Hub` 注入的 caller 或身份结果。
4. 追踪 AI 的完整推理原因。
5. 在 `Hub` 中设计复杂的全局频率配额系统。
6. 为本地项目引入高成本的请求级签名机制。

## 4. 三层边界

为避免把“接入 Hub”“被 Hub 治理”“service 内部自治”混在一起，本标准将 `service` 相关要求拆成三层边界。

### 4.1 接纳规范

接纳规范回答的问题是：`Hub` 是否愿意接纳这个 `service`。其关注点是：

1. `tool` 协议是否兼容。
2. 生命周期接口是否完整。
3. `manifest / register / heartbeat` 是否符合格式。
4. 是否能与 `Hub` 完成标准接入。

### 4.2 治理规范

治理规范回答的问题是：`Hub` 接纳后会对这个 `service` 做什么。其关注点是：

1. 生命周期编排。
2. caller / capability 前置筛选。
3. 路由选择。
4. 运行统计。
5. 状态汇总。
6. 展示与标记。
7. 退出前快照写入。

### 4.3 自治规范

自治规范回答的问题是：`service` 内部如何决定是否执行一个请求。其关注点是：

1. 是否信任 `Hub` 来源。
2. 是否拒绝非 `Hub` 请求。
3. 是否消费 `Hub` 注入的 caller / capability 结果。
4. 是否做用户身份鉴权。
5. 是否做对象级权限、内部限额和业务规则检查。

## 5. Service 的标准定义

### 5.1 Service 是什么

被 `Hub` 接纳的标准 `service` 应满足以下定义：

1. 是独立进程。
2. 通过 `tool` 协议对外暴露能力。
3. 具备标准生命周期接口。
4. 可通过 `manifest / register / heartbeat` 向 `Hub` 描述自身。
5. 接受 `Hub` 对其进行接入、路由、状态汇总和基础治理。

### 5.2 Service 不是什么

标准 `service` 不应被默认理解为：

1. 独立治理中心。
2. 强依赖 `Hub` 才能存在的唯一形态。
3. 由 `Hub` 代替其内部业务授权判断的被动执行器。

## 6. Hub-Service 关系

### 6.1 核心关系

`Hub` 与 `service` 的关系应理解为：

1. `Hub` 统一接纳符合规范的 `service`。
2. `Hub` 统一向外提供这些 `service` 的 `tool`。
3. `Hub` 负责默认的 caller / capability 筛选、路由、统计与展示。
4. `Service` 负责实际执行以及内部业务判断。

### 6.2 “必须通过 Hub”的准确含义

本标准中的“必须通过 Hub”应理解为：

1. `Service` 必须兼容 `Hub` 的 `tool + lifecycle` 协议，`Hub` 才会接纳它。
2. `Hub` 接纳后，其对外统一提供的标准入口是 `Hub`。
3. 这不等于 `service` 在物理上不能独立运行。
4. 这也不等于 `service` 内部必须强制消费 `Hub` 注入的 caller 或身份结果。

换言之：

1. 不兼容 `Hub` 协议，`Hub` 不治理你。
2. 兼容 `Hub` 协议，`Hub` 可以治理你。
3. `Service` 内部授权与鉴权仍属于 `service` 自治范围。

## 7. 平台安全与业务安全

### 7.1 平台安全

平台安全由 `Hub` 主导，主要包括：

1. 接纳符合规范的 `service`。
2. 统一对外提供 `tool`。
3. 基于 `allowed_caller_types` 和 `capabilities_required` 做前置筛选。
4. 维护运行统计、状态视图和基础日志。
5. 在退出前写入整体运行快照。

### 7.2 业务安全

业务安全由 `service` 主导，主要包括：

1. 用户身份证鉴权。
2. 对象级权限。
3. 领域级业务约束。
4. 是否消费 `Hub` 注入的 caller / capability / 鉴权结果。
5. 内部限额、内部降级和内部拒绝策略。

### 7.3 分工原则

`Hub` 与 `service` 的安全分工必须明确：

1. `Hub` 负责“是否应当转发到该 tool”。
2. `Service` 负责“收到后是否真正执行”。
3. `Hub` 的前置筛选不是业务授权系统。
4. `Service` 的内部授权不应回退给 `Hub` 代做。

## 8. Hub 提供给 Service 的治理能力

以下工具属于 `Hub` 的治理能力，不属于 `service` 自己的业务工具：

| Hub tool | 作用 | Service 侧使用方式 | 说明 |
| --- | --- | --- | --- |
| `hub.governance.service.register` | 服务注册与首次上报 | 启动后向 `Hub` 注册 service、实例、工具清单、endpoint、健康状态 | `service` 接入 `Hub` 的入口 |
| `hub.governance.service.heartbeat` | 服务心跳与状态刷新 | 运行期周期性上报实例状态、PID、endpoint、健康位 | `Hub` 用于跟踪实例可见性 |
| `hub.governance.service.drain` | 服务排空与停机协商 | `Hub` 或 `service` 在退出前触发 | 适用于有优雅退出语义的 service |
| `hub.system.state.get` | Hub 自身运行状态 | 查询 `Hub` 当前状态、版本、治理汇总 | `Hub` 的标准状态工具 |
| `hub.system.report_log` | 基础日志上报 | `service` 向 `Hub` 上报结构化日志事件 | 用于基础可检索日志 |
| `hub.system.health` | Hub 自身健康状态 | 运维或联动检查时使用 | 非业务主链路依赖 |
| `hub.system.version.get` | Hub 版本信息 | 标识当前治理环境版本 | 辅助能力 |
| `hub.system.shutdown` | Hub 自身停机 | `Hub` 需要退出时使用 | Hub 自身生命周期能力 |

## 9. Service Session 与治理视图

### 9.1 Service Session 的定义

`Hub` 维护的 `service session` 是运行态合并视图，用于统一记录：

1. `service` 的声明基线。
2. 实例的运行态事实。
3. `Hub` 的治理结果。
4. 当前是否可路由。

### 9.2 Session 的信息来源

`service session` 由以下几层信息合并而成：

1. `bootstrap secret`
   - 用于 `Hub <-> service` 的启动期可信接入
2. `manifest`
   - 描述 service 的静态声明基线
3. `register / heartbeat`
   - 描述实例运行事实
4. `Hub` 治理层
   - 描述路由、冲突、统计与可路由性

### 9.3 Session 字段分层原则

修订后的标准应始终区分：

1. `service` 自报事实
2. `Hub` 观察结果
3. `Hub` 治理结果

尤其是以下字段，其最终值应由 `Hub` 维护，而不是由 `service` 自设：

1. `reliability`
2. `rank`
3. `success_rate`
4. `call_count`
5. `conflict_reason`
6. 最终可路由性

### 9.4 Routing Overlay

除 `service session` 外，`Hub` 还维护一层 `routing overlay`，用于决定：

1. 哪个 `tool` 由哪个 `service` 接。
2. 当前是否启用。
3. 当前优先级和权重。
4. 是否处于熔断或摘流状态。

该层是 `Hub` 的治理产物，不应被 `service` 视为自己可直接写入的最终事实。

## 10. Manifest 与 Register

### 10.1 Manifest 的角色

`manifest` 是 `service` 的声明式基线，用于描述：

1. 这个 `service` 是什么。
2. 提供什么 `tool`。
3. 需要什么前提。
4. 如何启动。

`manifest` 是静态声明，不表达实例运行时状态。

### 10.2 Register 的角色

`register` 是 `service` 在运行期向 `Hub` 提交的实例级事实报告，用于描述：

1. 当前实例是谁。
2. 当前实例在哪里。
3. 当前实例声称实际提供什么工具视图。
4. 当前实例是否健康。

### 10.3 Register 的边界

`register` 只应上报运行时事实，不应申领最终治理权。因此：

1. `service` 可以上报实例事实。
2. `Hub` 可以据此维护治理视图。
3. `service` 不应通过 `register` 直接决定最终 `rank`、最终 `reliability`、最终公开级别或最终路由优先级。

### 10.4 Manifest 与 Register 的联动规则

联动原则如下：

1. `Hub` 以 `manifest` 作为声明基线。
2. `Hub` 以 `register / heartbeat` 作为实例运行态覆盖。
3. `Hub` 对冲突进行记录并形成治理结论。
4. `Hub` 只接受必要控制面字段，不接受任意私有业务字段的治理解释权。

## 11. Tool 标准协议

### 11.1 Tool 的作用

`tool` 是 `service` 对外暴露能力的主契约。标准 `service` 应以 `tool` 为中心，而不是以分散的私有 HTTP 路由作为长期正式契约。

### 11.2 Tool 命名

`tool_id` 应稳定表达逻辑能力。命名原则：

1. 应表达领域、类型和动作。
2. 不应把端口、进程名、文件路径等实现细节塞入名字。
3. 允许使用多段命名，但必须稳定、可读、可审查。

### 11.3 请求结构

标准 `tool` 请求应包含：

1. `tool_id`
2. `args`
3. `context`

其中 `context` 应承载统一上下文，例如：

1. `request_id`
2. `trace_id`
3. `caller`
4. `timeout_ms`
5. `capabilities`
6. `meta`

### 11.4 响应结构

标准 `tool` 响应应使用统一结构：

1. `ok`
2. `result`
3. `error`
4. `meta`
5. `effects`

要求如下：

1. `result` 主要承载业务语义。
2. `meta` 主要承载追踪与实例信息。
3. `effects` 主要承载副作用表达。
4. `state` 类工具的状态值必须写在 `result.status`。
5. `duration_ms` 应表达 service 内部净处理时间。

### 11.5 副作用表达

`service` 不应直接操作浏览器响应或外部上下文。标准做法是：

1. 把 cookie、header 等副作用写入 `effects`
2. 由 `Hub` 负责回写到调用方

## 12. Tool 元数据

### 12.1 设计原则

tool 元数据必须同时满足：

1. AI 易理解
2. `Hub` 易路由
3. 审查易判断
4. service 易实现

因此，tool 元数据不应只描述功能，还必须描述接入方式、前置门槛、副作用和风险。

### 12.2 标准字段

`manifest.provides[]` 与 `register.tools[]` 应至少能够表达以下字段：

| 字段名 | 说明 | 约束 |
| --- | --- | --- |
| `tool_id` | 工具唯一逻辑标识 | 必填 |
| `description` | 工具简要说明 | 必填，短、准、可读 |
| `input_schema` | 输入结构说明 | 推荐 JSON Schema 或等价结构 |
| `output_schema` | 输出结构说明 | 推荐 JSON Schema 或等价结构 |
| `protocol` | 协议形态 | 如 `http`、`uds` |
| `allowed_caller_types` | 允许的 caller 类型 | Hub 路由门槛；可包含 `all` |
| `capabilities_required` | 所需能力 | Hub 路由门槛 |
| `hub_only` | 是否只接受 Hub 作为可信入口 | 由 service 声明 |
| `hub_auth_required` | 是否需要 Hub 鉴权 | 由 service 声明；用于减少无意义计算 |
| `has_effects` | 是否存在副作用 | 布尔语义或等价表达 |
| `risk_lv` | 风险等级 | 默认 `0`，由 service 内部设定 |
| `streaming` | 是否流式 | 应与真实 transport 一致 |
| `ws_path` | 流式入口路径 | 仅流式工具使用 |
| `timeout_ms_default` | 默认超时 | 供 Hub 路由和调用参考 |
| `scope_support` | 业务作用域支持 | storage/database 类 service 常用 |
| `version` | 工具版本 | 可选，存在时应稳定 |

### 12.3 字段边界说明

以下边界必须在文档中明确：

1. `allowed_caller_types`
   - 只表达 Hub 侧的前置分发条件
   - 不等于 service 内部业务授权
2. `capabilities_required`
   - 只表达 Hub 侧能力门槛
   - 不等于内部执行权限
3. `hub_only`
   - 表达 service 对该 tool 的接入姿态
   - 不等于 Hub 可以物理强制全世界
4. `hub_auth_required`
   - 表达该 tool 是否依赖 Hub 可信注入
   - 与 `allowed_caller_types` 不是同一维度
5. `risk_lv`
   - 是 service 对 tool 风险的自声明
   - 可被 Hub 用于展示、标记和默认路由策略

### 12.4 `allowed_caller_types=all`

当 `allowed_caller_types` 包含 `all` 时，表示：

1. 该 tool 在 Hub 路由层面不依赖 caller 类型。
2. `Hub` 不需要为了 caller 类型匹配而额外构造门槛判断。
3. 这不等于 service 内部不能继续做自己的业务校验。

## 13. Caller 与 Capability 的使用规则

### 13.1 Caller 的含义

caller 是 `Hub` 对请求来源所做的统一分类结果。其作用是：

1. 给 `Hub` 提供路由门槛。
2. 给 `service` 提供可选的上下文参考。

caller 不是：

1. `service` 内部必须采纳的最终授权结论。
2. 对业务读写语义的完整表达。

### 13.2 推荐 caller 类型

逻辑值可以包括：

1. `anonymous`
2. `user`
3. `admin`
4. `surface`
5. `page`
6. `service`
7. `hub`
8. `all`

### 13.3 Capability 的含义

`capabilities_required` 表达的是该 tool 在 Hub 路由层面希望具备的能力前提。其作用是：

1. 帮助 `Hub` 决定要不要转发。
2. 帮助 AI 和开发者理解这个 tool 的前置条件。

其边界是：

1. 不替代 service 内部授权。
2. 不替代对象级权限。
3. 不替代内部限额。

## 14. 生命周期工具

### 14.1 最低必选项

一个被 `Hub` 接纳的标准 `service` 至少应提供以下生命周期工具：

1. `service.lifecycle.health`
2. `service.lifecycle.shutdown`
3. `service.lifecycle.state.get`

### 14.2 `service.lifecycle.health`

要求：

1. 应返回 `status`、`healthy` 及实例基本信息。
2. 用于 `Hub` 在不理解内部业务细节时判断实例可用性。
3. `status` 语义应与 `state` 工具保持一致。

### 14.3 `service.lifecycle.shutdown`

要求：

1. 允许 `Hub` 以 tool 方式触发优雅退出。
2. 调用后应尽快返回。
3. 真正退出可以异步执行。

### 14.4 `service.lifecycle.state.get`

要求：

1. 返回完整运行状态快照。
2. `result.status` 是主状态字段。
3. 应能表达本实例当前处于何种生命周期状态。

### 14.5 条件性能力

如果 `service` 存在排空语义、长连接或批处理语义，则应额外提供：

1. `service.lifecycle.drain`

## 15. Hub 对 Tool 的治理职责

### 15.1 Hub 负责什么

对于已接纳的 `tool`，`Hub` 应负责以下治理职责：

1. 按 `allowed_caller_types` 做前置 caller 门槛筛选。
2. 按 `capabilities_required` 做前置 capability 门槛筛选。
3. 维护默认路由与实例选择。
4. 维护基础运行统计。
5. 在管理视图中展示 tool 的状态与标记。

### 15.2 Hub 不负责什么

`Hub` 不负责：

1. 替 tool 完成内部业务鉴权。
2. 替 tool 判断资源归属。
3. 替 tool 判断业务限额是否超额。
4. 证明 tool 内部一定消费了 `Hub` 注入的 caller / capability / 身份结果。

### 15.3 Tool 级治理统计

`Hub` 应维护每个 tool 的治理统计视图，至少包括：

1. `reliability`
2. `success_rate`
3. `call_count`

这些字段属于 `Hub` 的治理产物，而不是 service 的最终自声明事实。

## 16. 推荐可信模式

### 16.1 目标

对需要信任 `Hub` 来源的 `tool`，推荐采用基于 `.service_secret` 的可信接入模式。该模式的目标是：

1. 让 `service` 能识别“这是否是来自 Hub 的受治理请求”。
2. 让 `service` 能拒绝非 `Hub` 来源。
3. 维持本地项目可接受的复杂度和性能。

### 16.2 核心原则

推荐可信模式下，应遵循以下原则：

1. `.service_secret` 是 `Hub <-> service` 启动期信任建立的核心机制。
2. 对需要 Hub 信任的 `tool`，service 应基于该机制识别 `Hub` 来源。
3. 对不需要 Hub 信任的 `tool`，可减少不必要的鉴权计算。
4. 本标准不要求每次调用都做请求级签名。

### 16.3 推荐但不伪强制

推荐可信模式应写成建议性与接入姿态要求，而不是伪强制表达。因此：

1. 若 tool 声明 `hub_only=true` 或 `hub_auth_required=true`，则表示该 tool 期望以 Hub 作为可信入口。
2. `service` 应实现与该声明一致的内部逻辑。
3. `Hub` 可以根据这些声明做标记、展示与默认路由决策。
4. `Hub` 不负责证明全世界都遵守这一点。

## 17. 流式工具

### 17.1 基本要求

对流式工具：

1. 必须显式声明 `streaming`。
2. 必须显式声明 `ws_path` 或等价入口。
3. `Hub` 只关心流式连接的生命周期、路由和可观测性。

### 17.2 语义边界

对流式通道：

1. `Hub` 不应解析业务流内容。
2. `Hub` 不应把流式内部消息数作为主要业务治理口径。
3. `service` 应在最终事件或关闭结果中表达最终状态。

## 18. Hub 退出前的运行快照

### 18.1 快照目标

`Hub` 在退出前应把当前自身与全部已接纳 `service/tool` 的运行态快照写入数据库。

### 18.2 快照内容

快照至少应包括：

1. `Hub` 自身状态
2. 全部 `service` 基本状态
3. 全部 `tool` 当前统计视图
4. 每个 tool 的：
   - `reliability`
   - `success_rate`
   - `call_count`

### 18.3 归属要求

快照应绑定：

1. 当前用户
2. 或 `anonymous`

### 18.4 快照用途

快照主要用于：

1. 退出前保留治理视图
2. 为后续恢复、分析和管理展示提供参考

## 19. 典型 service 模板

### 19.1 `account`

类型：认证与会话型 `service`

说明：

1. 面向登录态、token、单会话和密钥同步。
2. 用户身份鉴权属于其内部职责。
3. `Hub` 只负责其 tool 的接入和治理。

### 19.2 `chat_server`

类型：编排与流式交互型 `service`

说明：

1. 通常同时具备原子工具和流式工具。
2. 适合体现 `Hub` 的统一入口与默认路由价值。

### 19.3 `file_storage`

类型：文件与 blob 能力型 `service`

说明：

1. 应突出 `allowed_caller_types`、`scope_support`、`has_effects`。
2. 是否拒绝非 Hub 来源，由其内部自行决定。

### 19.4 `sql_db`

类型：数据库与共享存储能力型 `service`

说明：

1. 强调 caller 作用域、读写边界和跨租户隔离。
2. 作为数据类 `service`，其业务安全仍在 service 内部。

### 19.5 `ai_doubao`

类型：流式 AI 能力型 `service`

说明：

1. 应显式声明流式工具的协议形态、风险等级和副作用情况。
2. 更适合作为 AI 可调用能力的一类标准模板。

### 19.6 `surface_manager`

类型：Surface 扫描、session 与 capability 型 `service`

说明：

1. 侧重 Surface catalog、会话颁发和能力颁发。
2. 其 tool 元数据应帮助 `Hub` 和 AI 更好理解作用域与调用姿态。

## 20. 完成标准

当一个 `service` 被视为符合当前标准并可被 `Hub` 接纳时，应至少满足：

1. 兼容标准 `tool` 协议。
2. 提供最低生命周期工具。
3. 能通过 `manifest / register / heartbeat` 被 `Hub` 纳管。
4. 能让 `Hub` 对其 `tool` 做 caller / capability 前置筛选。
5. 能让 `Hub` 维护其运行统计与状态视图。

当一个 `tool` 被视为符合当前标准时，应至少满足：

1. 具备稳定 `tool_id`
2. 具备输入输出结构说明
3. 具备 caller/capability 门槛描述或明确 `all`
4. 具备协议形态说明
5. 具备副作用与风险等级说明

## 21. 非保证项

即使一个 `service` 被 `Hub` 接纳，以下事项仍不自动成立：

1. 该 `service` 内部一定正确做了用户身份鉴权。
2. 该 `service` 内部一定消费了 `Hub` 注入的 caller / capability。
3. 该 `service` 内部一定拒绝所有非 Hub 来源。
4. 该 `service` 的业务授权一定完整无缺。

被 `Hub` 接纳，表示：

1. 它符合接入规范。
2. 它进入了 `Hub` 的统一治理平面。
3. `Hub` 可以对其做路由、标记、展示和统计。

而不是：

1. `Hub` 对其内部安全做了完全背书。

---

**文档更新时间**：2026-03-19 20:00:27 CST

**本轮修改范围**：仅精炼 `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md` 的重复、冗余与啰嗦表述，不新增规范点，不删减既有约束含义。

**信息来源**：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md` 既有内容、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction.md` 的文档入口规则，以及本轮对本文件内部结构与语义重复点的逐段复核。
