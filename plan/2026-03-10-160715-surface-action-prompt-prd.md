# 产品功能设计文档（PRD）
**主题**：浏览器端 Surface（iframe 小应用）+ LLM Action 自主动作 + 提示词工程自动化  
**日期**：2026-03-10 16:07:15 CST  
**范围**：本地 `localhost` 个人/本地多用户使用场景（浏览器访问本地服务）  

---

## 1. 背景与目标

### 1.1 背景
产品在浏览器里运行（`localhost`），用户可通过 LLM 生成或编写多个“Surface 小应用”。每个 Surface 以单个 HTML（优先 iframe 承载）形式加载，支持实时交互与高频更新（如下棋：AI 反复移动棋子）。

### 1.2 目标
- 用 **iframe（Surface）** 承载 AI 生成的小应用，实现强 UI 隔离、可控权限与统一管理。
- 设计一套 **LLM 返回数据格式**，兼顾：
  - `content` 可尽快流式展示；
  - `action`（Action call）在消息完整接收后执行；
  - `content` 为空/缺失时仍可执行 `action`（不展示回答气泡）。
- 建立 **Action 系统**：LLM 可“自主动作”，动作既可做纯前端操作，也可通过 API 调用后端能力；并能记录行为历史供 LLM 回顾。
- 建立 **提示词工程自动化（Prompt Assembly）**：按配置渲染提示词、注入实时变量与 `allowed_actions`，并可观测可追溯。

### 1.3 非目标（本阶段不做）
- 不强制引入复杂的提示词片段版本/灰度/owner 系统（采用“默认配置 + 用户配置覆盖”的 config 思路）。
- 不要求所有 Action 统一自动继续机制（是否调用后端 LLM、是否继续下一轮由 Action 内部决定）。
- 不做 action/permission 记录导出（只需在 UI 内可查看）。

---

## 2. 核心概念与术语（统一口径）

> 本节用于后续研发与对话统一表述（中英对齐）。

- **Surface**：iframe 形式的“交互面板单元/小应用”，拥有独立 UI 与内部状态，可与父层通过消息协议通信。
- **Main Surface**：位于 `web/page` 的主页面（桌面/调度面），负责加载/管理 Surface、授权检查、Action 执行与全局可视化控制。
- **Surface Container**：Main Surface 内的容器组件，负责单个 Surface 的生命周期管理（fetch→授权→加载→运行期控制）。
- **Action**：可被调用的动作函数（可纯前端、可 RPC 触发某 Surface 行为、可通过 API 调用后端能力）。
- **Action call**：LLM 在一条 Message 中输出的动作调用请求（包含 `id/name/args`）。
- **Action record**：动作执行的记录（至少包括请求与结果、耗时、状态、可审计字段）。
- **Allowed actions**：当前 UI 状态机投影出的“可用 Action 白名单集合”（动态变化；全局通用 + 当前 Surface 可用动作）。
- **Message**：LLM 一次输出单元（流式到达，结束后形成完整文本）。
- **Observation**：由执行器生成并写回消息历史的“真实执行结果摘要”，用于让 LLM 知道“自己做过什么、做成了什么”。
- **Manifest（Surface Manifest）**：每个 Surface 的声明信息（至少包含权限需求、能力描述、可选的动作能力声明等）。

---

## 3. 总体架构（产品视角）

### 3.1 页面与资源组织
- `web/page/*`：Main Surface（桌面/调度面）。
- `web/surface/*`：Surface HTML（AI 生成或用户编写），同源 `localhost` 下不同路径。

### 3.2 运行期对象
- Main Surface（父层）可同时加载多个 Surface iframe（上限建议 `<= 10`）。
- 每个 Surface 在 Main Surface 内以“窗口”形式呈现（容器外框提供窗口控制按钮）。

---

## 4. Surface（iframe 小应用）设计

### 4.1 为什么用 iframe（核心收益）
- UI 隔离：DOM/CSS 天然隔离。
- 执行域隔离：结合 `sandbox` 可阻断同源特权（cookie/LocalStorage/父 DOM 访问）。
- 兼容性：单个 HTML 文件作为 Surface 交付单元，未来迁移/复用成本低。

