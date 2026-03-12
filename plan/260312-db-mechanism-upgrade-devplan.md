# 开发计划文档：数据库机制升级与存储分层重构

- 计划时间：2026-03-12 12:59 CST
- 对应需求：`plan/260312-1239-db-mechanism-upgrade-prd.md`
- 计划文档：`plan/260312-db-mechanism-upgrade-devplan.md`
- 计划基线：以当前工作树真实代码为准，而非历史计划文档或已过期实现假设
- 信息来源：
  - `doc/_instruction.md`
  - `doc/_devlog.md`
  - `main.go`
  - `internal/sqlite_store.go`
  - `internal/session.go`
  - `internal/protocol.go`
  - `internal/message_types.go`
  - `webui/page/chat/chat-store.js`
  - `webui/page/chat/event-router.js`
  - `webui/page/chat/session-controller.js`
  - `webui/page/chat/io-worker.js`
  - `webui/page/chat/action-engine.js`
  - `webui/page/chat/surface-bridge.js`
  - `webui/page/surface/manifest.js`
  - `webui/page/surface/surface-manager.js`
  - `webui/surface/demo-counter.html`

## 1. 评估结论

该 PRD 的主方向是成立的，且与项目已确认的中长期数据分层约定一致：
1. 配置继续保留在 JSON。
2. 高价值结构化数据统一收敛到单一 SQLite。
3. 低价值高吞吐过程日志独立分流，避免污染主库。
4. action 纳入 message 主链路，避免双写和语义分叉。
5. `surface_id / surface_version` 被提升为持久化兼容边界，这是后续 surface 生态扩展所必需的。

但 PRD 直接进入开发会有四类风险，必须先在计划中修正：
1. 现状引用有部分过期。
   - PRD 提到了 `internal/action_record_store.go` 和 `action_records.jsonl` 的现状依据，但当前工作树中该文件已删除，说明“action 双写”这部分已经不再是现状主线，开发计划必须按当前代码重写基线。
2. 现网代码仍是 `user + chat` 运行模型。
   - `main.go` 仍以 `-user-id` 与 `-chat-id` 初始化 `SQLiteStore`，`internal/sqlite_store.go` 的 schema 和查询也围绕 `chat_id` 展开。
   - PRD 直接切到 `user -> project -> thread -> message`，如果没有兼容层，会同时打断会话写入、历史拉取、surface state 和 LLM prompt 组装。
3. 前端历史 UI 目前只消费“聊天投影视图”，不是“全量消息流”。
   - `webui/page/chat/chat-store.js` 仅按 `role/content/message_id/created_at_ms` 平铺历史。
   - 当前 `fetch_history` 实现只返回 `CategoryChat + user/assistant`，如果直接改成返回全量 action/surface/phase，会污染聊天区展示和滚动逻辑。
4. surface 协议尚未达到 PRD 要求。
   - `webui/page/surface/manifest.js` 只解析 `surface_id/title/description/actions`，没有 `surface_type/surface_version`。
   - `webui/surface/demo-counter.html` 的 manifest 也未声明 `surface_type/surface_version`。
   - `webui/page/surface/surface-manager.js` 目前会为同名 surface 自动改名，这与“每用户维度全局唯一、可持久化复用”的约束冲突。

结论：
1. 该需求适合拆成“存储底座重构 + 写路径切换 + 读路径兼容 + surface 协议升级 + 前端历史/UI 适配 + 重置/验证”六个阶段推进。
2. 第一版不应同时引入完整的多 project/thread 可视化前端；应先在后端落地新层级，并为当前聊天页提供默认 project/thread 的透明兼容映射。
3. 聊天页需要继续保留“聊天投影视图”和“全量消息流”两个读模型，避免数据库升级时把 observer/surface/system 事件直接暴露成普通聊天气泡。

## 2. 当前代码现状与关键差距

### 2.1 后端存储现状

1. `main.go` 当前默认路径仍是 `data/users/default/chat_state.db`，不是 PRD 目标中的 `data/kagent.db`。
2. `internal/sqlite_store.go` 当前已包含：
   - `users`
   - `chats`
   - `messages`
   - `surface_states`
3. `messages` 已是统一消息模型，但关键主键和查询维度仍是：
   - `message_id`
   - `user_id`
   - `chat_id`
   - `turn_id`
   - `seq`
