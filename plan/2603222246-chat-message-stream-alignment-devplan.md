# Chat Message Stream Alignment 开发计划

## 0. 维护信息

- 文档类型：开发计划 / 低风险结构升级方案
- 创建时间：2026-03-22 22:46:02 CST
- 目标范围：
  - `webui/page/chat/*`
  - `services/chat_server/internal/app/*`
- 依据：
  - `doc/_instruction.md`
  - `doc/_instruction/structure.md`
  - `doc/_page-surface.md`
  - `webui/page/chat/components/chat-store.js`
  - `webui/page/chat/lib/event-router.js`
  - `webui/page/chat/lib/session-controller.js`
  - `webui/page/chat/lib/action-engine.js`
  - `webui/page/chat/components/surface-bridge.js`
  - `services/chat_server/internal/app/session_state.go`
  - `services/chat_server/internal/app/session_history.go`
  - `services/chat_server/internal/app/session_turns.go`
  - `services/chat_server/internal/app/session_actions.go`
  - `services/chat_server/internal/app/message_types_model.go`
  - `services/chat_server/internal/app/message_types_build.go`
  - `services/chat_server/internal/app/message_types_render.go`

## 1. 目标结论

本次改造不追求重写整套消息系统，只解决当前最影响一致性的四个问题：

1. `surface_registry_sync` / `surface_active_change` 必须和 `surface_runtime_context` / 生命周期消息一样，进入实时前端消息流。
2. 前端允许保留临时消息，但后端正式入库后，应尽量回填或替换前端临时消息，减少 `show more` 下与数据库视图的非必要差异。
3. observer 消息必须强制携带统一来源信息，至少能明确说明“来自哪个 surface”或“来自 page/chat 本身”。
4. assistant 与 observer 只统一到“都拥有 `say` 作为气泡主展示文本”；observer 额外细节继续放在结构化 body 内，不反向要求 assistant 承担不必要字段。

本次方案的核心原则：

1. 以后端标准消息为准，前端临时消息只做占位。
2. 不做“大统一消息框架重写”，只在当前模型上补齐最小统一外壳。
3. `show more` 模式优先接近数据库真实消息视图。
4. 可以接受局部不完美，但消息链路必须逻辑自洽、可解释、可验证。

## 2. 当前真实问题

## 2.1 实时链路不完整

当前服务端只有两类 observer 会实时发 `message_append`：

1. `surface_runtime_context`，且 `reason=runtime_actions_change`、surface 处于打开状态。
2. `surface_open / surface_close / surface_closed`。

这导致：

1. `surface_registry_sync`
2. `surface_active_change`

虽然会入库，但当前会话里默认不会实时出现在消息流中，只能靠后续 `fetch_history` 补看。

## 2.2 前端临时消息和数据库消息存在双轨

当前前端消息列表来源有三套：

1. `addChatMsg(...)` 实时插入的本地消息。
2. `appendStoredMessage(...)` 处理 `message_append` 的正式消息。
3. `handleHistorySync(...)` 处理历史回放的正式消息。

问题不在于“双轨存在”，而在于双轨之间缺少统一对齐机制：

1. 临时 assistant / observer 消息大多没有稳定键，无法在正式消息到达时回填。
2. 某些 observer 先被前端本地插入，之后服务端又以正式消息入库，但前端没有进行合并。
3. `show more` 下用户看到的是“临时对象 + 正式对象”混合流，不是数据库真实流的近似镜像。

## 2.3 observer 来源信息不统一

当前 observer 语义主要依赖：

1. `role=observer`
2. `category`
3. `message_type`
4. `payload_json`
5. `action_json`

但“来源是谁”没有被强制统一表达。

实际后果：

1. 有些消息能从 `surface_id` 推出来。
2. 有些 page 级 observer 只有 `active_surface_id` 或 registry。
3. 前端本地 observer 渲染时也未统一把来源显式展示给用户或 AI。

这会让 observer 在 `show more` 和 AI 上下文里都不够自解释。

## 2.4 assistant 与 observer 展示外壳不统一

当前 assistant 天然有 `say`；
observer 正式消息的核心可读文本主要落在 `content`，前端历史加载时会把 `say || content` 作为主文本展示。

这意味着“展示层”其实已经半统一，但“编码约束”还不统一：

1. assistant 是以 `say` 为中心构建的。
2. observer 往往是先有结构化 payload，再由服务端 render 出 `content`。
3. 前端临时 observer 有时甚至没有稳定主文本，只靠 `actionJSON` 和 debug 区显示。

