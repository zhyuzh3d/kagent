# WebUI 配套页面与 AI 自生成能力完整 PRD（修订稿）

## 文档信息

- 修订时间：2026-03-20 17:25:00 CST
- 修订范围：重写原始 PRD 的目标、范围、阶段拆分、验收口径、依赖、风险与待确认事项
- 依据文件：
  - `doc/_instruction.md`
  - `doc/_instruction/core.md`
  - `doc/_instruction/structure.md`
  - `webui/page/chat/*`
  - `webui/page/surface/*`
  - `webui/surface/buildin/counter/*`
  - `services/chat_server/*`
  - `services/surface_manager/*`
  - `services/ai_doubao/*`
  - `hub/internal/gateway/*`

---

## 1. 背景与当前现状

本需求不是单纯补前端页面，而是要把 `Page + Hub + Service + Surface` 这一整条链路补齐，使 WebUI 真正具备以下三类能力：

1. 对现有平台能力进行可视化治理与操作。
2. 支持通过 AI 生成和装载新的 Surface / Service。
3. 支持对话驱动的 UI/系统操作闭环。

根据当前仓库现状，以下事实已经可以确认：

1. `webui/page/service/` 目前不存在，因此“Service 管理页”属于新增页面，不是改造现有页面。
2. `webui/page/surface/index.html`、`webui/page/surface/admin.html` 已存在，且已有基础的 Surface 装载、管理、调试与 action 分发逻辑。
3. `services/surface_manager` 已提供 `ui.surface.catalog_list`、`ui.surface.get`、`ui.surface.enable_set`、`ui.surface.session_issue`、`ui.surface.capability_issue`、`ui.surface.runtime_status`、`ui.surface.logs_query`、`ui.surface.rescan`、`ui.surface.rebind`、`ui.surface.fs_*` 等工具，因此 Surface 管理并非从零开始。
4. `webui/page/chat/` 与 `services/chat_server` 已具备项目/线程管理、流式聊天、音频采集播放、action_report、surface_state_change 等基础能力，因此 Chat 页属于“补全并打通闭环”，不是纯新建。
5. `webui/surface/custom/` 目录已经存在，适合作为 AI 生成 Surface 的目标目录。
6. 当前仓库中还没有 `autogui` service，也没有 `buildin/task` surface，因此第四阶段属于新增后端 service + 新增 surface + 新增对话协作协议。

因此，这份需求的合理目标不是“同时一次性做完所有想法”，而是把它拆成清晰阶段，先做平台治理闭环，再做 AI 自生成，再做高风险的桌面自动化。

---

## 2. 产品目标

### 2.1 总目标

构建一个完整可用的 WebUI 运维与生成平台，使用户能够：

1. 在网页中查看和管理 Hub 已接纳的 Service 与 Surface。
2. 在聊天会话中以语音或文本方式驱动 Surface。
3. 通过 AI 在受控目录内生成可加载的 Surface 与可接入 Hub 的 Service。
4. 在受控范围内尝试“任务 -> 分析 -> 操作 -> 截图校验 -> 继续操作”的自动执行闭环。

### 2.2 非目标

以下内容本轮不应默认承诺为目标：

1. 不承诺做“完全无人工干预、任意桌面环境都稳定可用”的通用桌面智能体。
2. 不承诺 AI 生成出的任意 Go Service 都能零修正通过编译、注册和长期运行。
3. 不承诺把 Hub 变成业务授权系统；用户身份、对象权限和内部安全边界仍由具体 service 自治。

---

## 3. 总体设计原则