4. `surface_states` 当前主键是 `(user_id, chat_id, surface_id)`，与 PRD 的 `(user_id, surface_id)` 不一致。
5. `LoadSessionWindow` 与 `LoadContextBefore` 仍以 `chat_id` 和 `created_at_ms` 为主要查询边界。

### 2.2 会话与消息写入现状

1. `internal/session.go` 已将以下事件写入统一 `messages`：
   - `chat.user_message`
   - `chat.assistant_message`
   - `ai_action.call`
   - `ai_action.report`
   - `surface.surface_open`
   - `surface.surface_state`
   - `surface.surface_change`
   - `phase.convo_start`
   - `phase.convo_stop`
   - `phase.page_close`
   - `phase.turn_nack`
   - `config.config_change`
2. action report 与 surface state 已不再依赖独立文件存储才能成立，说明 PRD 的“统一进入 message 主链路”方向已经部分被当前代码吸收。
3. `bootstrapHistoryFromSQLite` 和 LLM 上下文构造依赖当前 `SQLiteStore` 的读接口，因此底层 schema 变更必须同步改造 prompt 装配与历史恢复。

### 2.3 前端与协议现状

1. `webui/page/chat/io-worker.js` 与 `internal/protocol.go` 目前只传递：
   - `cursor`
   - `surface_id`
   - `state_version`
   - `config_changed_paths`
   - action 结果相关字段
2. 聊天页历史滚动目前以 `created_at_ms` 作为游标，不支持新的自增主键游标，也不支持复合 cursor。
3. `webui/page/chat/action-engine.js` 与 `surface-bridge.js` 已经把 surface 事件回写到后端，但只理解：
   - `surface_id`
   - `state_version`
   - `business_state`
4. 当前 UI 没有 project/thread 切换器，意味着第一版应维持单活跃 thread 体验，不在本次升级中强行引入新的页面操作心智。

### 2.4 本次升级必须补齐的差距

1. 路径切换：
   - `chat_state.db` -> `data/kagent.db`
2. 逻辑层级切换：
   - `chat_id` -> `project_id + thread_id`
3. surface 主键切换：
   - `(user_id, chat_id, surface_id)` -> `(user_id, surface_id)`
4. 历史 cursor 切换：
   - `created_at_ms` -> 稳定主键游标
5. 协议字段补齐：
   - `surface_type`
   - `surface_version`
   - `project_id`
   - `thread_id`
   - 新 cursor 结构
6. 前端读模型分离：
   - 聊天展示视图
   - 全量消息数据视图

## 3. 本次开发目标与实施边界

### 3.1 本次会实现

1. 新的高价值库 `data/kagent.db` 初始化、重置与接线。
2. `user -> project -> thread -> message` 的后端持久化主链条。
3. 当前聊天页在不新增复杂入口 UI 的情况下，透明运行在默认 `project/thread` 上。
4. `surface_states` 切换到 `(user_id, surface_id)` 维度，并补齐 `surface_type/surface_version`。
5. 历史拉取协议与聊天页适配到新的稳定 cursor。
6. action / surface / config / phase / chat 全链路继续写入统一消息库。
7. operation 日志目录、写入器和最小字段约定落地。
8. 旧 DB 与旧 action JSONL 的重置删除流程、自动化验证和回归脚本。

### 3.2 本次不会实现

1. 完整的多 project/thread 前端管理界面。
2. 云同步、跨设备冲突合并或导入旧历史数据。
3. 完整 butler/worker operation 生态，只落最小写入框架和当前链路可产生的基础记录。
4. 文件索引、thread summary、memory 的完整业务功能。
   - 本次只要求 schema 与扩展点准备好；若当前业务尚无可靠生产路径，不强行造假数据。

## 4. 关键设计决策

### 4.1 兼容映射策略

第一阶段不直接废掉当前“单聊天页 = 单活动会话”的产品体验，而是在后端引入默认映射：
1. `user_id` 继续沿用当前运行时值。
2. 自动创建默认 `project_id`，建议使用稳定值，例如 `project-default`。
3. 当前 `chat-default` 语义收敛为默认 `thread_id`，避免前端现有心智突然变化。
4. 前端在本次升级里无需新增 project/thread 选择 UI，但协议和后端 store 需要为未来多实例扩展留口。