## 3. 改造原则

## 3.1 只做必要统一

统一目标只到这一层：

1. 所有正式展示消息都应有明确的主展示文本。
2. 主展示文本统一映射到 `say`。
3. assistant 保持现有简单结构，不新增重字段。
4. observer 保持结构化 payload，但同时保证 `say` 能说明“谁-发生了什么”。

不做的事：

1. 不强行让 assistant 拥有 observer 那套复杂来源/状态结构。
2. 不重写数据库 schema。
3. 不一次性消灭所有前端临时消息。

## 3.2 后端标准消息优先

正式消息的唯一权威来源应是服务端 `BuildMessage(...)` 之后的 `ChatMessage`。

前端的目标是：

1. 尽量等待或接纳正式消息。
2. 必要时先插临时消息。
3. 正式消息到达后尽可能升级、回填、替换临时消息，而不是长期并存。

## 3.3 observer 必须自解释

observer 最低要求：

1. `say` 一眼能看懂“谁-发生了什么”。
2. 结构化 payload 保留细节。
3. 来源字段标准化，不能依赖调用方各自随意拼。

## 4. 目标方案

## 4.1 补齐 surface context 的实时链路

调整服务端实时 observer 下发策略：

1. `surface_registry_sync` 实时发 `message_append`
2. `surface_active_change` 实时发 `message_append`
3. 保留现有 `surface_runtime_context` 的实时链路
4. 保留现有 `surface_open / surface_close / surface_closed` 的实时链路

说明：

1. `state_change` 是否全部实时，保持谨慎，不在本次计划里强制扩大。
2. 本次只补齐用户已明确要求、且语义最稳定的 page/surface context 消息。

## 4.2 为 observer 建立统一来源字段

在 observer payload 层新增最小统一来源外壳，建议统一约定为：

```json
{
  "source_kind": "surface|page|system",
  "source_id": "gomoku|page/chat|...",
  "source_label": "Gomoku|Chat Page|..."
}
```

落地要求：

1. surface 相关 observer 必须带 `source_kind=surface`
2. 若能定位具体 surface，必须带真实 `surface_id`
3. page 级 context 消息统一使用 `source_kind=page`、`source_id=page/chat`
4. 渲染 `say` 时优先使用 `source_label`

这样 observer 至少能稳定表达：

1. 这是谁发出来的
2. 是具体 surface，还是 page/chat 宿主级消息

## 4.3 统一 observer 的主展示文本到 `say`

本次不改“结构化 payload 为主”的事实，只补充一条规则：

1. 所有正式 observer 消息在服务端构建时，都必须产出 `say`
2. `say` 只负责主气泡文案，要求简洁、可读、能表达“谁-发生了什么”
3. 复杂细节仍放在 `payload_json` / `action_json` / `raw_data`

建议编码准则：

1. `say` 不追求完整还原 payload
2. `say` 不追求机器可逆
3. `say` 只作为主展示文本和 prompt 的轻语义入口

例如：

1. `Gomoku 已打开`
2. `Gomoku 注册了 4 个可调用动作`
3. `Chat Page 已同步可用 surface 列表（3 个）`
4. `Gomoku 当前状态变更：轮到黑棋`

## 4.4 前端增加“临时消息 -> 正式消息”对齐能力

目标不是通用 reconciliation 引擎，而是覆盖最有价值的场景：

1. assistant 流式消息在正式 assistant 历史消息出现时能被回填或替换
2. observer 临时消息在正式 observer 消息出现时能尽量合并
3. 无法可靠合并时，再退化为追加新消息

建议采用“最小可行匹配键”：

1. assistant：
   - 优先按 `turn_id + role=assistant + 当前 sessionEpoch`
   - 正式消息到达后补齐 `message_id/store_id/say/aside/action/rawData/parseError`
2. observer：
   - 优先按 `role=observer + turn_id + category + message_type + 来源键`
   - 若前端临时消息保存了 payload 指纹，可进一步用 payload 指纹匹配

不建议本次引入复杂全局 diff。

## 4.5 调整前端 observer 插入策略

前端本地直接 `addChatMsg("observer", ...)` 的逻辑应收敛为更保守的两类用途：

1. 明确需要即时反馈、而后端正式消息稍后才会到达的占位场景
2. 只用于当前运行态调试、不承诺是正式历史镜像的场景

