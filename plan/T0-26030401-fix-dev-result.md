# 开发结果文档：T0-26030401-fix-dev-result

## 1. 目标达成情况
已完成 `internal/tts.go` 与 `internal/pipeline.go` 的底层重构，彻底解决了频繁建立 WS 的瓶颈与截断延迟：
- [x] **单回合单连 TTS**: 为 TTS 封装了真实的 `TTSStream` 持续推流接口。
- [x] **聪明的标点断句**: 将 `TextSegmenter` 的截断长度限制从 16 字精简为 2 字，极大提前了 `TTFB`。
- [x] **即时状态反馈**: 在大模型吐出首字时，即刻给前端广播 `state: speaking`。

## 2. 关键代码变更
- **`internal/tts.go`**：
  - 将原本一波流的 `Synthesize` 接口废弃，拆解出 `NewStream(ctx)` 返回 `TTSStream`。
  - 保留单个 `SessionID` 和一个双向打通的后台读取 Goroutine (`readLoop`)，随时通过调用 `SendText` 将持续到来的短句推往服务端。
- **`internal/pipeline.go`**：
  - 移除了专门排队发请求的 TTS Worker goroutine 模型，直接将大模型出来的首包或短句顺向推入 `stream.SendText`。
  - 大模型彻底结束后，发送 `stream.Finish()` 安全关闭服务端 Session 任务。

## 3. 验证情况
- 依赖测试与构建均已跑通 (`go build` / `go test`)。
- `segmenter` 单测也已针对精简化的接口通过。

## 4. 下一步建设建议
当前 TTS 流打通后，大模型输出极快时，豆包语音合成的返回可能存在几百毫秒延时属于云端原生耗时，如果在弱网或服务器负载高峰期仍然存在顿挫感，后续可通过“长上下文带能量控制的 Audio Buffer”来进一步掩盖网络抖动（即在前端通过 Web Audio API 实现抖动缓冲 Jitter Buffer）。