1. 前端页面只做交互编排与状态展示，正式能力调用统一走 Hub 工具平面。
2. Surface 的身份与权限必须通过 `surface_manager` / Hub 颁发的 session 或 capability token 传递，不允许把用户 JWT 直接暴露给 iframe 内的 surface。
3. 新增的治理页面必须以“当前真实工具契约”为准，不依赖硬编码端口或私有直连 URL。
4. AI 生成功能必须先定义“生成目录、提示词包装机制、生成模板、验证流程、失败回滚/覆盖策略”，否则不可直接进入开发。
5. 高风险能力必须有人工确认点、日志记录和可中止机制，尤其是 Service 生命周期控制与桌面自动化。
6. 模板可以存在，但模板必须具备通用扩展性，目标是给大模型提供稳定参考，而不是把实现锁死在单一示例上。
7. 本版 PRD 以“先做通路与能力闭环”为优先目标；部分风险管控、细粒度权限与高风险动作确认机制可延后到后续版本补齐。
8. 所有新增能力都必须在现有 `Hub + Service` 框架标准下实现，不允许绕开 Hub 另起一套前后端直连协议、私有治理通道或脱离 manifest/lifecycle/tool 平面的新体系。
9. 前端页面开发必须参照现有 `chat` 与 `surface` 的对接机制，逐步建立并遵循统一的 `Page -> Surface -> Page` 结构：默认由 Page 通过 action/command 控制 Surface，由 Surface 通过事件、状态回报和结构化消息与 Page 沟通。
10. 新增页面、Surface 与 Chat 协作逻辑应优先复用并收敛现有 `action-dispatcher`、`surface-manager`、`surface-bridge`、`action_report`、`surface_state_change` 等机制，而不是为单一页面临时发明新的交互协议。

---

## 4. 分阶段需求

## 第一阶段：补全 Service / Surface / Chat 的基础治理闭环

### 4.1 阶段目标

补齐现有平台的管理页面与核心交互，使“Service 管理、Surface 管理、Chat 会话驱动 Surface”可以稳定可见、可操作、可调试。

本阶段的默认技术边界：

1. 所有后端配套能力必须继续收敛在现有 Hub-Service 框架内补齐。
2. 所有新页面与现有 `chat` / `surface` 页面一样，优先遵循统一的 Page-Surface 交互结构，而不是各做各的页面内私有逻辑。
3. 对于可视化子模块，优先设计为“Page 编排 + Surface 承载局部 UI/状态 + 事件回传”的结构，以便后续在 Chat、Surface Main、Admin 页面之间复用。

### 4.2 范围

#### A. 新增 `webui/page/service/`

页面目标：展示 Hub 当前已接纳和可治理的全部 Service，并提供基础生命周期与调试入口。

建议页面能力：

1. Service 列表
   - 展示 `service_id`、实例信息、健康状态、注册状态、最近心跳、提供的工具数、依赖工具、可靠性等级。
2. Service 详情面板
   - 展示 manifest、生命周期状态、路由/调用统计、最近错误、关键运行信息。
3. 生命周期与配置操作
   - 必须支持 `start`、`stop`。
   - 必须支持查看、编辑并保存 Service 自身关键参数。
   - 配置保存范围至少包含 `config` 与 `manifest`。
   - 必须支持 `restart`、`drain`。
   - 必须支持路由重绑或等价的“重新接纳/重新绑定治理路由”操作。
   - `health probe`、`state.get` 作为正式调试与状态能力一并提供。
4. 工具调试
   - 支持查看该 service 的 `provides/requires`。
   - 支持调用 `hub.admin.tool.probe` 或等价调试入口验证工具联通性。
5. 审计与日志入口
   - 能跳转或拉取最近治理审计、错误信息或简化日志。

本阶段需要的后端补充：

1. Hub 需要提供面向单个 Service 的正式治理工具，至少覆盖：`start`、`stop`、`config get/update`、`manifest get/update`。
2. Hub 还需要提供：`restart`、`drain`、`route rebind`、`temporary disable` 等正式治理工具，或提供可等价编排这些动作的工具组合。
3. 若当前 Hub 还没有这些工具，需要新增 `hub.admin.service.*` 或等价治理工具，再由页面消费。
4. 需要明确“保存 manifest/config 后是否自动重启生效”，并把策略写进页面提示。

#### B. 完善 `webui/page/surface/index.html`

页面目标：安全装载指定 Surface，并在页面外层为需要身份的 Surface 提供受控身份支持。

本阶段明确要求：

