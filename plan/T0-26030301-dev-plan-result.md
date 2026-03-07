# T0-2026-03-03-01 开发结果文档（MVP 实施结果）

- 文档版本：01  
- 结果状态：`Partially Done`（核心链路可运行，真实 TTS 发声仍存在阻塞）  
- 结果时间：2026-03-04 12:34 CST  
- 对照基线：
  - `plan/T0-2026-03-03-01.md`
  - `plan/T0-2026-03-03-01-dev-plan.md`
- 证据来源：
  - 代码与目录扫描（`main.go`、`internal/*`、`webui/index.html`、`go.mod`）
  - 本地验证命令：`go test ./...`、`go build -buildvcs=false ./...`
  - 会话中的真实运行日志（ASR/TTS 报错日志）

## 1. 结论摘要
1. 已完成本地单进程 MVP 主体工程：`127.0.0.1:18080` 的 HTTP+WS 服务、前端采集与回放、ASR->LLM->TTS 编排、打断与状态可观测。  
2. ASR 与 LLM 链路可工作，前端可见增量与最终文本，后端具备 turn 生命周期、背压与取消控制。  
3. TTS 代码已实现 V3 双向二进制协议交互，但真实环境仍出现 `403 resource not granted`、`tts server error(type=0xF/code=148)`、`i/o timeout` 等错误，导致“可回复文本但不稳定发声”。  
4. 当前交付可用于继续排障与迭代，不满足“稳定可说话”的最终验收线。

## 2. 需求与计划对照结果

### 2.1 需求文档 DoD（10.1）对照
1. `Start` 后状态流转（Connecting -> Listening）：`已达成`。  
2. 说话时可看到 ASR Final：`已达成`。  
3. AI 能语音回复并播放：`部分达成`（代码链路存在；真实 TTS 仍不稳定失败）。  
4. `Stop` 后释放资源并回 Idle：`已达成`。  
5. Speaking 时打断 <=1s：`部分达成`（前后端打断逻辑已实现，需在真实 TTS稳定后完整验收）。

### 2.2 开发计划里程碑对照
1. M1（骨架与配置）：`已完成`。  
2. M2（采集与 ASR）：`已完成`（含 ASR 重连退避）。  
3. M3（LLM + 分段 TTS）：`部分完成`（分段编排完成，真实 TTS 成功率不足）。  
4. M4（打断与恢复）：`已基本完成`（interrupt/stop 可清理，仍需在稳定 TTS 下压力回归）。  
5. M5（稳定性与文档）：`进行中`（已补基础测试并更新文档，真实 E2E 仍有阻塞）。

## 3. 实际落地内容（按模块）

### 3.1 服务与入口
- `main.go`
  - 启动 HTTP 服务，默认 `127.0.0.1:18080`。
  - 挂载 `/webui/`（兼容 `/static/`）与 `/ws`。
  - 使用 `github.com/gorilla/websocket` 处理 WS 升级。
  - 从 `config/configx.json`（可通过 `-config` 指定）加载模型配置。

### 3.2 协议与公共类型
- `internal/protocol.go`
  - 定义状态、控制消息、事件消息结构。
  - 统一事件构造：`status/error/asr_partial/asr_final/llm_delta/llm_final/tts_chunk`。
  - 支持 `tts_chunk` + binary 配对协议。

### 3.3 配置与工具
- `internal/config.go`
  - 支持 `models[].name + config(chat/asr_s/tts_s)` 加载。
  - 启动前配置完整性校验。
- `internal/id.go`, `internal/jsonutil.go`
  - 请求 ID 生成与 JSON 解析辅助。

### 3.4 会话状态机与并发控制
- `internal/session.go`
  - 状态机：`Idle/Connecting/Listening/Recognizing/Thinking/Speaking/Interrupted/Error`。
  - 队列策略：上行音频固定队列（满时丢旧），下行 TTS 固定队列（满时报错）。
  - `turn_id` 单调递增（`atomic.Uint64`）。
  - `start/stop/interrupt/utterance_end` 控制全覆盖。
  - ASR 失败自动重连（指数退避至 8s）。
  - turn 取消、去重（1200ms 窗口）与资源清理。

### 3.5 ASR Provider
- `internal/asr.go`
  - 按二进制协议封装 `asr_s` WS。
  - 上行：start frame + 连续音频 frame。
  - 下行：解析 partial/final，错误帧透传。
  - 拨号目标兼容：URL 与 resource_id 候选修正。

### 3.6 LLM Provider
- `internal/llm.go`
  - 调用 `chat/completions`，优先 SSE 流式解析。
  - 向上游持续回调 `llm_delta`，并输出 `llm_final`。

