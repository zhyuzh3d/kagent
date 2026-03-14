# 项目说明（doc/_instruction.md）

## 1. 项目概览
`kagent` 是一个单机运行的实时语音对话 MVP：浏览器页面通过 WebSocket 与 Go 服务通信，形成 `ASR -> LLM -> TTS` 的单会话闭环。

运行环境约定（会话确认）：
- 目标设备：近 5 年内、内存 8GB 或以上。
- 不考虑过老/低配设备的兼容性优化。

当前真实状态（基于 2026-03-14 代码扫描、运行验证与本轮会话核验）：
1. 后端主链路已经稳定在“单会话状态机 + 每个输入 turn 独立 ASR + 流式 LLM + 句级 TTS backlog 拼组”这一结构上。
2. 前端聊天页已从单个大脚本拆为多个运行模块，`index.html` 完成了侧边栏 (Sidebar) 架构升级。
3. **项目与会话管理已落地**：支持动态切换项目 (Project) 和会话 (Thread)，支持 CRUD 操作与拖拽 (Drag & Drop) 排序/移动。
4. WebSocket 握手已支持通过 URL Query Params 动态绑定目标 project/thread 上下文。
5. 后端 `SQLiteStore` 补齐了 `order_index` 字段支持及物理隔离的 CRUD 接口。
6. `messages` 已升级为分层消息模型：展示字段（`say/aside`）与调试字段（`action_json/raw_data/parse_error`）并存，支持 call 引用链字段（`ref_message_id/ref_action_slot`）。
7. 聊天页已支持 `show more` 调试视图：可查看 observer/system 消息及 `action_json/parse_error/raw_data` 详情；默认仍仅展示 user/assistant。
8. 旧 turn `partial` 气泡在 stale 场景下的收口边界问题仍待后续浏览器实机回归确认。

## 2. 核心设计原则
1. **单一事实源 (Single Source of Truth, SSoT)**: 所有的配置、版本和状态应尽量维持单一事实源，避免多副本同步导致的冲突。
2. **全客户端驱动 (Client-Driven) 架构**: 前端胖客户端 (Rich Client) + 后端瘦持久层 (Thin Server)。UI 在哪里，决策和状态就在哪里，后端仅负责传输、持久化和原子操作，不干预业务判定。

