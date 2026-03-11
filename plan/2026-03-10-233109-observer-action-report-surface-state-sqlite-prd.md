# 功能需求设计文档（PRD）
**主题**：Surface State / Action Report / SQLite Message Store 统一方案  
**日期**：2026-03-10 23:31 CST  
**适用范围**：本地 `localhost` 单机与本地多用户场景  
**信息来源**：当前仓库代码、既有 `surface/action` 计划文档、当前会话讨论结论

---

## 1. 背景

当前项目已经具备以下基础能力：
1. `/page/chat` 已能加载单个 `counter` surface 浮窗，并通过 action 改变其数字。
2. LLM 已可输出 `{content, action}` 结构，前端可解析并执行 action。
3. action 执行结果已初步写入本地文件并追加到会话历史。

但现阶段仍存在三类关键缺口：
1. LLM 只能“发起动作”，还不能稳定、统一地“感知 surface 当前状态”。
2. action 完成后的报告、后续自动续跑、连续动作调度，尚未形成统一机制。
3. 消息与 report 存储仍较零散，尚未形成面向本地多用户的统一 SQLite 数据模型。

本 PRD 的目标，是将上述三件事统一为一套稳定、可扩展、兼容主流模型接口的产品能力。

---

## 2. 目标与非目标

### 2.1 目标
1. 建立统一的 `surface state` 获取机制：对外通过标准 action `surface.get_state` 获取状态，对内通过 `state_change` 事件自动上报状态变化。
2. 建立统一的 `action report` 机制：action 完成后生成标准报告，可进入历史、可触发后续 LLM 调用。
3. 建立统一的自动续跑机制：LLM 可通过 action 参数声明是否在动作完成后继续思考。
4. 建立统一的消息存储模型：所有用户消息、AI消息、observer/report 消息、action 相关记录进入 SQLite。
5. 保持对不同模型接口的兼容：内部语义清晰，外部 role 映射可降级。

### 2.2 非目标
1. 本阶段不要求完成所有 surface 类型的通用 DOM 抽象语言。
2. 本阶段不追求多模型接口的完全统一实现，只定义统一内部语义与适配边界。
3. 本阶段不做远程分布式调度，仍以本地单机、多本地用户为边界。
4. 本阶段不将原始完整 DOM 大规模写入 LLM 上下文。

---

## 3. 统一设计原则

1. **状态优先于 DOM**：LLM 应优先看到业务状态摘要，而不是原始 DOM。
2. **report 是事实，不是聊天文案**：action report 描述真实执行与真实效果，不承担面向用户的修辞。
3. **内部语义与外部接口解耦**：内部使用统一抽象（如 `observer`），对外再映射到具体模型角色。
4. **自动续跑可控但不断言禁止**：系统应允许合理的连续动作，但用频率控制和队列机制约束极端情况。
5. **多用户优先**：消息、report、surface 状态与 action 记录都必须具备用户隔离能力。

---

## 4. 核心概念

### 4.1 Surface State
Surface 当前可被系统理解和读取的状态快照，不等同于原始 DOM。

推荐最小结构：
- `surface_id`
- `state_version`
- `business_state`：如 `count=200`
- `visible_text`：经白名单提取的关键文本
- `status`：如 `ready / frozen / loading / error`
- `updated_at`

### 4.2 surface.get_state
对外统一的标准只读 action。

语义：
1. LLM 或平台在需要确认某个 surface 当前状态时，调用 `surface.get_state`。
2. surface 返回当前业务状态摘要。
3. 该 action 完成后可产生 `action report`，并可按 `followup=report` 触发后续 LLM。

### 4.3 state_change
surface 内部在状态变化后，通过 `postMessage / MessageChannel` 向父层发送标准 `state_change` 事件。

语义：
1. `state_change` 是内部自动上报机制。
2. 它不要求 LLM 显式发起。
3. 它用于更新平台侧已知 surface 状态，并为后续 report 聚合提供依据。

### 4.4 Action Result
action 执行层返回的直接结果，主要描述：
- 成功/失败
- 执行耗时
- 返回值
- 错误信息

### 4.5 Action Effect
action 对界面或业务状态产生的实际影响，主要描述：
- surface 状态变化
- 关键可见文本变化
- 关键业务字段变化

