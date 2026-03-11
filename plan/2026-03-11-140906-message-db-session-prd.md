# Message 统一存储与 Session.messagehis 同步机制（PRD）

- 文档生成时间：2026-03-11 14:09:06 CST
- 范围：仅覆盖“消息数据库设计 + 消息写入/转义/组装机制 + session.messagehis 内存同步机制”
- 信息来源：本对话线程确定的约束与取舍（不含未讨论/未确认内容）

## 1. 背景与目标

### 1.1 背景
项目需要对“会影响 LLM 与用户对话内容”的各种消息进行统一存储与可控组装：不仅包括用户/助手正常对话，还包括 action report、对话阶段事件（开始/停止/页面关闭等）、surface 被打开后的状态与后续变化、配置变化提醒等。

现状存在的问题（抽象描述）：
- 不同类型的“对话相关事实”存储与注入方式不统一，难以扩展与演进。
- 对 LLM 投喂时缺少稳定、可控的语义化文本（同时又需要保留原始数据便于追溯）。
- 内存历史与数据库之间缺少明确的同步边界与裁剪规则。

### 1.2 目标
1. 建立**统一的单表 messages**，覆盖对话过程中所有需要存储的消息类型（通过 `category/type` 区分）。
2. 每条消息同时保存：
   - **语义化后的 content**（用于 UI 展示与 LLM 投喂）
   - **原始 payload_json**（用于追溯、调试、未来重渲染/迁移）
3. 建立**版本化的解析/转义机制**：当 `payload_json` 结构升级时，通过 `payload_schema_version` 选择对应解析/转义函数。
4. 建立 session 内存对象 `session.messagehis` 的同步机制：
   - 内存只缓存“足够当前 LLM 对话使用”的消息窗口（窗口按“正常对话锚点条数”计算，但缓存范围覆盖锚点时间到现在的所有消息）
   - 缓存时可只保留用于投喂的语义字段（如时间语义化字符串 + content），不强制缓存原始 payload（未来可调整）

## 2. 范围与非目标

### 2.1 本文范围
- 数据库：messages 单表结构、字段语义、约束建议、读写路径与查询裁剪规则。
- 消息：建议覆盖的 `category/type` 列表与语义化 content 的统一生成规则（含 action_call / action_report / surface.state / surface.change / config_change / phase 等）。
- Session：`session.messagehis` 与 DB 的同步、裁剪、冷启动加载策略。

### 2.2 非目标（明确不做）
- Surface 的“每用户状态保存与恢复”机制（由 surface 自身单独管理），不纳入 message 机制讨论与实现。
- Chat 页面“哪些 UI 交互应该写入 message”的选择与策略（由页面逻辑决定），本文不做枚举与裁决。
- 具体工程实现与代码改造（本 PRD 仅定义需求与设计，不落地编码）。

## 3. 术语
- **message**：对话过程中可被持久化的一条记录（用户/assistant/observer/system 都是一条 message）。
- **category/type**：消息分类体系。`category` 为更大类，`type` 为具体类型。
- **payload_json**：原始数据载荷（原样存储），用于追溯与后续版本化解析。
- **payload_schema_version**：payload 结构版本号，用于选择对应解析/转义函数。
- **content（语义化文本）**：自动生成的、稳定可投喂的文本内容（可同时用于 UI 与 LLM）。
- **session.messagehis**：每个用户会话（session）内存中缓存的消息历史窗口，用于 prompt 组装。
- **正常对话锚点**：仅指用户/assistant 的“正常对话消息”（最少显示内容），用它确定滑动窗口的最早时间点。

## 4. 设计原则（本线程已确认）
1. **单表 messages**：所有对话相关消息都以 message 形式写入同一张表，通过 `category/type` 区分。
2. **payload_json 原样保存 + 版本化解析**：不抽高频结构化字段；未来格式变化通过版本匹配解析/转义函数处理。
3. **时间语义化**：DB 保存可计算/可追溯的基础时间字段；进入内存与投喂时拼接语义化字符串。语义化包含：
   - 公历年月日时分秒（如 `2026年03月15日 18:32:10`）
   - 星期几
   - 农历信息
4. **surface 消息实时注入**：surface 侧产出 LLM 友好的 `state/change`（且 change 要克制）；message 机制只负责“插入对话流”，不承担 per-user UI 世界状态维护。
5. **滑窗按正常对话锚点计数**：先找到最近 N 条正常对话锚点最早时间点 T，再把 `T..now` 的所有消息读入内存（包括不可见消息，如 action report）。
6. **可见性由消息类型决定**：action report 默认不在普通 chat 列表展示；debug 时可以让所有消息可见。DB 不以人工设置 `visible` 字段为前提。
7. **interrupt 定义**：指 assistant 流式生成过程中被用户操作打断导致生成不完整；在 assistant 那条 message 上记录 `interrupt` 枚举与 `completion_status`。
8. `interrupt_at_ms`、`partial_text` 字段先预留，未来再填充。
9. **触发链路可追溯**：允许某条消息由另一条消息触发/派生；触发来源通过 `payload_json` 内字段指向 `message_id`（例如 `trigger_message_id`）。
10. **总数上限**：进入 session 内存与 prompt 的消息总数设定上限；当 `T..now` 区间内消息数量超过上限时，不再严格按锚点截取，而改用“按时间取最后 M 条”。

