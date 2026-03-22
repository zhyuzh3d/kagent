# AI Doubao Silero ASR Gate 开发计划

## 0. 维护信息

- 文档类型：开发计划 / 后端安全节费改造方案
- 创建时间：2026-03-22 23:00:00 CST
- 目标范围：
  - `services/ai_doubao/cmd/ai_doubao/*`
  - `services/ai_doubao/internal/app/*`
  - 必要时新增 `services/ai_doubao/internal/vad/*` 或等价私有目录
  - 必要时新增 `services/ai_doubao/config/*` 中的非敏感运行配置样例字段
- 明确不在本次范围：
  - `webui/page/chat/*`
  - `services/chat_server/internal/app/*`
  - Hub 工具协议与前端交互协议
- 依据：
  - `AGENTS.md`
  - `doc/_instruction.md`
  - `doc/_instruction/core.md`
  - `doc/_instruction/structure.md`
  - `services/ai_doubao/cmd/ai_doubao/media_ws_handlers.go`
  - `services/ai_doubao/internal/app/asr_runtime.go`
  - `services/ai_doubao/internal/app/asr_config.go`
  - `services/ai_doubao/internal/app/public_config.go`
  - `services/chat_server/internal/app/asr.go`
  - `services/chat_server/internal/app/session_turns.go`
  - `services/chat_server/internal/app/session_runtime.go`
  - `webui/page/chat/lib/audio-capture.js`
  - `webui/page/chat/lib/session-controller.js`

## 1. 目标结论

本次改造的正确目标不是“在已连接 Doubao ASR 后再做一次本地过滤”，而是将 `ai_doubao` 的 ASR 上游调用改为“本地先判定、确认有人声后再延迟连接 Doubao”。

计划落地后，应满足以下结果：

1. 前端与 `chat_server` 无需修改协议和调用方式，仍继续把 PCM16 音频流送入 `ai.speech.asr`。
2. `ai_doubao` 在本地先缓存一小段前置音频并执行 `silero` 人声判定。
3. 若未检测到有效人声，则本轮不连接 Doubao，不发送上游 ASR start/audio frame，并以空识别结果自然结束。
4. 若检测到有效人声，则立即连接 Doubao，并把缓存的 pre-roll 音频补发给上游，避免吞字。
5. 改造默认遵循“可关闭、可观测、可回滚”的安全策略，避免一次性将 ASR 主链路置于不可控状态。

本次方案的核心价值：

1. 降低空触发和环境噪声造成的 ASR 费用浪费。
2. 保持现有前端 Client-Driven 架构不变，避免引入新的前后端状态分裂。
3. 将风险限制在 `ai_doubao` 内部，符合当前 Service 自治边界。

## 2. 当前真实现状

基于现有代码核对，当前 ASR 主链路事实如下：

1. 前端已有基于 RMS 的轻量 VAD，只负责本地启停和 turn 节奏，不负责可靠的人声/非人声分类。
2. `chat_server` 通过 `ai.speech.asr` WebSocket 将音频持续转给 `ai_doubao`。
3. `ai_doubao` 当前在收到 ASR WS 请求后，会立刻创建 `DoubaoASRClient` 并执行 `Run(...)`。
4. `DoubaoASRClient.Run(...)` 当前实现会先连接 Doubao 上游并发送 ASR start frame，然后才开始消费音频帧。
5. 这意味着当前只要 `ai_doubao` 进入一次 ASR turn，就可能已经消耗上游连接与识别资源，即使后续音频没有真实人声。

对应代码证据：

1. `services/ai_doubao/cmd/ai_doubao/media_ws_handlers.go` 中 `handleASRWS(...)` 在读取 start 请求后直接执行 `asr.Run(ctx, audioCh, events, startReq.History)`。
2. `services/ai_doubao/internal/app/asr_runtime.go` 中 `Run(...)` 会先 `DialContext(...)` 连接上游，再发送 start frame。
3. `services/chat_server/internal/app/asr.go` 中 `HubASRClient.Run(...)` 对 `ai.speech.asr` 的使用方式是“先发 start，再持续发 binary 音频帧”。
4. `services/chat_server/internal/app/session_turns.go` 中空文本路径已经具备 `turn_nack` 与“no speech detected”收口逻辑。

## 3. 改造原则

## 3.1 只改 `ai_doubao`

本次改造必须保持以下边界：

