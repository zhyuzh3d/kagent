# Chat Surface Observer 新模式开发计划

## 0. 维护信息

- 文档类型：开发计划（`plan` 模式）
- 创建时间：2026-03-22 23:42:08 CST
- 目标：移除旧 observer 消息模式，按新的 7+1 事件模型统一消息插入机制
- 影响范围：
  - `webui/page/chat/components/*`
  - `webui/page/chat/lib/*`
  - `webui/lib/pageSurfaceTool.js`
  - `services/chat_server/internal/app/*`
- 依据：
  - `doc/_instruction.md`
  - `doc/_instruction/structure.md`
  - `doc/_page-surface.md`
  - `doc/_note.md`
  - 真实代码证据：`webui/page/chat/components/surface-bridge.js`、`webui/page/chat/lib/event-router.js`、`webui/page/chat/lib/session-controller.js`、`webui/page/chat/lib/io-worker.js`、`services/chat_server/internal/app/session_state.go`、`services/chat_server/internal/app/session_turns.go`、`services/chat_server/internal/app/session_actions.go`、`services/chat_server/internal/app/message_types_*`

---

## 1. 目标与非目标

### 1.1 目标

建立单一、可解释、可去重的 observer 消息机制，只保留以下 8 类可见消息：

1. `chat page opened`
2. `surface window opened, loading surface`
3. `surface registered`（完整 register 内容，含 surface 基本信息和 actions 详细信息）
4. `surface state changed`
5. `surface window state change`（仅用户操作结束后写入）
6. `send action to surface`
7. `received surface report`
8. `chat page closed`（可选，页面关闭前写入数据库）

### 1.2 非目标

1. 不改 Surface 协议语义（`surface_connect/register/ready/action_result/state_change` 本身不重写）。
2. 不做全量消息系统重构（仅针对 observer 插入策略收敛）。
3. 不引入新存储引擎或变更数据库 schema（默认沿用现有 `ChatMessage`）。

---

## 2. 现状问题（需移除）

当前重复与错位来自“多源插入 + 旧上下文消息直接可见”：

1. `surface_registry_sync` 与 `surface_active_change` 在初始化/上下文抖动时高频入流，形成刷屏重复。
2. 前端本地 observer 与后端正式 `message_append` 并存，语义重复。
3. `surface registered` 未被建模为一条独立关键消息，注册详情不稳定。
4. `surface window state` 缺少“交互结束”边界，容易在拖拽过程中多次记录。

---

## 3. 新模式总设计

## 3.1 单一写入原则

1. **后端为唯一正式写入者**：observer 的正式落库统一在 `chat_server`。
2. 前端只上报“事件事实”，不再本地拼装可见 observer 正式文本。
3. 页面只显示后端 `message_append` / `history_sync` 回放结果。

## 3.2 统一事件契约（Observer Event Envelope）

每条 observer 事件统一携带：

1. `event_name`：固定 8 类之一
2. `event_key`：幂等键（去重核心）
3. `source_kind`：`page|surface|system`
4. `source_id`：`page/chat` 或 `<surface_id>`
5. `source_label`：页面名或 surface 展示名
6. `surface_id` / `surface_name`（适用时）
7. `window_state`（适用时：`mode`、`float_geometry`、`dock_width`）
8. `payload`：事件原始事实（完整结构化数据）
9. `created_at_ms`

## 3.3 事件到消息映射（显示文案）

1. `chat.page.opened` -> `chat page opened.`
2. `surface.window.opened` -> `surface window opened, loading surface: <surface name>.`
3. `surface.registered` -> `surface <surface name> registered.`
4. `surface.state.changed` -> `surface <surface name> state changed.`
5. `surface.window.state.changed` -> `surface window state changed.`
6. `surface.action.sent` -> `send action to surface <surface name>: <action type>.`
7. `surface.report.received` -> `received surface report: <action type>.`
8. `chat.page.closed` -> `chat page closed.`

---

## 4. 旧模式移除清单（必须完成）

1. 停止将 `surface_registry_sync` / `surface_active_change` 作为可见 observer 消息写入。
2. 停止前端在 `event-router` 内对 `state_change/action_report` 的正式 observer 本地拼装（仅保留过渡调试时开关，不默认启用）。
3. `message_types_render.go` 中旧 `surface_context` 文案仅保留兼容读取，不再作为主生产路径。
4. 新事件模型上线后，`surface_context` 仅作为内部上下文同步，不直接面向用户显示。

---

## 5. 新增事件明细与关键字段

## 5.1 chat page opened

1. 触发：chat 页面基础加载完成且与 chat stream 建立连接后（无需开始对话）。
2. 字段：`page_session_id`、`project_id`、`thread_id`、`show_more`、`config_snapshot`（必要最小字段）。
3. 去重键：`chat.page.opened:<page_session_id>`。

## 5.2 surface window opened, loading surface

1. 触发：开始打开 surface window（用户下拉或 AI action 打开）且已确定窗口布局配置。
2. 字段：`surface_id`、`surface_name`、`open_reason`、`window_state{mode,float_geometry,dock_width}`。
3. 去重键：`surface.window.opened:<page_session_id>:<surface_id>:<open_seq>`。

## 5.3 surface registered（关键）

1. 触发：收到 `surface_register` 并完成 ack。
2. 字段（必须完整）：
  - `surface_register`：原始 register 包（完整透传）
  - `surface_registration`：宿主归一化后的 surface 基本信息
  - `surface_actions`：注册动作详细列表（完整描述符）
  - `runtime_context`：注册时 runtime 快照
3. 去重键：`surface.registered:<page_session_id>:<surface_id>:<register_seq|registered_at_ms>`。

## 5.4 surface state changed

