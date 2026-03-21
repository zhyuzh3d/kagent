# Hub 与 Services 超长 Go 文件拆分开发计划

## 1. 文档信息

- 生成时间：2026-03-21 18:22 CST
- 任务范围：基于仓库实际目录 `hub/` 与 `services/` 扫描全部 `.go` 文件，识别超过 500 行的单文件，并给出逐文件拆分思路。
- 事实依据：
  - 入口与结构文档：`doc/_instruction.md`、`doc/_instruction/structure.md`
  - 实时文件扫描：遍历 `hub/`、`services/` 下全部 `.go` 文件并统计行数
  - 顶层声明与函数热点：对超长文件抽取 `type/func/const/var`，并计算最长函数范围
  - 重复性核验：对超长文件做 SHA-256 聚类，确认完全重复文件簇

## 2. 结论先行

本次在 `hub/` 与 `services/` 共识别出 **22 个超过 500 行** 的 Go 文件。超长文件主要集中在 5 类问题：

1. `cmd/*/main.go` 同时承担配置加载、依赖装配、HTTP 路由注册、工具分发、Hub 注册、心跳守护与若干工具函数，职责明显过载。
2. 平台/网关文件把“状态存储 + 鉴权令牌 + 路由绑定 + 视图拼装 + 持久化”压进一个文件，变更耦合高。
3. `chat_server` 与 `surface_manager` 内部存在“领域逻辑 + 存储访问 + 协议编解码 + 运行时状态机”混放。
4. `message_types.go` 在 `chat_server`、`ai_doubao`、`sql_db` 三处完全重复；`hub_platform.go` 在 `file_storage` 与 `sql_db` 高度近似，存在可控的抽取机会。
5. 若按“平均分块”拆，会把锁、状态机、协议边界切碎；正确做法应按 **职责边界、依赖方向、共享稳定性、并发状态所有权** 拆分。

## 3. 扫描结果

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

## 4. 全局拆分原则

1. **先按职责边界拆，不按行数平均拆。** 同一把锁保护的数据、同一条请求链上的上下文、同一套协议编解码必须保留在同一聚合内。
2. **先抽“稳定边界”，再抽“共享边界”。** 先把一个文件拆成更清晰的同包文件；只有在跨项目语义稳定时，才考虑上升到 `pkg/`。这点尤其适用于 `message_types.go` 与 service 侧 `hub_platform.go`。
3. **入口函数只做装配。** 所有 `cmd/*/main.go` 最终应退化为：加载配置、构造依赖、注册路由、启动服务。
4. **持久化/网络调用/视图组装分离。** 读写数据库、调 Hub 工具、拼 JSON 响应、执行业务决策，不应继续挤在一个文件里。
5. **拆分顺序优先保守。** 先移动纯函数、协议结构、辅助工具；再移动带 I/O 的仓储；最后处理状态机与并发代码。

## 5. 重复代码观察

### 5.1 完全重复文件簇

经 SHA-256 核验，以下三个文件内容完全一致：

- `services/chat_server/internal/app/message_types.go`
- `services/ai_doubao/internal/app/message_types.go`
- `services/sql_db/internal/app/message_types.go`

### 5.2 高度近似文件簇

从顶层结构和函数序列看，以下两组文件高度近似：

- `services/file_storage/internal/app/hub_platform.go`
- `services/sql_db/internal/app/hub_platform.go`

说明：由于 `doc/_instruction/structure.md` 明确约束 `hub/` 与 `services/*` 原则上是相互独立项目，跨项目共享代码不宜直接仓促上提到 `pkg/`。建议先做“同包拆分”，再决定是否形成稳定公共包。

## 6. 逐文件拆分思路

### 6.1 Hub

#### `hub/internal/app/hub_platform.go`（1260 行）

- 现状证据：
  - 注册/心跳/摘除：`RegisterService`、`AcceptServiceHeartbeat`、`UnregisterService`
  - 鉴权与令牌：`PrepareServiceBootstrap`、`ServiceHubAuth`、`VerifyServiceAuth`、`VerifyHubAuth`、`IssueOriginCallerToken`、`VerifyOriginCallerToken`
  - 绑定与统计：`SetManualBinding`、`RecordToolCall`、`RefreshBindings`
  - 视图拼装：`ListTools`、`buildToolCandidatesLocked`、`buildToolViewLocked`