1. 不改前端音频采集、VAD 和 turn 控制协议。
2. 不改 `chat_server` 的 ASR client 协议与会话状态机。
3. 不改 Hub 工具平面和 `ai.speech.asr` 对外契约。

## 3.2 先判定，再连上游

节费目标只有在以下前提下才成立：

1. 本地 VAD/speech gate 先于 Doubao 上游连接发生。
2. 未命中人声时，完全不建立 Doubao ASR 上游连接。
3. 已命中人声时，补发 pre-roll，确保识别准确率不因延迟放行而明显下降。

## 3.3 默认安全优先

本次改造必须可配置关闭，并保留快速回退路径：

1. 默认需要开关控制。
2. 人声门控异常时，可退化为“直接走现有 Doubao ASR 逻辑”。
3. 观测字段必须能区分“本地拦截”“本地放行”“silero 异常退化”“上游真实调用失败”。

## 3.4 避免重演旧的后端能量误判问题

历史开发记录已明确说明：后端简单能量判定曾带来自激回声和误打断风险。故本次方案必须坚持：

1. `silero` 只用于“是否值得发送到 Doubao”的前置网关，不用于直接触发业务打断。
2. 不恢复后端主导的 interrupt 决策。
3. 不把本地 gate 的判断结果直接当作业务语义输入。

## 4. 目标设计

## 4.1 总体结构

在 `ai_doubao` 内部为当前 ASR WS 处理链新增一层本地 speech gate，推荐结构如下：

1. `handleASRWS(...)` 仍负责读取 start/control/audio 帧和对外回写事件。
2. 新增 `GatedASRSession` 或等价私有组件，负责：
   - 缓存前置 PCM
   - 调用 `silero` 执行人声判定
   - 在“未放行”与“已放行”两种状态间切换
   - 决定是否创建真实 `DoubaoASRClient`
3. `DoubaoASRClient` 继续作为“已放行后的真实上游适配器”，不承载本地 gate 逻辑。

推荐状态机：

1. `Idle`：已收到 start，请求已建立，尚未消费足够音频。
2. `Buffering`：本地收集前置帧，周期性送给 `silero` 判定。
3. `Passed`：确认有人声，创建 Doubao 上游连接，发送 start 和缓存音频，随后透明转发后续音频。
4. `Dropped`：到 finish 或连接结束前都未判到人声，本地结束，不访问 Doubao。
5. `Bypass`：配置关闭或 `silero` 初始化失败时，直接进入旧逻辑。

## 4.2 本地缓存与放行策略

建议设计为“双缓冲 + 最小连续命中”：

1. 缓存窗口：保留约 300ms 到 800ms PCM16 mono 16k 的 pre-roll。
2. 判定窗口：按 `silero` 适配的固定帧长切分，例如 30ms、60ms 或模型要求的采样点数。
3. 放行条件：不是单帧命中即放行，而是要求连续命中达到阈值，避免瞬态噪声误触发。
4. 丢弃条件：若收到 `finish`、客户端断连、或超过最大等待时长仍未命中，则直接本地结束。

关键要求：

1. 放行后必须把 pre-roll 一起补发给 Doubao。
2. 未放行时不得提前向 Doubao 发送 start frame。
3. 未放行结束时，不向前端伪造文本；应让上层自然走空文本收口。

## 4.3 `silero` 运行时接入策略

本次计划默认采用“模型推理与业务逻辑解耦”的设计，建议引入独立接口：

1. 定义 `SpeechDetector` 接口，例如：
   - `AcceptPCM(frame []byte) (decision SpeechDecision, err error)`
   - `Reset()`
   - `Ready() bool`
2. 通过该接口屏蔽底层实现差异：
   - 正式实现：`SileroDetector`
   - 兜底实现：`BypassDetector` 或 `NullDetector`
3. `GatedASRSession` 只依赖 `SpeechDetector`，不直接依赖 ONNX 细节。

这样做的目的：

1. 避免 `media_ws_handlers.go` 直接膨胀为推理逻辑堆栈。
2. 后续若替换 `silero`、增加阈值实验或做离线回放测试，不需要重写 ASR 主逻辑。
3. 可在无模型文件或无 ONNX runtime 的环境中安全退化。

## 4.4 配置设计

推荐在 `ai_doubao` 公开运行配置中新增独立门控配置，而不是把参数塞进现有 `chat.asr.*`：