### 4.2 读模型分离策略

数据库里保留全量 message 流，但对外至少提供两类读接口：
1. `thread full stream`
   - 给 LLM、调试、后续管理页、导出与高价值分析使用。
2. `chat projection`
   - 只给当前聊天页历史区使用，继续过滤为用户和助手主对话，必要时再有选择地混入少量 system 文本。

原因：
1. 这样才能满足 PRD 的“全量统一持久化”。
2. 同时不破坏 `webui/page/chat/chat-store.js` 当前的滚动、渲染与交互假设。

### 4.3 历史 cursor 策略

当前 `created_at_ms` 作为游标不再可靠，原因是：
1. 同毫秒多消息下排序不稳定。
2. 切到单库后跨 thread/project 查询更容易碰撞。

本次计划采用：
1. 库内自增整数主键作为历史分页 cursor。
2. 前端协议改为传输明确的 `before_id` 或等价 cursor 字段。
3. `created_at_ms` 继续保留为显示和时间语义字段，不再承担唯一分页职责。

### 4.4 surface 标识策略

PRD 要求 `surface_id` 每用户维度全局唯一，这会与当前 `surface-manager.js` 的自动重命名冲突。开发计划按以下方式收口：
1. 进入持久化的 surface 必须声明稳定 `surface_id`。
2. `surface_type` 与 `surface_version` 升级为 manifest 必填字段。
3. 聊天页和 surface 工作台都不再对“可持久化 surface”做静默自动改名。
4. 如果未来需要同类 surface 多开，必须显式引入“实例 id”和“模板 id”双层概念；本次不在无设计的情况下隐式支持。

### 4.5 重置优先于迁移

本需求已明确采用全量重置，因此开发上不做 legacy 数据迁移，只做：
1. 启动前删除旧库和相关 `-wal/-shm`。
2. 启动时重新初始化 `data/kagent.db`。
3. 以自动化测试确保旧路径不再被写入。

## 5. 分阶段开发计划

### 阶段 A：冻结契约与修订 PRD 基线

目标：先把需求和真实代码对齐，避免按过期现状开发。

实施项：
1. 修订实现基线说明。
   - 删除对 `internal/action_record_store.go` 的现状依赖描述。
   - 将“当前已写入统一 messages”的事实明确记录到实施说明。
2. 补齐必须先拍板的契约。
   - 默认 `project_id/thread_id` 命名
   - 历史接口 cursor 字段命名
   - `surface_type/surface_version` 的最小合法值
   - operation 日志最小字段集合
3. 形成“数据库模型变更清单”和“协议字段变更清单”，作为后续开发单一事实源。

完成标志：
1. 后端 schema、协议、前端适配三方使用同一组字段定义。
2. 不再存在“PRD 以为有、代码里其实没有”的依赖。

### 阶段 B：高价值库底座与启动重置

目标：让应用从 `data/kagent.db` 启动，并把旧库彻底退出主路径。

实施项：
1. 修改 `main.go` 默认存储路径和初始化参数。
2. 重构 `SQLiteStore` 为新库上下文。
   - 维护 `user_id`
   - 维护默认 `project_id`
   - 维护默认 `thread_id`
3. 新建或重建 schema：
   - `users`
   - `projects`
   - `threads`
   - `messages`
   - `surface_states`
   - 为后续扩展预留 `thread_summaries / memories / files / file_refs`
4. 落地启动重置流程。
   - 删除 legacy `chat_state.db*`
   - 删除试验版 `kagent.db*`
   - 删除 `action_records.jsonl`
5. 为重置流程增加明确日志和幂等保护。

完成标志：
1. 应用首次启动即可只依赖 `data/kagent.db` 成功运行。
2. legacy 路径不再生成新文件。

### 阶段 C：消息写路径改造

目标：在新主链条上保持当前会话、action、surface、config、phase 行为不回退。

实施项：
1. `internal/session.go` 写入改为落 `project_id/thread_id`。
2. `messages` 主键体系调整为：
   - `id` 自增主键
   - `message_uid` 稳定唯一键
3. 保留当前统一消息分类常量与 `BuildMessage` 语义化内容生成能力。
4. 审查所有写入点，确保不会遗漏：
   - `start/stop/page_close/turn_nack`
   - user/assistant
   - action call/report
   - surface open/change/state
   - config change
