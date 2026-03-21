# Page-Surface 目标规范对齐开发计划

## 0. 维护信息

- 文档类型：开发计划 / 差距分析
- 创建时间：2026-03-22 00:54:30 CST
- 目标文档：`doc/_page-surface.md`
- 分析范围：
  - `webui/page/chat/*`
  - `webui/page/surface/index.html` 及 `components/*`
  - `webui/page/surface/admin.html` / `admin.js`
  - `webui/page/surface/main.js` 及 `lib/*`
  - `webui/surface/buildin/*`
  - `hub/internal/app/identity.go`
  - `services/surface_manager/*`
  - `services/sql_db/*`
  - `services/file_storage/*`
- 结论性质：本文只列“为了完全符合 `doc/_page-surface.md` 还必须补齐的开发改进”，不复述 surface 内部业务实现自由度

## 1. 目标结论

目标规范要求建立一个统一的宿主-工作区契约，使：

1. 任意主页面都能以统一机制加载、连接、控制、观察、关闭任意自治 `surface runtime`
2. 任意合规 `surface` 都能以正式 `surface caller` 身份调用 Hub tools 和持久化能力
3. `page` 与 `surface` 之间只通过统一运行协议协作，不再依赖页面私有事件名、散装桥接逻辑和临时能力通道

基于当前代码，距离该目标还存在结构性缺口。核心判断如下：

1. Hub 侧 `surface caller` 身份链路尚未落地完成，仍停留在 `X-Surface-Token` 占位识别阶段
2. `page/chat`、`page/surface(index)`、`page/surface/main.js` 目前并不存在统一宿主运行时，而是多套半兼容实现并存
3. `surface` 运行协议尚未收敛到目标文档要求的 `connect -> register -> register_ack -> ready` 四阶段
4. `surface` 对平台工具访问仍混有 `surfacefs_request` 旧机制，未正式收敛到 `surface caller + Hub tools`
5. 目前缺少统一 `surfaceTool.js` / `pageSurfaceTool.js` 顶层公共库，导致每个 page / surface 都在各写一套桥接

## 2. 现状证据

### 2.1 Hub 身份链路

- `hub/internal/app/identity.go` 对 `X-Surface-Token` 的处理仍是占位逻辑，只把 caller 设为 `surf_placeholder`，没有真实验签，也没有恢复 `user_id` / `surface_id`
- `hub/internal/app/identity_test.go` 目前对应测试也是 placeholder 语义
- `pkg/toolproto/v1.go`、`pkg/hubsvc/session.go`、`hub/internal/security/headers.go` 已经支持 `caller.type=surface`、`X-Caller-Surface-Id`、`origin_caller` 等结构
- `services/sql_db/cmd/sql_db/bootstrap_runtime.go` 与 `services/file_storage/cmd/file_storage/tool_http_handler.go` 已经支持 `surface` scope，但要求 caller 中真实存在 `user_id + surface_id`

结论：底层 scope 能力已具备，Hub 入口认证尚未补齐，导致 `surface caller` 仍不是正式可用能力。

### 2.2 `page/chat` 现状

- `webui/page/chat/surface-bridge.js` 是当前最完整的宿主桥接，但仍只实现 `surface_connect` + `surface_ready`
- 同文件仍在消费 `surface_register_actions`、`surfacefs_request`、`host_call`
- `webui/page/chat/action-engine.js` 强绑定 assistant 语义，包含 `get_surfaces/open_surface/close_surface/surface.call.*` 等对话特化动作，不适合作为通用宿主层
- `webui/page/chat/tool-call.js` 只有普通 `/api/tool/call` 包装，没有 `surface caller` 专用工具面封装

结论：`chat` 可以作为高级宿主参考，但不能继续充当 page-surface 统一协议的事实源。

### 2.3 `page/surface(index)` 现状

- `webui/page/surface/components/runtime.js` 目前仍只发送 `surface_connect`，随后等待 `surface_ready`
- 同文件同时接收 `surfacefs_request` / `host_call` 与 `surface_actions`
- `dispatchAction()` 为 fire-and-forget，没有等待器、超时、结果相关性与统一错误模型
- `entry.allowed_host_calls || []` 被用于渲染宿主能力，但 catalog entry 侧并没有正式定义该字段
- `lastSurfaceState` 与窗口状态、业务状态没有严格分层

结论：`page/surface/index.html` 仍是试验宿主，不符合目标规范中的统一 runtime、workspace state、host actions 机制。

### 2.4 `page/surface(admin)` 现状