## 3. 当前目录结构（关键层级）
> 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage` 等。

```text
kagent/                                              # 仓库根目录
├── config/                                          # 配置目录
│   ├── config.json                                  # 公开默认配置
│   ├── configx.json                                 # 私密接入配置
│   └── configx.json.example                         # 私密配置模板
├── doc/                                             # 文档目录
│   ├── _devlog.md                                   # 开发日志（只追加）
│   └── _instruction.md                              # 项目说明（本文件）
├── internal/                                        # Go 后端核心实现
│   ├── config.go                                    # 私密模型配置加载
│   ├── public_config.go                             # 公开配置结构与默认值
│   ├── runtime_config.go                            # 公开配置加载、合并与保存
│   ├── session.go                                   # 会话状态机与 turn 生命周期
│   ├── asr.go                                       # ASR Provider
│   ├── llm.go                                       # LLM 流式请求
│   ├── tts.go                                       # TTS Provider
│   ├── pipeline.go                                  # 句级 backlog 拼组与 TTS 编排
│   ├── protocol.go                                  # 前后端协议
│   └── *_test.go                                    # 后端相关测试
├── plan/                                            # 需求、计划与结果文档
├── scripts/                                         # 本地脚本目录
│   ├── deploy.sh                                    # 构建与重启脚本
│   └── gitpush.sh                                   # 版本 bump 与 Git 推送脚本
├── webui/                                           # 前端静态资源根目录
│   ├── json/                                        # 前端元数据目录
│   │   └── config_info.json                         # 配置抽屉字段说明
│   └── page/
│       ├── account/                                 # 账户登录/注册页面
│       │   └── index.html                           # 账户页入口
│       └── chat/                                    # 实时对话页面目录
│           ├── index.html                           # 页面入口与装配层
│       ├── config-store.js                          # 配置读取/保存工具
│       ├── config-drawer.js                         # 配置抽屉
│       ├── sidebar-controller.js                     # 侧边栏项目与会话管理
│       ├── chat-store.js                            # 消息与气泡状态
│       ├── audio-playback.js                        # 播放队列与音频上下文
│       ├── audio-capture.js                         # 采集、降采样、抢话
│       ├── event-router.js                          # 协议事件路由
│       ├── session-controller.js                    # 会话控制及动态重连
│       └── io-worker.js                             # Worker 内 WS 与 VAD 定时器
├── main.go                                          # HTTP 服务入口
└── version.json                                     # 前后端版本单一事实源
```

## 3. 核心模块职责
1. `main.go`
- 启动 HTTP 服务、`/ws`、`/version`、`/api/config`、`/api/auth/*` 与静态资源服务。
- 负责 JWT 鉴权校验与 WebSocket 身份提取。

2. `internal/session.go`
- 维护浏览器会话生命周期。
- 处理 `start / stop / start_listen / interrupt / trigger_llm` 等控制消息。
- 协调 ASR、LLM、TTS 的一轮输入与回复。

3. `internal/asr.go`、`internal/llm.go`、`internal/tts.go`
- 分别负责识别、生成、合成三段外部能力接入。

4. `internal/pipeline.go`
- 将 LLM 增量文本按句切分。
- 根据 backlog 时长决定每次送多少句给 TTS。

5. `internal/runtime_config.go`
- 负责公开配置读取、与用户覆盖配置合并，以及保存 overrides。

6. `webui/page/chat/index.html`
- 负责页面入口、DOM 绑定、模块装配和版本展示。

7. `webui/page/chat/session-controller.js`
- 负责会话启动/停止、Worker 生命周期、WebSocket 建连和 `trigger_llm` 触发。

8. `webui/page/chat/audio-capture.js` 与 `audio-playback.js`
- 前者负责麦克风采集、降采样、抢话与音频上行。
- 后者负责 TTS 音频接收、播放队列和播放中断。

9. `webui/page/chat/chat-store.js` 与 `event-router.js`
- 前者负责消息气泡与 partial/final 状态。
- 后者负责前后端协议事件分发和 stale 过滤。

10. `webui/page/chat/config-drawer.js`
- 负责左侧配置抽屉，仅用于运行时体验调节，不参与主链路对话编排。

11. **多上下文切换 (Context Switching)**:
    - 侧边栏点击线程时，触发 WebSocket 带参数重连，由 `session-controller.js` 协调 `chat-store.js` 自动加载新线程的历史记录。
12. `webui/page/chat/sidebar-controller.js`
- 负责侧边栏项目与线程的 CRUD、动态切换、手放排序及跨项目移动，是多会话管理的中心枢纽。

## 4. 当前工作方式
### 4.1 前端
1. 点击开始后，前端创建 Worker、建立 `/ws`、启动麦克风。
2. **智能避让与确权打断 (Smart Ducking & Commit Stop)**:
   - **VAD Ducking**: 前端 VAD 检测到声音即刻平滑降低 AI 音量 (10%) 并开始 ASR，提供“我在听”的物理反馈。
   - **ASR Recovery**: 若 ASR 未识别出有效意图或声音消失，AI 音量平滑恢复 (100%)。
   - **Commit Stop**: 只有当 ASR 识别出具有打断意义的文字 (如“停下”、“等一下”) 时，前端才物理停止播放并向后端发送 `interrupt` 指令。
3. `asr_partial / asr_final` 更新用户气泡，`llm_delta / llm_final / tts_chunk` 更新 AI 回复和播放队列。
4. 调试时可开启 `show more`：前端会在消息气泡补充时间语义字段，并展示 `action_json / parse_error / raw_data`。

### 4.2 后端
1. 每个输入 turn 使用独立 ASR 连接。
2. `trigger_llm` 时显式 `Finish()` 当前 ASR，并等待最终文本收口。
3. LLM 增量文本经句级切分后进入 TTS backlog 编排，再下发给前端播放。
4. 空文本 turn 会返回 `turn_nack`，避免空输入误推进。

### 4.3 配置模块
1. 私密接入配置放在 `config/configx.json`。
2. 公开默认配置放在 `config/config.json`。
3. 用户覆盖配置保存在 `data/users/default/user_custom_config.json`。
4. 前端通过 `GET /api/config` 读取，通过 `PUT /api/config` 保存。
5. 左侧配置抽屉只展示 `webui/json/config_info.json` 里声明过的字段。

### 4.4 数据与存储（现状与规划约定）
按数据价值与访问模式分层存储。引入 JWT 认证后，系统支持真正的多用户身份隔离，所有个人数据按 `user_id` 严格解耦。

#### 4.4.1 当前落盘现状（可核验）
1. JSON 配置文件：
- 全局公开默认配置：`config/config.json`
- 私密接入配置：`config/configx.json`
- JWT 签名密钥：`data/.jwt_secret`（持久化密钥，确保重启后 Token 依然有效）
- 用户覆盖配置：`data/users/<user_id>/user_custom_config.json`

2. SQLite 主库（多用户高价值数据）：`data/kagent.db`
- 负责存储：`users/projects/threads/messages` 等。
- **严格隔离**：后端 `SQLiteStore` 在建连时会强制锁定 `user_id`，查询历史记录、项目列表等操作均带 ID 过滤，防止跨用户数据泄漏。
- `users` 表已扩展 `username` 与 `password_hash` 字段支持登录。
- `messages` 表当前以 `say/aside/action_json/ref_message_id/ref_action_slot/raw_data/parse_error` 作为核心字段，并保留 `message_uid + seq + 时间语义字段` 作为可追溯主轴。

3. 低价值高吞吐过程日志（operation，按用户按日分桶）：
- 路径约定：`data/users/<user_id>/ops/<YYYYMMDD>.jsonl`
- 用途：记录 action report、surface state_change 等“过程类”事件，避免污染主消息流与高价值查询。

#### 4.4.2 演进约定（部分已落地）
1. 三种存储方式（落盘形态约定）：
- JSON 文件存配置：全局 config + 每用户 override（不进入 SQLite）。
- 单一 SQLite 高价值库：`data/kagent.db`（已落地为默认主库）。
- 低价值高吞吐日志：operation 等过程数据以“按用户、按天分桶”的 JSONL 形式落在 `data/users/<user_id>/ops/` 下（已落地；当前未实现自动压缩归档）。

2. 高价值数据主链条（概念模型）：
- `app -> user -> project -> thread -> message`
- summary 与 memory：summary 为 thread 级一对一；memory 为从 thread 抽取的记录，间接归属 project（可冗余保存 `project_id` 便于聚合查询）。
- message 唯一性：不依赖 `turn_id`。库内使用自增 `id` 作为查询游标，同时保留 `message_uid` 作为跨系统稳定标识。

3. action 与 operation 的边界与执行模型（前端接管 Action）：
- **全客户端驱动 (Client-Driven) 架构模式**：采用“前端胖客户端 (Rich Client) + 后端瘦持久层 (Thin Server)”的原则。所有 Surface 和业务状态的生命周期（如打开、关闭）由前端主导并告知后端。所有的 `action_call`（如 `surface.get_state` 或操作 UI）必须下发到前端执行，由前端统一出具 `action_report`。后端不应拦截读库代办业务，仅负责 WebSocket 中转分发和持久化（写入 `messages` 数据流和持久化缓存库）。
- action：用户与主 AI 在 surface 上的真实互动动作记录，直接进入 message 数据流并写入高价值库。
- operation：AI 系统在后台默默执行的任务过程记录（如大模型思考链路、宿主 API 调用等），不进入 message 聊天流，只落入过程分桶日志用于溯源与训练。
- Surface 打开/关闭的动作协议约定（对齐前端 ActionEngine 与 LLM 提示词）：
  - 打开：先 `get_surfaces`（需 `followup=report`）拿列表，再命中目标时 `open_surface(target)`（需 `followup=report`）；未命中则直接回复找不到且不发动作。
  - 关闭：直接 `close_surface(target)`（需 `followup=report`），由前端执行并回传 report 再驱动 LLM 继续回复。

4. surface state（跨 project/thread 复用）：
- 当前落盘：`surface_states` 以 `(user_id, surface_id)` 为主键持久化快照，包含 `surface_type/surface_version/state_version/status/visible_text/business_state_json` 等字段。
- `surface_id` 约定为每用户维度全局唯一，用于跨 thread/project 复用 surface state。
- `surface_version` 用于 surface 升级后的 state 兼容与迁移判定；`surface_type` 用于分类（例如 `app`/`game`/`plan`）。

## 5. 最近关键变更摘要
1. 消息模型升级到 `say/aside/action_json/raw_data/parse_error`，并补齐 `ref_message_id/ref_action_slot` 引用链字段。
2. assistant JSON envelope 增加后端解析与异常兜底（解析失败可回放 raw 文本并打标）。
3. observer action 链路整理为 `call -> execute -> report`，并引入 followup pending 去抖（1 秒窗口）。
4. 聊天页新增 `show more` 开关，可查看 observer/system 与调试字段详情。
5. 默认主库为 `data/kagent.db`，operation 继续按 `data/users/<user_id>/ops/<YYYYMMDD>.jsonl` 分桶落盘。
6. 身份认证与多用户隔离保持生效（JWT + 用户维度数据强隔离）。
7. 修复点击“开始对话”重复加载历史消息的 Bug，并补齐前端消息 ID 去重防御。
8. **落地项目 (Project) 与线程 (Thread) 管理系统**：实现后端 CRUD/Migration 及前端侧边栏交互。
## 6. 项目术语表
| 术语                       | 定义（本项目语境）                                                                                                                                                                                 | 来源文件                                                                                            | 状态   |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- | ------ |
| `JWT`                      | 用于用户身份标识与权鉴的令牌，存储在 `kagent_token` Cookie 中。                                                                                                                                    | `internal/auth.go`, `main.go`                                                                       | active |
| `Auth Guard`               | 前端鉴权守卫，在对话页加载前检查身份，失效则重定向至 `page/account/`。                                                                                                                             | `webui/page/chat/index.html`                                                                        | active |
| `app`                      | 整个本地软件实例级别的范围，例如全局 UI 或默认行为。                                                                                                                                               | `config/config.json`, `webui/json/config_info.json`                                                 | active |
| `chat`                     | 一次“开始对话”到“停止”的完整实时对话范围。比单个 turn 大，比 app 小。                                                                                                                              | `config/config.json`, `webui/page/chat/index.html`                                                  | active |
| `thread`                   | 对话线/话题边界。当前实现默认只有一个 thread（`chat-default`）；规划为每个 project 下可有多个 thread。                                                                                             | `plan/T0-26030901-chat-config-modularization-dev-plan.md`, `webui/json/config_info.json`, `main.go` | active |
| `turn`                     | 一轮用户输入加对应 AI 回复，对应前后端都在使用的 `turn_id` 语义。                                                                                                                                  | `internal/session.go`, `webui/page/chat/chat-store.js`                                              | active |
| `message`                  | turn 内更细的消息单位，通常指聊天区里的单条用户或 AI 气泡。                                                                                                                                        | `webui/page/chat/chat-store.js`                                                                     | active |
| `say`                      | 消息主展示文本（气泡主体）。                                                                                                                                                                       | `internal/message_types.go`, `webui/page/chat/chat-store.js`                                        | active |
| `aside`                    | 消息附加小字说明（气泡次级文本）。                                                                                                                                                                 | `internal/message_types.go`, `webui/page/chat/chat-store.js`                                        | active |
| `action_json`              | 消息关联动作的结构化 JSON，默认折叠，仅在 `show more` 或动作标识中体现。                                                                                                                           | `internal/sqlite_store.go`, `webui/page/chat/chat-store.js`                                         | active |
| `raw_data` / `parse_error` | assistant 原始输出与解析异常信息，用于回放与调试。                                                                                                                                                 | `internal/assistant_envelope.go`, `internal/session.go`                                             | active |
| `show more`                | 聊天页调试开关，开启后展示 observer/system 消息与调试字段。                                                                                                                                        | `webui/page/chat/index.html`, `webui/page/chat/chat-store.js`                                       | active |
| `message_uid`              | message 的跨系统稳定标识（现状：`messages.message_uid` 唯一约束；同时使用自增 `id` 作为查询游标）。                                                                                                | `internal/sqlite_store.go`                                                                          | active |
| `抢话` / `barge-in`        | AI 正在说话时，用户再次开口并打断当前回复。                                                                                                                                                        | `internal/session.go`, `webui/page/chat/audio-capture.js`                                           | active |
| `空 turn`                  | 前端推进了 turn，但后端最终没有拿到有效文本，通常会收到 `turn_nack`。                                                                                                                              | `internal/session.go`, `webui/page/chat/event-router.js`                                            | active |
| `partial 气泡`             | 前端收到 `asr_partial` 后显示的斜体用户气泡，表示这句还没正式收口。                                                                                                                                | `webui/page/chat/chat-store.js`                                                                     | active |
| `有效回复 turn`            | 前端当前仍应接收 `llm_delta / llm_final / tts_chunk` 的回复轮次，用来防止空 turn 抢走回复流。                                                                                                      | `webui/page/chat/event-router.js`, `webui/page/chat/chat-store.js`                                  | active |
| `公开配置`                 | 可以被前端读取和保存的运行时配置，不包含敏感接入信息。                                                                                                                                             | `config/config.json`, `internal/runtime_config.go`                                                  | active |
| `私密配置`                 | 只用于本地服务端接入外部能力的敏感配置，例如 Token、AppID、私有 URL。                                                                                                                              | `config/configx.json.example`, `internal/config.go`                                                 | active |
| `用户覆盖配置`             | 用户保存的个性化配置覆盖项，只记录相对公开默认配置的差异。                                                                                                                                         | `internal/runtime_config.go`, `main.go`                                                             | active |
| `配置抽屉`                 | 聊天页左侧的运行时配置面板，用于调节部分体验参数。                                                                                                                                                 | `webui/page/chat/config-drawer.js`, `webui/json/config_info.json`                                   | active |
| `mtrca`                    | 前端配置字段上的生效层级提示标签，分别代表 `message / turn / thread / chat / app`。                                                                                                                | `webui/json/config_info.json`                                                                       | active |
| `kagent.db`                | 单一 SQLite 高价值库（现状）：默认路径为 `data/kagent.db`，可通过 `--sqlite-path` 指定。可通过 `scripts/reset_db.sh [messages|log|data|all]` 进行分级清理。 | `main.go`, `internal/sqlite_store.go`, `internal/storage_reset.go`, `scripts/reset_db.sh` | active |
| `project`                  | 用户侧长期容器（规划）：管理目标、上下文、文件集合、记忆集合等；包含多个 thread。                                                                                                                  | `doc/_instruction.md`                                                                               | active |
| `summary`                  | thread 级概要（预留表）：每个 thread 仅一条（可被增量更新），用于上下文压缩与快速回忆。                                                                                                            | `internal/sqlite_store.go`                                                                          | active |
| `memory`                   | 记忆条目（预留表）：从某个 thread 抽取出的可复用事实/偏好/结论等，间接归属 project。                                                                                                               | `internal/sqlite_store.go`                                                                          | active |
| `file ref`                 | 文件引用关系（预留表）：结构化数据到文件资产的引用索引，只存链接/索引信息，不存二进制内容。                                                                                                        | `internal/sqlite_store.go`                                                                          | active |
| `surface_id`               | surface 的标识。当前协议与数据流中以 `surface_id` 引用具体 surface；规划约束为每用户维度全局唯一，用于持久化 surface state。                                                                       | `internal/protocol.go`, `doc/_instruction.md`                                                       | active |
| `surface_type`             | surface 类型（现状：随 surface state 持久化；用于分类与路由），例如 `app`/`game`/`plan`。                                                                                                          | `internal/sqlite_store.go`, `internal/session.go`                                                   | active |
| `surface_version`          | surface 版本号（现状：随 surface state 持久化；用于升级后的 state 兼容与迁移判定）。                                                                                                               | `internal/sqlite_store.go`, `internal/session.go`                                                   | active |
| `action`                   | 高价值互动动作记录：用户与主 AI 真实交互产生的动作事件。按照**客户端驱动架构**，由前端直接负责并出具 report。直接进入 message 数据流持久化。                                                       | `internal/session.go`, `doc/_instruction.md`                                                        | active |
| `operation`                | 低价值高吞吐操作日志（现状）：过程事件不进入 message 流，按用户按日分桶写入 `data/users/<user_id>/ops/<YYYYMMDD>.jsonl` 用于溯源。                                                                 | `internal/operation_log.go`, `internal/session.go`                                                  | active |
| `inputops`                 | operation 的一种（规划）：UI 输入层操作序列，例如鼠标点击、键盘输入、后台窗体挪动等。                                                                                                              | `doc/_instruction.md`                                                                               | active |
| `hostops`                  | operation/action 协作类（规划）：前端因需要宿主（Go 运行时）底层能力而通过 API 调用后端的宿主操作。对于此场景，依然由前端驱动触发和合并 report 投递入消息流，后端只提供 API 实现而不干预消息组装。 | `doc/_instruction.md`                                                                               | active |
| `huddle`                   | operation 的一种（规划）：butler/worker 等 AI 角色之间的后台讨论、协作过程记录。                                                                                                                   | `doc/_instruction.md`                                                                               | active |
| `butler agent`             | 主智能体角色（规划）：对用户负责，编排 worker agents 与工具调用，产出对用户可理解的结果。                                                                                                          | `doc/_instruction.md`                                                                               | active |
| `worker agents`            | 工作智能体角色（规划）：按 butler 分派执行子任务，过程中由前端或后台串联各节点。                                                                                                                   | `doc/_instruction.md`                                                                               | active |

## 7. 待确认事项
1. 旧 turn 的 partial 气泡在被新 turn 顶掉后，是否采用“允许旧 `asr_final` 收口 + superseded fallback”双层策略；当前还未正式修复。
2. 配置抽屉后续开放给用户的字段范围仍需继续收敛，不宜把全部公开配置都暴露为可编辑项。
3. 当前验证以 Go 测试、构建和前端模块语法检查为主，浏览器侧仍需补 `show more`、解析异常气泡、连续 action followup 等实机回归。
4. `thread_summaries/memories/files/file_refs` 当前已建表但尚无稳定写入/读取链路，需要明确触发点与 UI/LLM 的使用场景后再补齐。

## 8. 文档更新时间与信息来源
- 更新时间：2026-03-14 16:15 CST
- 信息来源：
  - 前端：`sidebar-controller.js` 落地与 `session-controller.js` 重连改造
  - 后端：`sqlite_store.go` 增加 `order_index` 与 CRUD 接口
  - 开发计划：`plan/260314-chat-project-thread-mgmt-prd.md`
  - 仓库实时扫描与功能逻辑校对
