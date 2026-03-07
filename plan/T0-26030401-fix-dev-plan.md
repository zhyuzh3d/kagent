# 开发计划文档：T0-26030401-fix-dev-plan

## 1. 目标
重构 `internal/tts.go` 和 `internal/pipeline.go` 的交互逻辑，使得每次对话长文本时，无需频繁反复起停火山豆包 WS 短链接。提升响应速度，杜绝静音、丢字问题。

## 2. 计划与任务拆解

### 任务 1：抽象真正的流式 TTS Client 接口 (`internal/tts.go`)
- **[修改] `internal/tts.go`**：
  - 将原有的 `Synthesize(ctx, text) ([]byte, format, error)` 改为 `NewStream(ctx) (TTSStream, error)`。
  - 定义 `TTSStream` 接口：
    - `SendText(text string) error`: 发送 `ttsEventTaskRequest` 并将字串压给服务器。
    - `Finish() error`: 发送 `ttsEventFinishSession` 告知服务端结束。
    - 能通过独立后台 routine 或回调 `onChunk(chunk []byte, format string) error` 返回接收到的长音频音频段。
  - 原先的 `Synthesize` 将被剥离，在 `NewStream` 建连时发送 `StartConnection` 和 `StartSession`，返回可供持续推字的 `Stream` 实例对象。

### 任务 2：重构流水线编排调度 (`internal/pipeline.go`)
- **[修改] `internal/pipeline.go` 的 `TurnPipeline.RunTurn`**：
  - Turn 开场即初始化获取 `tts.NewStream(ctx)` 实例。
  - 大幅精简 `TextSegmenter` 的截断要求：取消 16 纯字符必须凑够才切分的硬锁条件。当扫描到语感合理的停顿符 `、，。！？\n` 时立刻 `SendText(seg)` 给到 TTS 服务器，抢夺响应首发。
  - 修正前端状态更新节点，在 LLM 第一次 `delta` 抵达瞬间立即广播 `StateSpeaking` 或者有对应反馈机制，减轻前端等待焦虑。
  - LLM 彻底接收完了，再由 `pipeline` 发起 `ttsStream.Finish()`，最后等待音频接收完毕安全退出流。

### 任务 3：本地验证 
- **[构建] 新版本**：`go build -buildvcs=false ./...` 检查通过。
- **[产出结果文档]**：生成 `plan/T0-26030401-fix-dev-result.md` 并按照 AGENTS.md 规范更新最新版本日志。

## 3. 风险预估
由于豆包的双向流式（Bidirectional TTS）在相同 Session ID 下发多个 `task_request` 在协议允许内，但这取决于豆包服务端真实支持连贯长序列包的缓存时长。若其依然要求切片严格，则必须保障每个小句一个 Request，本次长连接改造将验证协议实际支持度。
