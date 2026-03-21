# Hub 与 Services 超长 Go 文件拆分开发计划

## 1. 文档信息

- 初版时间：2026-03-21 18:22 CST
- 本次修订：2026-03-21 20:03 CST
- 修订目标：把原计划从“尽量压到 500 行以下”的导向，调整为“优先尊重现有代码结构和状态边界，不做过度拆分；在此基础上尽量保持文件相对独立、轻量”。
- 范围：扫描 `hub/` 与 `services/` 下全部 `.go` 文件，对超过 500 行的文件做结构化拆分规划。
- 依据：
  - `doc/_instruction.md`
  - `doc/_instruction/structure.md`
  - 仓库实时文件扫描与行数统计
  - 顶层声明、最长函数范围、重复文件聚类

## 2. 本轮修订后的核心结论

1. `>500 行` 在本计划中只作为**排查触发条件**，不再是硬性拆分目标。
2. 拆分的第一原则是**尊重结构边界**，尤其是状态机、锁、生命周期、协议编解码、Hub 互信和仓储边界。
3. 不追求“每个文件一定小于 500 行”。如果一个文件在边界清楚后仍有 `500~700` 行，但职责单一、耦合收敛、测试更容易补齐，则允许保留。
4. 真正高风险的不是“大文件”，而是：
   - 同文件混合多种 I/O 边界
   - 状态字段与锁的所有权不清
   - `main()`/handler 里塞进过多闭包逻辑
   - 跨项目共享抽取过早
5. 本次计划会给出**保守的一阶拆分方案**。一阶拆分后若仍有个别文件偏大，再做二阶细拆；不接受“一次性切成很多极小文件”。

## 3. 修订后的拆分原则

### 3.1 目标与非目标

- 目标：
  - 降低单文件职责数
  - 提高状态边界清晰度
  - 让入口文件回到“装配”角色
  - 让同类逻辑更容易补测试和复核
- 非目标：
  - 机械地把所有文件压到 500 行以下
  - 为了复用而过早抽公共包
  - 把一个聚合对象拆成多个难以理解的小碎片

### 3.2 拆分停止条件

当满足以下任意条件时，应该停止继续细拆：

1. 文件已经只承载一种稳定职责。
2. 继续拆分会把同一把锁保护的数据切散。
3. 继续拆分会让调用关系从“直线”变成“来回跳转”。
4. 继续拆分只能带来更小文件数值，却不降低理解成本。

### 3.3 安全优先级

- 高风险：状态机、并发、进程生命周期、鉴权令牌、Hub 互信
- 中风险：HTTP handler 聚合、仓储层、Hub tool 客户端
- 低风险：纯函数、结构体、渲染、normalize、模板与 helper

### 3.4 Service 独立性红线

必须保持每个 `service` 项目独立。拆分过程中，`services/<svc>` 只能依赖：

1. 自己项目内的代码
2. `pkg/hubsvc`
3. `pkg/toolproto`

除上述两类共享包外，**严禁新增对任何其他外部项目文件的引用**，包括但不限于：

- 引用其他 `services/*` 下的实现文件或内部包
- 引用 `hub/` 下的实现文件或内部包
- 为了复用而新增新的跨项目共享包

这条约束优先级高于“减少重复代码”。如果某段逻辑尚未稳定到足以进入 `pkg/hubsvc` 或 `pkg/toolproto`，则只能在各自 service 内局部保留或局部拆分，不得通过跨项目引用来换取更小文件。

### 3.5 行数预估的使用方式

本文中的“预估行数”只用于判断模块是否过大、是否可能仍需二阶拆分，不作为硬性 KPI。估算依据来自当前函数簇与辅助函数规模，属于粗略区间。

### 3.6 文件落位规则

拆分后的文件放置遵循以下统一机制：

1. 如果被拆分文件原本已经位于 `internal/app/`，则按“原地拆分”处理。
   - 新文件继续放在原目录
   - `package` 保持不变
   - 只做文件级拆分，不做包级迁移