- 拆分建议：
  - `hub_platform_registry.go`：保留 service 注册、心跳、状态切换与冲突管理。
  - `hub_platform_auth.go`：集中双向鉴权、bootstrap secret、origin caller token。
  - `hub_platform_binding.go`：手工绑定、统计、provider 打分、binding 刷新。
  - `hub_platform_view.go`：`ListServices/ListBindings/ListTools/ResolveToolRoute` 与视图组装辅助函数。
  - `hub_platform_manifest.go`：`normalize*`、`manifestHash`、`inferTransport*`、`reliabilityWeight` 等纯函数。
- 关键约束：
  - 不要把 `mu` 分裂成多个互不协调的锁，优先保持一个拥有者结构体，只拆同包文件。
  - `loadPersistedStateLocked/savePersistedStateLocked` 应与 binding/stats 靠近，避免状态落盘逻辑散落。

#### `hub/internal/gateway/tool_handler.go`（953 行）

- 现状证据：
  - HTTP 同步工具入口：`HandleCall`，单函数约 195 行
  - WS 入口与代理：`HandleWS`、`handleLegacyWS`、`proxyWS`
  - 鉴权/委托：`resolveRequestIdentity`、`resolveOriginDelegation`、`resolveHubAuth`
  - 效果处理：`applyToolEffects`、`syncAccountSessionFromResult`
- 拆分建议：
  - `tool_handler_call.go`：`HandleCall` 及请求规范化、错误返回、内部工具分派。
  - `tool_handler_ws.go`：`HandleWS`、legacy WS 兼容与反向代理。
  - `tool_handler_auth.go`：身份解析、Hub/Auth header 注入、origin delegation。
  - `tool_handler_effects.go`：cookie/header effects、account session 同步。
  - `tool_handler_select.go`：tool 选择、endpoint 解析、descriptor/WS path 查找。
- 关键约束：
  - `HandleCall` 应进一步拆成“请求解码/身份建立/路由选择/远端调用/响应回写”五段私有函数，否则只是文件拆分、复杂度不降。

#### `hub/internal/gateway/admin_service_tools.go`（855 行）

- 现状证据：
  - 查询聚合：`handleServiceGetTool`
  - 生命周期治理：`handleServiceStartTool/Stop/Restart/Drain/Enable/Disable/Rebind`
  - 配置与清单：`handleServiceManifestGet/Update`、`handleServiceConfigGet/Update/RestoreDefault`
  - 文件与构建：`handleServiceBuildTool`、`handleServiceFilesList/Read/Write`
  - 代码生成：`handleServiceGenerateTool`、`runServiceScaffoldGeneration`，并内嵌 `main/go.mod/README` 模板
- 拆分建议：
  - `admin_service_query_tools.go`：查询类工具，尤其是 `handleServiceGetTool`。
  - `admin_service_lifecycle_tools.go`：启动、停止、重启、drain、enable/disable、rebind。
  - `admin_service_config_tools.go`：manifest/config 读写与默认恢复。
  - `admin_service_files_tools.go`：build、文件读写。
  - `admin_service_generate_tools.go`：脚手架生成逻辑与模板；模板文本应移到 `internal/gateway/scaffold_templates.go` 或 `embed` 资源。
- 关键约束：
  - 该文件最大的复杂度不是行数，而是把治理 API、脚手架模板和一次性生成器塞在一起；生成器必须优先出列。

#### `hub/internal/supervisor/admin_ops.go`（684 行）

- 现状证据：
  - 运行态/清单/配置访问：`ReadRuntimeManifest`、`ReadStartupManifest`、`ReadConfigJSON`、`WriteConfigJSON`
  - 生命周期操作：`StartService`、`StopService`、`RestartService`、`DrainService`
  - 构建与文件：`BuildService`、`ListServiceFiles`、`ReadServiceFile`、`WriteServiceFile`
  - 描述与端口建议：`describeManagedService`、`NextSuggestedPort`
