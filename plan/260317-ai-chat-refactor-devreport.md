# AI & Chat Services Refactor (Hub-Centric Alignment) 开发报告

## 1. 目标与背景
本次开发的目的是重构 `ai-doubao` 和 `chat-server` 两个核心服务，使其完全符合 Hub-Centric 架构（Service -> Hub -> Service），彻底消除服务间的直连与强耦合。同时修复 Streaming Capabilities 注册的 Bug，清理遗留的大量复制粘贴的冗余代码。

## 2. 关键变更点
### 2.1 修复 Streaming 注册 Bug (ai-doubao, chat-server)
- **问题**：`toSupervisorToolsFromDescriptors` 中使用 `strings.EqualFold(descriptor.Streaming, "stream")` 判断是否支持流式返回，但实际配置为 `ws_binary`/`sse`/`none`，导致所有大模型/ASR/TTS工具均被注册为 non-streaming。
- **修复**：修改判断逻辑为 `isStreaming := streaming != "" && !strings.EqualFold(streaming, "none")`，同时补齐了 `chat-server` 在注册时遗漏的 `TimeoutMS` 和 `CapabilitiesRequired` 字段。

### 2.2 ai-doubao 标准化 Hub Endpoint
- **变更**：新增了 `/service/tool/exec` 统一工具执行入口。
- **机制**：通过 `hubsvc.VerifyServiceSessionTokenLoose` 使用 Hub 共享密钥严格校验 `X-Hub-Service-Token`。
- **路由**：针对 `ai.speech.tts` 进行分发处理，将其转发给底层的 `ttsClient.Synthesize`，并按 `toolproto.CallResponse` 标准结构返回 base64 编码的音频。原有的 `/v1/*` WebSockets/SSE 端点保留，提供给前端与遗留系统过渡期使用。

### 2.3 Hub 通用 WS 代理动态化
- **问题**：原 `/api/tool/ws` 端点硬编码固定转发给 `chat-server`。
- **修复**：读取 URL 的 `service_id` 查询参数进行动态服务发现 `reg, ok := hubPlatform.GetService(targetService)`，找不到或未传时默认降级回 `chat-server`。这使得 Hub 具备了通用的长连接握手透传能力。

### 2.4 剥离 chat-server 的不合规直连
- **变更**：完全删除了 `chat-server` 内部用于强管 `ai-doubao` 进程的 `AIServiceManager`，以及绕过 Hub 直接调用私有端点的 `ServiceProviderFactory`。
- **机制**：将 `providerFactory` 直接退化使用 `LocalProviderFactory`。即在过渡期，`chat-server` 直接调用底层大模型 SDK 来处理对话，直到后续形态进一步演化。这彻底切断了 `chat-server -> ai-doubao` 的异常链路。

### 2.5 大规模冗余代码清理
- **清理结果**：在 `ai-doubao` 侧安全删除了 `session.go`、`sqlite_store.go`、`provider_factory.go`、`operation_log.go`、`runtime_root.go`、`pipeline.go` 等完全未被使用的庞大文件集合（由于建项时直接复制于 `chat-server` 导致的残留），总计精简超过 2500 行无用代码。

## 3. 测试与验证
1. **编译检查**：执行 `go build ./...`（覆盖全局），所有服务（Hub, ai-doubao, chat-server 等）均一次性编译通过。
2. **测试用例**：执行 `go test ./hub/...`，无测试失败（已有的逻辑被完好隔离，未破坏 Hub 的状态维护）。

## 4. 后续建议
1. 验证目前的端到端语音识别与文本处理路径（需配合前端手动验证）。
2. 在系统稳定运行一段时间后彻底下线 ai-doubao 的私有 `/v1/*` 端点，推动所有组件通过 Hub `/api/tool/ws?service_id=ai-doubao` 建立长连。