2. 如果被拆分文件是各 service 的 `cmd/*/main.go`，则拆分后的业务实现文件**统一下沉到该 service 的 `internal/app/`**，与其他应用代码放在一起，不继续留在 `cmd/*/`。
3. `cmd/*/main.go` 在拆分后应尽量收缩为极薄入口，只保留：
   - flag 解析的最小入口
   - 调用 `internal/app` 启动装配函数
   - 进程级启动/退出的极小包装
4. 下沉到 `internal/app/` 的文件命名应当**按职责自主命名**，但在落盘前必须先检查并保证不与该目录现有文件重名。
5. 命名优先表达“该文件承载什么职责”，而不是机械套统一前缀。可以使用清晰的语义名称，例如：
   - `bootstrap_runtime.go`
   - `tool_http_handler.go`
   - `supervisor_registration.go`
   - `http_response_helpers.go`
   具体名称由实施时根据该 service 的现有文件族和语义自行确定。
6. 如果某 service 的 `internal/app/` 已存在同类职责文件，则优先并入已有文件族，或基于现有命名风格选择一个新的、不冲突的名称，避免制造两个语义接近但来源不同的文件。

## 4. 扫描结果

本次共识别出 **22 个超过 500 行** 的 Go 文件：

| 行数 | 文件 |
| ---: | --- |
| 1727 | `services/chat_server/internal/app/session.go` |
| 1415 | `services/surface_manager/cmd/surface_manager/main.go` |
| 1260 | `hub/internal/app/hub_platform.go` |
| 953 | `hub/internal/gateway/tool_handler.go` |
| 926 | `services/sql_db/internal/app/hub_platform.go` |
| 907 | `services/file_storage/internal/app/hub_platform.go` |
| 867 | `hub/internal/supervisor/lifecycle.go` |
| 855 | `hub/internal/gateway/admin_service_tools.go` |
| 853 | `services/file_storage/cmd/file_storage/main.go` |
| 788 | `services/sql_db/cmd/sql_db/main.go` |
| 784 | `services/ai_doubao/cmd/ai_doubao/main.go` |
| 757 | `services/chat_server/internal/app/hub_database_store.go` |
| 710 | `services/chat_server/cmd/chat_server/main.go` |
| 687 | `services/surface_manager/internal/app/message_types.go` |
| 684 | `hub/internal/supervisor/admin_ops.go` |
| 667 | `services/chat_server/internal/app/message_types.go` |
| 667 | `services/ai_doubao/internal/app/message_types.go` |
| 667 | `services/sql_db/internal/app/message_types.go` |
| 618 | `services/chat_server/internal/app/pipeline.go` |
| 598 | `services/account/internal/app/handler.go` |
| 593 | `services/ai_doubao/internal/app/asr.go` |
| 571 | `services/surface_manager/internal/app/hub_store.go` |

## 5. 重复与相似性核验

### 5.1 完全重复文件簇

经 SHA-256 核验，以下三份文件当前内容完全一致：

- `services/chat_server/internal/app/message_types.go`
- `services/ai_doubao/internal/app/message_types.go`
- `services/sql_db/internal/app/message_types.go`

### 5.2 高度相似文件簇

从类型定义与函数序列看，以下两份文件高度相似：

- `services/file_storage/internal/app/hub_platform.go`
- `services/sql_db/internal/app/hub_platform.go`

说明：`doc/_instruction/structure.md` 已声明 `hub/` 与 `services/*` 原则上互相独立，且稳定共享面当前主要是 `pkg/hubsvc`、`pkg/toolproto`。因此本计划建议：

1. 先做同包拆分。
2. 再做差异核验。
3. 若未来确实需要共享，也只能在严格论证后进入 `pkg/hubsvc` 或 `pkg/toolproto` 的既有共享边界；当前规划阶段默认不新增其他共享出口。

## 6. 分层执行策略

### 6.1 第一批先做低风险拆分

- 纯 helper、模板、normalize、render、schema 常量
- `cmd/*/main.go` 中的响应工具和注册/心跳辅助
- 重复 `message_types.go` 的本地拆分

### 6.2 第二批处理中风险聚合

- 仓储层
- Admin 查询/配置/文件工具
- Hub tool 客户端
- pipeline 内的纯算法簇

### 6.3 第三批再动高风险核心