5. 落地 operation writer，但生产范围只覆盖当前真实可产生的流程信号。
   - 不伪造 worker/huddle 数据
   - 仅为后续扩展留稳定文件格式和追加写机制

完成标志：
1. 当前聊天页所有核心交互都能继续写入新库。
2. action 与 surface 事件不会因 schema 切换而丢失。

### 阶段 D：历史读取、上下文装配与聊天页兼容

目标：保证数据库升级后，历史滚动、冷启动恢复和 LLM 上下文都继续可用。

实施项：
1. 重写 `LoadSessionWindow` 与 `LoadContextBefore`。
   - 输入维度改为 `user_id/project_id/thread_id`
   - cursor 改为新主键
2. 明确分离两类读取：
   - 给 LLM 的全量上下文窗口
   - 给聊天页的聊天投影视图
3. 更新 `internal/session.go` 冷启动逻辑：
   - `bootstrapHistoryFromSQLite`
   - prompt 组装依赖的新查询接口
4. 升级协议与 worker：
   - `internal/protocol.go`
   - `webui/page/chat/io-worker.js`
   - `webui/page/chat/session-controller.js`
5. 升级聊天页历史 UI：
   - `webui/page/chat/chat-store.js` 改用新 cursor
   - 仍只展示聊天投影，避免 observer/surface 污染 UI

完成标志：
1. 冷启动历史恢复正常。
2. 上滑加载历史正常。
3. 聊天区不出现无意义的 action/surface 内部消息刷屏。

### 阶段 E：surface 协议与 UI 适配

目标：让 `surface_id / surface_type / surface_version` 真正成为持久化与兼容边界。

实施项：
1. 扩展 manifest 解析。
   - `webui/page/surface/manifest.js`
   - 强校验 `surface_id`
   - 增加 `surface_type`
   - 增加 `surface_version`
2. 升级聊天页 surface 侧桥接。
   - `webui/page/chat/surface-bridge.js`
   - `webui/page/chat/action-engine.js`
   - 在 `state_change/action_result/surface_open` 中携带新增字段
3. 升级 surface 工作台。
   - `webui/page/surface/surface-manager.js`
   - 不再对可持久化 surface 静默改名
4. 升级示例 surface。
   - `webui/surface/demo-counter.html`
   - 补齐 manifest 字段
   - 明确 state 版本递增规则
5. 调整后端 `surface_states` 表和 upsert/load 逻辑。

完成标志：
1. surface state 可按 `(user_id, surface_id)` 跨 thread/project 复用。
2. manifest 缺字段时会被明确拒绝，而不是悄悄写出不可恢复数据。

### 阶段 F：回归验证与发布门禁

目标：在“全量重置 + 单库切换 + 协议升级”后保证整条链路可回归。

自动化测试最少覆盖：
1. 新库 schema 初始化。
2. 启动重置删除 legacy 文件。
3. message 写入与新 cursor 分页。
4. 聊天投影视图过滤正确。
5. surface state 主键切换后的 upsert/load。
6. manifest 缺失 `surface_type/surface_version` 的失败分支。
7. `page_close/config_change/action_result/state_change` 在新库下的回归。

手工联调最少覆盖：
1. 冷启动加载历史。
2. 开始对话 -> 语音输入 -> LLM -> TTS。
3. action 调用成功/失败/人工确认取消。
4. surface 打开、状态变化、重载、再次进入会话。
5. 页面关闭后重开，数据仍能从新库恢复。
6. 升级后确认旧路径不再产生新文件。

## 6. 任务拆解

### 6.1 后端任务

1. 重构 `main.go` 的默认数据库路径、默认 project/thread 初始化参数和重置入口。
2. 重构 `internal/sqlite_store.go`：
   - schema
   - 初始化
   - 写接口
   - 历史读接口
   - surface state 读写
3. 扩展 `internal/protocol.go` 以承载新 cursor 与新增 surface 字段。
4. 调整 `internal/session.go` 所有 message 写入点和历史恢复调用点。
5. 如 `internal/llm.go` 或 prompt 组装读取接口依赖旧 `chat_id`，同步迁移到 `thread` 维度。
6. 增加 operation writer 与文件路径管理。
7. 补齐或重写 `internal/sqlite_store_test.go`、`internal/session_logic_test.go`、`internal/session_action_result_test.go` 等回归测试。

