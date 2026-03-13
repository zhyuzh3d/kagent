# 消息结构与数据模型（PRD）

- 文档类型：需求说明（PRD）
- 生成时间：2026-03-13 （Asia/Shanghai）
- 依据：本次对话中对“messages 入库结构、UI 展示、action 调度与 LLM followup 缓冲机制”的一致结论整理（不兼容旧数据，允许直接重置数据库）

## 1. 背景与问题

现状存在以下痛点（来自对话复盘）：

- “对话气泡展示内容”与“LLM 原始输出/协议字段/执行链路数据”混杂在同一字段或多字段重复存储，导致：
  - 上下文拼装与 UI 展示逻辑复杂且不稳定。
  - action / report / state change 等 observer 事件信息冗余、难读、难调试。
- 当 LLM 被要求输出 JSON envelope（含 action）时，系统内会出现“原始输出、投影文本、草稿”等多处存储与推断，易产生概念混乱与重复。

本 PRD 的目标是：统一消息格式入库，拆分展示层与协议层，降低冗余并提高可调试性。

## 2. 目标（Goals）

1. messages 表对所有来源（user/assistant/observer/system）采用统一入库结构。
2. messages 的“可展示文本”拆分为三段存储：`say` / `aside` / `action`：
   - `say`：气泡主内容（默认显示）。
   - `aside`：气泡下方小字（默认显示，语义化动作解说/旁白等均写入此处或写入 `say` 的括号内；具体由内容策略决定）。
   - `action`：结构化 JSON（默认不显示，只显示一个 “A” 圆圈标识；show more 开启后展示完整 action JSON）。
3. LLM 输出强制为 JSON（assistant 的“原始文本输出”应可审计、可回放），并从中解析得到 `say/aside/action` 入库。
4. 解析失败有清晰的可视化与调试入口：
   - UI 气泡显示“格式异常”标志。
   - `raw_data` 保留完整原始数据；show more 展示 `parse_error` 与完整 `raw_data`。
5. observer 负责 user/assistant 之外绝大多数消息组装与插入（如对话关闭、surface state change、action 执行过程与结果等），但不影响默认 UI 的“只展示 user/assistant”。
6. 自动 followup（由 observer 触发的 LLM continuation）必须带缓冲合并机制：最多每 1 秒触发一次；且当 user 发送新消息时，若存在 pending 的 observer followup，应立即结束 pending，并在该次 user turn 的历史上下文中携带这些 observer 消息。

## 3. 非目标（Non-Goals）

- 不考虑旧数据迁移与兼容；允许直接删除并重置数据库。
- 暂不实现“边说边动”（streaming 中途触发动作）；仅在拿到 assistant 完整 JSON 后再执行动作。
- 暂不提供 user 输入 `aside` 的 UI/入口（当前 user 语音输入仅产生 `say`）。
- 不在数据库持久化“实时 allowed_actions 列表”；它仅为运行时数据结构。

## 4. 术语与约定

- **message_id**：消息唯一标识（字符串）。约定 `u` 开头保留为“user 自有相关”的前缀；本系统生成的 message_id 不使用 `u` 开头（推荐沿用 `msg-...` 风格）。
- **ref_message_id**：report/progress/execute 等消息引用的“触发它的那条 call 消息”的 message_id。
- **ref_action_slot**：预留的 action 槽位（默认 0），用于未来支持“同一条消息多个 action”的扩展。
- **show more**：chat 页面顶部开关；打开后同时显示：
  - action JSON、parse_error、raw_data；
  - 所有 observer/system 消息（若存在）。
- **surface_instance_name**：page 管理器对实际加载的 surface 实例分配的语义化名字（可读且唯一，例如 `Counter#1`）。LLM 与 action args 优先使用此名字作为 target。

## 5. 统一消息对象：入库逻辑视图

无论来源是谁，入库前都需被归一化为以下逻辑对象（写入 DB 时可拆为列）：