## 5. messages 单表：字段设计（逻辑结构）

> 说明：以下为逻辑字段。物理类型（TEXT/INTEGER/JSON 等）与索引策略在实现阶段确定。

### 5.1 必备基础字段
- `message_id`：消息唯一 ID（主键）
- `user_id`：用户标识
- `chat_id`：对话会话标识（同一用户可多 chat）
- `turn_id`：turn 标识（可为空/0；某些 phase/config 事件可不属于特定 turn）
- `seq`：同一 chat 内的单调序号（可选；用于在同一毫秒内稳定排序）

### 5.2 时间字段（可追溯 + 可语义化）
- `created_at_ms`：毫秒时间戳（排序/游标）
- `created_at_iso`：RFC3339（带时区）
- `created_at_local_ymdhms`：本地语义化公历时间（例如 `2026年03月15日 18:32:10`）
- `created_at_local_weekday`：本地星期几（例如 `星期三`）
- `created_at_local_lunar`：本地农历信息（格式后续细化，例如 `农历二月初六`）

> 进入 session.messagehis 时，可拼接为：`<ymdhms> <weekday> <lunar>`（不要求 DB 存一个合成字段）。

### 5.3 分类字段
- `role`：`user | assistant | observer | system`
- `category`：大类（示例：`chat | ai_action | user_action | surface | phase | config | error`）
- `type`：具体类型（示例见 6 章）

### 5.4 内容与载荷字段
- `content`：语义化文本（用于 UI 与 LLM；由写入时的转义/渲染函数生成；允许为空）
- `payload_schema_version`：int（必填）
- `payload_json`：TEXT（必填，原样保存）

> 约定：每条 message 都有 `content`（可为空）和 `payload_json`；`payload_json` 用于承载 chat 之外类型消息的额外数据（chat 类通常为 `{}` 或最小结构）。

### 5.5 assistant 完整性与打断字段（本线程确定新增）
- `completion_status`：`complete | interrupted | error`（先加上）
- `interrupt`：`none | vad | manual | other`
- `interrupt_at_ms`：INT（预留）
- `partial_text`：TEXT（预留）

## 6. category/type 建议清单（可扩展）

> 本清单只定义 message 机制“能够表达什么”，不裁决“UI 交互哪些必须写入”（该选择由页面决定，属非目标）。

### 6.1 正常对话（锚点消息）
- `category=chat`
  - `type=user_message`（role=user）
  - `type=assistant_message`（role=assistant）

> 说明：chat 类不包含 `observer/system` 的对话条目；`observer/system` 用于其它 category（动作、surface、phase、config、error 等）。

### 6.2 ai_action（LLM 发起的动作）
- `category=ai_action`
  - `type=call`（LLM 发起动作调用请求；允许带 `content`）
  - `type=report`（动作执行结果报告）
  - `type=combined`（同一条 message 同时表达 call+report 的语义；用于同步结果场景）

> 说明：不依赖固定 action_id；动作区分优先使用 `payload_json` 中的 action name + args（必要时可在 payload 内加入基于 message_id 的派生引用字段）。

### 6.3 user_action（用户触发的动作）
- `category=user_action`
  - `type=call`（用户触发动作请求；少见）
  - `type=report`（用户动作的异步结果；少见）
  - `type=combined`（默认：绝大多数 user_action 以 call+report 合体表达）

> 说明：很多用户操作的“结果”可仅由 `surface_change` 表达，从而不额外生成 user_action 的 `type=report`。

### 6.4 phase（对话阶段事件）
- `category=phase`
  - `type=convo_start`
  - `type=convo_stop`
  - `type=page_close`
  - `type=turn_nack`（空输入/无有效语音等）

### 6.5 surface（实时注入；由 surface 侧产出友好 payload）
- `category=surface`
  - `type=surface_open`（某 surface 被打开/加载）
  - `type=surface_state`（打开时提供一次 state）
  - `type=surface_change`（后续变化；要求克制/摘要）

### 6.6 config（配置变化提醒）
- `category=config`
  - `type=config_change`（配置变化通常由用户动作触发，但因语义特殊保留独立 category/type）

### 6.7 error（可选）
- `category=error`
  - `type=error_event`
  - `type=warning_event`

## 7. 语义化 content：统一生成规范（关键）

### 7.1 总体格式规范
- 目标：`content` 必须“统一自动生成、稳定、可读、可投喂”，并保留必要结构信息。
- 建议格式：**短自然语句摘要 + 括号尾巴结构**（括号内为固定 key=value 形式，必要时嵌 JSON；不包含 `at=`，因为时间由 message 自身字段提供）。