1. 页面默认支持通过 URL 参数指定要加载的 surface。
   - 统一使用 `surface_id` 作为正式入口参数。
   - 不再把“直接传裸 URL”作为正式主路径，只允许它作为调试或兼容模式。
2. 外层页面负责完成身份准备。
   - 通过 `ui.surface.get` / `ui.surface.catalog_list` 获取元数据。
   - 通过 `ui.surface.session_issue`、`ui.surface.capability_issue` 按需获取 surface 侧令牌。
   - 令牌只通过受控握手消息传给对应 iframe，不直接泄露用户原始 cookie。
3. 外层页面负责能力隔离。
   - 基于 manifest 权限决定是否签发 capability。
   - 若缺少权限或未登录，要给出可见错误状态，而不是静默失败。
4. 页面继续保留现有 action 调度与 record 机制，但需要补成“可被 Chat 页可靠调用”的正式桥接层。
5. 后续新增 surface 及 page-surface 协作，都应遵循同一模式：
   - Page 发 action / command
   - Surface 返回 event / state / result
   - Page 负责汇总编排、记录和展示

#### C. 完善 `webui/page/surface/admin.html`

页面目标：提供 Surface 的统一管理台，而不是仅有调试页。

建议补齐的管理能力：

1. Catalog 列表与筛选
   - 来自 `ui.surface.catalog_list`
   - 区分 `buildin/custom/extension`
   - 展示启用状态、版本、入口文件、权限声明、最近扫描结果
2. Surface 详情
   - manifest、入口文件、权限、静态资源路径、可声明 action
3. 运行态管理
   - `runtime_status`
   - `logs_query`
   - `rescan`
   - `rebind`
   - 启用/禁用切换
4. 调试与文件入口
   - 基于 `ui.surface.fs_*` 提供只读或受控编辑入口
   - 支持查看生成后的文件结构与 manifest
5. 身份与令牌操作
   - 保留并完善 `session_issue`、`capability_issue`
   - 明确 token 作用域、过期时间和适用 surface

#### D. 完善 `webui/page/chat/`

页面目标：让 Chat 页成为“用户与平台能力协同”的主入口。

本阶段需要补齐：

1. Project / Thread 管理体验
   - 当前已有创建、更新、删除、切换基础能力，需要完善默认空态、排序规则、重命名、切换稳定性、跨项目移动表现和异常提示。
2. 实时语音对话闭环
   - 当前已有音频采集/播放与 `app.chat.stream` 基础，需要补充状态提示、开始/停止/打断反馈、ASR/TTS 异常可见性。
3. LLM 控制 Surface 内部元素
   - Chat 侧 action engine 与 surface bridge 需要与 Surface 页的 action 协议完全对齐。
   - 需要改造 `webui/surface/buildin/counter/` 作为验证样例，使其能稳定响应“打开、刷新状态、执行 action、上报 action 结果、上报 state change”。
4. 对话记录中的结构化可见性
   - `action_report`、`surface_state_change` 需要在 UI 中被清晰展示，而不是只停留在底层消息流。
5. Chat 对 Surface 的控制与回收，也必须遵循统一 page-surface 结构：
   - Chat/Page 作为编排者
   - Surface 作为局部交互与执行载体
   - 双方通过 action / event / state 机制通信

### 4.3 第一阶段验收标准

1. 用户可以在页面中看到 Hub 当前管理的全部 Service，并对单个 Service 做至少“启动 + 停止 + 重启 + drain + 路由重绑 + 查看状态 + 编辑保存 config + 编辑保存 manifest”。
2. 用户可以通过 `surface/index.html?surface_id=...` 稳定打开指定 Surface，且需要身份的 Surface 可以通过外层安全获得 session/capability，而不是直接拿用户 cookie。
3. `surface/admin` 可以列出全部已扫描 Surface，并完成启用、重扫、重绑、查看运行态、查看日志、签发 token。
4. Chat 页能完成一个可复现 demo：
   - 打开 `counter` surface
   - 通过聊天或 action 机制让其执行至少一个可见动作
   - 在对话记录中看到对应 action/state 回报
5. 页面所有正式调用都经由 Hub 工具平面，不依赖前端私有硬编码端口。