- `chat_server/internal/app/session.go`
- `hub/internal/app/hub_platform.go`
- `hub/internal/gateway/tool_handler.go`
- `hub/internal/supervisor/lifecycle.go`

这四类必须先明确“状态所有权表”与“函数归属表”，再开始搬代码。

## 7. 逐文件改进后的拆分计划

### 7.1 Hub

#### `hub/internal/app/hub_platform.go`（1260 行）

- 可行性：高，但属于**高风险拆分**。原因是单结构体 `HubPlatform` 通过一把 `mu` 统一保护 `services/serviceAuths/conflicts/toolProviders/bindings/manualBind/stats`。
- 现状边界：
  - 注册与存活：`RegisterService`、`AcceptServiceHeartbeat`、`UnregisterService`、`MarkServiceDown/Active`
  - 鉴权：`PrepareServiceBootstrap`、`ServiceHubAuth`、`VerifyServiceAuth`、`VerifyHubAuth`、`IssueOriginCallerToken`、`VerifyOriginCallerToken`
  - 路由与统计：`SetManualBinding`、`RecordToolCall`、`RefreshBindings`、`rebuildToolProvidersLocked`
  - 视图：`ListServices/ListRegisteredServices/ListBindings/ListTools/ResolveToolRoute`
- 一阶拆分建议：
  - `hub_platform_types.go`：结构体、常量、builtin tool、构造函数，约 `120~180` 行
  - `hub_platform_registry.go`：注册、心跳、摘除、可靠性治理，约 `260~340` 行
  - `hub_platform_auth.go`：bootstrap/hub auth/origin caller token，约 `220~300` 行
  - `hub_platform_routing.go`：binding、provider stats、refresh/rebuild，约 `260~340` 行
  - `hub_platform_view.go`：List/Resolve/normalize 辅助，约 `220~320` 行
- 安全约束：
  - `mu` 仍只保留在 `HubPlatform` 主结构体中，不要拆成多锁。
  - `loadPersistedStateLocked/savePersistedStateLocked` 与 routing/stats 放在同一文件更安全。
  - 一阶拆分后若 `hub_platform_view.go` 仍偏大，可二阶把 normalize 纯函数独立出去；否则不要继续拆。

#### `hub/internal/gateway/tool_handler.go`（953 行）

- 可行性：高，风险高于平均但低于 `session.go`。因为它混合了 HTTP、WS、鉴权、路由选择和 effects。
- 现状边界：
  - `HandleCall` 约 195 行
  - `HandleWS` + legacy/proxy 一簇
  - `resolveRequestIdentity/resolveOriginDelegation/resolveHubAuth` 一簇
  - `applyToolEffects/syncAccountSessionFromResult` 一簇
- 一阶拆分建议：
  - `tool_handler_core.go`：构造、注册、错误回写、共用 helper，约 `120~180` 行
  - `tool_handler_call.go`：`HandleCall` 与 call path 私有子函数，约 `260~360` 行
  - `tool_handler_ws.go`：`HandleWS/handleLegacyWS/proxyWS`，约 `220~300` 行
  - `tool_handler_auth.go`：identity/origin delegation/hub auth，约 `120~180` 行
  - `tool_handler_effects.go`：effects、account sync、轻量解析工具，约 `120~180` 行
- 安全约束：
  - `HandleCall` 应同步拆成 4~5 个私有步骤函数，否则只是换文件不降复杂度。
  - WS 代理与普通 HTTP tool call 不要再放回同文件。
  - 若一阶后 `tool_handler_call.go` 在 `300+` 行但职责单一，可接受，不必再追求硬切。

#### `hub/internal/gateway/admin_service_tools.go`（855 行）

- 可行性：很高，属于**中风险、高收益**。该文件天然适合按工具族拆分。
- 一阶拆分建议：
  - `admin_service_query_tools.go`：`handleServiceGetTool` 与查询聚合，约 `180~260` 行
  - `admin_service_lifecycle_tools.go`：start/stop/restart/drain/enable/disable/rebind，约 `180~260` 行
  - `admin_service_config_tools.go`：manifest/config get/update/restore，约 `160~240` 行
  - `admin_service_files_tools.go`：build/files list/read/write，约 `140~220` 行
  - `admin_service_generate_tools.go`：generate + 模板渲染，约 `180~260` 行