- `webui/page/surface/admin.js` 仍把 `session_issue` / `capability_issue` 暴露为管理主流程
- 管理界面当前还围绕 package 文件编辑、token 试发、runtime/logs 查询展开，没有协议级校验与宿主/工作区能力对齐检查
- 生成入口 `ui.surface.generate` 仍会生成旧脚手架

结论：`admin` 目前是 package/token 管理台，不是目标规范下的工作区治理与协议对齐台。

### 2.5 旧链路并存

- `webui/page/surface/index.html` 实际加载的是 `components/app.js`
- `webui/page/surface/main.js` 及 `webui/page/surface/lib/*` 仍保留另一套旧 loader / LLM protocol / surface manager 逻辑
- `webui/page/surface/lib/surface-manager.js` 仍读取 `entry.manifest.actions`

结论：`page/surface` 当前同时存在“现行 components 链路”和“遗留 lib 链路”，必须统一，不应继续双轨并存。

### 2.6 当前内置 surface / 生成脚手架现状

- `webui/surface/buildin/counter/index.html` 目前发出 `surface_actions`、`surfacefs_request`、`host_call`、`surface_ready`
- `webui/surface/buildin/task/index.html` 与 `services/surface_manager/internal/app/surface_package.go` 生成脚手架，仍把动作放在 `manifest.actions`，并使用旧 `surface_ready` 结构
- 当前内置 surface 没有统一 `surfaceTool.js`，各自手写 message 协议与工具调用

结论：即便是仓库内“完成度最高”的 surface，也还没有达到目标规范要求。

## 3. 为完全符合指导文档必须补齐的全部技术要求

## 3.1 Hub 与身份层

### 3.1.1 补齐正式 `surface caller` 验证链路

必须实现：

1. Hub 在 `IdentityMiddleware` 中正式验签 `X-Surface-Token`
2. 从 token 恢复 `surface_id`、`user_id`、过期时间、token kind
3. 将 caller 注入为：
   - `caller.type = surface`
   - `caller.user_id = <owner>`
   - `caller.surface_id = <surface>`
4. 透传到 `toolproto.Context` 与 `X-Caller-*` headers
5. 同时保留 `origin_caller` 语义，避免后续 delegation 冲突

否则：

- `sql_db` / `file_storage` 的 surface scope 永远无法形成端到端正式能力
- `surface` 只能继续依赖 page 代理或非正式 header

### 3.1.2 统一 `surface_session_token` 语义

必须明确：

1. `surface_session_token` 是 `surface caller` 身份令牌，不只是临时 iframe 连接票据
2. page 通过 `ui.surface.session_issue` 获取该 token
3. surface 后续调用 Hub tools 时，以此 token 建立正式 caller 身份
4. capability token 若保留，只能是工具平面中某些特定能力的补充授权机制，不能继续主导 page-surface 标准设计

### 3.1.3 增加 Hub 端测试

至少补齐：

1. `X-Surface-Token` 合法、过期、伪造、缺字段测试
2. `surface -> hub -> sql_db`
3. `surface -> hub -> file_storage`
4. `surface + origin_caller` 的边界测试

## 3.2 运行协议层

### 3.2.1 收敛为四阶段协议

所有现行 page / surface 实现必须统一到：

1. `surface_connect`
2. `surface_register`
3. `surface_register_ack`
4. `surface_ready`

必须改动：

1. `surface_ready` 不再同时承担“注册动作”和“ready 完成”两种语义
2. `surface_actions`、`surface_register_actions` 旧事件名全部收敛到 `surface_register`
3. `actions` 统一位于顶层注册载荷，不再允许 `manifest.actions` 作为运行时动作源

### 3.2.2 `surface_register_ack` 由 page 返回宿主能力与关键信息

必须定义并落地：

1. `page_info`
2. `host_actions`
3. `workspace_state`
4. 运行时能力版本或协议版本

其中：

1. `host_actions` 列表只由 page 定义
2. surface 只能请求 page 已声明允许的 `host_actions`
3. 该 ack 是 surface 初始化宿主上下文的正式来源，不应靠额外散装事件补发

### 3.2.3 建立统一消息模型

至少需要统一以下消息：

1. `action_call`
2. `action_result`
3. `state_change`
4. `host_action_call`
5. `host_action_result`
6. `stream_open`
7. `stream_chunk`
8. `stream_end`
9. `stream_error`
10. `surface_log`
11. `surface_close`

需要同步规定：

1. `request_id` / `action_id` / `stream_id` 的关联规则
2. 错误结构
3. 超时语义
4. 关闭语义
5. 失败后的幂等处理

### 3.2.4 建立 action waiter 与超时模型