### 4.6 Action Report
由统一回调机制生成的结构化报告，综合 result 与 effect，用于：
- 写入消息历史
- 供下一轮 LLM 理解
- 驱动自动续跑

### 4.7 Observer
内部逻辑角色，表示“系统观察者/环境反馈者”。

说明：
1. `observer` 是**内部 canonical role**。
2. 发送给外部模型接口时，默认映射为 `assistant/model` 风格的结构化自述消息。
3. 未来若某模型接口支持更适合的 tool/result 通道，再按 provider 单独适配。

### 4.8 Followup Policy
action 声明动作完成后是否需要自动触发下一轮 LLM。

推荐最小枚举：
- `none`
- `report`

语义：
1. `none`：动作完成后生成 report，但不自动发起后续 LLM。
2. `report`：动作完成后生成 report；若当前无用户 turn 占用，则立即触发；否则挂起，等待下一次可安全执行的时机。

---

## 5. 功能需求一：Surface State 的统一 get_state / state_change

### 5.1 目标
让 LLM 在需要时，可以可靠获得 surface 当前的真实业务状态与关键可见内容。

### 5.2 state_change 自动上报机制
每个 surface 应具备统一的内部自动上报能力。

必需触发时机：
1. 初始化完成后。
2. action 执行完成且状态发生变化后。
3. 明确的用户交互导致状态变化后。
4. surface 进入异常/冻结/恢复等状态切换时。

`state_change` 最小内容：
- `surface_id`
- `event_type`
- `business_state`
- `visible_text`
- `state_version`
- `updated_at`

### 5.3 `surface.get_state` 标准 action
系统应提供统一的外部读取入口，而不是依赖模型“猜测当前界面状态”。

要求：
1. 每个受管 surface 默认实现 `surface.get_state`。
2. `surface.get_state` 返回业务状态摘要。
3. 对 LLM 来说，`surface.get_state` 是统一可用的 allowed action，不需要每个 surface 单独教会模型新的读取接口。
4. 用户询问“当前某个 surface 显示什么”时，LLM 应优先通过 `surface.get_state` 获取真实状态，再回答。

### 5.4 原始 DOM 处理原则
1. 默认不将完整 DOM 直接送入模型上下文。
2. 原始 DOM 仅可作为开发诊断或受控调试能力，不作为常规上下文输入。
3. 提供业务状态抽象，优先级高于原始 DOM。

### 5.5 与 result/effect 的关系
1. action result 描述“执行器返回了什么”。
2. action effect 描述“surface 最终变成了什么”。
3. effect 可以来源于 `state_change`，也可以由 `surface.get_state` 读取后的结果补充校验。
4. `action report` 应尽量同时携带 result 与 effect。

---

## 6. 功能需求二：Action Followup 与 Action Report

### 6.1 目标
让 LLM 不仅能发起动作，还能：
1. 知道动作结果与效果。
2. 在需要时选择继续自主行动。
3. 在不打扰用户时保持静默执行。

### 6.2 Action Call 扩展字段
建议 action 结构至少支持：
- `id`
- `name`
- `args`
- `followup`

其中：
- `followup=none`
- `followup=report`

### 6.3 全局 Action Callback
系统必须提供统一回调层。

每次 action 完成后，回调层至少完成：
1. 收集执行结果 `result`
2. 收集状态影响 `effect`
3. 生成标准 `action report`
4. 写入消息与 action 存储
5. 根据 `followup` 决定是否发起下一轮 LLM

### 6.4 Action Report 结构
推荐最小字段：
- `report_id`
- `origin=action_callback`
- `user_id`
- `chat_id`
- `turn_id`
- `surface_id`
- `action_id`
- `action_name`
- `followup`
- `status`
- `result_summary`
- `effect_summary`
- `business_state`
- `created_at`

### 6.5 Report 进入 LLM 的方式
内部：
- role = `observer`
- type = `action_report`

对外 provider 适配默认：
- 映射成 `assistant/model` 的结构化自述消息

示例语义：
`[action_report] counter set_count finished; status=ok; current_count=200`

说明：
1. 不依赖中途插入新的 `system` 消息。
2. 不伪装成 `user`。
3. 若未来模型接口支持更适合的 tool/result 通道，可定向升级。