1. 触发：收到 surface `state_change` 上报。
2. 字段：上报原文完整透传，附 `surface_id/surface_name`。
3. 去重键：`surface.state.changed:<surface_id>:<state_version>:<updated_at_ms>`（无版本时降级哈希）。

## 5.5 surface window state change

1. 触发：窗口位置/大小/浮动停靠变化。
2. **硬规则**：仅在用户交互结束后触发（`pointerup`/`pointercancel`/resize stop），拖拽过程中禁止写入。
3. 字段：`window_state{mode,float_geometry,dock_width}`、`change_reason`。
4. 去重键：`surface.window.state.changed:<surface_id>:<window_state_hash>`。

## 5.6 send action to surface

1. 触发：Page 向 surface 发送 action（进入 dispatch 前后择一，建议“发送成功入队后”）。
2. 字段：`action_id`、`action_name/type`、`args`、`target_surface_id/name`、`dispatch_source(user|ai)`。
3. 去重键：`surface.action.sent:<action_id>`。

## 5.7 received surface report

1. 触发：收到 action_result/report。
2. 字段：`action_id`、`action_name/type`、`status`、`result`、`effect`、`business_state`、来源 surface 信息。
3. 去重键：`surface.report.received:<action_id>:<report_seq|status_hash>`。

## 5.8 chat page closed（可选）

1. 触发：页面关闭前（`pagehide/beforeunload`）。
2. 字段：`page_session_id`、`active_surface_id`、`close_reason`、`timestamp`。
3. 去重键：`chat.page.closed:<page_session_id>`。
4. 可靠性：`ws send_control` + `keepalive` 回退（确保“关闭前尽量落库”）。

---

## 6. 统一插入机制设计

## 6.1 前端：统一 Reporter（单出口）

新增（或收敛）`observer-event-reporter`，所有 observer 事件只能经这个出口发送：

1. 规范化事件结构（补齐 `event_name/event_key/source/surface/window_state/payload`）。
2. 控制发送时机（尤其 `surface window state change` 在交互结束后发送）。
3. 本地不负责最终文案，不负责正式插入。

## 6.2 后端：统一 Event Writer（单入口）

在 `chat_server` 增加统一写入路径（例如 `appendObserverEvent(...)`）：

1. 按 `event_name` 选择 `category/message_type/say`。
2. 将结构化事件落入 `payload_json/raw_data`。
3. 执行 `event_key` 去重（会话内 + 最近历史窗口）。
4. 持久化后统一发 `message_append`。

---

## 7. 分阶段实施计划

## 阶段 A：协议与模型收敛（后端先行）

1. 扩展 `ControlMessage` 支持统一 `observer_event` 结构。
2. 新增 event_name 常量与 message_type 映射。
3. 实现 `event_key` 去重与统一写入函数。
4. 保留旧分支但默认不走（仅兼容回读）。

## 阶段 B：前端事件源改造

1. 页面初始化发送 `chat.page.opened`。
2. surface 打开流程发送 `surface.window.opened`（含窗口状态）。
3. `surface_register` 成功后发送 `surface.registered`（含完整 register 与 actions）。
4. state_change 上报转发为 `surface.state.changed`。
5. `dispatchAction` 发送 `surface.action.sent`。
6. action report 回调发送 `surface.report.received`。

## 阶段 C：窗口状态事件边界

1. 拖拽移动/缩放/停靠切换过程中只更新内存态，不发送 observer。
2. 在 `pointerup/pointercancel` 或 resize 结束后比对快照，变化才发送 `surface.window.state.changed`。

## 阶段 D：关闭事件与旧模式下线

1. 页面关闭前发送 `chat.page.closed`（含回退策略）。
2. 关闭 `surface_registry_sync/surface_active_change` 可见写入。
3. 清理旧消息渲染路径与冗余前端拼装逻辑。

---

## 8. 验收标准（逐条对应需求）

1. 页面加载完成、未开始对话，也会出现且仅出现一条 `chat page opened`。
2. 每次 surface window 打开阶段出现一条 `surface window opened, loading surface...`，包含窗口布局字段。
3. 每次 surface 注册成功出现一条 `surface <name> registered`，且 payload 含完整 register + actions 详情。
4. 每次收到 state_change 出现一条 `surface <name> state changed`，payload 为上报原文。
5. 拖拽/缩放过程中不写消息，仅结束时按最终状态写一条 `surface window state change`。
6. 每次发 action 出现一条 `send action to surface...`，含 action 参数。
7. 每次收 report 出现一条 `received surface report...`，含 report 原文。
8. 页面关闭前可选写入 `chat page closed`（最佳努力 + 回退）。
9. 不再出现 `surface_registry_sync/surface_active_change` 刷屏重复消息。

---

## 9. 验证方案

1. 单测：
  - `event_key` 去重
  - message_type 映射
  - `surface.window.state.changed` 仅结束触发
2. 集成：
  - 页面打开 -> 打开 surface -> 注册 -> state_change -> action -> report -> 关闭
  - 校验消息顺序、数量、字段完整性
3. 手工回归：
  - 下拉打开 surface
  - AI action 打开 surface
  - 拖拽/缩放/停靠切换
  - 刷新和重连后历史一致性

---

## 10. 风险与应对

1. 关闭事件在 unload 时可靠性不足：增加 keepalive 回退，验收按“最佳努力”定义。
2. 新旧逻辑并存窗口期重复：使用 feature flag 分阶段切换，先关可见旧消息，再下线旧入口。
3. 高频状态导致写入压力：`event_key + state_version/window_hash` 去重，必要时增加最小间隔阈值。

---

## 11. 交付物

1. 代码：observer 新模式实现（前后端统一事件机制）。
2. 文档：本计划 + 最终 devreport（含验收记录、字段样例、去重证明）。
3. 验证记录：测试命令与关键日志样本。