- 拆分建议：
  - `admin_ops_lifecycle.go`：`Start/Stop/Restart/Drain`
  - `admin_ops_manifest_config.go`：manifest/config 读写
  - `admin_ops_files.go`：文件枚举与文件路径解析
  - `admin_ops_build.go`：构建与 build command 解析
  - `admin_ops_describe.go`：managed service 描述、端口建议、纯工具函数
- 关键约束：
  - 与 `lifecycle.go` 的边界要清楚：`admin_ops.go` 负责“外部管理接口”，`lifecycle.go` 负责“内部启动机理”。

#### `hub/internal/supervisor/lifecycle.go`（867 行）

- 现状证据：
  - 配置/模型定义：`LifecycleConfig`、`StartupManifest*`
  - 启动编排：`StartAll`、`buildStartupPlan`、`firstFailedDependency`
  - 进程生命周期：`startOnce`、`trackProcess`、`watchProcess`、`stopProcess`
  - 配置规范化与 flag/env 工具：`normalize*`、`ensureFlagValue`、`flattenEnv`
- 拆分建议：
  - `lifecycle_config.go`：配置结构、加载与 normalize。
  - `lifecycle_plan.go`：启动 DAG、依赖排序、缺失依赖与环检测。
  - `lifecycle_process.go`：`startOnce/trackProcess/watchProcess/stopProcess`。
  - `lifecycle_manager.go`：`NewLifecycleManager/StartAll/StopAll/startService/serviceByID`。
  - `lifecycle_util.go`：flag/env/时间戳等纯辅助函数。
- 关键约束：
  - `managedService` 与 `managedProcess` 的所有权应集中，不要在拆分后形成双向依赖回跳。

### 6.2 surface_manager

#### `services/surface_manager/cmd/surface_manager/main.go`（1415 行）

- 现状证据：
  - 整个 `main()` 长达约 1021 行
  - 单文件同时包含：flag/config/bootstrap、HTTP 路由、tool exec 分发、Surface 生成、Hub 注册、heartbeat guard、响应辅助函数
- 拆分建议：
  - `bootstrap.go`：配置加载、bootstrap secret、register URL、启动前校验。
  - `http_routes.go`：`/healthz`、`/service/info`、`/service/tools`、`/admin/shutdown` 注册。
  - `tool_exec_handler.go`：`/service/tool/exec` 内部的 tool 分发与上下文校验。
  - `surface_generate.go`：`generateSurfaceByAI` 与 AI surface 生成流程。
  - `supervisor_register.go`：Hub 注册、心跳守护、`buildHubToolCallURL`、`postHubToolCall`。
  - `response_helpers.go`：`writeJSON`、`writeToolResponse`、`asString/asInt/asBool` 等。
- 关键约束：
  - `main()` 最终只保留依赖装配与 `http.ListenAndServe`，其余全部下沉。

#### `services/surface_manager/internal/app/hub_store.go`（571 行）

- 现状证据：
  - schema 建表：`EnsureSchema`
  - catalog 同步：`SyncScannedSurfaces`、`writeCatalogEntry`
  - 用户设置：`ListSurfacesForUser`、`SetSurfaceEnabled`、`loadUserSurfaceSettings`
  - 日志与 Hub 调用：`LoadRecentSurfaceMessages`、`appendLog`、`databaseQuery/databaseExecute/shareRead/shareWrite/callTool`
- 拆分建议：
  - `hub_store_schema.go`：建表 SQL 与初始化。
  - `hub_store_catalog.go`：catalog 读写与扫描同步。
  - `hub_store_user_settings.go`：用户启停状态。
  - `hub_store_logs.go`：surface 日志读写。
  - `hub_store_client.go`：Hub tool 调用与 row decode 工具。
- 关键约束：
  - 所有 Hub tool HTTP 调用细节要集中，否则后续超时、鉴权、重试策略无法统一。

#### `services/surface_manager/internal/app/message_types.go`（687 行）

- 现状证据：
  - 模型：`ChatMessage`、`MessageWrite`
  - 构建入口：`BuildMessage`，约 122 行
  - 规范化：`normalizeMessage*`、`normalizeActionJSON`、`normalizeRawDataJSON`
  - 渲染：`renderMessageContent`、`renderActionContent`、`renderSurfaceContent`
- 拆分建议：
  - `message_model.go`：结构体、常量。
  - `message_build.go`：`BuildMessage` 与 payload 组装。
  - `message_normalize.go`：role/category/type/status/interrupt JSON normalize。
  - `message_render.go`：content render 与 provider role 映射。
