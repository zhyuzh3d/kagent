# 消息模型统一改造开发计划（DevPlan）

- 文档时间：2026-03-13 20:23 CST
- 对应需求：`plan/260313-message-model-prd.md`
- 计划类型：开发计划（`-devplan`）
- 执行方式：按阶段落地代码、逐步验证、一次性交付

## 1. 目标与范围

### 1.1 总目标
围绕 `messages` 数据结构完成“展示层 / 协议层 / 调试层”解耦：
1. 后端统一入库结构（`say/aside/action_json/raw_data/parse_error/ref_*`）。
2. 前端默认只看 user/assistant，可在 `show more` 查看 observer/system 与调试字段。
3. action 链路满足 `call -> execute -> report` 的可追溯约束。
4. observer 自动 followup 具备 1 秒缓冲合并，并与 user 新输入互斥协同。

### 1.2 本次必须完成（与 PRD 对齐）
1. `messages` 表重置升级到新字段集合，并补齐必要索引。
2. 后端 `ChatMessage` 与持久化/历史查询逻辑切换到新字段主模型。
3. assistant 原始输出按 JSON 解析：产出 `say/aside/action`，失败写 `parse_error + raw_data`。
4. 前端气泡支持三段式展示（主文本、旁白、动作标记“A”）与 show more 调试面板。
5. 前端 action 引擎切换到 `say/aside/action` 协议，并保持动作执行结果回传。
6. observer 事件统一入库为结构化 action（`execute/report/progress/state_change`）。
7. 自动 followup 改为 pending 缓冲机制：最多每秒一次；user 新 turn 到来时并入历史并取消单独 continuation。
8. 补齐测试并通过 `go test ./...`、`go build -buildvcs=false ./...`。

### 1.3 非目标（本次不做）
1. 不做旧库迁移兼容，按 PRD 允许直接重置。
2. 不实现 streaming 中途动作触发（仍在 assistant final 后执行动作）。
3. 不实现 user 侧 `aside` 输入入口。

## 2. 现状差距

1. 当前模型仍以 `content + category/type + payload_json` 为主，未落地 `say/aside/action_json/raw_data/parse_error` 一等字段。
2. 前端仅渲染简单 `msg.role + content`，缺少动作标记与 show more 调试视图。
3. action_result 入库目前偏“文本化报告”，`ref_message_id/ref_action_slot` 追溯链不完整。
4. followup 目前以 `pendingFollowupReport` 为中心，尚未扩展到 `state_change/progress`，也缺少“user 新输入打断 pending 并并入 history”的统一逻辑。

## 3. 分阶段实施

## Phase A：后端消息模型与数据库升级

### A1. 数据结构改造
1. 重构 `internal/message_types.go`：
   - 为 `ChatMessage` 增加并主用字段：`say`、`aside`、`action_json`、`ref_message_id`、`ref_action_slot`、`raw_data`、`parse_error`。
   - 保留必要兼容字段（用于旧调用点过渡），但统一由新字段驱动可见文本与历史拼装。
2. 新增 action 规范化工具：
   - 统一识别 `call/execute/report/progress/state_change`。
   - 保证 `action_json` 非空时含 `type`。

### A2. SQLite schema 升级
1. 在 `internal/sqlite_store.go` 重置 `messages` 表为新列集合（按 PRD 6.1）。
2. 新增索引：`seq`、`turn_id+seq`、`ref_message_id+ref_action_slot+created_at_ms`、`role+created_at_ms`。
3. 更新 `needsSchemaReset` 判定：旧消息表结构自动触发 reset。

### A3. 读写链路切换
1. `AppendMessage/loadByQuery/LoadSessionWindow/LoadContextBefore` 全链路改用新列。
2. `LoadContextBefore` 默认仅返回 `user/assistant`；show more 打开时支持全角色历史。
3. 保证时间语义字段与 `message_id/seq/store_id` 行为不回退。

## Phase B：后端会话、LLM 解析与 observer 写入

### B1. assistant JSON 输出解析落地
1. 新增 assistant final 解析器：
   - 解析 `say/aside/action`（兼容 fenced JSON）。
   - 失败时写 `parse_error`，并用 `raw_data.raw_text` 兜底。
2. `llm_delta/llm_final` 的存储逻辑切换到“raw 保存 + 投影展示”模型。

### B2. observer 消息标准化
1. `handleActionResult` 生成两条 observer 消息：
   - `type=execute`（开始执行）
   - `type=report`（执行结果）
2. `handleStateChange` 统一写 `type=state_change` action 消息。
3. 所有 report/progress/execute/state_change 填充 `ref_message_id/ref_action_slot`（可追溯 call）。

### B3. followup 缓冲机制升级
1. 将 pending 数据从“仅 report”扩展为“可触发 followup 的 observer 消息集合”。
2. 去抖定时器改为 1 秒上限触发 continuation。
3. 当 user 新输入触发 `startTurn` 时：
   - 立即结束 pending 的单独 continuation 机会；
   - pending observer 消息直接保留在 history 里参与本次 user turn 推理。

## Phase C：前端渲染与 action-engine 升级

### C1. 聊天气泡三段式渲染
1. `chat-store.js` 改造消息节点结构：主文本（say）、小字（aside）、动作标识（A）。
2. 默认视图仅展示 `user/assistant`；observer/system 默认隐藏。
3. parse_error 默认样式展示“格式异常”并显示 `raw_text` 前缀。

### C2. show more 调试视图
1. 在 `webui/page/chat/index.html` 增加 show more 开关。
2. 开启后每条消息附加展示：
   - `action_json` pretty print
   - `parse_error`
   - `raw_data` pretty print
   - 时间语义字段
3. 开启后可显示 observer/system 消息。

### C3. action-engine 协议切换
1. 从旧 `content/action.name` 过渡到新 `say/aside/action`。
2. 处理 assistant 解析失败场景（不上报非法动作，只做错误可视化）。
3. 保持与后端 `action_result/state_change` 控制消息兼容。

## Phase D：验证与回归

### D1. 后端单测
1. `sqlite_store_test.go`：校验新字段落库与查询过滤。
2. `session_action_result_test.go`：校验 `call/execute/report` 链路与 ref 追溯。
3. 新增解析测试：assistant JSON 成功/失败分支。

### D2. 前端行为核验（最小可验证）
1. 手工验证默认 UI 与 show more 切换。
2. 手工验证 parse_error 样式与 raw_data 展示。
3. 手工验证 action A 标识与动作执行后 observer 链路插入。

### D3. 工程验证
1. `go test ./...`
2. `go build -buildvcs=false ./...`

## 4. 风险与应对

1. **模型改动面广，兼容风险高**：通过“保留兼容字段 + 新字段主驱动”降低一次性切换风险。
2. **历史查询与前端渲染耦合**：先稳定后端查询契约，再切前端展示。
3. **action 引用链断裂风险**：统一由后端生成 `ref_message_id/ref_action_slot`，前端仅透传 action 执行结果。
4. **followup 竞态**：使用单点状态机（pending + timer + continuationRunning）并补测试覆盖。

## 5. 验收标准（DoD）

1. `messages` 表和代码结构可验证地满足 PRD 第 6 节核心字段要求。
2. assistant JSON 解析成功/失败两条路径均可见、可调试、可追溯。
3. 默认 UI 与 show more 行为符合 PRD 第 7 节。
4. action 链路具备 `call -> execute -> report` 与 ref 追溯能力。
5. followup 缓冲策略满足“最多 1 秒一次 + user 新输入并入 history”。
6. 验证命令通过，且无新增编译错误。
