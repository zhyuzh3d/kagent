# 项目说明（doc/_instruction.md）

## 1. 项目概览
`kagent` 当前是一个本地运行的实时语音对话 MVP：浏览器页面通过 WebSocket 与 Go 服务通信，形成 `ASR -> LLM -> TTS` 的单会话闭环。

当前真实状态（基于代码扫描、运行验证与本轮会话核验）：
1. 前端已实现麦克风采集、Worker 持有 WebSocket、客户端停顿检测、可打断播放、调试日志与版本展示。
2. 后端已实现单页面单会话的状态机、按 turn 隔离的 ASR 连接、流式 LLM、句级/多句拼组 TTS 编排。
3. 当前对话链路采用“前端主导触发 LLM、后端主导 TTS 拼组”的混合机制：前端负责 `start_listen / interrupt / trigger_llm` 与回复归属判定，后端负责 `Finish ASR`、取最终文本并启动 LLM/TTS。
4. 前端已将“输入 turn”和“有效回复 turn”分离：`currentTurn` 代表当前输入轮次，`activeTurnId` 代表当前仍应接收 `llm_delta / llm_final / tts_chunk` 的回复轮次，用于避免空 turn 抢走有效回复。
5. 当前 TTS 已加入“单段失败不中断整轮”的容错，并在片段失败或空音频时向前端发送 `tts_warn` 调试事件；真实供应商稳定性仍受外部接口行为影响。
6. 已落地前后端版本号（`version.json`）、部署脚本与本地自停机接口，便于快速重启和排障。

