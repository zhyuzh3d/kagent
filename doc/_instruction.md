# 项目说明（doc/_instruction.md）

## 1. 项目概览
`kagent` 当前已从“规则/文档骨架”进入 T0 MVP 实现阶段：仓库内已落地 Go 本地服务与纯原生前端，目标是完成单页面、单会话的实时语音对话闭环（ASR -> LLM -> TTS）。

当前真实状态（以代码扫描与本地验证为准）：
1. Go 服务入口、WS 会话管理、ASR/LLM/TTS 适配、前端采集/播放均已落地。  
2. ASR 与 LLM 链路可运行。  
3. TTS 仍有真实接口稳定性问题（资源授权/协议细节/网络抖动叠加），导致“有文本但可能无声”。
4. 已落地前后端独立版本号（同一文件记录），并提供本地部署脚本与自停机接口，便于快速重启与排障。

## 2. 当前目录结构（关键层级）
> 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage` 等。

```text
kagent/                                # 仓库根目录
├── .agent/                            # 运行环境/代理规则（当前仅含规则文档）
│   └── rules/                         # 规则目录
│       └── agent.md                   # 代理规则说明（待确认其与 AGENTS 的分工）
├── .codex/                            # Codex 配置目录（Skills 等）
│   └── skills/                        # 模式技能集合（chat/plan/dev）
│       ├── chat/                      # chat 模式规则
│       ├── dev/                       # dev 模式规则
│       └── plan/                      # plan 模式规则
├── doc/                               # 项目文档（AI 管理：说明/日志）
│   ├── _devlog.md                     # 开发日志（只追加）
│   └── _instruction.md                # 项目说明（本文件，需与仓库一致）
├── config/                            # 配置目录（示例/私密配置均在此）
│   ├── config.json                    # 可公开配置（当前占位）
│   ├── configx.json                   # 私密配置（必须 gitignore）
│   └── configx.json.example           # 私密配置示例模板（脱敏，可入库）
├── internal/                          # Go 后端核心实现（ASR/LLM/TTS/会话）
│   ├── asr.go                         # ASR Provider：上行音频、下行识别事件
│   ├── llm.go                         # LLM Provider：流式对话增量解析
│   ├── tts.go                         # TTS Provider：双向 WS 协议封装与收包
│   ├── pipeline.go                    # Turn 编排：LLM 分段 -> TTS 分段
│   ├── session.go                     # WS 会话：状态机、队列、turn 生命周期
│   ├── protocol.go                    # WS 控制/事件协议定义（JSON + binary 配对）
│   ├── config.go                      # configx.json 解析与校验（models[].config）
│   ├── version.go                     # version.json 解析（backend/webui 版本）
│   ├── id.go                          # 请求 ID 生成工具
│   ├── jsonutil.go                    # JSON 容错解析工具
│   └── *_test.go                      # 基础单测（配置/编排/能量判定）
├── plan/                              # 需求/计划/结果等过程文档
│   ├── T0-26030301.md                 # T0 MVP 需求文档
│   ├── T0-26030301-dev-plan.md        # T0 MVP 开发计划（同名 -dev-plan）
│   └── T0-26030301-dev-plan-result.md # T0 MVP 开发结果（同名 -dev-result，当前文件名待统一）
│   ├── T0-26030401-fix.md             # T0 修复记录（存在性来自目录扫描）
│   ├── T0-26030401-fix-dev-plan.md    # 修复开发计划（存在性来自目录扫描）
│   └── T0-26030401-fix-dev-result.md  # 修复开发结果（存在性来自目录扫描）
├── ref/                               # 参考资料（脱敏）
│   └── doubao-doc.md                  # 官方文档入口索引与笔记（脱敏）
├── webui/                             # 纯原生 Web UI（无构建）
│   └── index.html                     # 页面：采集/播放/打断/调试面板
├── scripts/                           # 本地脚本（工作流工具）
│   ├── gitpush.sh                     # 自动 bump 版本 + commit + push
│   └── deploy.sh                      # 构建后端 + 停旧启新 + 健康检查
├── AGENTS.md                          # 项目最高原则与工作流规范
├── README.md                          # 项目愿景与概览
├── version.json                       # 版本单一事实源（backend/webui CalVer）
├── go.mod                             # Go 模块定义
├── go.sum                             # Go 依赖校验和
└── main.go                            # Go 服务入口（HTTP + /ws + 静态资源）
```

## 3. 核心模块职责
1. `main.go`  
- 启动 HTTP 服务与 `/ws`；默认监听 `127.0.0.1:18080`。  
- 挂载静态页面 `/webui/index.html`（并兼容 `/static/index.html`）。  
- 加载 `config/configx.json` 中模型配置（可通过 `-config` 覆盖）。
- 读取 `version.json` 并在启动日志输出版本；提供 `/version` 返回 backend/webui 版本。
- 提供 `POST /admin/shutdown`（loopback 限制）用于快速自停机（脚本部署使用）。

2. `internal/session.go`  
- 维护 session 状态机与 turn 生命周期。  
- 处理 `start/stop/interrupt/utterance_end`。  
- 管理上行音频队列、下行 TTS 队列、取消与清理。

3. `internal/asr.go`  
- 连接 `asr_s` WebSocket。  
- 输入 PCM 音频帧，输出 `partial/final` 识别事件。  
- 包含失败重连所需的拨号兼容处理。

4. `internal/llm.go`  
- 调用 `chat/completions` 流式接口。  
- 解析 SSE 增量，回传 `llm_delta/final`。

5. `internal/tts.go`  
- 实现 V3 双向 WS 二进制协议交互（连接/会话/任务/收包）。  
- 当前是发声链路主要风险点（真实接口稳定性未完全闭环）。

6. `internal/pipeline.go`  
- 串联 ASR 文本 -> LLM 增量 -> 分段 TTS。  
- 负责 `llm_delta` 事件下发与 `tts_chunk + binary` 输出。

7. `webui/index.html`  
- 纯原生 UI（无 Node、无构建工具）。  
- 麦克风采集（AudioWorklet 优先、ScriptProcessor 降级）。  
- 16k PCM 上行、TTS 下行播放、前端 barge-in 触发。

## 4. 开发与运行方式（可验证）
1. 运行测试：`go test ./...`  
2. 构建（规避本机 VCS stamping 问题）：`go build -buildvcs=false ./...`  
3. 启动：`go run .`  
4. 访问：`http://127.0.0.1:18080/webui/index.html`（或兼容：`/static/index.html`）