### 7.2 action_call（示例规范）
语义摘要（不伪造结果） + 结构尾巴：
- `收到，我将把 counter 设置为 100。（ai_action.call name=surface.call.counter.set_count args={"count":100} followup=report）`

### 7.3 action_report（示例规范）
结果摘要（成功/失败/阻塞） + 结构尾巴：
- `动作执行成功：counter=100。（ai_action.report name=surface.call.counter.set_count status=ok followup=none result="..." effect={"business_state":{"count":100}}）`

### 7.4 phase（示例规范）
固定短句 + 结构尾巴（可选）：
- `对话开始。（convo_start）`
- `对话停止。（convo_stop）`
- `页面关闭。（page_close）`

### 7.5 surface.state / surface.change（示例规范）
由 surface 侧产出“LLM 友好摘要”，message 层只做轻量包装：
- `已打开 surface：counter。（surface_open name=counter）`
- `counter 当前状态：count=100。（surface_state name=counter state={"count":100}）`
- `counter 发生变化：99→100。（surface_change name=counter delta={"count":100}）`

## 8. payload_json 版本化解析/转义机制

### 8.1 核心约束
- DB 不抽取高频字段，payload 结构未来允许演进。
- 每条 message 带 `payload_schema_version`，写入时记录；读取/重渲染时按版本选择解析/转义函数。

### 8.2 转义函数职责
对给定 `(category, type, payload_schema_version, payload_json)`：
1. 校验 payload（最小必需字段存在、类型正确）
2. 生成稳定的 `content`（按第 7 章规范）
3. （可选）对 payload 做截断/脱敏（若后续需要）

## 9. session.messagehis：内存同步机制

### 9.1 session 定义
- session 是“每个用户的内存缓存对象”。
- 本文只讨论 `session.messagehis`：用于 prompt 组装的历史消息窗口。

### 9.2 冷启动加载（从 DB 到 session）
1. 计算锚点：查询最近 N 条正常对话锚点（`category=chat` 且 `type in {user_message, assistant_message}`）。
2. 取最早锚点的 `created_at_ms = T_anchor`。
3. 拉取 `[T_anchor, now]` 区间内的所有 messages（不区分可见/不可见；由 type 决定 UI 呈现）。
4. 若区间内消息数量超过上限 `M_session_max`：改为按时间倒序取最后 `M_session_max` 条（不再严格按锚点截取）。
5. 将最终结果按时间顺序写入 `session.messagehis`。

### 9.3 在线追加（实时写入）
当产生新消息：
1. 写入 DB（落 `payload_json` 与语义化 `content`，写入时间语义字段）。
2. 将该消息追加到 `session.messagehis`。
3. 若追加消息是正常对话锚点（user/assistant message），更新锚点计数并重新计算 `T_anchor`（若锚点窗口向前滚动）。
4. 裁剪：从 `session.messagehis` 中移除所有 `created_at_ms < T_anchor` 的消息。
5. 总量裁剪：若 `session.messagehis` 条数超过 `M_session_max`，从最旧消息开始丢弃直到满足上限（此时不强制保留锚点完整区间）。

> 裁剪按时间点执行，与“不可见消息数量”无关：只要落在锚点时间范围内，都保留在内存以便 prompt 组装。

### 9.4 进入 prompt 的字段（session 中应缓存什么）
本线程确认：ring buffer 需要“投喂字段 + 语义时间”。
建议 session.messagehis 的每条缓存项至少包含：
- `role`
- `category/type`（用于调试与后续策略）
- `created_at_local_ymdhms + weekday + lunar`（进入 LLM 时可拼接）
- `content`
- `completion_status/interrupt`（assistant message）

## 10. 验收标准（Definition of Done）
1. messages 单表能覆盖：正常对话、ai_action/user_action（call/report/combined）、phase 事件、surface_open/state/change、config_change（类型可扩展）。
2. 每条 message 同时具备：基础字段 + 时间语义字段 + `payload_schema_version` + `payload_json` + 语义化 `content`。
3. assistant message 支持记录：`completion_status` 与 `interrupt`（并预留 `interrupt_at_ms`、`partial_text`）。
4. session.messagehis 冷启动与在线追加遵循锚点规则：最近 N 条正常对话锚点 -> 最早锚点时间点 T -> 尝试缓存 `T..now` 全量消息；若超过 `M_session_max`，按时间取最后 `M_session_max` 条。
5. `payload_json` 结构升级时可通过 `payload_schema_version` 选择解析/转义函数，保证 `content` 可稳定生成。

## 11. 待确认事项（后续决策点）
1. `category/type` 的具体枚举与命名规范（尤其是 chat 的“正常对话锚点”如何精确定义）。
2. 语义化时间字段的具体来源与格式（农历字段格式、是否需要节气/干支等）。
3. `content` 是否一份兼容 UI + LLM，还是拆分 `ui_content` 与 `llm_content`（本线程暂按“一份 content”设计）。
4. 是否需要 `seq` 字段（用于同毫秒内稳定排序）；若不需要，需明确排序 tie-breaker。