### 4.2 默认权限策略（关键结论）
- Surface 均部署在同源 `localhost` 下，但 **默认通过 iframe sandbox 切断同源特权**：
  - 默认 `sandbox` 仅开放：`allow-scripts`、`allow-downloads`。
  - 默认 **不开放**：`allow-same-origin`（需要显式确认）。
- `allow=`（Permissions Policy）能力：
  - **由 Surface Manifest 填充**（camera/microphone 等）；
  - **不因 allow 内容弹窗确认**（本阶段：只对超出默认 sandbox 的权限做“加载前确认”）。

### 4.3 Manifest 预检与加载前确认
Surface Container 加载一个 Surface 的流程：
1. `fetch` Surface HTML（文本）；
2. 从 HTML 提取 Manifest（JSON，固定位置/固定标记）；
3. 按 Manifest 生成 iframe `allow` 列表与 `sandbox` 列表；
4. 若 `sandbox` 超出默认权限（例如申请 `allow-same-origin`、`allow-popups`、`allow-forms`、`allow-top-navigation*` 等），父层弹窗确认“是否允许加载该 Surface（以该权限模板运行）”；
5. 用户同意后，创建并加载 iframe。

> 说明：`sandbox/allow` 的变更通常需要重载/重建 iframe 才能完全生效，因此“加载前确认”是默认路径。

### 4.4 Surface Container（窗口容器）功能
每个 Surface 以窗口形式呈现，容器外框提供最小化控制按钮：
- 最小化 / 最大化
- 手工调整窗口大小（拖拽 resize）
- 重新加载（reload）Surface
- 查看当前授权情况（显示 sandbox/allow 配置与是否确认）
- **冻结/暂停 Surface 消息通道**（新增需求）：
  - 冻结后：父层不再向该 Surface 分发任何消息；也不再处理该 Surface 发来的消息（或仅允许心跳/诊断类消息，具体由实现决定）。
  - UI 表现：窗口显示“已冻结”状态，可一键恢复。

### 4.5 Surface 间通信策略
- 技术底层允许 Surface 间互发事件与接收事件。
- 工程实践默认：**所有 Surface 只向 Main Surface 发事件**；由 Main Surface 选择性转发（路由表/约定）。

### 4.6 消息通道与身份绑定（避免依赖 event.origin）
考虑 `sandbox` 默认不包含 `allow-same-origin` 时，`postMessage` 的 `origin` 可能为 `null`，不适合作为鉴别依据。
- 推荐：父层为每个 Surface 建立 `MessageChannel`，并通过首次 `postMessage` 传递 `port`（transferable）。
- 通道绑定信息：
  - `surface_id`（父层生成或来自 manifest）
  - `session_token`（握手 token，防止串线）

---

## 5. LLM 返回数据格式（Framework 1：数据格式部分）

### 5.1 设计目标
- `content` 流式尽快显示（边到边显示）。
- `action` 只在 Message 完整接收后执行，避免流式重复/撤回导致双触发。
- `content` 为空或缺失时仍能执行 `action`（不显示回答气泡）。
- 最小 token 成本、语义明确、易于 AI 理解与前端解析。

### 5.2 协议：单 JSON 对象（强约束）
统一输出严格 JSON（双引号），结构如下：
```json
{
  "content": "string (可为空)",
  "action": {
    "id": "string",
    "name": "string",
    "args": "object",
    "timeout_s": "number (可选)"
  }
}
```
规则：
- `action` 可为 `null`（纯回答，无动作）。
- `content` 可为空字符串 `""`；也允许未来扩展为缺失（但实现上建议始终存在，简化流式解析）。

### 5.3 流式显示与执行时机
- 流式阶段：
  - 前端自动监测数据流，识别并增量提取/解码 `content` 字符串（处理转义）。
  - `content` 字符串一旦结束（遇到未转义的 `"` 结束符），停止更新回答气泡。
  - 仍继续接收剩余流（包含 `action` JSON 等）。