- 安全约束：
  - `runServiceScaffoldGeneration` 与内嵌模板优先分离，它们对治理工具主路径是噪声。
  - 这里不建议继续细拆到“一工具一文件”。

#### `hub/internal/supervisor/admin_ops.go`（684 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `admin_ops_lifecycle.go`：Start/Stop/Restart/Drain，约 `140~220` 行
  - `admin_ops_manifest_config.go`：manifest/config 读写，约 `180~260` 行
  - `admin_ops_files.go`：List/Read/Write 文件与路径解析，约 `150~220` 行
  - `admin_ops_build.go`：BuildService + build command，约 `100~160` 行
  - `admin_ops_describe.go`：describe/NextSuggestedPort/纯辅助，约 `80~140` 行
- 安全约束：
  - `managedServiceLocked` 与 `describeManagedService` 仍应紧邻管理对象相关逻辑，不要飘散。

#### `hub/internal/supervisor/lifecycle.go`（867 行）

- 可行性：高，但属于**高风险拆分**。原因是启动编排和进程观察存在强时序关系。
- 现状边界：
  - config model
  - DAG 规划：`buildStartupPlan`、`firstFailedDependency`
  - 启动/停止：`StartAll/startService/startOnce/StopAll/stopOne/stopProcess`
  - 观察与重启：`trackProcess/watchProcess`
- 一阶拆分建议：
  - `lifecycle_types.go`：config/startup model，约 `120~180` 行
  - `lifecycle_manager.go`：New/StartAll/StopAll/startService/serviceByID，约 `220~320` 行
  - `lifecycle_plan.go`：DAG 与依赖规划，约 `120~200` 行
  - `lifecycle_process.go`：startOnce/track/watch/stopProcess，约 `220~320` 行
  - `lifecycle_util.go`：normalize/flag/env/helper，约 `100~160` 行
- 安全约束：
  - `watchProcess` 与 restart policy 的语义必须保持原子理解，不要拆到多个互相跳转的文件。
  - 若一阶后 `lifecycle_process.go` 接近 300 行但仍围绕同一进程模型，可以接受。

### 7.2 surface_manager

#### `services/surface_manager/cmd/surface_manager/main.go`（1415 行）

- 可行性：很高，且收益明显。当前 `main()` 约 1021 行，是典型入口过载。
- 一阶拆分建议：
  - `bootstrap_runtime.go`：flag/config/bootstrap/依赖装配，约 `160~240` 行
  - `tool_http_handler.go`：路由注册与 `/service/tool/exec` 主分发，约 `320~460` 行
  - `supervisor_registration.go`：Hub register/heartbeat/buildHubToolCallURL/postHubToolCall，约 `160~240` 行
  - `surface_generation.go`：`generateSurfaceByAI`，约 `40~100` 行
  - `http_response_helpers.go`：`writeJSON/writeToolResponse/asString/asInt/asBool/healthzRequested`，约 `120~180` 行
- 安全约束：
  - `tool_http_handler.go` 允许保留为较大的单文件，因为 tool exec 分发本身是同一稳定职责。
  - 不要把每个 route handler 都拆成单文件。

#### `services/surface_manager/internal/app/hub_store.go`（571 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `hub_store_schema.go`：建表与 schema 常量，约 `60~120` 行
  - `hub_store_catalog.go`：SyncScannedSurfaces/read/write catalog，约 `180~260` 行
  - `hub_store_user.go`：List/Get/Set user surface state，约 `120~180` 行
  - `hub_store_logs.go`：LoadRecentSurfaceMessages/appendLog，约 `120~180` 行
  - `hub_store_client.go`：database/share/tool call 封装与 row decode，约 `120~180` 行
- 安全约束：
  - 所有 Hub tool HTTP 调用封装集中保留在 `hub_store_client.go`，避免后续鉴权、超时策略散落。

#### `services/surface_manager/internal/app/message_types.go`（687 行）