## 2. 当前目录结构（关键层级）
> 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage` 等。

```text
kagent/                                      # 仓库根目录
├── .agent/                                  # 规则目录（当前仅含代理规则文档）
│   └── rules/                               # 规则子目录
│       └── agent.md                         # 代理规则说明（与 AGENTS 的边界待确认）
├── .codex/                                  # Codex 配置与技能目录
│   └── skills/                              # chat/plan/dev 模式技能
│       ├── chat/                            # chat 模式说明
│       ├── dev/                             # dev 模式说明
│       └── plan/                            # plan 模式说明
├── config/                                  # 配置目录
│   ├── config.json                          # 公开配置占位文件
│   ├── configx.json                         # 私密配置（必须忽略）
│   └── configx.json.example                 # 私密配置示例模板
├── doc/                                     # 项目文档目录
│   ├── _devlog.md                           # 开发日志（只追加）
│   └── _instruction.md                      # 项目说明（本文件）
├── internal/                                # Go 后端核心实现
│   ├── asr.go                               # ASR WebSocket Provider 与 Finish 语义处理
│   ├── llm.go                               # LLM 流式接口与 SSE 解析
│   ├── tts.go                               # TTS WebSocket Provider 与音频收包
│   ├── pipeline.go                          # LLM 增量 -> 句子缓存 -> backlog 拼组 -> TTS 编排
│   ├── session.go                           # 会话状态机、turn 管理、ASR/TTS 生命周期
│   ├── protocol.go                          # 前后端控制/事件协议定义
│   ├── config.go                            # 配置解析与模型选择
│   ├── version.go                           # version.json 读取
│   ├── id.go                                # 请求 ID 生成工具
│   ├── jsonutil.go                          # JSON 容错解析工具
│   └── *_test.go                            # 当前后端基础测试
├── plan/                                    # 需求/计划/结果文档
│   ├── T0-26030301.md                       # T0 MVP 需求文档
│   ├── T0-26030301-dev-plan.md              # T0 MVP 开发计划
│   ├── T0-26030301-dev-plan-result.md       # T0 MVP 开发结果（文件名待统一）
│   ├── T0-26030401-fix.md                   # T0 修复记录
│   ├── T0-26030401-fix-dev-plan.md          # 修复开发计划
│   └── T0-26030401-fix-dev-result.md        # 修复开发结果
├── ref/                                     # 参考资料目录
│   └── doubao-doc.md                        # 外部接口文档摘录与笔记
├── scripts/                                 # 本地工作流脚本
│   ├── deploy.sh                            # 构建、停旧启新与健康检查
│   └── gitpush.sh                           # bump 版本并 commit/push
├── webui/                                   # 纯原生前端资源目录（作为根静态服务目录挂载）
│   ├── favicon.ico                          # 页面图标（通过 /favicon.ico 直接访问）
│   ├── img/                                 # 页面静态图片资源
│   │   └── 10knet_logo.png                  # 当前品牌图资源
│   └── page/                                # 页面子目录
│       └── chat/                            # 对话页面
│           └── index.html                   # 单页面 UI、Worker、采集、播放与调试逻辑
├── AGENTS.md                                # 项目最高原则与工作流规范
├── README.md                                # 项目概览与愿景说明
├── go.mod                                   # Go 模块定义
├── go.sum                                   # Go 依赖校验和
├── main.go                                  # HTTP 服务入口与路由挂载
└── version.json                             # backend/webui 版本单一事实源
```

## 3. 核心模块职责
1. `main.go`
- 启动 HTTP 服务与 `/ws`，默认监听 `127.0.0.1:18080`。
- 将 `webui/` 目录作为根静态文件服务。`/` 重定向到 `/page/chat/`。
- 提供 `/version` 与 `POST /admin/shutdown`。

2. `internal/session.go`
- 维护浏览器会话生命周期与状态机。
- 处理 `start / stop / start_listen / interrupt / trigger_llm / utterance_end`。
- 每个 ASR turn 独立创建专用连接，并在新 turn 开始前清空旧音频队列。
- 在 `trigger_llm` 时调用 `ASR Finish()`，短暂等待最终文本后再启动 LLM。
- `start_listen` 只在后端当前处于 `Speaking` 时立即打断旧回复；处于 `Thinking` 时不会仅凭 onset 取消旧生成。
- 当新的 `asr_partial / asr_final` 已确认存在真实文本，且 turn_id 晚于当前生成轮次时，再中断 `Thinking / Speaking` 中的旧回复。
- 通过 `setTurnState` 为 `Thinking / Speaking / Listening / Error` 发送显式 turn 绑定的状态事件。
- 当最终文本为空时向前端发送 `turn_nack`，避免前端错误推进。

3. `internal/asr.go`
- 建立 ASR WebSocket，发送起始帧、音频帧与结束帧。
- 输出 `partial / final` 识别事件。
- 将 `finish last sequence` 等正常结束 close 视为预期结束，而不是直接报错。

4. `internal/llm.go`
- 调用流式 `chat/completions` 或兼容 Responses/SSE 事件。
- 将增量文本回传给 `pipeline`，并向前端透传 `llm_delta`。

5. `internal/pipeline.go`
- 以整句为基础单元缓存 LLM 文本，仅在 `。！？；.!?;…` 或换行处切句。
- 依据已发送语音的估算积压时长，动态将 `1/2/3/5/10` 句拼成一个 TTS 任务，并受总字数上限约束。
- 对单段 TTS 失败或空音频做“记录后继续”的容错；仅在整轮完全没有产出有效音频时返回错误。
- 在单段 TTS 失败或空音频时向前端发送 `tts_warn` 事件，便于前端调试日志定位“说到一半没声音”的片段级故障。

6. `internal/tts.go`
- 为单个文本片段建立 TTS 任务并返回音频数据。
- 与 `pipeline` 配合，通过 `tts_chunk + binary` 配对协议下发前端。

7. `webui/page/chat/index.html`
- 主线程负责 UI、AudioContext、音频播放与消息渲染。
- Web Worker 负责 WebSocket、`vad_utterance_end` 定时器与上行音频转发。
- 前端本地维护 `preRollBuffer`、`currentTurn`、`activeTurnId` 与 `replyOnsetGuard*` 状态，并在检测停顿时显式发送 `trigger_llm`。
- `currentTurn` 表示当前输入轮次；`activeTurnId` 表示当前仍应接收回复流的轮次，`llm_delta / llm_final / tts_chunk` 不再直接按最新 onset turn 过滤。
- `AudioWorklet` 模块通过 `ensurePCMWorkletLoaded()` 做一次性加载，避免重复注册 `pcm-processor`。
- 通过 `playbackEpoch` 防止旧音频 decode 后回流，通过 `sessionEpoch` 防止暂停再开始后旧会话气泡被覆盖。
- AI 播放期间以真实播放状态（`isPlaying / playbackQueue`）而不是纯文本状态作为 barge-in 判定依据。
- 在 `trigger_llm` 后进入短暂 `replyOnsetGuard` 窗口，抑制 `Thinking` 阶段的瞬时误 onset；真正的 `Speaking` 打断仍走快速 `barge-in`。

## 4. 当前对话机制（前后端概括）
### 4.1 前端侧
1. 用户点击开始后，页面创建 Worker、建立 `/ws`、启动麦克风。
2. 音频优先走 `AudioWorklet`，失败时退回 `ScriptProcessor`；`AudioWorklet` 模块在同一 `AudioContext` 中只加载一次。
3. 本地以 RMS 阈值和持续帧数判断用户是否开始说话；首次命中时发送 `start_listen`，AI 正在播时则改走 `interrupt`。
4. Worker 维护 500ms 停顿检测；一旦检测到 `vad_utterance_end`，前端立即发送 `trigger_llm`，并开启约 1.2 秒的 `replyOnsetGuard` 窗口，抑制 `Thinking` 阶段的误 onset。
5. 前端将事件处理分成“输入链路”和“回复链路”两套规则：
   - `asr_partial / asr_final` 继续按 `currentTurn` 过滤，避免旧识别结果覆盖当前用户气泡。
   - `llm_delta / llm_final / tts_chunk` 改按 `activeTurnId` 过滤，避免空 turn 抢走有效回复。
6. `activeTurnId` 只会在明确进入 `Thinking / Speaking` 或收到回复载荷时推进；普通 `start_listen` 不会直接覆盖它，`turn_nack` 也不会把它回退到空 turn。
7. 重新开始对话时递增 `sessionEpoch`，保证新旧会话的同一 `turn_id` 不会互相覆盖。

### 4.2 后端侧
1. `start` 启动首个 ASR turn，后续每次 `start_listen / interrupt` 都重建一个专用 ASR WebSocket。
2. `trigger_llm` 触发后端调用 `ASR Finish()`，短等最终结果，然后取后端最终文本优先、前端快照兜底。
3. `start_listen` 到达时，后端只会在当前处于 `Speaking` 时立即打断；处于 `Thinking` 时会先保留旧生成，等待新 ASR 结果是否真的出现。
4. 一旦新的 `asr_partial / asr_final` 已确认存在非空文本，且 `speechTurnID > lastStartedTurnID`，后端才中断旧的 `Thinking / Speaking` 回复，兼顾实时性与空 turn 误触发容错。
5. `pipeline` 立即向前端发送 `llm_delta`，同时把完整句子积压到 TTS 分组器。
6. 分组器依据估算 backlog 决定每次送几句给 TTS，以降低长回复时的碎片感和连接抖动。
7. TTS 产出的音频以 `tts_chunk` 元信息 + 二进制音频分片下发；单段失败或空音频额外发送 `tts_warn` 给前端调试日志。

### 4.3 当前已落实的主要改进
1. 前端打断逻辑改为以真实音频播放状态判定，避免单纯 `Speaking` 文本状态误伤麦克风采集。
2. 前端加入 `playbackEpoch`，解决打断后旧音频“回魂”问题。
3. 前端加入 `sessionEpoch`，解决停止后重开时新识别结果覆盖旧气泡的问题。
4. 后端在 `trigger_llm` 时重新等待 ASR final，并对空 turn 回发 `turn_nack`。
5. 后端将 ASR 正常 Finish close 视为可接受结束，避免把正常收尾误判成错误。
6. TTS 编排改成“整句切分 + backlog 动态拼句 + 单段失败不中断整轮”，并将片段失败通过 `tts_warn` 透传到前端调试日志。
7. 前端加入 `replyOnsetGuard`，减少 `Thinking` 阶段误 onset 导致的空 turn。
8. 前端将“输入 turn”与“有效回复 turn”拆开处理，避免空 turn 把仍在生成的有效回复误判成 stale。
9. 前端消息渲染已改用 `textContent`，避免把 ASR/LLM 文本直接注入 `innerHTML`。

## 5. 日志与输出规范（Log Convention）
为了在前后端形成“协调对照”的统一流，项目抛弃了 `slog` 原生 JSON 层级，采用了严格单行文本配合**语义模块（Semantic Modules）**及**镜像动作词汇（Action Vocabulary）**的输出规范。

### 5.1 基础格式
`YYYY-MM-DD HH:MM:SS [LEVEL] [ModuleName:line] [Turn:X] "text..." -> Action Message`

### 5.2 核心对照矩阵
前后端日志按照真实时间线形成紧密对照，关键交互如下：

1. **Voice Onset（前端听音开始）**
   - 前端：`[FrontendVAD] [Turn:X] -> Speech onset detected (start_listen)`
   - 后端：`[SessionManager:L] [Turn:X] -> VAD listening started`
2. **Barge-In（抢话打断）**
   - 前端：`[FrontendVAD] [Turn:X] -> Barge-in committed (interrupt)`
   - 后端：`[SessionManager:L] [Turn:X] -> VAD interrupt received (reason)`
3. **LLM Trigger（确定句子终结，触发思考）**
   - 前端：`[SessionControl] [Turn:X] "当前文本" -> Trigger LLM explicitly...`
   - 后端：`[SessionManager:L] [Turn:X] "当前文本" -> LLM Triggered`
4. **TTS 合成与下发**
   - 后端：`[AudioPipeline:L] [Turn:X] "第一小句" -> TTS start sending...`
   - 后端：`[AudioPipeline:L] [Turn:X] -> TTS finished sending`
   - 后端：`[AudioPipeline:L] [Turn:X] "失败片段" -> TTS segment synth failed...`
   - 前端：`[TTSBackend] [Turn:X] "失败片段" -> code=tts_segment_failed ...`
   - 前端：`[FrontendAudio] [Turn:X] -> 播放与队列日志...`

通过该规范，可在终端（`app.log`）或 WebUI 的调试面板中获得纯净对称的运行时排障视角，无需过滤冗余打字/空包日志（均已被平息为 `[DEBUG]` 级别）。

## 6. 开发与运行方式（可验证）
1. 运行测试：`go test ./...`
2. 运行竞态测试：`go test -race ./...`
3. 构建：`go build -buildvcs=false ./...`
4. 启动：`go run .`
5. 访问：`http://127.0.0.1:18080/page/chat/`

