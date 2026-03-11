# 开发计划文档：Observer / Action Report / Surface State / SQLite

- 计划时间：2026-03-10 23:54 CST
- 对应需求：`plan/2026-03-10-233109-observer-action-report-surface-state-sqlite-prd.md`
- 计划文档：`plan/2026-03-10-233109-observer-action-report-surface-state-sqlite-prd-dev-plan.md`
- 适用范围：本地 `localhost` 单机与本地多用户场景

## 1. 计划目标

围绕 PRD 中定义的三条能力线，形成可落地、低回归、分阶段可验证的实施方案：
1. 建立 `surface.get_state + state_change` 的统一状态协议。
2. 建立 `observer/action_report + followup=report` 的统一调度与历史注入机制。
3. 将消息、report、action 记录与 surface 状态迁移到 SQLite，形成多用户统一存储层。

本计划强调：
1. 先统一协议与数据模型，再做自动续跑与持久化迁移。
2. 优先保证现有 `/page/chat` 主链路不回归。
3. 对 PRD 中尚未钉死的技术问题，先明确前置澄清与实现边界。

## 2. 已识别问题与前置澄清

以下问题不是阻塞 PRD 成立，但如果不先澄清，开发过程中极易返工。

### 2.1 多用户身份模型先以 `default user` 落地
现状：
1. 当前项目虽已有 `data/users/default/...` 路径，但运行时并无正式的本地多用户身份、会话切换、用户登录或 user context 注入机制。
2. PRD 已要求 SQLite 以 `user_id` 为一级隔离键。

结论：
1. SQLite 方案仍需从第一天保留 `user_id`。
2. 本阶段实现直接以 `default user` 为唯一运行用户。

建议：
1. 所有数据库表保留 `user_id` 字段。
2. 当前运行时统一注入 `default`。
3. 后续再扩展真正的多用户入口，不阻塞本阶段开发。

### 2.2 `surface.get_state` 的能力协商属于安全增强，不是阻塞项
现状：
1. PRD 要求“每个受管 surface 默认实现 `surface.get_state`”。
2. 现有 surface demo 还没有正式的能力协商层，也没有 manifest 级别的“必备能力校验”。

结论：
1. 即使不做协商，`surface.get_state` 缺失时也可以通过失败 report 回退。
2. 但 capability negotiation 仍建议保留，作为更安全的增强，而不是硬前置。

建议：
1. 第一版可先允许平台统一暴露 `surface.get_state`，缺失时返回标准失败 report。
2. 同时预留 manifest/握手中的 `capabilities.get_state=true/false` 字段。
3. 后续再把协商结果接入 allowed actions 策略，提升安全性与可解释性。

### 2.3 自动续跑缺少“安全触发点”定义
现状：
1. PRD 规定：用户正在说话时，report 入队，等待下一次安全时机。
2. 当前代码里 LLM 触发主要由用户语音 turn 结束驱动，尚无正式“内部 continuation turn”调度入口。

结论：
1. 必须新增内部 continuation scheduler。
2. “下一次安全时机”不能只写成口头规则，必须落到可判断状态。

建议：
1. 最小安全触发条件：
   - 当前无活跃用户输入采集中 turn
   - 当前无未完成的自动 continuation turn
   - 当前会话未停止
2. continuation 必须有独立来源元数据：`origin=action_callback`
3. continuation 进入 LLM 前，应与真实用户消息保持消息边界，不得拼接成用户文本。

### 2.4 Action 超频/超限额时改为统一用户确认
现状：
1. 当前讨论已决定：超频或超限额时，不直接自动 pending 执行。
2. 改为统一弹窗要求用户确认继续或取消。

结论：
1. 不再区分读动作/写动作，统一由限流层管理。
2. 用户确认结果不单独发 report，而写入最终 `action_report` 字段。

建议：
1. `action_report` 增加：
   - `manual_confirm`
   - `block_reason`
2. `block_reason` 最小枚举：
   - `rate_limit`
   - `quota_limit`
3. 若用户取消，则最终 report 标记取消结果。
4. 若用户不操作，则 action 保持阻塞等待，不自动失效。

### 2.5 Report 聚合只在多 action 并发时触发
现状：
1. 讨论已确认：只有多个 action 同时执行时，才触发 1 秒聚合。
2. 单个 action 默认立即 report。

结论：
1. 聚合条件应由“并发 action 数量”驱动，而不是“动作类型”驱动。
2. 多个并发 action 下额外 1 秒延迟可接受。