### 4.4 第一阶段可行性判断

可行性高，可以做。

原因：

1. `surface_manager`、`chat_server`、`ai_doubao`、现有 WebUI 已提供较多基础能力。
2. 真正新增从零开始的部分主要是 `page/service` 和若干后端治理工具补口。
3. 风险主要在“Hub 是否已有足够完整的 service 生命周期治理工具”，但这个风险是可补齐的，不构成方向性阻塞。

---

## 第二阶段：AI 生成 Surface

### 5.1 阶段目标

让 `webui/page/surface/admin.html` 支持通过 AI 生成新的 Surface 模块，并把产物按用户维度落盘，随后可被 `page/surface/index.html?surface_id=...` 直接加载。

### 5.2 范围

建议把“AI 生成 Surface”限定为受模板约束的生成流程，而不是无限制代码生成。

建议流程：

1. 用户在 `surface/admin` 输入需求描述。
2. 页面先用提示词工程包装层补齐上下文：
   - 目标产物结构
   - manifest 必填字段
   - 允许的权限范围
   - 页面/Surface 交互协议
   - 可注入的通用模板与示例代码
3. 页面调用 AI 对话能力生成：
   - `manifest.json`
   - `index.html`
   - 可选的 `README.md` 或调试说明
4. 生成结果写入用户专属目录：`webui/surface/<user>/custom/<surface_name>/`
5. 触发 `ui.surface.rescan` / `ui.surface.rebind`
6. 在 admin 页展示生成结果、diff 摘要、校验结果
7. 页面提供在线编辑能力，允许用户直接修改已生成的 `manifest.json`、`index.html` 以及其他受支持文件，再次保存并重扫。
8. 用户确认后，可一键在 `surface/index` 中打开该 surface

### 5.3 最少需要补齐的约束

1. 生成模板
   - 必须定义 Surface scaffold 的最小模板，不建议让 AI 直接从空白生成任意结构。
2. 提示词包装机制
   - 页面必须内置严谨的 system/developer prompt 包装层，而不是把用户原始自然语言直接发给模型。
   - 包装层需注入输出格式约束、目录约束、manifest 约束、协议约束和示例代码片段。
   - 模板必须具有通用扩展性，不能只适配单个 demo。
3. 文件写入边界
   - 目标方案为 `webui/surface/<user>/custom/<surface_name>/`
   - 写盘身份必须基于 Hub 提供的用户上下文，不允许前端自行拼接任意用户目录。
   - 由于当前 `surface_manager` 只扫描 `webui/surface/{buildin,ext,custom}`，本阶段必须同步改造扫描与 catalog 结构，才能支持用户目录。
   - 由于当前 `file_storage` 默认写入 `data/` 根，本阶段若坚持写入 `webui/`，则必须新增受控写盘能力或扩展 `file_storage` 的写入根策略。
4. 命名与覆盖策略
   - 若同名目录已存在，是覆盖、版本化还是拒绝写入，需要明确。
5. 校验流程
   - 至少校验 manifest 字段、入口文件存在、能被 catalog 扫描。
6. 安全策略
   - 权限声明不能由模型无限放大，需有白名单或人工确认。
7. 在线编辑能力
   - 需要定义允许编辑的文件类型、保存方式、冲突处理和“保存后自动重扫/手动重扫”的策略。

### 5.4 第二阶段验收标准

1. 用户可以在页面输入一段自然语言需求，经过提示词包装后生成一个新的用户专属 surface 目录。
2. 新生成的 surface 能被 `ui.surface.catalog_list` 扫描到。
3. 新生成的 surface 能被 `page/surface/index.html?surface_id=...` 成功加载。
4. 用户可以在线编辑生成后的文件并再次保存。
5. 生成或编辑失败时，页面能明确显示是“生成失败、写盘失败、校验失败、保存失败还是重扫失败”。

### 5.5 第二阶段可行性判断

可行性中高，可以做，但必须收窄成“模板化生成 + 校验 + 重扫装载”。

原因：