- 可行性：高，属于低风险纯逻辑拆分。
- 一阶拆分建议：
  - `message_types_model.go`：常量、`ChatMessage`、`MessageWrite`，约 `120~180` 行
  - `message_types_build.go`：`BuildMessage` 与 payload 组装，约 `180~260` 行
  - `message_types_normalize.go`：normalize/infer/helper，约 `160~240` 行
  - `message_types_render.go`：render/humanize/provider role，约 `140~220` 行
- 安全约束：
  - 这是轻量文件族，不必再继续细拆。

### 7.3 chat_server

#### `services/chat_server/cmd/chat_server/main.go`（710 行）

- 可行性：高，入口聚合特征清晰。
- 一阶拆分建议：
  - `bootstrap_runtime.go`：配置、manifest、bootstrap、provider 装配，约 `140~220` 行
  - `tool_http_handler.go`：`/service/tool/exec`，约 `180~260` 行
  - `tool_ws_handler.go`：`/service/tool/ws` 与 session 建立，约 `140~220` 行
  - `supervisor_registration.go`：register/heartbeat，约 `140~220` 行
  - `http_response_helpers.go`：`toSupervisorTools/asString/asInt/writeJSON` 等，约 `80~140` 行
- 安全约束：
  - `providerFactory` 与 session wiring 保持在 bootstrap/WS 邻近区域，不要拆得太散。

#### `services/chat_server/internal/app/session.go`（1727 行）

- 可行性：高，但这是全仓库**最高风险文件之一**。
- 风险来源：
  - 多把锁：`stateMu/writeMu/asrCancelMu/turnMu/historyMu/draftMu/interruptMu/actionMu/actionRefMu`
  - 运行态字段高度集中
  - 既有实时链路，又有历史持久化和 action continuation
- 建议先做“状态所有权表”：
  - runtime/连接：`conn/rootCtx/rootCancel/writeMu`
  - turn/asr：`asrCancelMu/turnMu/turnID/turnCancel/lastASRText*`
  - history/assistant：`historyMu/chatHistory/draftMu/assistantDrafts/assistantFinalized`
  - action：`actionMu/userTurnActive/continuation*/pendingFollowups/actionRateWindow/actionDedup/actionCallRefIDs`
- 一阶拆分建议：
  - `session_runtime.go`：`Session` 结构、构造、`Run/readLoop/ttsSenderLoop/cleanup`、基础 send/emit，约 `260~360` 行
  - `session_turns.go`：`handleControl/startASRTurn/startTurn/cancelASR/interrupt/stopAll`，约 `260~360` 行
  - `session_history.go`：history window、draft、finalize、bootstrapHistoryFromStore，约 `240~340` 行
  - `session_actions.go`：`handleActionResult/evaluateActionGuard/followup/continuation/actionRef`，约 `320~440` 行
  - `session_state.go`：state setter/getter、config change、轻量纯函数，约 `120~200` 行
- 安全约束：
  - 一阶之后 `session_actions.go` 很可能仍接近或略高于 400 行，但这仍是可接受结果，因为 action continuation 本身是单一复杂域。
  - 不建议再把 `session_actions.go` 强拆成“guard/report/ref/followup”四五个小文件，除非后续业务继续增长。

#### `services/chat_server/internal/app/hub_database_store.go`（757 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `hub_database_store_types.go`：Project/Thread/interface/options，约 `80~140` 行
  - `hub_database_store_schema.go`：DDL/init/default ID 初始化，约 `180~280` 行
  - `hub_database_store_messages.go`：Append/Load/ContextBefore，约 `180~260` 行
  - `hub_database_store_projects.go`：project CRUD，约 `120~180` 行
  - `hub_database_store_threads.go`：thread CRUD，约 `120~180` 行
  - `hub_database_store_client.go`：query/execute/row decode helper，约 `100~160` 行
- 安全约束：
  - `query/execute` 和行解码工具保留在一个 client 文件里，不要复制到 message/project/thread 文件。

#### `services/chat_server/internal/app/pipeline.go`（618 行）