page 统一运行时必须提供：

1. `callSurfaceAction(surfaceID, action, options)`
2. 等待 `action_result`
3. 超时自动失败
4. runtime 关闭时批量 reject 待完成 action
5. 对流式 action 的单独等待通道

当前 `page/surface/components/runtime.js` 的 fire-and-forget 不满足规范。

## 3.3 `surface actions` 与 `host actions` 体系

### 3.3.1 `surface actions` 描述符标准化

`surface_register.actions` 至少应统一为：

1. `name`
2. `description`
3. `args_schema`
4. `result_schema`
5. `timeout_ms_default`
6. `side_effect`
7. `streaming`

改造要求：

1. 所有内置 surface、脚手架、page runtime 都按同一描述符消费
2. page 只能调用已注册 action
3. page 调用前应做 descriptor 级基本校验

### 3.3.2 `host_actions` 正式替代 `host_call`

必须统一命名：

1. 旧 `host_call` / `host_call_result` 改为 `host_action_call` / `host_action_result`
2. `host_actions` 在 `surface_register_ack` 中由 page 下发
3. 旧 `allowed_host_calls` 命名统一迁移到 `host_actions` 或 `allowed_host_actions`

### 3.3.3 `host_actions` 分类与默认集

需要在通用库和 page 侧统一出最小标准集，例如：

1. 宿主交互类：`flash`、`toast`、`open_link`、`focus_surface`
2. 工作区管理类：`update_workspace_state`、`request_resize`、`request_focus`、`close_surface`
3. 协议辅助类：`get_page_info`、`refresh_host_actions`

注意：

1. 这些分类属于宿主公共能力，不等于限制 surface 的业务模式
2. Hub tools 不属于 `host_actions`，仍归工具平面

## 3.4 工具平面与公共 SDK

### 3.4.1 建立 `webui/lib/surfaceTool.js`

必须抽出统一 surface 侧 SDK，至少提供：

1. `connectRuntime()`
2. `registerSurface()`
3. `markReady()`
4. `emitStateChange()`
5. `emitLog()`
6. `callHostAction()`
7. `callTool()`
8. `closeRuntime()`
9. 流式消息收发辅助

所有内置 surface、AI 生成 surface、手工 surface 都应默认基于该库开发，不再手写协议细节。

### 3.4.2 建立 `webui/lib/pageSurfaceTool.js`

必须抽出统一 page 侧 SDK，至少提供：

1. `loadSurface()`
2. `unloadSurface()`
3. `connectSurfaceRuntime()`
4. `awaitSurfaceRegister()`
5. `ackSurfaceRegister()`
6. `awaitSurfaceReady()`
7. `callSurfaceAction()`
8. `updateWorkspaceState()`
9. `registerHostActions()`
10. `handleStreamFrame()`

目标：

1. `page/chat` 与 `page/surface/index` 共享同一运行时核心
2. 新 page 不再各写一套 iframe / MessageChannel / waiter / timeout 逻辑

### 3.4.3 `surface` 调 Hub tools 的 transport 抽象

规范上应统一为：

1. `surfaceTool.call(toolID, args, options)`
2. 底层 transport 可由实现决定
3. 但语义上必须是“surface 以正式 caller 身份调用 Hub tools”

建议实现优先级：

1. 先支持浏览器直调 `/api/tool/call` + `X-Surface-Token`
2. 若后续 iframe sandbox 或跨源策略有约束，再在 SDK 内补 transport 适配，而不是重新改协议

## 3.5 状态模型

### 3.5.1 明确区分 `runtime state` 与 `workspace state`

必须统一：

1. `runtime state` 由 surface 上报
2. `workspace state` 由 page 持有

`runtime state` 至少包含：

1. `lifecycle_status`
2. `business_state`
3. `visible_text`
4. `state_version`
5. `updated_at_ms`

`lifecycle_status` 至少支持：

1. `starting`
2. `ready`
3. `idle`
4. `busy`
5. `error`
6. `closing`

### 3.5.2 `workspace state` 正式纳入通用运行时

必须统一支持：

1. `open`
2. `focused`
3. `frozen`
4. `minimized`
5. `maximized`
6. `geometry.x`
7. `geometry.y`
8. `geometry.width`
9. `geometry.height`
10. `z_index`

要求：

1. `page/surface/index` 的窗口管理能力要进入通用 page runtime
2. `chat` 若以浮窗方式展示 surface，也必须兼容同一 workspace state 结构
3. workspace state 更新应有统一事件或 host action，而不是 page 内部私有状态

## 3.6 Streaming 机制