```json
{
  "chat": {
    "asrGate": {
      "enabled": true,
      "mode": "silero",
      "preRollMs": 480,
      "maxWaitMs": 1200,
      "minSpeechFrames": 3,
      "speechProbThreshold": 0.6,
      "silenceProbThreshold": 0.35,
      "bypassOnError": true,
      "debugLogDecisions": false
    }
  }
}
```

配置原则：

1. 开关必须存在，支持一键关闭。
2. 所有阈值都应有保守默认值。
3. `bypassOnError` 默认建议为 `true`，确保主链路可用性优先。
4. 模型文件路径与 ONNX runtime 依赖路径若属于敏感或部署细节，应放在 `configx.json` 或服务私有路径，不暴露到公开说明中。

## 4.5 事件与对外行为约束

对 `chat_server` 和前端而言，本次改造后行为应保持兼容：

1. 若判定为无人声，不发送 `partial/final` 文本事件即可。
2. 是否发送空 `endpoint` 事件需要谨慎；优先保持“无文本、正常 finish”即可，让上层依据已有 `Finish + wait final + empty text` 收口。
3. 不新增必须由前端消费的新事件类型。
4. 若需调试观测，应优先写日志，不扩散到正式协议。

## 5. 分阶段实施计划

## 5.1 第一阶段：抽离“延迟连接上游”的基础骨架

目标：

1. 将 `ai_doubao` 现有“收到 start 后立即连 Doubao”的流程，重构为“可延迟创建上游 client”的结构。
2. 在不接入 `silero` 的前提下，先让代码具备 `buffer -> decision -> connect` 的基础形态。

建议修改点：

1. `services/ai_doubao/cmd/ai_doubao/media_ws_handlers.go`
2. `services/ai_doubao/internal/app/asr_runtime.go`
3. 新增 gate/session 相关私有文件

阶段验收：

1. 代码结构已允许在真正放行前不创建 `DoubaoASRClient`。
2. 关闭 gate 时行为与当前版本保持一致。
3. 已具备 pre-roll 缓存和透明转发骨架。

## 5.2 第二阶段：接入 `SpeechDetector` 抽象与旁路实现

目标：

1. 引入 `SpeechDetector` 接口。
2. 先实现 `BypassDetector`，确保 gate 框架在无模型情况下可联调。

阶段验收：

1. `GatedASRSession` 仅依赖接口，不直接依赖具体 `silero` 实现。
2. `BypassDetector` 启用时可立即放行，上游行为与旧逻辑一致。
3. 现有 ASR 冒烟链路可正常工作。

## 5.3 第三阶段：接入 `silero` 正式实现

目标：

1. 为 `ai_doubao` 引入 `silero` 运行时与模型加载逻辑。
2. 实现固定帧长推理、概率阈值判断和最小连续命中策略。

重点任务：

1. 模型文件管理：确定模型文件放置路径、缺失时的错误语义和部署方式。
2. runtime 管理：确定 ONNX runtime 的 Go 绑定、动态库依赖和启动初始化时机。
3. 资源复用：尽量复用单例 detector 或模型 session，避免每个 ASR turn 重新加载模型。

阶段验收：

1. `silero` 可稳定初始化，且不会在每轮 ASR 时重复高成本加载。
2. 命中人声时能正常放行并产出 Doubao 识别结果。
3. 未命中人声时不建立 Doubao 上游连接。

## 5.4 第四阶段：观测、调参与容错

目标：

1. 补齐日志、计数和调试信息。
2. 通过实机语音与环境噪声场景做阈值调优。

至少应记录：

1. gate 启动次数
2. gate 放行次数
3. gate 拦截次数
4. gate 初始化失败退化次数
5. 从开始缓存到放行的耗时
6. 放行后首个 Doubao partial/final 的耗时

阶段验收：

1. 可通过日志区分“silero 拦截”“silero 放行”“silero 故障旁路”。
2. 噪声误放行率和语音漏放行率达到可接受水平。
3. 首字吞音问题可控。

## 6. 安全与风险控制

## 6.1 最大风险

本次改造的主要风险不是代码编译，而是运行时行为：

1. 误判漏放行，导致真实用户语音没被送去识别。
2. 放行过慢，导致首字或短句前半段被吞。
3. `silero` runtime 初始化或部署失败，导致 `ai_doubao` ASR 整体不可用。
4. 每连接一次都重复加载模型，引发 CPU、内存和时延飙升。

