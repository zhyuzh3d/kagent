# 开发结果文档：/page/chat 集成 Counter Surface Action

- 对应计划：`plan/2026-03-10-164300-chat-page-surface-action-counter-dev-plan.md`
- 结果时间：2026-03-10 16:43 CST

## 1. 完成结论

本轮已完成 `/page/chat` 的 Surface + Action 最小闭环改造：
1. chat 页面可打开/关闭 counter surface 浮窗。
2. AI 最终回复可解析 `{content, action}` 结构。
3. action 可驱动 `demo-counter` iframe 更新数字。
4. 后端 system prompt 已补充 action 输出约束提示。

## 2. 已完成项（对应计划阶段）

### 阶段 A（计划落盘）
1. 已落盘开发计划文档。

### 阶段 B（浮窗与通信）
1. 新增 `webui/page/chat/surface-bridge.js`，负责单 Surface iframe + MessageChannel。
2. `webui/page/chat/index.html` 增加 Surface 按钮、浮窗样式与挂载点。

### 阶段 C（action 解析执行）
1. 新增 `webui/page/chat/action-engine.js`，实现 action envelope 解析与白名单动作映射。
2. 改造 `webui/page/chat/event-router.js`，在 `llm_final` 执行 action hook。
3. 改造 `webui/page/chat/chat-store.js`，支持 AI 气泡读写/移除（content/action 分离）。

### 阶段 D（后端 prompt 增强）
1. 改造 `internal/llm.go`，构建 system prompt 时注入 counter action 协议提示。
2. 新增 `internal/llm_prompt_test.go`，覆盖“追加提示”和“避免重复追加”。

### 阶段 E（验证）
1. 前端模块语法检查通过：
   - `node --check webui/page/chat/chat-store.js`
   - `node --check webui/page/chat/event-router.js`
   - `node --check webui/page/chat/action-engine.js`
   - `node --check webui/page/chat/surface-bridge.js`
2. 后端验证通过：
   - `go test ./...`
   - `go build -buildvcs=false ./...`

## 3. 风险与后续

1. 当前 action envelope 解析在前端完成，若模型输出不稳定，可能出现“无动作执行”。
2. 语音播报链路暂未做 action 文本剥离，后续可在 pipeline 做优化。
3. 当前仅接入单 counter surface，后续可扩展为多 surface 动态注册。  