配置约束：
1. 私密配置位于 `config/configx.json`，不得入库。
2. 公开示例使用 `config/configx.json.example`。
3. 若 TTS 报资源或权限错误，应先核对 `resourceId / voiceType / model` 的实际授权组合。

## 7. 最近关键变更摘要（最近 1-3 条）
1. 2026-03-08：补齐播放/会话隔离、ASR Finish 语义、整句 backlog 拼组 TTS 与单段失败容错。
2. 2026-03-08：实装全局前后端统一单行文本日志格式、语义模块重命名及镜像对照词表规范。
3. 2026-03-08：修正 `Thinking` 阶段空 turn 抢占问题，加入 `replyOnsetGuard`、回复 turn 归属分离、`tts_warn` 调试事件与“确认识别到真实文本后再中断旧回复”的后端策略。

## 8. 项目术语表
| 术语               | 定义（本项目语境）                                          | 来源文件                                            | 状态   |
| ------------------ | ----------------------------------------------------------- | --------------------------------------------------- | ------ |
| T0                 | 第一代可运行语音对话 MVP                                    | `plan/T0-26030301.md`                               | active |
| turn_id            | 单轮对话唯一递增编号，绑定 ASR/LLM/TTS 事件                 | `internal/session.go`                               | active |
| trigger_llm        | 前端在停顿检测后显式触发后端启动 LLM 的控制消息             | `webui/page/chat/index.html`, `internal/session.go` | active |
| barge-in           | AI 说话时用户开口抢话并打断当前回复                         | `webui/page/chat/index.html`, `internal/session.go` | active |
| activeTurnId       | 前端当前仍应接收 `llm_delta / tts_chunk` 的有效回复轮次     | `webui/page/chat/index.html`                        | active |
| replyOnsetGuard    | 前端在 `trigger_llm` 后短暂抑制误 onset 的保护窗口          | `webui/page/chat/index.html`                        | active |
| playbackEpoch      | 前端用于废弃旧播放队列和旧 decode 结果的世代号              | `webui/page/chat/index.html`                        | active |
| sessionEpoch       | 前端用于隔离停止后重开会话消息气泡的世代号                  | `webui/page/chat/index.html`                        | active |
| SentenceSegmenter  | 后端按整句切分 LLM 增量文本的分句器                         | `internal/pipeline.go`                              | active |
| playback backlog   | 后端依据已发送语音估算的未播放时长，用于决定每次 TTS 拼几句 | `internal/pipeline.go`                              | active |
| tts_chunk 配对协议 | 先发 JSON 元数据、再发对应 binary 音频分片的下行协议        | `internal/protocol.go`, `internal/session.go`       | active |
| tts_warn           | 后端向前端发出的可恢复 TTS 片段失败/空音频调试事件          | `internal/protocol.go`, `internal/pipeline.go`      | active |
| configx.json       | 私密配置文件（Token/Key）                                   | `config/configx.json.example`, `internal/config.go` | active |
| version.json       | 前后端版本单一事实源                                        | `version.json`, `internal/version.go`               | active |
| /version           | 返回 backend/webui 版本信息的接口                           | `main.go`                                           | active |
| /admin/shutdown    | 本地自停机接口                                              | `main.go`, `scripts/deploy.sh`                      | active |

## 9. 待确认事项
1. 真实用户环境下，当前 backlog 估算值与前端真实播放时长的偏差是否需要进一步校正。
2. `3000 / 5000 / 10000 / 20000 ms` 与对应字数上限是否需要根据常见回复长度继续微调。
3. 真实网络抖动下，ASR 与 TTS 的重试/超时策略是否需要统一抽象。
4. `plan/T0-26030301-dev-plan-result.md` 文件名仍与“同名 -dev-result”约定不一致，后续需要统一。

## 10. 文档更新时间与信息来源
- 更新时间：2026-03-08 12:56 CST
- 信息来源：
  - 仓库实时扫描（目录与文件内容）
  - 当前工作区代码核验（`internal/asr.go`、`internal/pipeline.go`、`internal/protocol.go`、`internal/session.go`、`webui/page/chat/index.html`）
  - 本地验证（`go test ./...`）
  - 本轮会话中的用户反馈与调试日志（空 turn 抢占、TTS 片段失败、回复流 stale 丢弃问题）