- 关键约束：
  - 该文件暂时只在 `surface_manager` 内局部拆分，不建议直接拉入 `pkg/`，因为与其他服务的消息契约是否完全稳定仍需治理确认。

### 6.3 chat_server

#### `services/chat_server/cmd/chat_server/main.go`（710 行）

- 现状证据：
  - `main()` 约 520 行
  - 同时处理：bootstrap、HTTP tool 入口、WS 入口、provider factory、Hub 注册、heartbeat guard、工具函数
- 拆分建议：
  - `bootstrap.go`：配置、manifest、bootstrap secret、Hub URL。
  - `tool_exec_handler.go`：`/service/tool/exec`
  - `tool_ws_handler.go`：`/service/tool/ws`
  - `supervisor_register.go`：注册与 heartbeat
  - `response_helpers.go`：轻量工具函数
- 关键约束：
  - `providerFactory` 与 session 初始化路径要留在一处装配函数内，避免 WS 入口与工具入口各自重建。

#### `services/chat_server/internal/app/session.go`（1727 行）

- 现状证据：
  - 长函数热点：`handleActionResult` 约 194 行，`handleControl` 约 114 行，`finalizeAssistantMessage` 约 90 行
  - 单文件承载：WebSocket 收发、ASR turn 控制、状态机、历史消息、assistant draft/final、action follow-up、去重与限流
- 拆分建议：
  - `session_runtime.go`：`Session` 结构、`Run`、生命周期与 cleanup。
  - `session_ws.go`：`readLoop`、`ttsSenderLoop`、发送/接收事件。
  - `session_turns.go`：`handleControl`、`startASRTurn`、turn cancel/interrupt。
  - `session_assistant.go`：draft/finalize/history 维护。
  - `session_actions.go`：action result、follow-up flush、去重与限流。
  - `session_state.go`：状态切换、state/event 发射。
- 关键约束：
  - 按“状态所有权”拆，不按功能名随意拆；涉及 `turnMu/stateMu/historyMu/actionMu` 的代码必须明确归属。

#### `services/chat_server/internal/app/hub_database_store.go`（757 行）

- 现状证据：
  - 初始化/建表：`init`
  - 默认 ID 管理：`initDefaultIDs`、`ensureUserLocked/ensureProjectLocked/ensureThreadLocked`
  - 消息仓储：`AppendMessage`、`LoadSessionWindow`、`LoadContextBeforeWithMode`
  - 项目/线程仓储：`List/Create/Update/Delete Project/Thread`
  - Hub tool SQL 封装：`query`、`execute`
- 拆分建议：
  - `hub_database_schema.go`：DDL 和初始化。
  - `hub_database_scope.go`：默认 user/project/thread 决议。
  - `hub_database_messages.go`：消息读写。
  - `hub_database_projects.go`：项目仓储。
  - `hub_database_threads.go`：线程仓储。
  - `hub_database_client.go`：query/execute 与行解码工具。
- 关键约束：
  - 先稳定 `HubToolClient` 抽象，再拆仓储；否则每个文件都会重复拼接 SQL 调用协议。

#### `services/chat_server/internal/app/pipeline.go`（618 行）

- 现状证据：
  - 主流程：`RunTurn` 约 156 行
  - 文本切分：`SentenceSegmenter`
  - 播放背压：`playbackBacklogEstimator`
  - LLM envelope 投影：`llmContentProjector` 与 JSON 解析辅助函数
- 拆分建议：
  - `pipeline_turn.go`：`TurnPipeline` 与 `RunTurn`
  - `pipeline_segmenter.go`：句子切分器
  - `pipeline_backlog.go`：播放背压估算与时长估算
  - `pipeline_projector.go`：LLM envelope 投影与 JSON 预览解析
- 关键约束：
  - `RunTurn` 本身还需要再做私有函数抽取，否则只拆文件不足以降低认知负担。

#### `services/chat_server/internal/app/message_types.go`（667 行）

- 现状证据：与 `ai_doubao/sql_db` 同名文件完全一致。
- 拆分建议：
  - 先按 `message_model/build/normalize/render` 四段本地拆开。
  - 第二阶段评估是否抽成稳定公共包；未完成契约评审前，不建议直接让多个 service 依赖新的 `pkg/`。