### 6.2 前端聊天页任务

1. 调整 `webui/page/chat/io-worker.js` 传输新 cursor 和新增字段。
2. 调整 `webui/page/chat/session-controller.js` 的首屏历史拉取与 page close 兼容。
3. 调整 `webui/page/chat/chat-store.js`：
   - 使用新 cursor
   - 保持滚动定位稳定
4. 调整 `webui/page/chat/event-router.js`，确保 `history_sync` 结构变更后仍能安全消费。
5. 调整 `webui/page/chat/action-engine.js` 和 `surface-bridge.js`，补齐 `surface_type/surface_version` 流转。
6. 视历史项结构变化，微调 `webui/page/chat/index.html` 中的 UI 事件接线与展示文案。

### 6.3 Frontend Surface 任务

1. 调整 `webui/page/surface/manifest.js` 的字段解析与校验。
2. 调整 `webui/page/surface/surface-manager.js` 的 surface 标识策略。
3. 更新示例 surface manifest 和状态事件。
4. 确认 `webui/page/surface/action-dispatcher.js` 不会因 surface 标识规则变化而失配。

## 7. 主要风险与缓解策略

1. 风险：单次改动面过大，后端与前端协议容易错位。
   - 缓解：先冻结字段契约，再分阶段提交；历史接口和 surface 协议必须先有测试后接 UI。
2. 风险：全量重置误删当前仍需保留的 JSON 配置。
   - 缓解：删除清单白名单化，只触达 legacy DB、`-wal/-shm` 和旧 action JSONL。
3. 风险：聊天页历史区直接吃全量消息，导致 UI 噪音和滚动异常。
   - 缓解：后端提供聊天投影视图接口，不让前端自己从全量流中盲过滤。
4. 风险：surface 自动改名与持久化唯一标识冲突，导致旧状态无法恢复。
   - 缓解：本次明确禁止静默改名；需要多开时另起设计，不在本需求里偷渡。
5. 风险：`thread/project` 逻辑引入后，当前 LLM prompt 读取不到正确上下文。
   - 缓解：把 prompt 装配放入阶段 D 同步改造，不能只换写路径不换读路径。
6. 风险：operation 需求边界过大，拖慢主库重构。
   - 缓解：本次只落文件分桶和最小 writer，不要求覆盖未来所有 agent 角色。

## 8. 验收口径

本次开发完成后，至少满足以下标准：
1. 应用默认仅使用 `data/kagent.db` 作为高价值数据主库。
2. 升级后 legacy `chat_state.db*` 和 `action_records.jsonl` 不再被写入。
3. 当前聊天页的开始对话、历史上拉、action 回写、surface 状态同步、配置变更上报全部可用。
4. 聊天区展示仍保持“用户/助手主对话”体验，不被内部消息刷屏。
5. surface state 已按 `(user_id, surface_id)` 维度保存，并包含 `surface_type/surface_version`。
6. 新库读写、重置逻辑、历史分页和 surface 协议至少有基础自动化回归。

## 9. 推荐实施顺序

1. 先做阶段 A。
   - 没有字段契约冻结，不要直接改 schema。
2. 再做阶段 B。
   - 先让应用能稳定在 `data/kagent.db` 启动。
3. 然后做阶段 C 和阶段 D。
   - 写路径和读路径必须成对升级。
4. 再做阶段 E。
   - surface 协议升级要建立在新库和新消息主链条已经稳定的前提上。
5. 最后做阶段 F。
   - 没有全链路验证，不建议进入正式升级。

## 10. 待确认事项

1. 默认 `project_id` 与默认 `thread_id` 的最终命名是否沿用 `project-default` / `chat-default`，还是统一改成 `thread-default`。
2. 本次是否要求把 `thread_summaries / memories / files / file_refs` 一并建表，还是只保留迁移脚手架。
3. 聊天页是否需要在本次升级中暴露“切换 thread”的 UI 入口；若不需要，应在计划内明确维持单活跃 thread。
4. operation 日志压缩格式是否立即上 `zst`，还是先保留纯 `jsonl`，等验证稳定后再压缩。
