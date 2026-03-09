# 项目说明（doc/_instruction.md）

## 1. 项目概览
`kagent` 是一个单机运行的实时语音对话 MVP：浏览器页面通过 WebSocket 与 Go 服务通信，形成 `ASR -> LLM -> TTS` 的单会话闭环。

当前真实状态（基于 2026-03-09 代码扫描、运行验证与本轮会话核验）：
1. 后端主链路已经稳定在“单会话状态机 + 每个输入 turn 独立 ASR + 流式 LLM + 句级 TTS backlog 拼组”这一结构上。
2. 前端聊天页已从单个大脚本拆为多个运行模块，`index.html` 主要承担页面装配和事件绑定。
3. 项目已经具备公开配置、用户覆盖配置和前端配置抽屉，但这只是对话页的辅助能力，不是项目主功能。
4. 当前仍有一个已定位但未修复的边缘问题：旧 turn 的 partial 用户气泡在被新 turn 顶掉后，可能因 stale 过滤而长期保持斜体。

## 2. 当前目录结构（关键层级）
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
│   └── page/chat/                                   # 实时对话页面目录
│       ├── index.html                               # 页面入口与装配层
│       ├── config-store.js                          # 配置读取/保存工具
│       ├── config-drawer.js                         # 配置抽屉
│       ├── chat-store.js                            # 消息与气泡状态
│       ├── audio-playback.js                        # 播放队列与音频上下文
│       ├── audio-capture.js                         # 采集、降采样、抢话
│       ├── event-router.js                          # 协议事件路由
│       ├── session-controller.js                    # 会话启动/停止与 Worker 生命周期
│       └── io-worker.js                             # Worker 内 WS 与 VAD 定时器
├── main.go                                          # HTTP 服务入口
└── version.json                                     # 前后端版本单一事实源
```

## 3. 核心模块职责
1. `main.go`
- 启动 HTTP 服务、`/ws`、`/version`、`/api/config` 与静态资源服务。

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

## 4. 当前工作方式
### 4.1 前端
1. 点击开始后，前端创建 Worker、建立 `/ws`、启动麦克风。
2. 本地检测开口、停顿和抢话，必要时发送 `start_listen / interrupt / trigger_llm`。
3. `asr_partial / asr_final` 更新用户气泡，`llm_delta / llm_final / tts_chunk` 更新 AI 回复和播放队列。

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

## 5. 最近关键变更摘要
1. 对话页完成模块化拆分，`index.html` 从大脚本收敛为装配层。
2. 新增公开配置与用户覆盖配置机制，前端可通过配置抽屉读取和保存部分运行参数。
3. 会话停止和页面卸载时增加了 Worker 与音频链路的清理，避免旧连接和旧状态残留。

## 6. 项目术语表
| 术语 | 定义（本项目语境） | 来源文件 | 状态 |
| --- | --- | --- | --- |
| `app` | 整个本地软件实例级别的范围，例如全局 UI 或默认行为。 | `config/config.json`, `webui/json/config_info.json` | active |
| `chat` | 一次“开始对话”到“停止”的完整实时对话范围。比单个 turn 大，比 app 小。 | `config/config.json`, `webui/page/chat/index.html` | active |
| `thread` | 话题边界。当前实现里只有一个 thread 概念，没有多话题并存。 | `plan/T0-26030901-chat-config-modularization-dev-plan.md`, `webui/json/config_info.json` | active |
| `turn` | 一轮用户输入加对应 AI 回复，对应前后端都在使用的 `turn_id` 语义。 | `internal/session.go`, `webui/page/chat/chat-store.js` | active |
| `message` | turn 内更细的消息单位，通常指聊天区里的单条用户或 AI 气泡。 | `webui/page/chat/chat-store.js` | active |
| `抢话` / `barge-in` | AI 正在说话时，用户再次开口并打断当前回复。 | `internal/session.go`, `webui/page/chat/audio-capture.js` | active |
| `空 turn` | 前端推进了 turn，但后端最终没有拿到有效文本，通常会收到 `turn_nack`。 | `internal/session.go`, `webui/page/chat/event-router.js` | active |
| `partial 气泡` | 前端收到 `asr_partial` 后显示的斜体用户气泡，表示这句还没正式收口。 | `webui/page/chat/chat-store.js` | active |
| `有效回复 turn` | 前端当前仍应接收 `llm_delta / llm_final / tts_chunk` 的回复轮次，用来防止空 turn 抢走回复流。 | `webui/page/chat/event-router.js`, `webui/page/chat/chat-store.js` | active |
| `公开配置` | 可以被前端读取和保存的运行时配置，不包含敏感接入信息。 | `config/config.json`, `internal/runtime_config.go` | active |
| `私密配置` | 只用于本地服务端接入外部能力的敏感配置，例如 Token、AppID、私有 URL。 | `config/configx.json.example`, `internal/config.go` | active |
| `用户覆盖配置` | 用户保存的个性化配置覆盖项，只记录相对公开默认配置的差异。 | `internal/runtime_config.go`, `main.go` | active |
| `配置抽屉` | 聊天页左侧的运行时配置面板，用于调节部分体验参数。 | `webui/page/chat/config-drawer.js`, `webui/json/config_info.json` | active |
| `mtrca` | 前端配置字段上的生效层级提示标签，分别代表 `message / turn / thread / chat / app`。 | `webui/json/config_info.json` | active |

## 7. 待确认事项
1. 旧 turn 的 partial 气泡在被新 turn 顶掉后，是否采用“允许旧 `asr_final` 收口 + superseded fallback”双层策略；当前还未正式修复。
2. 配置抽屉后续开放给用户的字段范围仍需继续收敛，不宜把全部公开配置都暴露为可编辑项。
3. 目前主要验证仍是 Go 测试、构建和前端模块语法检查，浏览器侧真实语音回归还需要继续补。

## 8. 文档更新时间与信息来源
- 更新时间：2026-03-09 18:16 CST
- 信息来源：
  - 仓库实时扫描（目录与文件内容）
  - 当前工作区代码核验（`main.go`、`internal/*.go`、`webui/page/chat/*.js`）
  - 本轮会话中的用户确认（`chat`、`mtrca`、单机版软件前提、术语边界要求）
  - 本地验证（`go test ./...`、`go build -buildvcs=false ./...`、前端模块语法检查）