- Message 完整接收结束：
  - 对完整文本做 `JSON.parse(fullText)`。
  - 若 parse 成功且 `action != null`：执行 Action。
  - 若 parse 失败：**不执行** Action（避免误触发）。
- `content` 为空/缺失：
  - 不显示回答气泡；
  - 若 parse 成功且 `action != null`：仍执行 Action。

### 5.4 Prompt 侧约束（供提示词引擎注入）
- 强制模型仅输出上述 JSON（不得输出多余文本）。
- 若希望动作尽快执行：建议 `content` 为空或尽量短，并将 `action` 放在 JSON 尾部。

---

## 6. Action 系统（Framework 1：Action 设计完整思路）

### 6.1 设计原则
- **开放**：Action 可做纯前端、可触发 Surface 行为、可调用后端 API。
- **边界清晰**：LLM 只能调用 `allowed_actions` 白名单中的 Action。
- **责任下沉**：频率/额度/确认/是否继续下一轮 LLM 等逻辑尽量由 Action 内部实现；框架提供统一能力（见 ctx）。
- **可追溯**：每次 Action 执行都有 Action record，并可写回 Observation 到消息历史。

### 6.2 Allowed actions（动态白名单）
- Allowed actions = 全局通用 Action + 当前 UI 状态（已加载 Surfaces、窗口状态、授权状态）下可用的 Action。
- 列表动态变化：
  - 例如下棋 Surface 与打牌 Surface 暴露的动作不同；
  - Surface 冻结时，其相关动作应从 allowed_actions 中移除或标记不可用。

### 6.3 每条 Message 的 Action 规则
- 每条 Message 最多一个 `action`（Action call）。
- 不限制不同 Message/不同对话并发执行多个 Action（允许“上一个 action 未结束，下一个 Message 的 action 已开始”）。

### 6.4 执行器（Dispatcher）职责
- 解析协议、流式展示 `content`。
- Message 结束后 parse 完整 JSON，决定是否执行 Action。
- 校验 Action name 必须在 allowed_actions 中；args 做轻量校验（类型/范围/大小）。
- 调用 Action（JS 函数）并收集结果与耗时。
- 写入 Action record；并写回 Observation 到消息历史（短摘要，非自述）。

### 6.5 统一 ctx（运行时上下文）
框架为每次 Action 执行注入 `ctx`，作为“统一能力入口”，避免 Action 随意触碰全局变量：
- `ctx.state`：读取当前 Main Surface 与各 Surface 的状态快照（只读或受控读写）。
- `ctx.surface`：对指定 Surface 的受控操作接口（例如发送事件、请求渲染、查询可用动作）。
- `ctx.ui`：主界面 UI 原语（toast/confirm/窗口操作/显示进度等）。
- `ctx.api`：后端 API 调用封装（可选；本地项目仍可能有本地服务）。
- `ctx.db`：Action record 的读写接口（可按 action/category/time 查询）。
- `ctx.audit`：统一的审计写入工具（将 message_id/action_id/耗时等标准字段落库）。
- `ctx.cancel`：协作式取消信号（Action 可自愿响应取消/超时）。
- `ctx.now`/`ctx.logger`：时间与日志（可选）。

> 注：是否在 Worker 中执行、如何实现取消/超时属于工程实现细节，本 PRD 只要求具备协作式取消能力与可记录结果。

### 6.6 Action record（记录模型）
记录目标：
- 让用户可查看“LLM 做了哪些动作、成功失败、耗时、结果摘要”；
- 让 LLM 可通过 Observation 知道“自己做过什么”；
- 让 Action 自身可读历史用于限额/频控等逻辑（按实际结果计算）。

建议字段（最小可用）：
- 标识：`record_id`、`message_id`、`action_id`、`action_name`
- 请求：`args_json`、`ts_start`
- 结果：`status(ok/fail)`、`ts_end`、`duration_ms`、`result_json`
- 审计摘要（可选）：`effects`（若提供，用于“按实际结果计数/计费/限额”，例如实际支付 20 而非参数 100）