建议：
1. 当且仅当并发 action 数量大于 1 时，开启 1 秒聚合窗口。
2. 聚合 report 记录多个 action 的结果与最终 surface 状态。
3. 单 action 流程不引入额外聚合延迟。

### 2.6 `observer` 是内部角色，不是对外通用 role
现状：
1. PRD 已说明 `observer` 是内部 canonical role。
2. 主流模型接口不保证支持该角色。

结论：
1. 必须实现 provider adapter。
2. 不能把内部 `observer` 直接透传给外部模型。

建议：
1. 平台内部统一存 `role_internal=observer`。
2. 外发时默认映射为结构化 `assistant/model` 文本消息。
3. provider 适配层作为单独模块实现，而不是散落在各调用点。

## 3. 总体实施策略

采用“三层四阶段”实施：

### 3.1 三层
1. 协议层
   - `surface.get_state`
   - `state_change`
   - `action_report`
   - continuation metadata
2. 调度层
   - action callback
   - report queue
   - continuation scheduler
   - rate limiting / dedupe / manual confirmation
3. 存储层
   - SQLite schema
   - message/action/surface state repository
   - 历史读取与 provider role 映射

### 3.2 四阶段
1. 阶段 A：协议与数据模型定型
2. 阶段 B：chat/surface 状态链路接入
3. 阶段 C：observer/report/continuation 调度落地
4. 阶段 D：SQLite 迁移与历史读取切换

## 4. 详细实施方案

### 阶段 A：协议与数据模型定型

目标：
1. 在不全面改业务逻辑的前提下，先把协议结构钉死。

任务：
1. 定义 surface 握手协议扩展：
   - `surface_id`
   - `capabilities.get_state`
   - `state_version`
2. 定义 `state_change` 事件结构：
   - `surface_id`
   - `event_type`
   - `business_state`
   - `visible_text`
   - `status`
   - `state_version`
   - `updated_at`
3. 定义 `surface.get_state` action 请求/响应结构。
4. 定义 `action_report` 结构：
   - `origin=action_callback`
   - `followup`
   - `result_summary`
   - `effect_summary`
   - `business_state`
   - `manual_confirm`
   - `block_reason`
5. 定义 SQLite 初版 schema 草案。

交付物：
1. 协议常量与结构定义草案。
2. SQLite schema 文档或 migration 草案。

验收：
1. `surface.get_state`、`state_change`、`action_report` 字段集稳定。
2. 多用户 schema 包含 `user_id/chat_id/message_id/action_id/surface_id`。

### 阶段 B：chat / surface 状态链路接入

目标：
1. 让平台真正具备“可读状态”，而不只是“可发动作”。

任务：
1. 改造 `demo-counter` surface：
   - 默认实现 `get_state`
   - 状态变化时发 `state_change`
2. 改造 chat 侧 `surface bridge`：
   - 预留 surface capabilities
   - 维护最新 `surface_state` 缓存
   - 暴露 `surface.get_state` 标准 action
3. 改造 allowed actions 生成逻辑：
   - 第一版允许统一开放 `surface.get_state`
   - 后续可按 capability negotiation 收紧
4. 明确 `result` 与 `effect` 的来源合并规则：
   - 执行器返回值
   - `state_change` 自动上报
   - 必要时再补 `get_state` 校验

交付物：
1. `surface.get_state` 最小闭环。
2. surface 状态缓存层。

验收：
1. 用户问“现在数字是多少”时，LLM 可以通过 `surface.get_state` 间接获得状态。
2. 平台能记录最近一次 `state_change`。

### 阶段 C：Observer / Action Report / Continuation 调度落地

目标：
1. 让 action 完成后的反馈、自动续跑和连续动作成为正式能力。

任务：
1. 引入统一 action callback 模块。
2. 每次 action 完成后生成标准 `action_report`。
3. 实现 `pending report queue`。
4. 实现 continuation scheduler：
   - `origin=action_callback`
   - safe trigger 判定
   - 与用户 turn 解耦
5. 引入 action call rate limit。
6. 引入同 action + 同 args 短时间去重。
7. 引入统一用户确认弹窗机制：
   - block_reason
   - manual_confirm
   - confirm / cancel / waiting
8. 当多个 action 并发时，启用 1 秒 report 聚合。

交付物：
1. observer/report 统一调度层。
2. 自动续跑链路。

验收：
1. `followup=report` 可触发自动续跑。
2. 用户说话期间 report 不丢失，只入队。
3. 连续动作链可运行，且 action 频率限制可控。

### 阶段 D：SQLite 存储迁移

目标：
1. 把消息和 report 从零散 JSON/文件逻辑迁移到统一数据库。