### 3.6.1 iframe 与 host 页面之间完全可以做流式传输

技术判断：

1. `postMessage` / `MessagePort` 本身支持多次分块发送消息
2. 因此 page 与 iframe 之间完全可以建立流式协议
3. 真正需要规范化的不是“能不能流”，而是“怎么标识、怎么关联、怎么结束”

### 3.6.2 必须定义统一流式帧协议

建议统一：

1. `stream_open`
2. `stream_chunk`
3. `stream_end`
4. `stream_error`

并规定：

1. `stream_id`
2. `source`
3. `related_action_id`
4. `mime` 或 `content_type`
5. `sequence`
6. `done`

必须落地到：

1. `pageSurfaceTool.js` 的流式接收与重组器
2. `surfaceTool.js` 的流式发送辅助
3. `chat` 与 `surface/index` 的 UI 展示层

## 3.7 `page/chat` 必须做的改造

### 3.7.1 宿主核心下沉到公共库

必须把 `webui/page/chat/surface-bridge.js` 中通用部分迁到 `webui/lib/pageSurfaceTool.js`，包括：

1. catalog 刷新
2. runtime 打开/关闭
3. MessageChannel 建立
4. action waiter
5. state 缓存
6. iframe 生命周期处理

保留在 chat 页的只应是：

1. assistant action 编排
2. chat transcript 观察与展示
3. 对话特化的 surface 调度策略

### 3.7.2 清除 assistant 私有协议对 page-surface 的反向污染

必须避免：

1. `action-engine.js` 的 assistant envelope 成为 surface 基础契约
2. `surface.call.*` / `tool.call.*` 这种对话层 action 命名反向定义 page-surface 运行时

处理原则：

1. chat 可以继续保留这套对话映射
2. 但它只能是 chat 外层适配器，不是 page-surface 标准层

## 3.8 `page/surface/index` 必须做的改造

### 3.8.1 从试验宿主升级为最小标准宿主

必须补齐：

1. 统一公共 runtime 接入
2. `surface_register` / `surface_register_ack` / `surface_ready` 四阶段
3. action 调用等待与错误模型
4. workspace state 展示与编辑
5. host actions 清单展示
6. 流式事件展示

### 3.8.2 对接窗口状态

当前已有拖拽/缩放窗口逻辑，但尚未正式进入协议。必须：

1. 让窗口状态映射到统一 `workspace_state`
2. 支持 page 修改后同步给 surface
3. 支持 surface 通过 `host_action` 请求某些窗口级变化

## 3.9 `page/surface/admin` 必须做的改造

### 3.9.1 从 token/package 管理台升级为治理与对齐台

必须新增或调整：

1. 展示 surface package manifest 与 runtime register 信息的差异
2. 展示 actions、host_actions、runtime state、workspace state、协议版本
3. 对 surface 进行“协议合规检查”
4. 对脚手架生成器给出目标规范版本，而不是旧事件模型

### 3.9.2 弱化 `capability_issue` 的中心地位

因为目标规范下：

1. 工具平面主机制是 `surface caller + Hub tools`
2. `capability_issue` 若保留，应视为某类细粒度能力补充
3. admin 不应继续把 capability token 作为 surface 开发主体验路径

## 3.10 内置 surface 与脚手架必须做的改造

### 3.10.1 更新所有 built-in surfaces

至少包括：

1. `webui/surface/buildin/counter`
2. `webui/surface/buildin/task`
3. 其他内置 surface

统一改造为：

1. 使用 `surfaceTool.js`
2. 使用 `surface_register`
3. 使用顶层 `actions`
4. 使用 `host_action_call`
5. 工具访问走 Hub tools，不再以 `surfacefs_request` 为主

### 3.10.2 更新生成器

`services/surface_manager/internal/app/surface_package.go` 和 `ui.surface.generate` 生成模板必须同步升级，确保新生成 surface 默认就是合规实现，而不是继续制造旧协议资产。

## 3.11 旧资产清理

### 3.11.1 清理或迁移 `webui/page/surface/main.js` 旧链路

必须做二选一：

1. 将其剩余有价值逻辑迁入新公共库
2. 然后删除旧入口和旧 `lib/*` 平行协议实现

原则：

1. 不为旧链路保留长期兼容层
2. 避免 `components/*` 与 `lib/*` 双运行时并存

### 3.11.2 清理旧事件与旧字段

最终应移除：

1. `surface_actions`
2. `surface_register_actions`
3. `host_call`
4. `host_call_result`
5. `surfacefs_request` 作为正式主协议
6. `manifest.actions` 作为运行时动作源

## 3.12 测试与验收