配置约束：
1. 私密配置在 `config/configx.json`，不得入库。  
2. 公开示例使用 `config/configx.json.example` 脱敏模板。  
3. 若 TTS 报 `resource not granted`，优先检查 `resourceId` 与音色/模型授权匹配。

## 5. 最近关键变更摘要（最近 1-3 条）
1. 2026-03-03：完成项目规则与文档基线（`AGENTS.md`、`doc/*`、脱敏与忽略策略）。  
2. 2026-03-04：完成 T0 MVP 主体代码落地（Go 服务、前端、ASR/LLM/TTS、测试）。  
3. 2026-03-06：落地版本号体系（`version.json`）、`gitpush.sh`/`deploy.sh`、`/admin/shutdown`，并将配置迁移到 `config/`。

## 6. 项目术语表
| 术语 | 定义（本项目语境） | 来源文件 | 状态 |
|---|---|---|---|
| T0 | 第一代可运行语音对话 MVP | `plan/T0-26030301.md` | active |
| Trinity Zero | T0 的别名 | `README.md` | active |
| C.A.T Zero | T0 的别名（Consciousness/Agency/Transcendence） | `README.md` | active |
| turn_id | 单轮对话唯一递增编号，绑定文本与音频事件 | `internal/session.go` | active |
| asr_s | 豆包语音识别流式接口配置键 | `internal/config.go` | active |
| tts_s | 豆包语音合成流式接口配置键 | `internal/config.go` | active |
| barge-in | AI 说话中用户开口触发打断 | `webui/index.html`, `internal/session.go` | active |
| tts_chunk 配对协议 | 先发 JSON 元数据，再发 binary 音频分片 | `internal/protocol.go`, `internal/session.go` | active |
| configx.json | 私密配置（Token/Key）文件（当前位于 `config/`） | `.gitignore`, `internal/config.go` | active |
| version.json | 前后端版本单一事实源（backend/webui CalVer） | `version.json`, `internal/version.go` | active |
| /version | 返回 backend/webui 版本信息的 HTTP 接口 | `main.go` | active |
| /admin/shutdown | 本地自停机接口（deploy.sh 使用） | `main.go`, `scripts/deploy.sh` | active |
| AI 管理文档 | `doc/` 下以下划线开头文档（说明/日志） | `AGENTS.md` | active |

## 7. 待确认事项
1. TTS `code=148/type=0xF` 的最终根因是否为协议字段细节、资源授权，或两者叠加。  
2. `resourceId` 与 `voiceType` 的可用组合清单（建议后续固化为可验证表）。  
3. 真实网络波动下的重试与超时策略是否需要统一到 ASR/TTS 两端。  
4. 当前多数代码尚未形成 Git 提交历史（本地存在大量未跟踪文件），后续需补齐可追溯提交。

## 8. 文档更新时间与信息来源
- 更新时间：2026-03-06 16:04 CST  
- 信息来源：
  - 仓库实时扫描（`find` / 文件内容）
  - 代码核验（`main.go`、`internal/*`、`webui/index.html`）
  - 本地验证（`go test ./...`、`go build -buildvcs=false ./...`）
  - 本轮会话中的真实运行日志（ASR/TTS 报错现象）
