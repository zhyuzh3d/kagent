# Chat Surface Context 升级开发计划

## 0. 维护信息

- 文档类型：开发计划 / 架构升级方案
- 创建时间：2026-03-22 14:16:03 CST
- 目标范围：
  - `webui/page/chat/*`
  - `webui/lib/pageSurfaceTool.js`
  - `services/chat_server/internal/app/*`
  - `services/ai_doubao/internal/app/llm.go`
- 依据：
  - `doc/_instruction.md`
  - `doc/_instruction/core.md`
  - `doc/_page-surface.md`
  - `webui/page/chat/surface-bridge.js`
  - `webui/page/chat/index.html`
  - `webui/page/chat/action-engine.js`
  - `webui/page/chat/session-controller.js`
  - `webui/page/chat/io-worker.js`
  - `services/chat_server/internal/app/session_state.go`
  - `services/chat_server/internal/app/session_actions.go`
  - `services/chat_server/internal/app/session_continuation.go`
  - `services/chat_server/internal/app/pipeline_turn.go`
  - `services/ai_doubao/internal/app/llm.go`

## 1. 目标结论

本次升级的目标不是在旧链路上打补丁，而是为 `page/chat` 建立一套更严谨的 `chat + surface` 协作机制，使其同时满足以下事实：

1. AI 在任何时刻都能知道当前页面可用的 surface 列表。
2. AI 在任何时刻都能知道当前页面已打开且当前激活的 surface，以及该 surface 当前可调用的 runtime actions。
3. surface 的状态变化会持续写入消息流，即使用户尚未开始语音对话也一样成立。
4. `surface` 调用 `host action call_ai_reply` 后，只要 chat 会话已经启动，就能触发一轮 AI continuation。
5. continuation 产出的 JSON envelope 中若带 `action`，该 action 必须可以执行。
6. 上述能力不能破坏 `page/chat` 现有“用户发言 -> AI 回复”的主链路，也不能让 observer 噪声淹没 LLM 上下文。

硬边界：

1. **绝不支持**“未 start 也能被 `call_ai_reply` 拉起 AI 回复”。
2. 本次方案以升级后的严格语义为准，**不为旧兼容保留双轨逻辑**。
3. `page` 级 surface 上下文与 `surface` 内部业务状态必须分层，不能混写成同一种消息语义。

## 2. 当前实现审查

## 2.1 已具备的基础能力

当前代码已经具备以下能力：

1. `surface` 可通过 `host action call_ai_reply` 请求 `page/chat` 显式触发一轮 AI 回复。
2. `page/chat` 已可把真实 `surface state_change` 通过 `state_change` control 写入服务端会话历史。
3. continuation 已被改造成显式触发模式，不会因 observer 消息自动发言。
4. AI 回复中的 `action` 已可经 `action-engine -> surfaceBridge.dispatchAction()` 执行。

这说明主链路的协议骨架已存在，不需要推翻重写。

## 2.2 关键缺口

当前仍存在以下结构性缺口：

1. AI 看不到页面级 surface 上下文。
说明：
当前 `surfaceBridge.emitStateChange()` 只用于刷新页面下拉 UI，不进入会话消息流；因此 AI 不会主动知道可用 surface 列表、当前激活 surface、当前已打开 surface 的 runtime actions。

2. surface 事件在“页面刚载入但 ws 尚未 ready”阶段可能丢失。
说明：
`page/chat` 会在页面初始化时预加载 surface catalog，但 `sessionController` 的 websocket 连接晚于部分 surface 逻辑建立；`io-worker.js` 在 `wsReady()` 之前会直接丢弃 control 消息，因此“对话未开始也写消息流”的要求当前并不可靠。

3. observer 消息目前是“全量历史 + 全量投喂”。
说明：
服务端 `shouldIncludeInLLMHistory()` 对 observer surface/action 报告基本都允许进入 LLM 历史；如果后续把 surface registry、active surface、持续 state_change 都写入消息流而不做投影，模型上下文会被高频 observer 信息快速淹没。

4. system prompt 仍假设“打开 surface 前必须先调 `get_surfaces`”。
说明：
当前 prompt 没有把“页面已知的 surface registry / active surface”当成正式上下文，因此即使 page 已经知道所有 surface，模型也无法利用这份知识，只能机械地再调用 `get_surfaces`。