### 6.4 file_storage

#### `services/file_storage/cmd/file_storage/main.go`（853 行）

- 现状证据：
  - `main()` 约 625 行
  - 混合了启动装配、tool exec 路由、生命周期工具、Hub 注册、heartbeat、响应工具
- 拆分建议：
  - `bootstrap.go`
  - `tool_exec_handler.go`
  - `lifecycle_tools.go`
  - `supervisor_register.go`
  - `response_helpers.go`
- 关键约束：
  - `service.lifecycle.*` 与业务 storage tool dispatch 建议分文件，否则生命周期改动仍会影响业务入口。

#### `services/file_storage/internal/app/hub_platform.go`（907 行）

- 现状证据：
  - 结构与 `services/sql_db/internal/app/hub_platform.go` 高度近似
  - 职责仍包含：service 注册、session token、binding/stats、tool views、persisted state、manifest normalize
- 拆分建议：
  - 第一阶段：在本 service 内拆成 `registry/auth/binding/view/manifest` 五类文件。
  - 第二阶段：与 `sql_db` 做逐函数 diff，如果差异仅在 type alias 和少量字段，可考虑抽成 service 侧共享内部包。
- 关键约束：
  - 不建议一上来就与 Hub 真正的 `hub/internal/app/hub_platform.go` 合并；Hub 与 service 侧平台对象的安全边界并不相同。

### 6.5 ai_doubao

#### `services/ai_doubao/cmd/ai_doubao/main.go`（784 行）

- 现状证据：
  - `main()` 约 442 行
  - 额外包含 `handleASRWS`、`handleLLMWS`、`synthesizeTTS`
  - 同时管理工具入口、媒体 WS、Hub 注册与生命周期别名
- 拆分建议：
  - `bootstrap.go`
  - `tool_exec_handler.go`
  - `tool_ws_handler.go`
  - `media_handlers.go`：ASR/LLM/TTS 专用入口
  - `supervisor_register.go`
  - `response_helpers.go`
- 关键约束：
  - 媒体协议入口与 Hub 工具入口不要继续混在同文件，否则改一个协议会拖着整个 service 启动文件一起变。

#### `services/ai_doubao/internal/app/asr.go`（593 行）

- 现状证据：
  - `Run` 约 176 行
  - 协议处理：`buildASRClientFrame`、`parseASRServerFrame`
  - 载荷解析：`parseASRPayload`、`extractASRText`、`hasDefiniteUtterance`
  - 连接准备：`prepareDialTargets`、`buildASRHeaders`、`buildStartPayload`
- 拆分建议：
  - `asr_runtime.go`：`DoubaoASRClient`、`Run`、关闭逻辑
  - `asr_protocol.go`：帧编解码、gzip
  - `asr_payload.go`：ASR payload 解析与文本提取
  - `asr_config.go`：dial target、header、start payload
- 关键约束：
  - 网络时序与协议格式要分开；协议解析应尽量变成纯函数，以便后续补测试。

#### `services/ai_doubao/internal/app/message_types.go`（667 行）

- 现状证据：与 `chat_server/sql_db` 同名文件完全一致。
- 拆分建议：同 `chat_server/internal/app/message_types.go`。

### 6.6 account

#### `services/account/internal/app/handler.go`（598 行）

- 现状证据：
  - 请求总入口：`HandleTool`
  - 账户操作：`handleRegister/login/logout/me/passwordChange`
  - 生命周期与状态：`handleHealth`、`handleStateGet`、`Initialize`、`handleShutdown`
  - 通用响应与 token 工具：`baseResponse`、`badRequest/...`、`issueToken`
- 拆分建议：
  - `handler_tool_entry.go`：`HandleTool` 与 tool 路由
  - `handler_auth.go`：注册/登录/登出/密码修改/`me`
  - `handler_lifecycle.go`：初始化、健康、状态、关闭
  - `handler_response.go`：统一响应构造
  - `token.go`：密码散列、token 签发、session id
- 关键约束：
  - `HandleTool` 应收敛成路由表或 switch 分发器，不要继续承载业务细节。