### 6.6 自动续跑调度
当 `followup=report` 时：
1. action 完成后总是先生成 report。
2. 若当前没有用户 turn 运行，可立即发起新的内部 LLM 调用。
3. 若当前用户正在说话或当前用户轮次未结束，则进入 `pending report queue`。
4. 下一次可安全执行时，再把 queued report 送入 LLM。
5. 对 `surface.get_state` 这类只读 action，也适用同一 followup/report 机制。

### 6.7 “用户正在说话”时的策略
本阶段采用更稳的策略：
1. 不要求在用户说话过程中打断用户。
2. action report 入队。
3. 在下一次安全的 LLM 触发点，与新的用户输入一起送入，但 report 仍保持独立消息身份。

### 6.8 连续动作支持
系统必须允许以下模式：
1. 用户要求：依次设为 `100 -> 200 -> 300`
2. LLM 每次 action 完成后看到 report
3. 再决定下一步 action
4. 中间可以保持空 content，不打扰用户

说明：
1. “空 content” 不能作为停止条件。
2. 连续动作是系统正向能力，不应被粗暴禁止。

### 6.9 极端情况防护
建议采用“频率控制优先”的护栏，而不是简单靠固定步数粗暴停止。

必需机制：
1. **Action Call Rate Limit**
   - 对同一 chat/session 限制 action 发起频率
   - 例如：每分钟最多 10 次 action call
   - 超出后进入 pending，而不是直接丢弃
2. **同 action + 同 args 短时间去重**
   - 避免异常自激或重复执行
3. **状态变化判定**
   - 若 action 未引发目标 surface 状态变化，应允许系统据此停止继续尝试

可选机制：
1. `max_auto_steps` 仅作为最终保险丝，不作为主控制手段

### 6.10 多 action report 合并
建议加入 report 聚合机制。

推荐策略：
1. 以固定短窗口（如 1 秒）对 report 做聚合
2. 同一 surface 的多条 report 合并为一条聚合 report
3. 聚合 report 至少包含：
   - action 列表
   - 最终状态
   - 关键 effect 摘要

目标：
1. 降低上下文碎片
2. 降低 token 消耗
3. 提升连续动作场景的稳定性

---

## 7. 功能需求三：统一消息存储与 SQLite 多用户设计

### 7.1 问题定义
当前消息、action 记录、用户覆盖配置等数据散落在用户目录 JSON 文件中，不利于：
1. 多用户隔离
2. 统一查询
3. 历史回放
4. report 与消息的关联读取

### 7.2 总目标
引入项目内置 SQLite，作为本地多用户的统一消息存储层。

### 7.3 存储边界
SQLite 至少承载：
1. 用户信息（本地用户）
2. chat 会话
3. message
4. action call
5. action report
6. surface state（可选持久化）

### 7.4 统一消息模型
所有进入 LLM 上下文的文本性对象，都应统一进入 `message` 表。

包括：
1. 用户消息
2. assistant 回复
3. observer/action_report 消息
4. 未来可能的 tool/result 映射消息

推荐消息字段：
- `message_id`
- `user_id`
- `chat_id`
- `turn_id`
- `role_internal`：`user / assistant / observer / system_internal`
- `role_provider`：当前 provider 的外部映射角色
- `message_type`：`chat / action_report / action_summary / note`
- `content_text`
- `visibility`：`visible / hidden`
- `created_at`

说明：
1. report 也是消息。
2. report 默认 `visibility=hidden`，不一定显示在聊天气泡中。
3. 是否送入 LLM 上下文，由读取层决定，而不是由是否显示决定。

### 7.5 Action 专用表
除消息表外，仍建议保留独立 action 相关表，用于结构化检索。

建议最小表：
1. `action_calls`
   - action 请求本身
2. `action_reports`
   - action 的 result/effect/report 聚合结果

这样好处是：
1. message 表服务“对话上下文”
2. action 表服务“结构化查询与审计”
3. 二者通过 `message_id / action_id / report_id` 关联

### 7.6 Surface 状态表（推荐）
建议新增 `surface_states` 表。

用于记录：
- 当前各 surface 最新状态
- 最近一次状态更新时间
- 最近一次摘要文本

作用：
1. 让 `surface.get_state` 能快速读取最新状态
2. 为 report 聚合提供统一依据
3. 为多用户、多 chat 隔离提供数据基础

### 7.7 多用户隔离要求
SQLite 设计必须显式包含 `user_id`。