5. page 级状态与 surface 内部状态尚未分层建模。
说明：
“可用 surface 列表”“当前打开/激活的 surface”“浮动/停靠模式”都属于 `page` 的 workspace/context 事实，不属于某一个 surface 的 `business_state`。如果继续塞进现有 `state_change(surface_id, business_state)`，语义会持续混乱。

## 3. 升级设计原则

## 3.1 分层原则

必须明确拆分三类事实：

1. `page surface context`
内容：
可用 surface registry、当前激活 surface、当前打开 surface、当前打开 surface 的 runtime 注册信息与 actions 摘要。

2. `surface runtime state`
内容：
具体 surface 自己上报的 `business_state / visible_text / state_version / status`。

3. `chat conversation state`
内容：
用户消息、AI 回复、AI action call、action report、continuation 控制。

三者都可以进入消息流，但必须使用不同的消息类型和不同的 LLM 投影策略。

## 3.2 存储全量，投喂摘要

消息流应保存真实发生的全部 page/surface 事件，但 LLM 输入不能直接复用存储历史，而应在发送给模型前做投影：

1. 存储层追求可追溯。
2. Prompt 层追求低噪声、低歧义、强语义。

## 3.3 显式触发优先

observer 事件永远只负责“进入历史”和“更新上下文”，不负责自动触发 AI 发言。

只有两种情况允许启动模型：

1. 用户显式 `trigger_llm`
2. surface 显式 `call_ai_reply`

除此之外，任何 registry/state/action report 更新都不能自动启动回复。

## 3.4 不为旧兼容保留过渡层

本次直接建立新的 page 级 surface context 语义和 LLM history 投影语义，不额外保留“旧 observer 裸消息即最终 prompt 事实源”的兼容层。

## 4. 目标方案

## 4.1 新的 page 级 surface context 模型

新增一个由 `page/chat` 维护的权威快照模型，例如：

```json
{
  "context_version": 12,
  "updated_at_ms": 1760000000000,
  "reason": "catalog_refresh|active_surface_change|runtime_ready|runtime_closed|runtime_actions_change",
  "registry": [
    {
      "surface_id": "gomoku",
      "name": "Gomoku",
      "surface_type": "buildin",
      "version": "1.0",
      "desc": "..."
    }
  ],
  "active_surface_id": "gomoku",
  "open_surfaces": [
    {
      "surface_id": "gomoku",
      "title": "Gomoku",
      "ready": true,
      "mode": "floating|docked",
      "actions": [
        { "name": "get_state", "description": "..." }
      ]
    }
  ]
}
```

设计要求：

1. `registry` 是 page 层看到的所有可用 surface 列表。
2. `active_surface_id` 是下拉菜单当前选中的 surface。
3. `open_surfaces` 只描述当前已打开 runtime 的 surface。
4. `open_surfaces.actions` 必须来自 runtime register 后的真实动作，而不是静态 manifest 猜测。
5. `mode` 只描述 page 工作区状态，不写入某个 surface 的 `business_state`。

## 4.2 新的消息类型设计

服务端新增 page 级 surface context 消息类型，不复用现有 `state_change`：

1. `surface_registry_sync`
用途：
记录 page 当前可用 surface 列表快照。

2. `surface_active_change`
用途：
记录 page 当前激活的 surface 变化。

3. `surface_runtime_context`
用途：
记录当前已打开 surface runtime 的标题、ready 状态、actions 摘要、workspace mode 等 page 视角事实。

保留现有真实 `surface state_change`，但语义只表示 surface 自己的业务状态变化。

这组消息都使用 `RoleObserver`，但分类应新增独立 `CategorySurfaceContext`，不要继续混入 `CategorySurface`。

## 4.3 page -> session 的可靠投递机制

为满足“对话未开始也要写入消息流”，必须新增页面级可靠投递机制：

1. `sessionController` 增加 control outbox。
2. worker / websocket 未 ready 时，surface context 和 state control 先进入本地队列，不直接丢弃。
3. `ws_open` 后自动 flush 队列。
4. project/thread 切换重连后，page 必须重新发送当前完整 surface context 快照。
5. 当前已打开 surface 的最新 runtime state 也要在重连后做一次 resync。

这样才能保证：