- 可行性：高，风险较低，因为主要由算法簇构成。
- 一阶拆分建议：
  - `pipeline_turn.go`：`TurnPipeline`、`RunTurn`、TTS/LLM 编排，约 `180~260` 行
  - `pipeline_segmenter.go`：`SentenceSegmenter`，约 `80~140` 行
  - `pipeline_backlog.go`：backlog estimator、speech duration，约 `100~160` 行
  - `pipeline_projector.go`：LLM envelope projector 与 JSON 解析，约 `180~260` 行
- 安全约束：
  - `RunTurn` 仍建议在同文件内再抽几个私有步骤函数，但不必继续拆出更多文件。

#### `services/chat_server/internal/app/message_types.go`（667 行）

- 可行性：高，低风险。
- 一阶拆分建议：
  - `message_types_model.go`：`120~180` 行
  - `message_types_build.go`：`180~260` 行
  - `message_types_normalize.go`：`160~240` 行
  - `message_types_render.go`：`140~220` 行
- 安全约束：
  - 先做本地拆分。
  - 二阶再判断是否和 `ai_doubao/sql_db` 提炼公共实现。

### 7.4 file_storage

#### `services/file_storage/cmd/file_storage/main.go`（853 行）

- 可行性：高，入口过载明显。
- 一阶拆分建议：
  - `bootstrap_runtime.go`：`140~220` 行
  - `tool_http_handler.go`：tool exec + lifecycle tool dispatch，约 `240~360` 行
  - `supervisor_registration.go`：register/heartbeat，约 `140~220` 行
  - `http_response_helpers.go`：`100~160` 行
- 安全约束：
  - `service.lifecycle.*` 与业务 storage tool dispatch 可放在同一个 `tool_http_handler.go`，不必进一步拆散。

#### `services/file_storage/internal/app/hub_platform.go`（907 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `hub_platform_types.go`：`100~160` 行
  - `hub_platform_registry.go`：`220~300` 行
  - `hub_platform_auth.go`：`140~220` 行
  - `hub_platform_routing.go`：`220~300` 行
  - `hub_platform_view.go`：`180~260` 行
- 安全约束：
  - 与 `sql_db` 先平行拆出同样的文件边界，便于后续逐文件 diff。
  - 不建议当前就与 `hub/internal/app/hub_platform.go` 共用实现。

### 7.5 ai_doubao

#### `services/ai_doubao/cmd/ai_doubao/main.go`（784 行）

- 可行性：高。
- 一阶拆分建议：
  - `bootstrap_runtime.go`：`140~220` 行
  - `tool_http_handler.go`：tool exec 与 lifecycle alias，约 `180~260` 行
  - `media_ws_handlers.go`：ASR/LLM/TTS handler，约 `180~260` 行
  - `supervisor_registration.go`：register/heartbeat，约 `140~220` 行
  - `http_response_helpers.go`：`80~140` 行
- 安全约束：
  - 媒体入口与 Hub 工具入口必须分开，这是比“压行数”更重要的边界。

#### `services/ai_doubao/internal/app/asr.go`（593 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `asr_runtime.go`：client、Run、Finish，约 `180~260` 行
  - `asr_protocol.go`：frame/gzip 编解码，约 `140~220` 行
  - `asr_payload.go`：payload 解析、文本抽取、utterance 判断，约 `120~180` 行
  - `asr_config.go`：dial target/header/start payload，约 `100~160` 行
- 安全约束：
  - 网络时序逻辑与协议格式逻辑应分开，但不必拆得更细。

#### `services/ai_doubao/internal/app/message_types.go`（667 行）

- 可行性：高，低风险。
- 一阶拆分建议：同 `chat_server/internal/app/message_types.go`。
- 安全约束：先本地拆分，不抢跑公共化。

### 7.6 account

#### `services/account/internal/app/handler.go`（598 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `handler_entry.go`：`HandleTool` 与 tool 路由，约 `100~160` 行
  - `handler_auth.go`：register/login/logout/me/password change，约 `220~300` 行
  - `handler_lifecycle.go`：Initialize/health/state/shutdown，约 `120~180` 行
  - `handler_response.go`：baseResponse/error helper/token helper，约 `120~180` 行
- 安全约束：
  - `issueToken/hashPassword/verifyPassword` 若后续还增长，可再挪到 `token.go`；当前一阶可先不拆。