```json
{
  "message_id": "msg-...",
  "turn_id": 0,
  "seq": 0,
  "created_at_ms": 0,
  "role": "user|assistant|observer|system",
  "say": "",
  "aside": "",
  "action": null,
  "ref_message_id": "",
  "ref_action_slot": 0,
  "parse_error": "",
  "raw_data": { "raw_text": "", "llm": {}, "extra": {} }
}
```

### 5.1 `action` 的统一规则

为了减少“消息分类争议”，不在顶层强制区分 chat/call/report/progress；而以 `action.type` 显式标记动作语义：

- 当 `action` 为空：该消息为纯聊天/通知（仅 `say/aside` 生效）。
- 当 `action` 非空：该消息为 action 相关消息，必须包含 `action.type`：
  - `call`：发起动作（典型来源：assistant；未来可扩展为 user/observer）。
  - `execute`：observer 插入，表示已开始执行某条 call（不触发自动 followup）。
  - `report`：observer 插入，表示某条 call 的最终结果。
  - `progress`：observer 插入，表示某条 call 的中间过程（可选，不推荐高频使用）。
  - `combined`：可选扩展（同一条消息同时表达执行与结果；是否启用由实现决定）。
  - `state_change`：surface state change 等事件（由 observer 插入，用于驱动推理与 UI 调试）。

### 5.2 action 结构：按 `action.type` 的最小字段约束

> 约束目标：够用且精简；更复杂的 result/status 结构由具体 action 函数自行决定。

- `call`
  - 必填：`type="call"`, `path`, `args`, `followup`
  - `followup` 枚举：`none|report|report_progress`
- `execute`
  - 必填：`type="execute"`, `ref_message_id`, `ref_action_slot`
  - 可选：`dispatch_info`（仅调试用，例如解析到的 surface_id、命中的 surface_instance_name 等）
- `report`
  - 必填：`type="report"`, `ref_message_id`, `ref_action_slot`, `state`, `desc`, `result`
  - `state` 建议枚举：`success|fail|pending`
- `progress`
  - 必填：`type="progress"`, `ref_message_id`, `ref_action_slot`, `state`, `desc`
  - 可选：`status`（过程状态对象；结构由 action 自己决定）
  - `state` 建议枚举：`running|pending`
- `state_change`
  - 必填：`type="state_change"`, `surface_instance_name`, `delta_or_state`

## 6. 数据库设计（重置后）

### 6.1 messages 表（建议字段）

> 以下为建议最小集合；实现可增补索引字段，但应保持入库结构稳定与可调试。

- 标识与时序
  - `id`（自增主键）
  - `message_id TEXT UNIQUE NOT NULL`
  - `turn_id INTEGER NOT NULL`
  - `seq INTEGER NOT NULL`（全局递增）
  - `created_at_ms INTEGER NOT NULL`
  - `created_at_iso TEXT NOT NULL`
  - `created_at_local_ymdhms TEXT NOT NULL`
  - `created_at_local_weekday TEXT NOT NULL`
  - `created_at_local_lunar TEXT NOT NULL`
- 来源
  - `role TEXT NOT NULL`（user/assistant/observer/system）
- 展示层（三段）
  - `say TEXT NOT NULL DEFAULT ''`
  - `aside TEXT NOT NULL DEFAULT ''`
  - `action_json TEXT NOT NULL DEFAULT ''`（空字符串表示无 action；非空必须为 JSON 且包含 `type`）
- 引用关系（用于 report/progress/execute 关联 call）
  - `ref_message_id TEXT NOT NULL DEFAULT ''`
  - `ref_action_slot INTEGER NOT NULL DEFAULT 0`
- 原始数据与解析错误
  - `raw_data TEXT NOT NULL DEFAULT ''`（推荐为 JSON 字符串；至少包含 `raw_text`）
  - `parse_error TEXT NOT NULL DEFAULT ''`