### 3.12.1 自动化验证项

至少需要补齐：

1. Hub `surface caller` 身份测试
2. `pageSurfaceTool.js` 协议状态机测试
3. `surfaceTool.js` 注册/ready/streaming 测试
4. `counter` 作为最小 surface 的端到端测试
5. `page/chat` 与 `page/surface/index` 共享 runtime 核心的回归测试

### 3.12.2 手工验收项

至少验证：

1. 用户登录后，page 打开 surface，surface 可拿到正式 `surface_session_token`
2. surface 通过 Hub tools 成功读写自己的 `user-surface` 数据
3. page 只能调用已注册 action
4. surface 只能调用 page 已允许的 `host_actions`
5. `busy` / `idle` 状态切换可见
6. 窗口位置、大小、冻结、最小化、最大化状态可维护
7. 流式 action 在 iframe 与 host 之间可稳定传输和结束

## 4. 建议实施顺序

### 阶段 A：身份与工具平面打底

先做：

1. Hub 正式验证 `X-Surface-Token`
2. 补齐 surface caller 注入
3. 打通 `surface -> hub -> sql_db/file_storage`

原因：

1. 这是目标规范的根基
2. 没有正式 caller，前端运行时再漂亮也只是壳

### 阶段 B：公共 SDK 与协议落地

再做：

1. `webui/lib/surfaceTool.js`
2. `webui/lib/pageSurfaceTool.js`
3. 四阶段协议
4. streaming 帧协议

原因：

1. 先把公共层收敛，后续页面与 surface 才不会重复返工

### 阶段 C：宿主页迁移

依次迁移：

1. `page/surface/index`
2. `page/chat`

原因：

1. 先把最小宿主做成标准宿主
2. 再把 chat 作为高级宿主接到同一核心上

### 阶段 D：surface 与生成器迁移

再做：

1. built-in surfaces
2. 生成脚手架
3. admin 生成入口

原因：

1. 让后续新增 surface 默认遵循新规范
2. 防止新旧协议资产继续增长

### 阶段 E：治理台与旧资产清理

最后做：

1. admin 合规检查与可视化
2. 删除旧 `main.js/lib` 链路
3. 删除旧事件名与旧字段兼容

## 5. 完成标准

当且仅当以下条件全部成立，才可认为当前仓库“完全符合 `doc/_page-surface.md` 指导文档”：

1. Hub 已能正式识别并验证 `surface caller`
2. `surface` 可直接通过 Hub tools 访问其合法 scope 下的数据与能力
3. 所有主页面统一使用 `pageSurfaceTool.js`
4. 所有内置 surface 与脚手架统一使用 `surfaceTool.js`
5. 运行协议统一为 `surface_connect -> surface_register -> surface_register_ack -> surface_ready`
6. `surface actions` 与 `host actions` 双向机制已正式收敛
7. `runtime state` 与 `workspace state` 已分层
8. streaming 协议已定义并落地
9. `surfacefs_request`、`host_call`、`manifest.actions` 等旧主路径已退出正式体系
10. `page/chat` 与 `page/surface/index` 均已接入同一宿主运行时核心

## 6. 风险与注意事项

1. 不能为了兼容现有 `page/chat` 或 `page/surface` 旧实现而回退目标协议设计
2. 不能把 assistant 对话层 action 命名直接当成 page-surface 基础协议
3. 不能把 capability token 继续当成 surface 工具访问的唯一正式机制
4. 不能把内部 worker / agent 生命周期写进 page-surface 统一规范
5. 必须接受一次性迁移旧事件名与旧结构的成本，否则未来会持续双轨维护

## 7. 本文依据

- `doc/_page-surface.md`
- `hub/internal/app/identity.go`
- `hub/internal/security/headers.go`
- `pkg/toolproto/v1.go`
- `pkg/hubsvc/session.go`
- `services/surface_manager/internal/app/surfacefs.go`
- `services/surface_manager/cmd/surface_manager/tool_http_handler.go`
- `services/sql_db/cmd/sql_db/bootstrap_runtime.go`
- `services/file_storage/cmd/file_storage/tool_http_handler.go`
- `webui/page/chat/action-engine.js`
- `webui/page/chat/surface-bridge.js`
- `webui/page/chat/tool-call.js`
- `webui/page/surface/components/runtime.js`
- `webui/page/surface/admin.js`
- `webui/page/surface/main.js`
- `webui/page/surface/lib/surface-manager.js`
- `webui/surface/buildin/counter/index.html`
- `webui/surface/buildin/task/index.html`
- `services/surface_manager/internal/app/surface_package.go`