对于已经能稳定拿到服务端正式消息的 observer 类型，前端应优先消费正式 `message_append`，减少自拼消息。

本次重点收敛对象：

1. `surface_registry_sync`
2. `surface_active_change`
3. `surface_runtime_context`
4. 生命周期消息

## 4.6 `show more` 的一致性目标

本次不承诺“100% 等于数据库视图”，但至少达到：

1. show more 模式下，surface context 和生命周期消息与数据库同步
2. assistant 最终消息与数据库同步
3. observer 的主文本与来源信息和数据库标准消息一致
4. 剩余仍为前端临时态的消息，数量应显著减少且可解释

## 5. 分步实施

## 5.1 第一阶段：服务端 observer 标准化

目标：

1. 为 observer payload 补统一来源字段
2. 为 observer 正式消息补稳定 `say`
3. 扩大 `message_append` 覆盖到 `surface_registry_sync / surface_active_change`

建议修改点：

1. `services/chat_server/internal/app/session_state.go`
2. `services/chat_server/internal/app/message_types_render.go`
3. `services/chat_server/internal/app/message_types_build.go`
4. 必要时补 `message_types_model.go` 注释或常量说明

验收：

1. 新写入的相关 observer 消息都能看到非空 `say`
2. `surface_registry_sync / surface_active_change` 会实时进入当前前端流
3. observer payload 中都带统一来源字段

## 5.2 第二阶段：前端正式消息优先与回填

目标：

1. `appendStoredMessage(...)` 能优先回填可匹配的临时消息
2. `addChatMsg(...)` 继续保留，但只作为占位机制

建议修改点：

1. `webui/page/chat/components/chat-store.js`
2. `webui/page/chat/lib/event-router.js`

重点实现：

1. 为临时消息增加最小匹配信息
2. 正式消息到达时先尝试“升级现有临时消息”
3. 匹配失败才新增 DOM 节点

验收：

1. assistant 最终消息不再长期出现“临时一条 + 正式一条”双轨
2. surface context observer 在实时进入后，与后续历史拉取不重复

## 5.3 第三阶段：前端 observer 入口收敛

目标：

1. 减少前端自拼 observer 消息
2. 让 page/chat 里的 surface 相关 observer 尽量都走正式链路

建议修改点：

1. `webui/page/chat/lib/event-router.js`
2. `webui/page/chat/lib/action-engine.js`
3. `webui/page/chat/components/surface-bridge.js`

策略：

1. 保留必要的即时本地反馈
2. 对于已有正式回推的类型，优先消费正式消息
3. 避免同一语义在前端和后端各插一条

## 6. 风险与取舍

## 6.1 不追求一次性绝对统一

这是有意取舍。

原因：

1. 现有 chat 流存在流式、控制、历史、observer 多条链路
2. 一次性做“完全统一消息总线”风险过高
3. 用户当前需要的是一致性显著提升，而不是消息系统重写

## 6.2 observer `say` 可能与 payload 摘要不完全一致

这是可接受的。

原则上：

1. `say` 是主展示文本
2. `payload` 是事实原文
3. 若二者冲突，以 payload 为准

## 6.3 page 级 observer 不一定对应单一 surface

例如 `surface_registry_sync` 本身是 page 级消息。

因此本次统一来源字段时，必须接受两类来源：

1. 具体 surface 来源
2. `page/chat` 宿主来源

不要错误地把所有 observer 都强绑到某一个 surface。

## 7. 验收标准

完成后至少满足：

1. 打开 chat 页面后，`surface_registry_sync / surface_active_change` 能实时进入消息流。
2. surface 生命周期与 `surface_runtime_context` 继续实时进入消息流。
3. 这些 observer 在 show more 下都能显示明确的主文本和来源。
4. observer 主文本统一走 `say` 作为 `msg-main` 展示。
5. assistant 不被强制增加复杂 observer 字段。
6. 前端临时消息在正式消息到达后，能够在主要场景下被回填或替换，而不是长期并列。
7. 上拉历史后，不会因为实时 append 与历史回放造成明显重复。

## 8. 建议执行顺序

1. 先改服务端标准消息：来源字段、observer `say`、实时回推范围。
2. 再改前端 `chat-store`：做正式消息回填。
3. 最后收敛前端本地 observer 注入点，减少重复源。

这个顺序最稳妥，因为：

1. 先有稳定正式消息，前端回填才有依据。
2. 若先删前端临时逻辑，容易造成交互空窗。
3. 先服务端、后前端，便于每一步都做可观察验证。