任务：
1. 新增 SQLite 初始化与 migration。
2. 新增 repository：
   - `users`
   - `chats`
   - `messages`
   - `action_calls`
   - `action_reports`
   - `surface_states`
3. 新消息、新 report 双写或直接切换写 SQLite。
4. 改造历史读取链路：
   - 从 SQLite 读取
   - 过滤 `visibility`
   - 合并 `observer/action_report`
   - provider role 映射
5. 保留短期兼容层：
   - 旧文件继续存在但不再作为权威源

交付物：
1. SQLite schema 与 migration。
2. 历史读取仓储层。

验收：
1. 新会话消息与 report 进入 SQLite。
2. `user_id` 隔离真实生效。
3. LLM 历史上下文改从 SQLite 组装。

## 5. 代码落点建议

### 后端
1. `internal/protocol.go`
   - 增加 `state_change` / `surface.get_state` / `action_report` 协议结构
2. `internal/session.go`
   - continuation scheduler
   - report queue
   - 历史注入
   - user confirmation block state
3. `internal/llm.go`
   - provider adapter 输入组装
   - observer -> provider role 映射
4. `internal/`
   - 新增 SQLite repository、migration、schema 管理

### 前端
1. `webui/page/chat/surface-bridge.js`
   - state cache、`get_state`
   - 预留 capabilities 解析
2. `webui/page/chat/action-engine.js`
   - report 上报
   - confirmation 状态处理
3. `webui/surface/*.html`
   - `get_state` 与 `state_change` 实现
4. `webui/page/chat/`
   - 新增或扩展用户确认弹窗 UI

## 6. 验证矩阵

### 6.1 协议验证
1. `surface.get_state` 协议字段稳定可用
2. `state_change` 能正确更新平台缓存

### 6.2 动作验证
1. `surface.get_state` 返回状态后，LLM 可基于 report 回答用户
2. 写动作触发后，`result + effect` 都能进入 report

### 6.3 自动续跑验证
1. `followup=none` 不自动续跑
2. `followup=report` 在 idle 时立即续跑
3. 用户说话时 report 入队，随后续跑
4. 连续动作链不会因空 content 被错误中断

### 6.4 限流与去重验证
1. 超频/超限额 action 触发用户确认弹窗
2. 相同 action + 相同 args 在短窗口内去重
3. 用户确认/取消/未操作三种状态都能正确反映到最终 report

### 6.5 SQLite 验证
1. 新消息写入 `messages`
2. report 写入 `messages + action_reports`
3. surface 最新状态写入 `surface_states`
4. 多用户隔离可通过测试验证

## 7. 风险与缓解

1. 风险：多用户模型先定义不清，SQLite 很快返工。
   - 缓解：先把 `user_id` 固化进 schema 与 repository 接口，即便当前 UI 仍只有 `default` 用户。
2. 风险：continuation scheduler 与现有 turn 生命周期耦合过深。
   - 缓解：新建独立内部调度状态，不直接复用现有用户 turn 语义。
3. 风险：统一用户确认可能让 action 链长时间阻塞。
   - 缓解：在 UI 中明确展示 `waiting for confirmation` 状态，并允许用户手工调高限额参数。
4. 风险：provider 角色适配分散实现导致历史组装混乱。
   - 缓解：集中在单一 adapter 层完成 role 映射。

## 8. 推荐执行顺序

1. 先完成协议与 schema 草案，尤其是 `surface.get_state/state_change/action_report`。
2. 再让 `demo-counter` 跑通状态链路。
3. 再接 observer/report/continuation 调度。
4. 最后切 SQLite 并做历史读取切换。

原因：
1. 没有统一状态协议，后续 report 只能继续靠临时字符串拼接。
2. 没有统一调度层，连续动作能力无法稳定扩展。
3. SQLite 若先落地但消息语义未统一，只会把混乱状态持久化。

## 9. 本次计划结论

该 PRD 的主方向成立，但在正式开发前必须接受以下结论：
1. `surface.get_state` 不是“文案约定”，而是必须落实为 capability + protocol + runtime cache。
   当前阶段 capability negotiation 作为安全增强保留，不作为第一版阻塞条件。
2. 自动续跑不是简单“收到 report 就再调一次 LLM”，而是需要独立 scheduler。
3. SQLite 迁移的真实前提，是先定义最小多用户模型（当前固定 `default`）与统一消息语义。

若按上述顺序推进，该方案具备稳定落地条件；若跳过这些前置澄清直接编码，极大概率在 continuation、multi-user 和 provider 兼容层返工。  