1. 生成物主要是静态 Web 资源，构建和注册复杂度远低于 Go Service。
2. 但你要求的目标目录是用户专属目录，而当前 `surface_manager` 扫描模型并不支持这一结构，所以这里包含明确的结构改造工作。
3. 若不加提示词包装、模板和校验，质量波动会非常大，需求会失控。

---

## 第三阶段：AI 生成 Service

### 6.1 阶段目标

让 `page/service` 支持通过 AI 生成新的后端 Service 模块，并使其能够被 Hub 动态接纳和治理。

### 6.2 范围

这一阶段应明确为“受约束的 service scaffold 生成与接入”，不是让模型自由生产任意复杂后端系统。

建议流程：

1. 用户在 `page/service` 输入需求描述。
2. 页面先通过提示词工程包装层注入：
   - Service scaffold 约束
   - manifest / lifecycle 约束
   - Hub 接入约束
   - 配置文件结构约束
   - 可参考的通用模板与示例代码
3. 页面调用 AI 对话能力，结合现有 service 模板生成新的服务目录。
4. 生成产物写入用户专属目录：`data/user/<user>/service/custom/<svc-name>/`
5. 完成最小检查：
   - `manifest.json` 合法
   - 入口命令合法
   - 生命周期工具存在
   - 至少可注册到 Hub
6. 页面提供在线编辑能力，允许用户直接修改生成后的源码、配置与 manifest。
7. 支持执行真实 `build` 构建。
8. 构建成功后支持 Hub 真实拉起、停止、临时禁用等治理动作。
9. 在 `page/service` 中查看状态、日志和工具列表

### 6.3 本阶段必须先明确的关键点

1. 目标目录
   - 已明确为 `data/user/<user>/service/custom/<svc-name>/`。
   - 这意味着 Hub 需要支持“用户专属自定义 service 工作区”的加载、构建与治理，而不是只扫描仓库内固定 `services/*`。
2. 动态启动机制
   - 本轮目标已经明确为支持真实构建，并让 Hub 对自定义 service 执行真实拉起、停止、临时禁用。
   - 因此需要补齐“用户工作区 service -> build 产物 -> Hub 接纳与治理”的完整链路，而不是只做静态生成。
3. 代码生成边界
   - 本轮已明确允许生成完整 Go 工程、执行真实 `build` 构建，并接入 Hub 治理。
   - 仍需在实现阶段明确：是否修改 `hub/config/services.json`，还是采用动态注册/临时清单机制。
4. AI 工具链
   - 生成代码应基于 Hub 提供的用户身份，通过 `file_storage` 或等价受控写盘能力落到用户目录。
   - 但真正生成后端 service 还需要目录模板、构建、manifest 规范和注册流程，不只是对话 + 写文件。
5. 提示词包装机制
   - 与 Surface 一样，Service 生成必须经由严谨的 prompt 包装层，不能把用户原始需求直接交给模型返回任意代码。
6. 在线编辑与构建联动
   - 页面需要支持“生成 -> 在线编辑 -> 保存 -> build -> 拉起/停止/临时禁用”的完整闭环，而不是只停留在代码生成。

### 6.4 第三阶段验收标准

1. 用户可以提交一个受约束的 Service 需求并生成对应目录。
2. 生成物至少能通过结构校验，并包含合法 manifest 与生命周期工具声明。
3. 用户可以在线编辑生成后的文件，再次保存并重新构建。
4. 构建成功后，Hub 能真实识别、拉起、停止、临时禁用并治理该 service。
5. `page/service` 能看到该自定义 service 的状态、日志与工具清单。

### 6.5 第三阶段可行性判断

可行性中等偏低，能做，但前提是先缩成“脚手架生成 + 受控接入”，不能直接承诺“实时生成任意 service 并稳定运行”。

原因：

1. Go Service 比 Surface 多了编译、依赖、运行、清单、Hub 接纳、生命周期、安全边界等复杂度。
2. 你已经把生成目录明确成用户专属数据目录，并要求真实 `build`、真实拉起、停止、临时禁用，这意味着 Hub 侧要新增“从用户工作区加载并治理自定义 service”的机制。
3. 该阶段仍然可做，但工作量已明显扩大，不能再按“纯脚手架生成”估算。