1. 页面刚打开时的 registry 快照不会丢。
2. 未 start 但已打开 surface 的状态事件仍会进会话历史。
3. reconnect 后新 session 能恢复当前 page surface 上下文。

## 4.4 LLM 历史投影器

在服务端 LLM 输入构建前新增专用投影层，不再把 observer 历史原样喂给模型。

目标投影规则：

1. 全量保留最近的 user / assistant 消息。
2. 只保留最新一条 `surface_registry_sync`。
3. 只保留最新一条 `surface_active_change`。
4. 每个已知 surface 只保留最新一条 `surface_runtime_context`。
5. 每个已知 surface 只保留最新一条 `surface state_change`。
6. 最近一次 action report 保留。
7. `action_call / action_execute` 继续不进入 LLM 历史。

这样模型在任何一轮都能看到：

1. 当前 page 能打开哪些 surface。
2. 当前选中的是哪个 surface。
3. 当前打开 surface 的 runtime actions 是什么。
4. 当前 surface 的最新业务状态是什么。

同时不会被整个 observer 时间线淹没。

## 4.5 system prompt 升级

`chatSurfaceActionPromptSuffix` 需要同步升级，核心约束改为：

1. 优先依据“当前 page 已知的 surface context”推理。
2. 只有当 context 缺失、过期或目标不明确时，才调用 `get_surfaces`。
3. 若目标 surface 已打开且 actions 已知，可直接产生 `surface.call.<surface_id>.<action_name>`。
4. 若目标 surface 未打开但 registry 中存在，可直接调用 `open_surface(target)`。
5. `call_ai_reply` 触发的 continuation 与普通用户 turn 使用同一 envelope/action 规则。

额外约束：

1. AI 每轮最多产生一个 `action`。
2. action 执行结果不会自动触发下一轮 AI 回复。
3. 若动作结果仍需进一步解释，必须等待新的显式触发：
   - 用户继续说话
   - 或 surface 再次调用 `call_ai_reply`

这样可保持确定性，避免 observer/action report 递归连锁触发。

## 4.6 continuation 的严格启动边界

明确保留以下硬规则：

1. `call_ai_reply` 只有在 `session.started == true` 时才允许成功。
2. 若会话未 start，host action 返回 `{requested:false, reason:"session_not_running"}`。
3. page 仍应记录 surface 上下文和状态变化，但不得自动拉起模型。

这是本次方案必须坚持的边界，不允许后续实现中被弱化。

## 5. 实施分期

## 5.1 第一阶段：page 级 surface context 正式建模

目标：

1. 在 `page/chat` 内形成权威 `surface context snapshot`。
2. `surfaceBridge` 可导出完整快照。
3. registry、active surface、runtime actions、workspace mode 的变化都有明确 reason。

计划修改点：

1. `webui/page/chat/surface-bridge.js`
2. `webui/page/chat/index.html`
3. 必要时增加 `webui/page/chat/surface-context.js`

完成标准：

1. page 内可随时获取一份完整 surface context 快照。
2. 这份快照不依赖聊天会话是否 start。

## 5.2 第二阶段：可靠投递与重连重放

目标：

1. 所有 surface context / state control 在 ws 未 ready 时不丢失。
2. reconnect 后自动全量重放当前上下文。

计划修改点：

1. `webui/page/chat/session-controller.js`
2. `webui/page/chat/io-worker.js`
3. `webui/page/chat/index.html`

完成标准：

1. 页面启动前期的 surface registry 不丢。
2. 未 start 时打开/切换 surface，历史中仍可看到对应 observer 记录。
3. thread/project 切换后，新会话立即拥有当前 page surface context。

## 5.3 第三阶段：服务端消息模型重构

目标：

1. 引入 `CategorySurfaceContext` 及新的 message types。
2. page 级 context 与 surface 自身 state 严格分层。

计划修改点：

1. `services/chat_server/internal/app/message_types_model.go`
2. `services/chat_server/internal/app/message_types_render.go`
3. `services/chat_server/internal/app/session_state.go`
4. 视需要新增 `services/chat_server/internal/app/session_surface_context.go`

完成标准：

1. `surface_registry_sync`
2. `surface_active_change`
3. `surface_runtime_context`
4. `surface state_change`

这四类消息都能被独立落库、独立渲染、独立投影。