## 6.2 风险缓解策略

1. 默认提供 `enabled` 开关与 `bypassOnError`。
2. 保留完整旧路径，可在 gate 异常时立即回退。
3. 放行逻辑必须带 pre-roll 回补。
4. 初始阈值保守设置，先宁可多放行，避免大规模漏识别。
5. 将 `silero` 初始化前置到服务启动或受控懒加载，避免在首轮会话中产生不可控大抖动。

## 6.3 不建议的做法

1. 不建议把 `silero` 判断结果直接映射为 `interrupt` 或业务事件。
2. 不建议在 `chat_server` 再重复加一层后端 gate，与 `ai_doubao` 形成双重状态。
3. 不建议先连 Doubao 再等待 `silero` 结论，那样几乎失去节费意义。

## 7. 测试与验收设计

## 7.1 单元级验证

至少覆盖以下场景：

1. 连续静音，直到 finish，确保不连接 Doubao。
2. 短促噪声脉冲，确保不误放行。
3. 正常人声开头，确保放行并补发 pre-roll。
4. 人声较短、停顿较快，确保不会因等待过长而吞掉整句。
5. detector 初始化失败，且 `bypassOnError=true`，确保退化到旧逻辑。
6. detector 初始化失败，且 `bypassOnError=false`，确保错误可解释且不会静默卡死。

## 7.2 集成级验证

至少覆盖以下链路：

1. `webui/page/chat -> chat_server -> hub -> ai_doubao` 的正常语音对话。
2. 环境噪音、键盘声、敲桌子等非人声输入。
3. AI 播报中用户插话，确保现有前端 Ducking / barge-in 行为不被破坏。
4. 连续多轮对话，确认 detector 状态会正确 reset，不污染下一轮。

## 7.3 观测验收

完成后至少应能回答：

1. 本地 gate 拦截了多少次原本会进入 Doubao 的空触发。
2. 放行后对首个 ASR partial/final 的时延增加了多少。
3. 是否出现明显漏识别和吞字回归。

## 8. 实施顺序建议

建议按以下顺序推进，避免一次跨太多层：

1. 先完成 `ai_doubao` 内部延迟连接骨架和 pre-roll 缓冲。
2. 再引入 `SpeechDetector` 抽象和 bypass 版本，先跑通主链路。
3. 然后接入 `silero` 正式实现。
4. 最后再做阈值调优、日志补齐和部署稳定性验证。

若资源有限，建议拆成两个里程碑：

1. 里程碑 A：延迟连接骨架 + bypass + 配置开关 + 观测
2. 里程碑 B：`silero` 正式接入 + 调参 + 实机回归

## 9. 完成标准

本次改造完成后，必须同时满足：

1. `ai_doubao` 可在不修改前端和 `chat_server` 的情况下启用本地 speech gate。
2. 未判定为人声的音频流不会连接 Doubao ASR 上游。
3. 判定为人声的音频流可正常连接 Doubao，并维持现有识别能力。
4. gate 具备配置开关、错误旁路和日志观测。
5. 对话主链路在 `silero` 失败、缺失或关闭时，仍能退化回现有稳定模式。
6. 实机验证中不存在明显的新回归：大量漏识别、首字吞音、异常卡死、打断行为失真。

## 10. 待确认事项

以下事项在正式开发前应再做一次工程核实：

1. 目标部署环境是否已具备可接受的 ONNX runtime 引入条件。
2. `silero` 模型文件将随仓库存放、随构建产物分发，还是运行时外挂。
3. Doubao 计费语义更偏“建立会话即计费”还是“实际音频识别时计费”；若能进一步确认，可更准确评估收益上限。
4. 是否需要在 `service/admin` 配置页暴露 `asrGate.*` 公开参数。

---

**文档更新时间**：2026-03-22 23:00:00 CST

**本轮修改范围**：新增 `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/plan/2603222300-ai-doubao-silero-asr-gate-devplan.md`，沉淀“仅改 `ai_doubao`、通过本地 `silero` speech gate 节省 Doubao ASR 调用成本”的分阶段开发计划。

**信息来源**：本轮用户目标确认、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/AGENTS.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`，以及对 `services/ai_doubao`、`services/chat_server`、`webui/page/chat` 现有 ASR 真实链路的代码核对结果。