### 7.7 sql_db

#### `services/sql_db/cmd/sql_db/main.go`（788 行）

- 可行性：高。
- 一阶拆分建议：
  - `bootstrap_runtime.go`：`140~220` 行
  - `tool_http_handler.go`：tool exec + lifecycle，约 `220~340` 行
  - `supervisor_registration.go`：register/heartbeat，约 `140~220` 行
  - `http_response_helpers.go`：`100~160` 行
- 安全约束：
  - 与 `file_storage` 采用相同文件边界，便于横向复核。

#### `services/sql_db/internal/app/hub_platform.go`（926 行）

- 可行性：高，风险中等。
- 一阶拆分建议：
  - `hub_platform_types.go`：`100~160` 行
  - `hub_platform_registry.go`：`220~300` 行
  - `hub_platform_auth.go`：`140~220` 行
  - `hub_platform_routing.go`：`220~300` 行
  - `hub_platform_view.go`：`180~260` 行
- 安全约束：
  - 与 `file_storage` 先做镜像式拆分，再决定是否提炼共享代码。

#### `services/sql_db/internal/app/message_types.go`（667 行）

- 可行性：高，低风险。
- 一阶拆分建议：同 `chat_server/internal/app/message_types.go`。
- 安全约束：先本地拆分，不抢跑公共化。

## 8. 建议的实施顺序

### Phase 1：建立安全骨架

1. 先为高风险文件写“状态所有权表/函数归属表”。
2. 建立目标文件框架，但暂不大量移动逻辑。
3. 把纯 helper、types、render、normalize、schema 先迁出。

### Phase 2：拆入口与仓储

1. 拆各 service 的 `cmd/*/main.go`
2. 拆 `admin_service_tools.go`
3. 拆 `hub_database_store.go`、`hub_store.go`
4. 拆三份 `message_types.go`

### Phase 3：拆高风险核心

1. `hub_platform.go`
2. `tool_handler.go`
3. `lifecycle.go`
4. `session.go`

## 9. 验收标准

### 9.1 一阶验收

- 编译通过
- 测试或最小烟测通过
- 主要状态边界未被打散
- 入口文件明显回到“装配/分发”角色
- 仓储、协议、render/helper 已分层

### 9.2 二阶验收

- 若某文件仍显著偏大，再判断是否存在新的稳定子边界
- 若不存在稳定子边界，则允许保留，不以“必须更小”为理由继续切分

## 10. 主要风险与控制措施

- 风险：高风险文件拆完后调用关系更绕。
  - 控制：先出函数归属表，再搬代码。
- 风险：锁与状态字段散落到多个文件后难以追踪。
  - 控制：一个状态域只归一个文件主责；其他文件只调用方法，不直接碰字段。
- 风险：重复代码过早提炼成公共包，扩大跨项目耦合。
  - 控制：先同包拆分，再做差异核验；共享面严格限制在 `pkg/hubsvc` 与 `pkg/toolproto`。
- 风险：为了压行数而继续细拆，最后得到很多“只有几十行但没有独立意义”的文件。
  - 控制：使用第 3 节的拆分停止条件。
- 风险：拆分过程中为了复用而引入 `service -> service`、`service -> hub` 或其他新共享包依赖，破坏项目独立性。
  - 控制：把“service 独立性红线”作为硬约束；除 `pkg/hubsvc` 和 `pkg/toolproto` 外，不允许新增任何外部引用。

## 11. 本轮修订后的执行判断

- 最适合马上动手的文件：
  - `hub/internal/gateway/admin_service_tools.go`
  - `services/surface_manager/cmd/surface_manager/main.go`
  - `services/chat_server/cmd/chat_server/main.go`
  - 三份 `message_types.go`
- 需要先做结构草图再动手的文件：
  - `services/chat_server/internal/app/session.go`
  - `hub/internal/app/hub_platform.go`
  - `hub/internal/gateway/tool_handler.go`
  - `hub/internal/supervisor/lifecycle.go`

这四个文件后续即使完成一阶拆分，也不承诺每个结果文件都小于 500 行；本计划优先保证结构正确、职责稳定、可维护性上升。