### 6.2 索引建议

- `(seq)`：按时间线分页/加载。
- `(turn_id, seq)`：按 turn 聚合。
- `(ref_message_id, ref_action_slot, created_at_ms)`：快速查某个 call 的执行链路（execute/report/progress）。
- `(role, created_at_ms)`：UI 过滤与 show more。

## 7. UI/交互需求

### 7.1 默认展示（show more 关闭）

- 仅展示 `role in {user, assistant}` 的消息气泡。
- 气泡内容：
  - 主体：`say`
  - 小字：`aside`（若为空则不展示）
  - 若 `action_json` 非空：在气泡上显示一个小圆圈 “A” 标志（仅提示存在动作，不展示动作详情）

### 7.2 show more 开启

同时启用以下行为：

1. 展示所有 `observer/system` 消息（按 `seq` 时间线插入）。
2. 对每条消息在小字区域追加展示：
   - 语义化时间字段：`created_at_local_ymdhms`、`created_at_local_weekday`、`created_at_local_lunar`
   - `action_json`（若非空，pretty print）
   - `parse_error`（若非空）
   - `raw_data`（pretty print；解析失败时重点展示 `raw_data.raw_text`）

### 7.3 解析失败 UI

当 `parse_error` 非空时：

- 默认展示（show more 关闭）：仍显示一条 assistant 气泡，但改为“消息格式异常”样式（感叹号标志）。
- 气泡文案：显示 `raw_data.raw_text` 的前 12 个字符 + `...`（若不足 12 个则显示全部）。
- show more 开启时：展示完整 `parse_error` 与完整 `raw_data`（此时 `action_json` 必为空）。

## 8. LLM 输出协议（assistant）

### 8.1 基本要求