### 6.7 sql_db

#### `services/sql_db/cmd/sql_db/main.go`（788 行）

- 现状证据：
  - `main()` 约 542 行
  - 结构与 `file_storage` 高度相似：启动装配、tool exec、生命周期、Hub 注册、heartbeat、辅助函数
- 拆分建议：
  - `bootstrap.go`
  - `tool_exec_handler.go`
  - `lifecycle_tools.go`
  - `supervisor_register.go`
  - `response_helpers.go`
- 关键约束：
  - 可与 `file_storage` 对齐目录形态，便于后续横向治理，但先不要强行合并实现。

#### `services/sql_db/internal/app/hub_platform.go`（926 行）

- 现状证据：
  - 与 `file_storage/internal/app/hub_platform.go` 函数序列高度接近
  - 涵盖 persisted state、session token、binding/stats、tool views、manifest normalize
- 拆分建议：
  - 第一阶段：同包内拆成 `registry/auth/binding/view/manifest`
  - 第二阶段：与 `file_storage` 做语义差异核验，若差异稳定且有限，再评估抽公共实现
- 关键约束：
  - 若抽公共包，必须先定义 service 侧平台对象的稳定 API；否则只会把耦合从文件内挪到包间。

#### `services/sql_db/internal/app/message_types.go`（667 行）

- 现状证据：与 `chat_server/ai_doubao` 同名文件完全一致。
- 拆分建议：同 `chat_server/internal/app/message_types.go`。

## 7. 建议的执行优先级

### P0：先处理最危险的入口与状态机

1. `services/chat_server/internal/app/session.go`
2. `services/surface_manager/cmd/surface_manager/main.go`
3. `hub/internal/gateway/tool_handler.go`
4. `hub/internal/app/hub_platform.go`

### P1：再处理治理与存储聚合文件

1. `hub/internal/gateway/admin_service_tools.go`
2. `hub/internal/supervisor/lifecycle.go`
3. `services/chat_server/internal/app/hub_database_store.go`
4. `services/surface_manager/internal/app/hub_store.go`
5. `services/ai_doubao/internal/app/asr.go`

### P2：最后处理可模板化、可复用的重复簇

1. `services/chat_server/internal/app/message_types.go`
2. `services/ai_doubao/internal/app/message_types.go`
3. `services/sql_db/internal/app/message_types.go`
4. `services/file_storage/internal/app/hub_platform.go`
5. `services/sql_db/internal/app/hub_platform.go`
6. 各 service `cmd/*/main.go`

## 8. 推荐实施步骤

1. 先在目标目录内建立新的同包文件，不改 package，不改对外 API。
2. 第一轮只迁移纯函数、结构体定义、模板文本和响应辅助函数。
3. 第二轮迁移仓储读写与 HTTP handler。
4. 最后一轮拆状态机、并发控制和生命周期流程。
5. 每拆完一个文件，至少补三类验证：`go test`、编译验证、关键 handler/流程的最小烟测。

## 9. 风险与控制

- 风险：拆分后出现循环依赖。
  - 控制：所有新文件维持同 package；只有确认稳定后才升级为新 package。
- 风险：状态锁与共享字段被分散，造成并发错误。
  - 控制：先定义“状态所有权表”，明确每组字段由哪个文件负责。
- 风险：重复代码过早抽公共，反而扩大跨项目耦合。
  - 控制：先本地拆分，再做跨服务差异比对。
- 风险：`main.go` 被拆成很多文件但逻辑仍是巨型闭包。
  - 控制：必须同步把长 handler 继续抽成私有函数，不接受“只搬文件不降复杂度”。

## 10. 本文结论的可追溯点

- 行数阈值：以实时扫描结果为准，阈值为 `> 500` 行。
- 最长函数热点：
  - `services/surface_manager/cmd/surface_manager/main.go:24` 的 `main()` 约 1021 行
  - `services/chat_server/internal/app/session.go:1091` 的 `handleActionResult()` 约 194 行
  - `hub/internal/gateway/tool_handler.go:102` 的 `HandleCall()` 约 195 行
- 完全重复簇：`services/chat_server/internal/app/message_types.go`、`services/ai_doubao/internal/app/message_types.go`、`services/sql_db/internal/app/message_types.go`