关于 `result_json` 与 `effects`：
- `result_json`：Action 的任意业务输出（可自定义，供 UI/后续逻辑使用）。
- `effects`：可选的规范化审计摘要（用于统计/限额/展示关键影响），默认可为空；不要求承载中间过程。

### 6.7 Observation 写回消息历史
- 由执行器生成 Observation，写回到消息历史，供 LLM 后续回顾：
  - 包含：`action_id/action_name/status/关键结果摘要/record_id`
  - 避免写入大 JSON，必要时让 LLM 通过查询类 Action 拉取 record 详情。

---

## 7. 提示词工程自动化（Framework 2：Prompt Assembly 完整思路）

### 7.1 设计目标
- 采用“默认配置 + 用户配置文件覆盖”的轻量思路（类似 config）。
- 支持变量注入、条件拼接、输出约束、可观测追溯。
- 与 Surface/Action 体系打通：动态注入 `allowed_actions` 与关键状态摘要。

### 7.2 配置分层（简化版）
- `defaults`：内置默认提示词配置。
- `user_config`：每用户可编辑配置文件（覆盖 defaults）。
> 不单独引入 `session_overrides` 概念；运行时变化通过“渲染上下文”输入体现。

### 7.3 渲染上下文（Runtime Render Context）
渲染上下文由产品运行态提供，至少包含：
- 当前时间、语言等基础变量
- Main Surface 状态摘要（窗口/Surface 列表、冻结状态等）
- 当前 `allowed_actions` 列表（含动作说明与参数 schema 的精简版）
- 当前加载的 Surface Manifest 摘要（用于提示模型理解可用能力）

### 7.4 片段拼接与条件逻辑
- 以最小规则实现条件注入：
  - 例如：若存在 chess Surface，则注入 chess 交互规则摘要；
  - 若某 Surface 冻结，则从 allowed_actions 中移除相关动作并注入“不可用”说明。
- 避免过度复杂 DSL；以配置驱动为主。

### 7.5 输出约束（必须注入）
提示词引擎必须注入硬约束：
- 只允许输出协议规定的严格 JSON（content+action）。
- 仅可调用 `allowed_actions` 中的 action.name。
- 若希望尽快执行动作：建议减少/不输出 content。

### 7.6 可观测性与追溯
每次渲染输出应记录：
- `prompt_hash`（最终渲染文本 hash）
- 使用的配置版本/文件 hash（defaults + user_config）
- 渲染上下文摘要（避免敏感数据明文；可存 key 列表与长度等）
- 使用的模型与时间戳

---

## 8. 权限与安全（面向本地个人/本地多用户）

### 8.1 基本假设
- 运行在 `localhost`，用户对本地应用与 AI 有较高信任。
- 但 Surface 为 AI 生成 HTML，仍需防止“无意间越权/误触发权限/破坏主界面”的风险。

### 8.2 本阶段策略
- 通过 iframe sandbox 默认切断同源特权（不开放 allow-same-origin）。
- `allow=` 能力按 manifest 填充，不做加载前确认（用户确认主要针对 sandbox 超出默认范围）。
- 提供“冻结/暂停消息通道”作为运行期止血能力。

---

## 9. 关键用户体验（UX）约束
- Surface 加载前若涉及高风险 sandbox 权限（尤其 allow-same-origin），必须明确弹窗告知风险并确认。
- LLM 回复应尽快呈现内容；动作执行延后到消息完整接收，确保稳定性。
- `content` 为空但有 action 的情况：UI 不显示回答气泡，但应显示动作执行中的状态（例如窗口内状态/主界面轻提示）。

---

## 10. 待确认事项（Open Questions）
- Surface Manifest 的嵌入方式与字段最小集合（HTML 内嵌 vs sidecar JSON）。
- `allowed_actions` 的精简表达格式（在提示词中如何描述参数 schema 以节省 token）。
- Action record 的存储形态（本地 DB 选型与查询 API 形态属于工程实现，需后续落地）。
- Worker 执行与强制取消策略：是否需要统一要求某些类别 Action 必须可中断（工程约束）。