- assistant 的每次回复必须是一个 JSON 文本（可为纯 JSON 或 ```json fenced code block，但推荐纯 JSON）。
- JSON 中必须包含以下字段：
  - `say`（字符串，必填）
  - `aside`（字符串，可选；缺省视为空）
  - `action`（对象或 null；若存在必须包含 `type="call"` 且满足 call 的字段约束）

示例（带 action）：
```json
{
  "say": "好的，我这就帮你关闭计数器服务",
  "aside": "（执行关闭 surface 动作）",
  "action": { "type":"call", "path":"close_surface", "args":{ "target":"Counter#1" }, "followup":"report" }
}
```

示例（不带 action）：
```json
{ "say":"我在呀，你有什么需要帮忙的？", "aside":"", "action": null }
```

### 8.2 raw_data 的写入（assistant）

assistant 入库时的 `raw_data` 应尽可能保留“可复用的原始信息”，建议结构：

```json
{
  "raw_text": "<LLM 原始输出字符串（JSON 文本）>",
  "llm": { "model":"", "usage":{}, "provider":"", "request_id":"" },
  "extra": {}
}
```

> `llm/extra` 的字段集随实现与可得信息扩展，但不得破坏 `raw_text` 字段。

## 9. observer 职责与消息注入规则

### 9.1 职责边界

- 除“对话开始的 system 提示词”外，几乎所有通知/状态/执行链路消息均由 observer 组装并入库：
  - 对话关闭（来自 page action）
  - surface state change
  - action execute/report/progress

### 9.2 默认 say/aside

- observer 消息默认 `say=''`、`aside=''`，除非具体 page/surface JS 函数返回值要求展示文案。
- show more 模式下仍可查看 observer 的 `action_json/raw_data/parse_error` 以调试。

## 10. action 调度与执行链路（page 调度器）

### 10.1 allowed_actions（运行时）

page 维护一个实时 `current_allowed_actions` 映射（不写 DB）：

- 包含 page 全局可用 actions
- 包含当前已打开的 surface 实例暴露的 actions（以 `surface_instance_name` 做语义化路由入口）

该映射用于：
- 校验 assistant 发起的 `action.path/args` 是否允许调用；
- 将 LLM 提供的 `target=surface_instance_name` 映射到实际执行对象（例如具体 surface_id 或 iframe 通道）。

### 10.2 执行流程（followup=report 为例）

1. assistant 产生一条消息，解析得到 `action.type=call`，入库。
2. observer/page 调度器捕获该 call：
   - 插入一条 `action.type=execute` 的 observer 消息（必须含 `ref_message_id/ref_action_slot`）。
   - 执行 action（成功/失败均应结束）。
3. 若 `followup=report`：
   - 调度器插入一条 `action.type=report` 的 observer 消息（必须引用 `ref_message_id/ref_action_slot`，并带 `state/desc/result`）。
4. 触发自动 followup 的策略由第 11 节控制（缓冲合并）。

### 10.3 progress 与 state_change 的优先级建议

- 不推荐 action 函数高频触发 progress；
- 若需要中间状态用于推理/驱动 UI，优先触发 surface 的 `state_change` 事件；
- progress 的“覆盖更新/只保留最新”由 action 自己处理（系统不强制）。

## 11. LLM followup 缓冲合并机制（自动 continuation）

### 11.1 触发原则

- observer 的某些消息会“积累为 pending”，等待合并后触发一次 continuation（例如 report / progress / state_change）。
- execute 消息不触发 pending（仅用于调试与时序对齐）。
- 自动 continuation 的频率限制：最多每 1 秒一次（debounce）。

### 11.2 pending 与 user 新消息的交互（关键一致结论）

当存在 pending 的 observer 消息时：

- **若 user 发起新消息（新 turn）**：必须立即结束 pending；
  - 这些 pending observer 消息将被包含在该次 user turn 发送给 LLM 的 history 中（一起参与推理）。
  - 不再单独触发一次 continuation（避免重复）。

### 11.3 合并与定时器（建议实现要求）

建议采用“去抖定时器 + pending 标志”的方式（不采用轮询扫描 session 变化）：

- 当产生“会触发 followup 的 observer 消息”时：
  - 入库并进入 session
  - 标记 `pending_followup=true`
  - 重置定时器：到期后若仍 pending 且当前无 user turn/LLM 生成进行中，则发起一次 continuation
- 每次 LLM 完整回复落库（assistant final）后：
  - 重置该定时器，确保至少 1 秒安静期后才自动发送 observer followup

> 目标效果：减少高频 state_change/report 导致的连续 LLM 请求，同时不影响 user 的对话发送。

## 12. 验收标准（DoD）

1. messages 表落库字段满足：`say/aside/action_json/raw_data/parse_error/ref_message_id/ref_action_slot` 等核心字段，且所有角色统一入库。
2. assistant 输出 JSON 能被稳定解析并拆分入库；解析失败能：
   - 写入 `parse_error`；
   - `raw_data` 保留原始内容；
   - UI 显示“格式异常”并可在 show more 查看完整 raw_data 与 parse_error。
3. 默认 UI 仅展示 user/assistant；show more 同时展示 action 详情与 observer/system 消息。
4. action 执行链路满足：
   - call → execute → report（followup=report）链路完整；
   - report/progress/execute 可通过 `ref_message_id/ref_action_slot` 追溯到对应 call。
5. 自动 followup 缓冲机制满足：
   - observer 消息触发的 continuation 最多每秒一次；
   - user 新消息到来时，pending 立即结束且被包含进该次 history，不再单独 continuation。

## 13. 待确认事项（本轮未强制拍板）

1. `say` 与 `aside` 的内容策略：语义化动作解说放 `aside` 还是放 `say` 的括号内（两者都允许，但需统一风格以利于 TTS/可读性）。
2. 哪些 observer `action.type` 会触发 pending followup（建议默认：`report/progress/state_change`；`execute` 不触发）。
3. `action.path/args` 的标准化命名规范（例如 page action 命名、surface action 命名、实例名格式）。

