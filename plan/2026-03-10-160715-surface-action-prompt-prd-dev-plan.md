# 开发计划文档：Surface + Action + Prompt Assembly

- 需求来源：`plan/2026-03-10-160715-surface-action-prompt-prd.md`
- 计划文档：`plan/2026-03-10-160715-surface-action-prompt-prd-dev-plan.md`
- 编写时间：2026-03-10 17:10 CST
- 适用范围：本地 `localhost` 浏览器端 MVP（同仓库当前架构）

## 1. 目标与约束

### 1.1 总目标
在现有 `kagent` 单机架构内，完成可运行的 Surface（iframe）调度页、LLM Action 执行链路和提示词组装链路的首个闭环，实现“可加载 Surface、可执行动作、可追溯记录、可组装提示词”。

### 1.2 本轮开发范围（In Scope）
1. 新增 Main Surface 页面（`webui/page/surface/`）与基础调度能力。
2. Surface Manifest 预检、默认 sandbox/allow 策略与加载前确认。
3. MessageChannel 绑定（`surface_id + session_token`）与冻结/恢复消息通道。
4. 单消息 JSON 协议解析：`content` 流式展示 + `action` 结束后执行。
5. Action Dispatcher（白名单校验、参数校验、执行、记录、Observation）。
6. Prompt Assembly（defaults + user override + runtime context + allowed_actions）。
7. 最小可用 demo Surface（`webui/surface/`）用于端到端联调。

### 1.3 本轮非目标（Out of Scope）
1. 不引入后端持久化数据库（Action record 先内存 + `localStorage`）。
2. 不改造现有语音会话主链路（`/ws`、ASR/LLM/TTS）为强依赖项。
3. 不实现复杂策略系统（灰度/多版本 owner 审批）。
4. 不实现强制中断型 Action 执行器（先提供协作式取消接口占位）。

### 1.4 约束条件
1. 不破坏既有 `/page/chat/` 能力。
2. 默认权限策略必须“最小授权”：`sandbox="allow-scripts allow-downloads"`。
3. 动作执行必须严格受 `allowed_actions` 控制，禁止越权调用。
4. 所有关键行为可追溯（至少有日志与 record）。

## 2. 需求分解与设计决策

### 2.1 Surface 加载与权限
1. 加载流程：`fetch HTML -> 解析 manifest -> 计算权限 -> 超额确认 -> 创建 iframe`。
2. Manifest 约定（MVP）：支持内嵌 `<script type="application/json" id="surface-manifest">...</script>`。
3. 权限决策：
   - 默认 `sandbox`: `allow-scripts allow-downloads`
   - 申请额外 sandbox token 时弹确认
   - `allow` 由 manifest 填充（camera/microphone 等）

### 2.2 通信与身份绑定
1. 每个 Surface 使用独立 `MessageChannel`。
2. 握手字段：`surface_id`、`session_token`。
3. 冻结策略：
   - 冻结后父层停止向子层派发业务消息。
   - 冻结后父层忽略来自该 Surface 的业务消息。

### 2.3 LLM JSON 协议执行
1. 协议结构：
   - `content: string`
   - `action: {id,name,args,timeout_s}|null`
2. 执行时机：
   - 流式阶段仅展示 `content`。
   - 完整消息 `JSON.parse` 成功后再执行 `action`。
   - parse 失败绝不执行 action。
   - `content` 为空时不生成回答气泡，但仍可执行 action。

### 2.4 Action 系统
1. Dispatcher 责任：
   - 白名单校验（`allowed_actions`）
   - 参数基础校验
   - 执行计时
   - record 落地
   - Observation 生成并写回历史
2. 首批 Action：
   - `ui.toast`
   - `surface.send_event`
   - `surface.freeze`
   - `surface.unfreeze`
   - `surface.reload`
   - `records.query`

### 2.5 Prompt Assembly
1. 采用两层配置：
   - defaults（代码内置）
   - user override（`localStorage`）
2. 渲染输入：
   - 当前 Surface 列表/冻结状态
   - allowed_actions
   - manifest 摘要