---

## 第四阶段：`autogui` 服务模块与 `task` Surface

### 7.1 阶段目标

新增一个极简但可用的 `autogui` service，提供鼠标、键盘、截图等工具；同时新增 `buildin/task` surface，作为 Chat 页里的任务执行面板，让对话可以驱动桌面操作闭环。本阶段先只支持 macOS，桌面自动化实现直接基于 `robotgo`。

### 7.2 范围

建议把本阶段严格限制为“受控 MVP”，而不是全功能桌面代理。

建议最小能力集：

1. `autogui` service
   - 鼠标移动、点击、滚动
   - 键盘输入与按键组合
   - 屏幕截图
   - 基础坐标/窗口信息返回
2. `buildin/task` surface
   - 输入任务目标
   - 展示当前计划
   - 展示执行中的 operation 列表
   - 展示最近截图与结果判断
   - 提供暂停/继续/终止
3. Chat 集成
   - 对话中可装载 `task` surface
   - LLM 将任务拆为多个 operation
   - 每步操作后调用截图工具做结果比对
   - 若结果不符合预期则继续规划下一步

### 7.3 建议的执行模型

本阶段建议采用“AI 主规划 + 系统执行 + 人工必要参与”的协同模型。本版不额外加入复杂风险管控，优先打通能力闭环。

#### A. 任务状态机

`task` surface 至少维护以下状态：

1. `draft`：刚收到任务，等待 AI 输出执行计划。
2. `await_confirm`：需要用户确认或继续执行时等待输入。
3. `running`：正在执行 operation。
4. `await_human_action`：需要用户手动完成某一步，例如登录、验证码、手动聚焦窗口。
5. `paused`：人工暂停。
6. `failed`：执行失败，保留错误上下文与截图。
7. `completed`：任务完成。
8. `aborted`：人工终止。

#### B. Operation 数据结构

每个 operation 至少记录：

1. `operation_id`
2. `type`
3. `args`
4. `planned_reason`
5. `started_at`
6. `finished_at`
7. `status`
8. `before_screenshot_ref`
9. `after_screenshot_ref`
10. `verification_result`
11. `requires_user_step`

#### C. `autogui` 最小工具集

建议正式工具至少包含：

1. `autogui.mouse.move`
2. `autogui.mouse.click`
3. `autogui.mouse.scroll`
4. `autogui.keyboard.type`
5. `autogui.keyboard.press`
6. `autogui.screen.capture`
7. `autogui.screen.capture_region`

#### D. AI 与人工协作原则

1. AI 负责拆解任务、决定下一步操作、读取截图结果并给出继续/停止判断。
2. 系统负责执行低层操作、记录日志、保存截图、汇总状态。
3. 人工负责必要介入、无法自动完成的步骤和手动推进。

#### E. 关键确认规则

以下情况进入 `await_confirm` 或 `await_human_action`：

1. 任务流需要用户手动完成登录、授权、验证码、窗口切换等动作时。
2. AI 无法从截图与上下文中稳定判断下一步操作时。
3. 连续多次截图校验失败时。

#### F. 截图校验策略

每次关键 operation 后执行：

1. 截图
2. 把“目标预期 + 当前截图摘要 + 上一步动作”发送给模型
3. 让模型返回：
   - 是否成功
   - 是否需要重试
   - 是否要改用新操作
   - 是否需要人工介入

#### G. `task` surface 的 UI 最小组成

1. 任务输入区
2. 当前计划区
3. 状态机与当前阶段展示
4. operation 时间线
5. 最近截图与校验结论
6. 确认 / 继续 / 暂停 / 终止按钮

### 7.4 必须补齐的产品与运行约束

1. 运行平台先固定为 macOS。
2. 桌面自动化实现直接基于 `robotgo`。
3. 系统需兼容 macOS 的辅助功能授权与屏幕录制授权要求。
4. `task` surface 必须提供暂停、继续、终止与查看执行记录的入口。
5. 所有 operation、截图摘要、判断理由都必须可见。