至少遵守：
1. chat 属于 user
2. message 属于 chat，也属于 user
3. action report 属于 chat，也属于 user
4. surface state 至少要带 `user_id`，必要时带 `chat_id`

### 7.8 读取策略
LLM 读历史时，不应简单读取“最后 N 条 message 文本”。

建议读取层具备以下能力：
1. 按 `visibility` 和 `message_type` 过滤
2. 将普通聊天消息和 report 消息按时间合并
3. 必要时做 report 聚合压缩
4. provider 适配时再映射 `observer -> assistant/model`

### 7.9 迁移原则
1. 新消息与 action/report 从切换时开始写 SQLite。
2. 旧 JSON 文件不立即删除，可保留过渡期。
3. 最终以 SQLite 作为唯一权威消息源。

---

## 8. 关键场景

### 场景 A：LLM 只执行动作，不立即续跑
1. 用户说：把数字改成 100
2. LLM 回复 `{content, action, followup=none}`
3. action 执行
4. 生成 action report
5. report 写入 SQLite 与历史
6. 不立即续跑
7. 用户下次对话时，LLM 可从历史中看到这条 report

### 场景 B：LLM 执行动作后自动继续
1. 用户说：请连续把数字设为 100、200、300
2. LLM 首轮输出动作一：`100 + followup=report`
3. 动作完成，生成 report
4. 系统触发下一轮内部 LLM
5. LLM 根据 report 决定动作二：`200 + followup=report`
6. 重复直到完成

### 场景 C：用户说话时 action 完成
1. action 在后台完成
2. report 生成
3. 用户当前正在说话
4. report 入 pending queue
5. 当前用户 turn 结束后，在下一次安全触发点送入 LLM

### 场景 D：多个 action 同时完成
1. 多个 report 在短窗口内到达
2. 聚合器在 1 秒内合并
3. 统一写入一条聚合 report message 和一批结构化 report 记录

### 场景 E：LLM 主动查看 surface 当前状态
1. 用户问：现在这个 surface 里的数字是多少
2. LLM 首轮输出只读 action：`surface.get_state + followup=report`
3. 该轮 `content` 可为空，也可为“稍等我看看”
4. 系统执行 `surface.get_state`
5. 生成包含当前 state 的 `action report`
6. 自动发起下一轮 LLM
7. LLM 根据 report 中的 state 回答用户

---

## 9. 验收标准

### 9.1 Surface State
1. 每个受管 surface 默认支持 `surface.get_state`。
2. 每个受管 surface 在状态变化时能发出 `state_change`。
3. LLM 可读取结构化状态，而不是依赖原始 DOM。

### 9.2 Action Report
1. 每个 action 执行后都能生成 report。
2. report 同时可进入消息历史与结构化 action 存储。
3. `followup=report` 可稳定触发自动续跑或挂起入队。

### 9.3 自动续跑
1. 系统支持连续动作链。
2. 用户说话期间不会错误丢失 report。
3. action call 频率限制与去重生效。

### 9.4 SQLite
1. 新消息与 report 写入 SQLite。
2. 多用户数据隔离可验证。
3. 历史读取链路可从 SQLite 组装出 LLM 上下文。

---

## 10. 风险与待确认项

### 10.1 风险
1. 各模型接口对“中途插入特殊消息”的兼容性不同。
2. 连续动作链若 prompt 约束不足，仍可能出现非预期循环。
3. 多 surface 并发时，report 聚合和状态版本一致性会变复杂。

### 10.2 待确认
1. `followup` 是否只保留 `none/report` 两个值，还是预留扩展枚举。
2. report 聚合窗口是否固定为 1 秒，还是允许配置。
3. `surface_states` 是否持久化全量历史，还是只保留最新快照。
4. SQLite 数据库文件路径、初始化策略与版本迁移机制。

---

## 11. 推荐后续落地顺序

1. 先完成 `surface.get_state + state_change` 的统一协议与数据结构。
2. 再完成 `action report + observer + followup` 的统一调度层。
3. 最后切换到 SQLite 统一消息存储，并让历史读取完全依赖 SQLite。

该顺序的原因：
1. 没有统一 state，就很难定义可靠 report。
2. 没有统一 report，就难以稳定实现自动续跑。
3. 没有清晰的数据模型，SQLite 落地容易做成“只换存储、不换结构”的低价值迁移。  