### 3.7 TTS Provider
- `internal/tts.go`
  - 实现 V3 双向 WS 二进制帧封装与解析。
  - 流程：`StartConnection -> StartSession -> TaskRequest -> FinishSession`，读取 `TTSResponse/SessionFinished`。
  - 目前按“每段文本一次会话”执行。

### 3.8 编排与分段
- `internal/pipeline.go`
  - ASR Final 文本触发 turn。
  - LLM 增量输出分段（`TextSegmenter`）并串行送 TTS。
  - 发送 `llm_delta/llm_final` 与 `tts_chunk+binary`。

### 3.9 前端采集与播放
- `webui/index.html`
  - 纯原生单页 UI（Start/Stop、状态、调试日志、对话区）。
  - 采集：`getUserMedia` + `AudioWorklet`（失败降级 ScriptProcessor）。
  - 音频：48k float -> 16k PCM16，20ms 分片上行。
  - 播放：`tts_chunk` 元数据与 binary 配对入队播放。
  - 打断：前端 RMS 触发 `interrupt` + 立即停播。

## 4. 验证结果

### 4.1 自动化验证
1. `go test ./...`：通过（`kagent/internal`）。  
2. `go build ./...`：受本机 VCS stamping + Xcode license 影响失败。  
3. `go build -buildvcs=false ./...`：通过。

### 4.2 已有测试覆盖
- `internal/config_test.go`：配置加载。  
- `internal/pipeline_test.go`：分段器与 turn pipeline 基本行为。  
- `internal/session_test.go`：能量阈值判定。

### 4.3 手工运行观察（来自真实日志）
1. ASR 曾出现 `bad handshake` / `unexpected EOF`，已通过协议与重连改造得到改善。  
2. 当前主要阻塞在 TTS：
   - `403 requested resource not granted`
   - `tts server error(type=0xF/code=148)`
   - `read ... i/o timeout`
3. 表现为：文本回复可见，但语音播放不稳定或无声。

## 5. 偏差与调整记录
1. 相比“仅标准库”约束，已按计划引入唯一必要依赖 `gorilla/websocket`。  
2. TTS 目前采用“分段调用 + 会话级收音后回传”，不是链路级持续复用流式。  
3. 针对接口不稳定，增加了 ASR URL/resource 兼容与自动重连机制；TTS 仍需进一步对齐官方示例做包级校准。

## 6. TTS 问题边界与根因分析（当前结论）
1. 已确认不是“未开发 TTS”：`internal/tts.go` 已完整实现 WS 建连、会话、任务、收包流程。  
2. 已确认存在“资源授权层”问题：日志出现 `requested resource not granted`，属于账号资源/权限与 `resource_id` 组合不匹配。  
3. 仍存在“协议或网关交互层”不确定性：`type=0xF/code=148` 出现在 `start_connection` 后，提示请求帧或连接上下文仍可能与目标网关期望不完全一致。  
4. 网络稳定性也影响结果：同会话中出现 `i/o timeout`（ASR/TTS 都出现过），说明本地到网关链路有抖动因素。  
5. 因此当前结论是“配置授权问题 + 协议细节风险 + 网络抖动”叠加，而非单点问题。

## 7. 当前可运行方式（本地）
1. 准备 `config/configx.json`（确保 `chat/asr_s/tts_s` 完整且资源已授权）。  
2. 启动：`go run .`（或 `go run . -config config/configx.json -model doubao -addr 127.0.0.1:18080`）。  
3. 访问：`http://127.0.0.1:18080/webui/index.html`（或兼容：`/static/index.html`）。  
4. 页面点击 `Start`，观察状态/调试日志与对话输出。

## 8. 下一步（按优先级）
1. 以官方 Go 示例为基准逐字段对齐 `tts.go` 帧编码（特别是 error frame、session meta、event payload 细节）。  
2. 固化 `resource_id` 与 `voiceType` 对应关系，提供可验证的 `config/configx.json.example` 说明表。  
3. 增加 TTS 抓包级 debug（可开关，默认脱敏）以定位 `code=148` 的请求差异。  
4. 在 TTS 稳定后补做 10.3 手工清单回归（多轮、3 次打断、Stop 各状态阶段）。

## 9. 交付物清单（本轮存在）
1. `main.go`  
2. `webui/index.html`  
3. `internal/config.go`  
4. `internal/session.go`  
5. `internal/protocol.go`  
6. `internal/asr.go`  
7. `internal/llm.go`  
8. `internal/tts.go`  
9. `internal/pipeline.go`  
10. `internal/id.go`  
11. `internal/jsonutil.go`  
12. `internal/*_test.go`  
13. `go.mod` / `go.sum`  
14. `config/configx.json.example`