### 7.5 第四阶段验收标准

1. `autogui` service 可在 macOS 上基于 `robotgo` 被 Hub 接纳，并提供最小鼠标、键盘、截图工具。
2. `task` surface 能在 Chat 页中被打开并持续展示任务执行过程。
3. 至少存在一个受控 demo 任务可以闭环执行，例如：
   - 打开已有页面
   - 点击固定区域
   - 输入固定文本
   - 截图确认结果
4. 用户可以中止任务，并查看完整操作日志。

### 7.6 第四阶段可行性判断

可行性中低，只能承诺做“受控 MVP”，不能承诺高可靠通用桌面代理。

原因：

1. 该阶段涉及 OS 权限、桌面环境差异、坐标稳定性、截图理解误差和误操作风险。
2. 这是整份 PRD 中风险最高的一阶段。
3. 如果收窄为“macOS + robotgo + 受控 demo 任务 + 操作全记录”的 MVP，则可以做。

---

## 5. 全局依赖

1. Hub 需要补充完整的 Service 生命周期治理工具，或明确现有能力边界。
2. `surface_manager` 需要作为 Surface 真正唯一的 catalog 与令牌来源。
3. Chat 页、Surface 页、Surface 内嵌 iframe 三者之间需要统一 action / state / event 协议。
4. AI 生成功能需要统一提示词包装层、模板、命名规则、目录约束、校验流程和失败回退策略。
5. 若 Surface 生成目标目录采用 `webui/surface/<user>/custom/...`，则 `surface_manager` 必须扩展扫描根、catalog 结构与静态装载策略；当前实现尚不支持。
6. 若 Service 生成目标目录采用 `data/user/<user>/service/custom/...`，则 Hub 必须新增从用户工作区加载自定义 service 的机制；当前实现尚不支持。
7. 桌面自动化阶段已锁定为 macOS + `robotgo`，仍需处理系统授权与运行环境约束。

---

## 6. 明显缺失、已补入 PRD 的关键细节

原始需求中明显缺失、这次已补齐的内容包括：

1. 每一阶段的目标、范围和验收标准。
2. `page/service` 的建议页面能力与所需后端补充。
3. `surface/index` 如何安全承接用户身份，而不是把“支持账号系统”停留在口号。
4. `surface/admin` 应包含的真实管理能力，而不是泛泛的“全面管理”。
5. `chat` 与 `surface` 之间 action 协议对齐和 `counter` 验证样例的角色。
6. AI 生成 Surface / Service 所需的提示词包装层、模板、生成目录、校验、覆盖策略与失败处理。
7. `autogui + task` 阶段的状态机、operation 结构、macOS/`robotgo` 运行约束与 MVP 范围。

---

## 7. 额外确认

1. Service 真实 `build` 后的接入方式最终采用：
   - 修改 `hub/config/services.json`
   - 动态注册 / 临时运行清单
   - 以上两者结合
2. Surface 在线编辑时，默认开放全部文件类型可改：
   - 包括`manifest.json`、`index.html`、`*.js`、`*.css`
   - 允许整个目录受控编辑
3. Service 在线编辑时，默认开放其自身目录内全部文件类型可改，允许删除文件。

---

## 8. 我对可行性和可交付性的判断

如果按修订后的范围推进，我的判断是：

1. 第一阶段：可以做，且应该优先做。
2. 第二阶段：可以做，但它已经不再只是“生成页面代码”，还包含用户目录扫描与静态装载规则改造。
3. 第三阶段：可以做缩窄版，但它已经不再只是“写一个 Go 服务目录”，还包含用户工作区装载与 Hub 治理机制改造。
4. 第四阶段：可以做 MVP，且适合按“AI + 人工协同”的路线推进；不建议把它定义成“默认高可靠通用自动操作代理”。

换句话说，这份需求不是不能做，而是原始版本把“平台治理、代码生成、桌面自动化”混在了一起，缺少边界、依赖和验收定义。现在这份修订稿已经把它整理成可以继续排期和分阶段实施的版本。
