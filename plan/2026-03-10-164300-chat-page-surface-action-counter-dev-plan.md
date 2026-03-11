# 开发计划文档：/page/chat 集成 Counter Surface Action

- 计划时间：2026-03-10 16:43 CST
- 需求来源：当前会话用户需求（基于 `surface/action` 既有实现思路）
- 参考文档：
  - `plan/2026-03-10-160715-surface-action-prompt-prd.md`
  - `plan/2026-03-10-160715-surface-action-prompt-prd-dev-plan.md`

## 1. 目标定义

在不破坏现有语音对话主链路（ASR/LLM/TTS）的前提下，改造 `/page/chat`：
1. 可加载并展示一个极简 `demo-counter` Surface 浮窗（iframe）。
2. AI 在回复中可携带 action 参数。
3. 前端可解析 action 并通过统一 action 机制驱动 iframe 更新数字。

## 2. 范围与边界

### 2.1 本次范围
1. `/page/chat` UI 增加 Surface 浮窗入口与面板容器。
2. 新增 chat 侧 `surface bridge`（单 Surface 生命周期 + MessageChannel）。
3. 新增 chat 侧 `action engine`（解析消息、校验 action、执行动作）。
4. 事件路由改造：在 `llm_final` 时做 action 解析与执行，支持 `content` 与 `action` 分离。
5. 后端最小改造：补充 system prompt 协议提示，提升模型输出 action 结构概率。

### 2.2 非范围
1. 不做多 Surface 调度（本次固定 counter demo）。
2. 不做后端 action record 持久化。
3. 不重构 ASR/LLM/TTS 主链路协议。

## 3. 技术方案

### 3.1 Chat 页面结构改造
1. 在 chat header 增加 `Surface` 按钮。
2. 页面新增浮窗容器（默认隐藏），用于加载 `/surface/demo-counter.html`。
3. 浮窗支持：显示/隐藏、重载、冻结状态展示。

### 3.2 Surface Bridge（前端）
1. 新文件：`webui/page/chat/surface-bridge.js`。
2. 责任：
   - 创建/销毁 iframe；
   - 建立 `MessageChannel` 握手（`surface_connect`）；
   - 封装 `dispatchAction(actionCall)`；
   - 维护 frozen 状态（冻结时拒绝动作）。

### 3.3 Action Engine（前端）
1. 新文件：`webui/page/chat/action-engine.js`。
2. 解析策略（按优先级）：
   - 纯 JSON 对象 `{content, action}`；
   - 代码块 JSON（```json ... ```）中的 `{content, action}`。
3. 执行策略：
   - 校验 action 白名单（仅 counter 相关动作）；
   - 调用 `surface-bridge.dispatchAction`；
   - 将 `content` 写回 AI 气泡（若空则隐藏/移除气泡）。

### 3.4 Event Router / Chat Store 改造
1. `event-router.js` 增加 action hook（仅在 `llm_final` 执行）。
2. `chat-store.js` 增加 AI 气泡读写接口：
   - `getAIMsgText(turnId)`
   - `setAIMsgText(turnId, text)`
   - `removeAIMsg(turnId)`
3. 保持原有 stale 过滤逻辑不变。

### 3.5 后端 Prompt 最小增强
1. 改造 `internal/llm.go` 构建 system prompt 时追加 action 协议提示（轻量）。
2. 原则：
   - 普通回答仍可返回自然文本；
   - 需要控制 counter 时，返回 `{content, action}` 结构。

## 4. 分阶段执行

### 阶段 A：文档与骨架
1. 落盘本计划文档。
2. 生成 chat surface/action 模块骨架文件。

### 阶段 B：前端浮窗与通信
1. chat 页面接入浮窗 UI。
2. 完成 `surface-bridge.js` 并连通 `demo-counter`。

### 阶段 C：action 解析与执行
1. 完成 `action-engine.js` 解析器。
2. 改造 event-router/chat-store 接入 action 执行链路。

### 阶段 D：后端 prompt 增强
1. 在 `internal/llm.go` 注入 action 输出约束提示。
2. 确认对现有普通对话链路兼容。

### 阶段 E：验证与交付
1. 前端 JS 语法检查。
2. `go test ./...` 与 `go build -buildvcs=false ./...`。
3. 输出完成项/风险/后续建议。

## 5. 验收标准

1. 进入 `/page/chat` 可一键显示 counter surface 浮窗。
2. 当 AI 回复带有合法 action 时，counter 数字可被更新。
3. action 非法或超白名单时，不执行且可见错误日志。
4. 普通无 action 对话流程不回归。

## 6. 风险与缓解

1. 风险：模型不稳定输出 action JSON。
   - 缓解：后端 system prompt 追加明确协议；前端支持 code-block JSON 兜底解析。
2. 风险：动作文本被 TTS 朗读影响体验。
   - 缓解：先保证功能闭环；后续可在 pipeline 里做 action 文本剥离优化。
3. 风险：浮窗通信未就绪导致动作丢失。
   - 缓解：surface bridge 增加 ready/frozen 校验与错误回执。

## 7. 本轮执行承诺

1. 本轮将直接完成阶段 A~E 并交付可运行版本。
2. 若出现阻塞，优先保持“普通聊天不回归”，并给出最小可行 fallback。  