3. 产出：
   - 最终 prompt 文本
   - prompt hash
   - 配置 hash
   - 渲染时间

## 3. 实施阶段与任务清单

### 阶段 A：页面与骨架（P0）
1. 新建 `webui/page/surface/index.html`，提供主布局、窗口区域、Action/记录/Prompt 面板。
2. 新建模块化脚本（状态管理、Surface 管理、Action 管理、Prompt 组装）。
3. 保证独立访问路径 `/page/surface/`。

验收：
1. 页面可打开，基础面板正常渲染。
2. 不影响 `/page/chat/`。

### 阶段 B：Surface 生命周期（P1）
1. 实现 manifest 抽取、权限计算与加载前确认。
2. 实现 Surface Container 操作：最小化、最大化、重载、冻结/恢复、查看授权。
3. 实现 MessageChannel 握手与父子消息收发。

验收：
1. 可加载 demo Surface 并成功握手。
2. 冻结后消息停止，恢复后继续。

### 阶段 C：LLM Action 执行链路（P1）
1. 实现流式 `content` 提取器（增量展示）。
2. 完整消息 parse 后执行 action。
3. 执行结果写入 record，并生成 Observation。

验收：
1. `content` 能边接收边显示。
2. parse 失败不会触发 action。
3. `content=""` 且 action 有效时可执行动作且无回答气泡。

### 阶段 D：Prompt Assembly（P2）
1. 实现 defaults + user override 合并。
2. 注入 runtime context 与 allowed_actions。
3. 输出 hash 与渲染元数据。

验收：
1. Prompt 预览与 hash 可见。
2. allowed_actions 变化时 prompt 同步变化。

### 阶段 E：联调与收敛（P2）
1. 增补 demo Surface（manifest + channel handler）。
2. 自测核心路径与异常路径。
3. 补充最小文档注释。

验收：
1. 端到端跑通：加载 Surface -> 输入 JSON 消息 -> 执行动作 -> 产生日志/记录。

## 4. 验证矩阵（科学可复现）

1. 权限验证：
   - Case A：manifest 无额外 sandbox -> 无确认弹窗。
   - Case B：manifest 申请 `allow-same-origin` -> 必须弹窗确认。
2. 协议验证：
   - Case A：合法 JSON + content + action -> content 流式展示，结束执行 action。
   - Case B：合法 JSON + 空 content + action -> 不显示回复，执行 action。
   - Case C：非法 JSON -> 不执行 action，记录 parse 错误。
3. 冻结验证：
   - 冻结后父发子、子发父业务消息均停用。
4. 白名单验证：
   - 非 `allowed_actions` 动作一律拒绝，写入失败 record。
5. 追溯验证：
   - 每次 action 至少记录：`record_id/message_id/action_id/action_name/status/duration_ms/result`。

## 5. 风险与应对

1. 风险：流式 JSON 解析边界（转义字符）复杂。
   - 应对：采用状态机提取 content；最终以 full-text JSON.parse 作为唯一执行依据。
2. 风险：iframe 权限策略易误配导致不可用。
   - 应对：授权视图可视化当前 sandbox/allow；错误回退默认模板。
3. 风险：Action 并发造成状态冲突。
   - 应对：记录内含 message_id/action_id；对同一 Surface 操作做轻量串行化（先到先执行）。

## 6. 交付物

1. 计划文档：本文件。
2. 代码交付：
   - `webui/page/surface/*`
   - `webui/surface/*`（demo）
   - 必要时 `main.go` 路由细化（若需要）。
3. 验证输出：
   - 构建/测试命令结果
   - 手工验收步骤与结果摘要

## 7. 执行顺序（本次会话）

1. 完成本开发计划文档落盘。  
2. 立刻进入 `dev` 实施阶段 A+B+C 的可运行最小闭环。  
3. 完成基础验证（至少包含静态资源可访问性、Go 构建或测试不回归）。  
4. 输出本轮完成项、未完成项与下一步建议。  