## 5.4 第四阶段：LLM history 投影器

目标：

1. LLM 不再直接吃原始 observer 历史。
2. 只吃压缩后的最新 surface context 与关键状态。

计划修改点：

1. `services/ai_doubao/internal/app/llm.go`
2. 视需要新增 `services/chat_server/internal/app/history_projection_*.go`

完成标准：

1. 同一 surface 多次 state_change 只向 LLM 提供最后一条。
2. registry / active surface 始终以最新快照进入 prompt。
3. user / assistant 对话消息保持原有语义不变。

## 5.5 第五阶段：Prompt 与 action 策略升级

目标：

1. 模型学会优先使用 page 已知 surface context。
2. action 生成与执行逻辑保持单轮、确定、可验证。

计划修改点：

1. `services/ai_doubao/internal/app/llm.go`
2. `webui/page/chat/action-engine.js`

完成标准：

1. 当前已知 surface 存在时，AI 不会机械重复 `get_surfaces`。
2. 已打开 surface 且 action 已知时，AI 可直接发 `surface.call.<id>.<action>`。
3. continuation 产生的 action 能正常执行。

## 6. 验收标准

功能验收必须覆盖以下场景：

1. 页面载入后未 start，对话列表默认不显示 observer，但 `show more` 可看到首条 `surface_registry_sync`。
2. 未 start 时切换 surface，下拉变化会写入 `surface_active_change`。
3. 未 start 时 surface 自己持续上报状态，消息流中能看到对应记录。
4. 未 start 时 surface 调 `call_ai_reply`，不会触发 AI 回复，并返回明确失败原因。
5. start 后，surface 调 `call_ai_reply`，会触发 continuation。
6. continuation 能读到当前 registry、active surface、当前打开 surface 的 actions 摘要。
7. continuation 产出带 `action` 的 envelope 时，action 能执行。
8. action report 不会自动递归触发下一轮 AI 回复。
9. reconnect / thread 切换后，当前 page surface context 会重新进入新会话。
10. 正常用户说话 -> AI 回复链路不退化。

## 7. 验证计划

必须至少包含以下验证：

1. 前端模块级导入检查：
   - `surface-bridge.js`
   - `session-controller.js`
   - `chat-store.js`
   - 新增的 surface context 模块
2. 服务端单元测试：
   - surface context message build/render
   - LLM history projection
   - `call_ai_reply` 在 started / not-started 两种状态下的边界
3. 端到端手工回归：
   - 页面加载但未 start
   - start 后 surface 主动触发 AI
   - continuation + action 执行
   - reconnect 后上下文重建

## 8. 风险与控制

主要风险：

1. observer 事件量上升导致数据库与内存历史增长更快。
控制：
投影层压缩进入 LLM 的 observer；必要时再单独为 `CategorySurfaceContext` 增加更激进的历史保留策略。

2. reconnect 重放可能产生重复 context 消息。
控制：
page 级 context payload 必须携带 `context_version` 或稳定哈希，服务端在写入前做幂等判定。

3. runtime actions 变化与 active surface 切换可能短时乱序。
控制：
所有 page context 消息都带 `updated_at_ms + context_version + reason`，服务端投影时按最新版本覆盖。

4. 模型可能同时看到“registry 快照”和旧的 `get_surfaces` action report。
控制：
Prompt 明确声明“优先使用最新 page context”；投影器只保留最新 registry 快照与最近 action report，避免多份同义上下文并存。

## 9. 本计划的实施裁剪结论

本次升级明确不做以下事情：

1. 不支持未 `start` 的 `call_ai_reply` 拉起 AI。
2. 不保留“旧 observer 裸历史直接作为 prompt 事实源”的兼容模式。
3. 不把 page 工作区状态塞进某个 surface 的 `business_state`。
4. 不让 action report 自动递归触发新的 AI 回复。

## 10. 执行顺序建议

建议严格按以下顺序开发：

1. page context 建模
2. 可靠投递与重放
3. 服务端新消息类型与写库
4. LLM history 投影
5. system prompt 升级
6. 回归验证

原因：

1. 先建模，才能稳定定义消息。
2. 先保证可靠送达，后端写库与投影才有意义。
3. 先有投影，再改 prompt，才能避免模型读到一堆未整理 observer 噪声。
