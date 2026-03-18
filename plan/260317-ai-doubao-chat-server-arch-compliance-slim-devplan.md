# ai-doubao + chat-server 架构合规与瘦身清理开发计划（Dev Plan）

- 日期：2026-03-17
- 范围：`services/ai-doubao`、`services/chat-server`（为达成目标，计划中包含对 Hub 的必要最小改动点）
- 目标关键词：**架构合规优先**、功能完整、去冗余、删除未使用代码、服务自洽独立（不依赖服务目录外的代码与资源）、`chat-server` 仅通过 Hub 访问外部能力
- 方案取向（用户已确认）：**采用方案 B（前端优先编排）+ 保留现有混合机制**

---

## 0. 约束（用户已确认，视为不可违反）

1. **configx 的定位**：敏感配置应保存在各自 service 的 `config/configx.json` 中；全项目已对 `configx.json` 做 Git 忽略（不需要做安全整改类动作）。
2. **服务完全独立**：每个 service 必须是“可单独拷走运行”的子项目：
   - 不允许依赖 `services/<service>/` 目录外的任何 **代码库**（例如 `kagent/pkg/*` 不能 import）。
   - 不允许依赖 service 目录外的任何 **运行时资源**（例如根目录 `config/`、`data/`、`webui/` 等）。
   - 允许“每个 service 内部重复实现一份必要的通用逻辑”（以换取独立性）。
3. **Service 的文件系统边界（硬约束）**：
   - 所有 service 进程只能直接读写其自身目录内的文件与数据（例如 `services/<service>/...`）。
   - `data/user/<username>/...` 等用户数据目录：任何读写都必须通过 Hub 的工具调用完成（禁止 service 进程直接触达这些路径）。默认应由前端（Page）发起；如确需 `service -> hub -> service`，必须走严格 allowlist + reliability 约束，避免形成“service 可离线读写用户数据”的隐性能力。
   - 同时，前端 `webui/page/chat` 并不只限于调用 `chat-server` 的工具：它可通过 Hub 调用任意 service 的任意工具（前提是 Hub 路由可用且鉴权通过）。
4. **强架构边界（对 chat-server）**：
   - `chat-server` 为 `category=app` 的应用型 service：**禁止**直接访问外部 AI（如豆包）/本地 DB/本地文件。
   - 所有外部能力访问必须指向 **Hub**，由 Hub 代理到 `ai-doubao`/`database`/`file` 等服务。
   - 典型约束：`chat-server` 只能通过 Hub 调用 `ai.llm.stream` 等工具实现流式对话，不能直接调用豆包 API；不能直接打开 sqlite 文件。
5. **删除未使用代码**：完全没有被使用的代码或文件，必须清除。
6. **合规优先**：先保证机制正确（按 PRD/理念文档要求），再做瘦身；瘦身不能破坏架构合规与功能完整。
7. **Service↔Service 通路策略（用户补充）**：
   - Hub 允许 `service -> hub -> service` 的工具调用通路，但 **原则上不推荐**，应优先 `webui/page -> hub -> service`。
   - 当发生 `service -> hub -> service`：Hub 必须向接收方提供可靠的调用方身份信息；接收方可按 `reliability`（例如仅接受 `trusted`）限制来访。
   - 本计划不引入“绕过 Hub 的 service->service 直连”。在 `.service_secret` 的零信任模型下，接收方 service 应只接受 Hub 注入的 `X-Hub-Service-Token`；若未来要允许直连，需要重做“签发边界/信任根”的设计，不建议在本轮混入。
8. **manifest 与 register 的关系（用户补充）**：
   - `manifest.json` 与 `/api/service/register` 均可包含重叠字段；
   - 以 **register 的运行时注册信息为可信来源**，manifest 作为静态说明与未来扩展空间保留（例如启动参数说明、未来扩展字段）。

---

## 1. 架构理念基线（作为“合规判定标准”）

依据（项目权威说明与已落地 As‑Is）：
- `doc/_instruction.md`
- `plan/260316-hub-service-platform-architecture-prd.md`
- `plan/260316-kagent-architecture-philosophy-ana.md`
- Hub 工具网关：`hub/internal/gateway/tool_handler.go`（`POST /api/tool/call`）

### 1.1 必须成立的不变量（面向本次改造）

1. **Hub 是唯一网关**：Page/Service 对“平台能力”的访问必须经由 Hub，可被路由、审计、治理。
2. **Service 零信任**：Service 只相信 Hub 注入的受保护 headers（尤其 `X-Hub-Service-Token` + caller headers）。
3. **能力以 tool_id 表达**：跨服务调用应体现为 `tool_id` 调用链路，而不是端口直连（端口可存在但不作为业务依赖入口）。
4. **chat-server 只做业务编排**：不承载 provider 直连、也不承载本地存储；它是“业务协调者”。
5. **Hub 对外只暴露工具平面**：除 supervisor/control-plane（如 `/api/service/*`，loopback-only）外，Hub 的业务能力访问统一收敛为 tool 级调用（原子化 `POST /api/tool/call` + 流式 `GET /api/tool/ws`）。
6. **前端优先编排（方案 B）+ 混合机制保留**：前端（Page）负责交互仲裁（VAD/interrupt/trigger）；`chat-server` 负责会话协议与业务聚合，但其所有外部能力访问必须走 Hub 的 tool 平面。

### 1.2 与 `plan/260316-kagent-architecture-philosophy-ana.md` 的对齐检查

#### 1.2.1 已对齐点（本计划与白皮书一致）

1. **逻辑路径优先（Logic > Physics）**：本计划以 `tool_id` 为唯一协作接口（对应白皮书“架构不变量 #1”）。
2. **跨 Service 协作可被 Hub 治理**：本计划把 chat-server 的外部能力访问（AI/DB/File）统一收敛到 Hub（对应白皮书“架构不变量 #2”）。
3. **受保护 headers 由 Hub 注入**：本计划不允许 chat-server 自行伪造 caller 身份（对应白皮书“架构不变量 #3”）。
4. **平台契约补齐**：本计划要求 `ai-doubao` 补齐 `POST /service/tool/exec`（对应白皮书“Service 最小平台契约（As‑Is）”）。

#### 1.2.2 潜在矛盾（用户要求 vs 白皮书 As‑Is）与处理策略

> 说明：白皮书明确包含 “As‑Is（已落地现状）” 与 “To‑Be（目标理念）”。当用户要求与 As‑Is 冲突时，本计划按用户要求执行，但会把必要的平台改造点明确列出，并保持不变量不被破坏。

1. **Service Session Token 的 shared secret 位置**
   - 白皮书 As‑Is：shared secret 存于仓库根 `data/.service_secret`，Hub 与各 Service 共享（白皮书 6.1）。
   - 用户要求：Service 运行时不依赖 service 目录外资源（不能依赖根目录 `data/`）。
   - 处理策略（本计划采用）：
     1) **语义澄清**：`.service_secret` 是 Hub 与所有 Service 的“零信任通信基础”，用于 HMAC 签名/验签 `X-Hub-Service-Token`，它不属于业务配置（与 `configx.json` 概念无关）。
     2) Service 侧：在 **service 自身目录内**保存一份 `.service_secret`（例如 `services/<service>/run/.service_secret` 或 `services/<service>/.service_secret`），service 仅从自身目录读取并用于验签。
     3) Hub 侧：保持白皮书 As‑Is（继续从 `data/.service_secret` 读取并签发 token）。
     4) 部署/启动时保证 **Hub 与各 Service 的 `.service_secret` 值一致**（同源生成、复制分发；均为 gitignore）。
     5) 可选增强：若未来要彻底消除“Hub 与 service 共享同一文件语义”的耦合，可再演进到“按 service_id 多密钥”模型；本计划不强制。

2. **`chat-server` 的数据归属 vs 禁止本地 DB**
   - 白皮书不变量 #5：`chat-server` 负责聊天业务数据（一致性责任在 chat-server）。
   - 用户要求：`chat-server` 禁止直接访问本地 database/file。
   - 处理策略：将“物理存储”下沉到 `database-service`/`file-service`，但保持“逻辑归属”仍在 chat-server：由 chat-server 定义 schema/写入规则/一致性策略，并通过 Hub 工具网关调用存储工具。

3. **`/api/tool/ws` 既有语义冲突**
   - 白皮书 As‑Is：`GET /api/tool/ws` 是 Hub->chat-server 的工具流代理（固定用于 chat 业务 WS，见白皮书 5.3）。
   - 用户新要求：Hub 的流式工具调用统一采用 WebSockets，并以“反向代理隧道（Reverse Proxy Tunneling）”方式实现；hub提供标准的鉴权机制，通道打通后 Hub 只做路由选择与隧道转发，不解析、不额外处理业务协议。hub只负责打通通道和记录通道关闭日志。
   - 处理策略（本计划采用）：升级 `GET /api/tool/ws` 为“通用流式工具网关”：
     - 以 `tool_id` 为选择依据：`GET /api/tool/ws?tool_id=...`；
     - Hub 根据路由选择 provider，并使用工具注册元数据中的 `WSPath` 建立到目标 service 的 WS 连接，然后双向转发帧（含二进制）；
     - **强制模式重构**：正式废弃基于 `service_id` 的旧有直连模式；Hub 将限制所有 WS 建立请求必须携带有效的 `tool_id`，不再提供对 legacy 链路的向下兼容，从而确保架构在“逻辑寻址”层面上的彻底合规。

4. **方案 B + 混合机制的落点：把 chat 会话 WS 明确为工具**
   - 建议将 chat 会话 WS 作为一个流式工具（例如 tool_id=`app.chat.stream`，ws_path=`/service/tool/ws`），从而让前端 `page/chat` 的 WS 链路也符合“Hub 只提供 tool 级调用”的统一规则。
   - 兼容策略：保留 legacy `GET /api/tool/ws`（无 tool_id）一段迁移期；前端逐步迁移到 `GET /api/tool/ws?tool_id=app.chat.stream`。

---

## 2. 现状审计（与约束/理念的冲突点）

### 2.1 chat-server 存在的“架构违规”

1. **直连外部 AI（违规）**
   - 现状：`services/chat-server/internal/app/asr.go`、`llm.go`、`tts.go` 实现了豆包直连客户端；`services/chat-server/internal/app/provider_factory.go` 默认返回这些客户端。
   - 结论：`chat-server` 当前可绕开 Hub 直接访问外部 AI，违反“app service 禁止直接访问外部 AI”。

2. **直连本地数据库（违规）**
   - 现状：`services/chat-server/internal/app/sqlite_store.go` 直接打开 sqlite（默认指向仓库根 `data/kagent.db`），并被 `cmd/chat-server/main.go` 初始化使用。
   - 结论：违反“chat-server 禁止本地 database/file 访问”与“服务不依赖 service 目录外资源”。

3. **本地文件写入（高风险违规/灰区）**
   - 现状：`services/chat-server/internal/app/operation_log.go` 直接写入 `data/users/...`。
   - 结论：与“禁止本地 file 文件访问”冲突（即使写入的是日志，也属于文件系统副作用）。

4. **依赖 service 目录外代码（违规）**
   - 现状：`services/chat-server/cmd/chat-server/main.go` import `kagent/pkg/hubsvc`、`kagent/pkg/toolproto`。

5. **依赖 service 目录外运行时资源（违规）**
   - 现状：默认读取 `config/config.json`、`data/users/.../user_custom_config.json`、`data/.service_secret`、以及 `runtime_root.go` 通过寻找根目录 `webui/config` 判定 app root。

### 2.2 ai-doubao 存在的“架构缺口/不完整”

1. **未提供 Hub 标准工具执行入口（缺口）**
   - 现状：`services/ai-doubao/cmd/ai-doubao/main.go` 仅提供 `/v1/*` 私有端点与 `/service/info`、`/service/tools`，但不提供 `/service/tool/exec`（Hub 工具网关默认调用的是 `/service/tool/exec`）。
   - 结论：导致“ai.* 工具”无法被 Hub 的 `POST /api/tool/call` 正式路由执行，破坏“工具聚合与平台治理”的闭环。

2. **依赖 service 目录外代码（违规）**
   - 现状：`services/ai-doubao/cmd/ai-doubao/main.go` import `kagent/pkg/toolproto`。

3. **存在大量未使用文件（应清理）**
   - 现状：`services/ai-doubao/internal/app/` 内包含明显与 ai-doubao 职责无关且在该 service 内无引用的文件（如 `hub_platform.go`、`auth.go`、`protocol.go`、`context_meta.go` 等）。

---

## 3. 目标状态定义（可验收、可回归）

### 3.1 chat-server 合规定义（必须全部满足）

1. **网络访问约束**
   - 代码层面：`services/chat-server/**` 中不得出现对外部 AI host（例如 `ark.cn-beijing.volces.com`、`openspeech.bytedance.com`）的直接调用实现与配置使用。
   - 运行时：chat-server 所有跨能力请求目标仅为 Hub（例如 `http://127.0.0.1:18080` 或其配置值）。

2. **存储访问约束**
   - 不得直接打开 sqlite 文件/本地数据库。
   - 不得直接写入/读取业务文件到本地文件系统（日志与缓存也视为副作用，除非被明确允许且限定在 service 自己目录内；本计划按“禁止”执行）。
   - 所有持久化都通过 Hub 的 `storage.database.*`、`storage.file.*` 工具链路间接完成（由 Hub 路由到对应 service）。

3. **调用链路约束**
   - ASR/LLM/TTS 的调用必须通过 Hub：
     - LLM 流式对话：通过 Hub 调用/代理 `ai.llm.stream`（满足“流式”）。
     - ASR 流式：通过 Hub 调用/代理 `ai.speech.asr`（满足音频流输入与事件输出）。
     - TTS：通过 Hub 调用 `ai.speech.tts`（同步即可）。

4. **独立性约束**
   - `services/chat-server` 不 import 任何 `services/chat-server` 目录外包。
   - 默认配置、运行数据目录、必要密钥（如 service token secret）均位于 `services/chat-server/` 内部。

### 3.2 ai-doubao 合规定义（必须全部满足）

1. **工具平台契约**
   - 实现 `POST /service/tool/exec`：可被 Hub 的 `POST /api/tool/call` 正常路由执行。
   - 至少支持：`ai.speech.tts`（同步）与 `ai.llm.stream`（流式）与 `ai.speech.asr`（流式）。
2. **独立性约束**
   - `services/ai-doubao` 不 import 任何 service 目录外包。
   - 所需 config/version/运行资源均在 `services/ai-doubao/` 内部闭环。

---

## 4. 方案选型（流式能力如何“通过 Hub”）

### 4.1 问题：需要把“流式工具”纳入统一的 Tool 网关语义

白皮书 As‑Is 已有两类入口雏形：
- 原子化工具：`POST /api/tool/call`（同步 JSON `CallResponse`）
- 工具流（WS）：`GET /api/tool/ws`（当前主要用于 chat-server 的 WS 代理）

用户新要求将“流式工具”统一定义为 **WebSockets + 反向代理隧道**：
- Hub 只做 **路由选择 + 鉴权头注入 + 隧道转发**；
- 隧道建立后 Hub 不解析业务协议，不额外处理消息；
- 必须仍以 `tool_id` 为唯一入口，不允许“直接访问某个 service”。

### 4.2 推荐方案（可落地、与理念一致）

将 Tool 网关统一为两种协议形态，并全部以 `tool_id` 驱动：

1. **原子化工具（HTTP/HTTPS）**：继续使用 `POST /api/tool/call`
   - Hub 解析 `CallRequest`，路由选择 provider，注入受保护 headers，调用 service 的 `POST /service/tool/exec`，返回 `CallResponse`。

2. **流式工具（WebSockets 隧道）**：升级 `GET /api/tool/ws` 为通用“流式 Tool 网关”
   - 入口：`GET /api/tool/ws?tool_id=<...>`
   - Hub 行为：
     - 根据 `tool_id` 做路由选择；
     - 使用注册元数据中的 `WSPath`（新增字段）拼接到目标 service endpoint（TCPURL/UDS）得到目标 WS 地址；
     - 在 WS 握手中注入 `X-Hub-Service-Token` 与 caller headers（如未来需要“用户链路委托”，再引入可选的 delegate 机制，本计划默认不强制）；
     - 建立双向隧道后仅转发帧（含二进制），不解析业务消息。

> 该方案满足“Hub 只提供 tool 级调用”“流式工具用 WS 隧道”“所有跨服务协作可被 Hub 治理”三项核心约束。

---

## 5. 分阶段开发计划（高度可执行）

> 说明：阶段内的“删除文件/清理代码”必须放在对应能力替换之后，避免先删导致无法验证；每阶段都有明确验收与回归。

### Phase 1（合规底座）：消除“service 外部依赖”，清除确定未使用文件

#### 1.1 chat-server：移除对外部包的 import，收敛运行时资源到 service 内

- 任务 A：把 `kagent/pkg/toolproto`、`kagent/pkg/hubsvc` 的依赖替换为 `services/chat-server/internal/*` 内部实现（仅复制/实现当前 chat-server 用到的最小结构体与算法）。
  - 影响文件（当前）：`services/chat-server/cmd/chat-server/main.go`、以及所有使用 toolproto/hubsvc 的文件。
  - 验收：`services/chat-server` 目录内 `rg "kagent/pkg/"` 无结果。

- 任务 B：将 `.service_secret` 的 service 侧读取点迁入 **chat-server 自身目录**（不再读取根目录 `data/.service_secret`）
  - 做法：
    - 在 `services/chat-server/` 内约定一个固定路径存放 `.service_secret`（推荐 `services/chat-server/run/.service_secret`，便于与其它运行态文件隔离；并保持 gitignore）。
    - chat-server 仅从该本地路径读取，用于验签 Hub 注入的 `X-Hub-Service-Token`。
    - Hub 侧保持白皮书 As‑Is，仍从根目录 `data/.service_secret` 读取并签发 token；部署时将同一 secret 内容分发到各 service 的本地 `.service_secret`。
  - 验收：`cmd/chat-server/main.go` 不再读取仓库根 `data/`；仅从自身 `services/chat-server/` 内读取 `.service_secret`。

- 任务 C：调整 app root/路径解析逻辑，使其只依赖 `services/chat-server/` 内部结构（不再要求根目录 `webui/config` 存在）。
  - 涉及：`services/chat-server/internal/app/runtime_root.go`（或废弃该文件，改为更简单的“以 exeDir 为根”策略）。
  - 验收：将 `services/chat-server/` 单独拷贝到临时目录仍可运行。

- 任务 C2：消除对根目录公共配置与用户配置文件的依赖（保持“仅经 Hub 获取外部信息”）
  - 背景：chat-server 现状会读根目录 `config/config.json` 与 `data/users/.../user_custom_config.json`（超出 service 边界）。
  - 做法（推荐）：
    1) chat-server 不再直接读取根目录配置文件；
    2) 运行时公共配置与用户自定义配置由 **前端（Page）** 通过 Hub 统一获取/管理（符合“方案 B：前端优先编排”与“优先 webui-hub-service”）；
    3) chat-server 仅消费前端在会话 WS 控制消息中下发的“配置快照/差异”（例如利用现有控制消息字段 `config_snapshot`、`config_changed_paths` 等），并以该快照驱动自身会话行为（超时、队列大小、策略参数等）。
  - 验收：chat-server 目录内不再出现对根目录 `config/` 与 `data/users/` 的直接文件读取路径依赖。

#### 1.2 ai-doubao：移除对外部包的 import，清除未使用文件

- 任务 D：把 `kagent/pkg/toolproto` 依赖替换为 `services/ai-doubao/internal/*` 内部最小实现。
  - 验收：`services/ai-doubao` 目录内 `rg "kagent/pkg/"` 无结果。

- 任务 D2：为 `ai-doubao` 补齐 `.service_secret` 的自洽读取（为后续 `/service/tool/exec`/流式 WS 入口校验做准备）
  - 做法：在 `services/ai-doubao/` 内约定固定路径存放 `.service_secret`（同 Task B 的约定策略），并实现最小的 `X-Hub-Service-Token` 校验逻辑（仅在 Hub 会访问的入口使用）。
  - 验收：`ai-doubao` 不再依赖根目录 `data/.service_secret`。

- 任务 E：删除“在 ai-doubao 内部完全无引用”的文件（先删确定无引用项；其余等待 Phase 2 的职责拆分后再删）。
  - 首批明确无引用（以当前仓库扫描为准）：
    - `services/ai-doubao/internal/app/context_meta.go`
    - `services/ai-doubao/internal/app/protocol.go`
    - `services/ai-doubao/internal/app/auth.go`
    - `services/ai-doubao/internal/app/hub_platform.go`
    - `services/ai-doubao/internal/app/hub_builtins.go`（不建议整文件删除：当前仍被 `cmd/ai-doubao/main.go` 使用；应删掉 `BuiltinServiceManifests` / `ChatServerServiceManifest` 等与 ai-doubao 无关部分，并收敛为最小 manifest 构造逻辑）
  - 验收：`go test`（或最小 build）仍通过；`ai-doubao` 可启动并提供现有 `/v1/*`。

---

### Phase 2（核心合规）：chat-server 只通过 Hub 调 AI / DB / File

#### 2.1 chat-server：移除本地 sqlite 与本地文件写入

- 任务 F：删除/替换本地 sqlite 依赖
  - 做法：将 `SQLiteStore` 抽象为 `ChatStore` 接口（仅保留 chat-server 实际需要的 CRUD 与历史读取能力），实现一个 `HubDatabaseStore`：
    - 所有数据操作转换为对 Hub `POST /api/tool/call` 的调用（tool_id 使用 `storage.database.query/execute/insert/update/delete` 或项目既有的 database tool 集合）。
    - 身份与权限（按用户补充规则收敛）：chat-server 作为 service caller 调 Hub 时，由 Hub 以受保护 headers 向下游暴露 `caller.type=service`、`service_id`、以及（如需要）`reliability`，下游按 allowlist + reliability 做允许域控制。
  - 删除目标：
    - `services/chat-server/internal/app/sqlite_store.go`
    - `cmd/chat-server/main.go` 中的 `--sqlite-path`、`NewSQLiteStore` 初始化与 Close
  - 验收：
    - chat 页面 sidebar 的 project/thread CRUD 正常（依赖 `webui/page/chat/sidebar-controller.js` 的 `app.chat.*` 工具链路保持可用）。
    - 不存在任何本地 sqlite 打开行为（代码层面 `rg "sqlite|database/sql|\\.db"` 在 chat-server 内不应出现用于本地直连的实现）。

- 任务 G：移除本地 ops 文件写入
  - 做法（按本计划默认执行）：删除 `services/chat-server/internal/app/operation_log.go` 与所有调用点，仅保留标准输出日志（由 Hub/部署脚本统一采集）。
  - 验收：chat-server 无 `os.OpenFile` 等本地写文件行为。

#### 2.2 chat-server：移除豆包直连（ASR/LLM/TTS 只能走 Hub）

- 任务 H：实现 `HubAIClient`（在 chat-server 内部）
  - LLM（WS 隧道）：通过 Hub `GET /api/tool/ws?tool_id=ai.llm.stream` 建立 WebSockets 隧道，按工具协议发送请求并接收 delta/final。
  - ASR（WS 隧道，二进制）：通过 Hub `GET /api/tool/ws?tool_id=ai.speech.asr` 建立 WebSockets 隧道，发送 start + pcm frames + finish control，接收 partial/final。
  - TTS（同步）：调用 Hub `/api/tool/call`，tool_id=`ai.speech.tts`，返回 base64 音频。
  - 替换点：`ProviderFactory` 默认实现改为“Hub 模式”，并删除 `LocalProviderFactory` 与豆包直连 client。
  - 删除目标：
    - `services/chat-server/internal/app/asr.go`
    - `services/chat-server/internal/app/llm.go`
    - `services/chat-server/internal/app/tts.go`
    - `services/chat-server/internal/app/provider_factory.go` 中的直连实现（保留接口与 Hub provider）
  - 验收：chat-server 内 `rg "volces|openspeech|ark\\.cn-"` 无结果；流式对话仍可用。

- 任务 H2：清理 `chat-server` 的 `configx.json`（移除业务范围外的豆包密钥配置）
  - 背景：用户已明确 chat-server 不需要也不应携带豆包密钥；并且 chat-server 禁止直连外部 AI。
  - 做法：
    1) 调整 `services/chat-server/internal/app/config.go` 的校验：当 `ai_service.mode=service` 时，不再要求 `chat/asr_s/tts_s` 的第三方密钥字段完整；
    2) 收敛 `services/chat-server/config/configx.json`：仅保留 chat-server 自身需要的配置（如 hub 地址、ai_service 的 hub‑tool 相关开关/超时等），删除豆包密钥字段；`.service_secret` 由专用文件管理，不作为业务配置字段出现。
  - 验收：`services/chat-server/config/configx.json` 中不再出现第三方 provider 的 `apiKey/accessToken/appId/wsUrl` 等字段；chat-server 仍能正常通过 Hub 调用 `ai.*`。

---

### Phase 3（平台补齐）：让“ai.* 流式工具”在 Hub 侧成为一等公民

> 本阶段按你补充的“统一规则”执行：Hub 只提供 **tool 级别** 的调用；流式工具统一走 `GET /api/tool/ws?tool_id=...` 的 **反向代理隧道**，Hub 不解析业务协议。

#### 3.1 工具注册元数据升级（为流式 WS 隧道提供定位信息）

- 任务 I：为“流式工具”在注册/manifest 元数据中新增 `WSPath`（或等价字段）
  - 背景：Hub 要建立 WS 隧道，必须知道目标 service 的 WS 入口路径；仅有 `tool_id + endpoint` 不足以定位。
  - 改造点（需要全链路一致）：
    - Service 注册 payload（`/api/service/register`）中 tools 列表增加 `ws_path` 字段；
    - manifest（`manifest.json` 的 provides/tool 描述）也保留 `ws_path` 字段（用于静态说明与未来扩展）；
    - Hub 平台侧保存的工具描述（`ServiceToolDescriptor`/路由表）同步保存该字段，并以 **register 为可信来源**：
      - 若 register 缺失但 manifest 有：可降级使用 manifest（并记录告警），但不作为长期依赖；
      - 若两者冲突：以 register 为准，并记录冲突告警。
    - 仅当工具为“流式工具（WebSockets）”时要求 `ws_path` 非空；原子化工具保持走 `/service/tool/exec` 不需要该字段。
  - 验收：Hub 可从注册信息中拿到 `tool_id -> ws_path` 映射。

- 任务 I2：将 chat 会话 WS 显式注册为流式工具（方案 B + 混合机制的关键落点）
  - 目标：把现有 chat 会话 WS 入口从“特殊反代”升级为“标准流式 tool”。
  - 做法：
    - chat-server `manifest.json`/register tools 中新增 `app.chat.stream`（或你确认的命名）：
      - `streaming=true`（或等价标记）
      - `ws_path=/service/tool/ws`
    - Hub 路由表将其视为普通流式工具，可用 `GET /api/tool/ws?tool_id=app.chat.stream&project_id=...&thread_id=...` 建立隧道。
  - 验收：前端迁移后不再依赖 legacy `GET /api/tool/ws`（无 tool_id）也能正常工作。

#### 3.2 Hub 路由与网关升级：把 `/api/tool/ws` 升级为通用“流式 Tool 网关”

- 任务 J：升级 `GET /api/tool/ws` 支持 `tool_id` 路由选择与隧道建立
  - 行为：
    1) Hub 从 URL query 读取 `tool_id`；
    2) Hub 通过路由引擎选择 provider（service + instance）；
    3) Hub 读取该 provider 对应工具的 `ws_path`；
    4) Hub 与目标 service 建立 WS 连接（endpoint + ws_path），并注入 `X-Hub-Service-Token` + caller headers；
    4.1) 除 `tool_id` 外，其它 query 参数（如 `project_id`/`thread_id`）需要透明转发到目标 WS（以保持 chat 会话协议与未来扩展能力）；
    5) Hub 仅做双向帧转发（含二进制），不解析 payload。
  - 兼容性：当 `tool_id` 为空时，保持 As‑Is 默认代理 chat-server 的 `/service/tool/ws`（避免破坏现有前端链路）。
  - 验收：前端可用统一入口 `GET /api/tool/ws?tool_id=<streaming_tool>` 建立到任意流式工具的 WS 隧道。

#### 3.3 service->hub->service 通路的治理收敛（允许但不推荐）

- 任务 K：为“service 作为 caller 调 Hub”建立明确的 allowlist + reliability 约束（与用户补充规则一致）
  - 原则：
    - 默认仍以 `webui/page -> hub -> service` 为主；
    - 仅对“会话聚合必需”的 app service（例如 chat-server 的会话 WS / 必需 AI 子调用）允许发起 `service -> hub -> service`；
    - 接收方 service（`ai-doubao`/`database-service`/`file-service`）按 `caller.service_id` 与 `reliability` 限制允许域（例如仅接受 `trusted`）。
  - Hub 改造点（最小集）：
    1) `POST /api/tool/call`：对 service caller 的 `tool_id` 做严格 allowlist（避免 service caller 获得平台级超级权限）。
    2) `GET /api/tool/ws?tool_id=...`：允许 service caller（同 allowlist 策略），并把 service caller 身份与 reliability 作为受保护 headers 注入到下游 WS 握手。
    3) reliability 的来源：来自 register（推荐）或 supervisor registry 的 tags/字段；Hub 作为唯一可信注入方。
  - 验收：在不强制引入用户委托 token 的情况下，chat-server 仍可通过 Hub 调用必要的 `ai.*`/`storage.*` 工具完成会话聚合；同时其可调用范围能被 Hub 与接收方双重收敛。

#### 3.4 ai-doubao：按“原子工具 + 流式工具（WS）”补齐平台能力

- 任务 L：实现 `POST /service/tool/exec`（原子化工具入口）
  - 最小闭环：先让 `ai.speech.tts` 走 `CallRequest/CallResponse`（同步返回 base64 音频）。
  - 必须校验：`X-Hub-Service-Token`（使用本地 `.service_secret` 验签）。
  - 验收：Hub `POST /api/tool/call` 可路由执行 `ai.speech.tts`。

- 任务 M：为 `ai.llm.stream` 提供 WebSockets 版流式端点，并在注册元数据中提供 `WSPath`
  - 背景：你已明确“流式工具统一走 WebSockets 隧道”；现状 `ai-doubao` 的 LLM 流式为 SSE，不满足统一规则。
  - 目标：
    - 新增/改造一个 WS 端点用于 `ai.llm.stream`（例如 `/v1/llm/ws` 或收敛到 `/service/tool/ws` 分发）；其消息协议与现有 `AIServiceLLMStreamEvent` 对齐（delta/final/error）。
    - 注册元数据为 `ai.llm.stream` 填充 `ws_path` 指向该端点。
    - 同步为 `ai.speech.asr` 填充 `ws_path`（可复用现有 `/v1/asr/stream`），并为该 WS 入口补齐 `X-Hub-Service-Token` 校验。
  - 验收：前端通过 Hub `GET /api/tool/ws?tool_id=ai.llm.stream` 可拿到 delta/final；`ai.speech.asr` 同理。

---

## 6. 代码与文件清理清单（按“必须删除的未使用代码”与“替换后可删除代码”分两类）

### 6.1 立即可删（当前已确认在各自 service 内无引用）

ai-doubao：
- `services/ai-doubao/internal/app/context_meta.go`
- `services/ai-doubao/internal/app/protocol.go`
- `services/ai-doubao/internal/app/auth.go`
- `services/ai-doubao/internal/app/hub_platform.go`
- `services/ai-doubao/internal/app/hub_builtins.go`（不建议整文件删除：应只删除与 ai-doubao 无关部分，并收敛为最小 manifest 构造逻辑）

chat-server：
- `services/chat-server/internal/app/auth.go`
- `services/chat-server/internal/app/hub_platform.go`
- `services/chat-server/internal/app/hub_builtins.go`（删掉 `BuiltinServiceManifests`/`BuildAIServiceManifest` 等未使用部分，仅保留 chat-server 自身 manifest 生成或改为读 `manifest.json`）

### 6.2 Phase 2/3 完成后可删（替换链路完成后删除）

chat-server（在 Hub AI client 落地后）：
- `services/chat-server/internal/app/asr.go`
- `services/chat-server/internal/app/llm.go`
- `services/chat-server/internal/app/tts.go`
- `services/chat-server/internal/app/provider_factory.go` 中的 `LocalProviderFactory` 相关逻辑

chat-server（在 Hub database/file store 落地后）：
- `services/chat-server/internal/app/sqlite_store.go`
- `services/chat-server/internal/app/operation_log.go`（若改为经 Hub 写入或取消）

---

## 7. 验证与回归（每阶段都必须可操作）

### 7.1 静态合规扫描（必须通过）
- chat-server：
  - `rg -n \"ark\\.cn-|openspeech\\.bytedance\\.com|volces\" services/chat-server` 结果为空
  - `rg -n \"sqlite|database/sql|os\\.OpenFile|os\\.ReadFile\" services/chat-server` 不得出现“业务路径依赖本地 DB/file”的实现（允许读取自身 `services/chat-server/config/*`）
  - `rg -n \"kagent/pkg/\" services/chat-server` 结果为空
- ai-doubao：
  - `rg -n \"kagent/pkg/\" services/ai-doubao` 结果为空

### 7.2 端到端主链路验证（必须通过）
- Hub + services 启动后：
  - chat 页面 project/thread CRUD 正常（`app.chat.project_*` / `app.chat.thread_*`）
  - 语音输入 -> ASR partial/final 正常
  - LLM 流式输出正常（delta 连续到达）
  - TTS 合成与音频下发正常
  - 任意时刻停止 Hub：service 按“反向心跳”机制退出（若当前已实现）。

---

## 8. 风险与回退策略

1. **流式代理实现复杂度高**：Phase 3 需要 Hub 具备 WebSockets 隧道的透明代理与路由选择（含二进制帧），建议先实现最小闭环（只支持 `ai.llm.stream` / `ai.speech.asr`），避免泛化过早。
2. **service->hub->service 的滥用风险**：虽然允许，但必须有严格 allowlist + reliability 约束与审计，避免 app service 变相获得“平台级超级权限”。
3. **数据模型迁移风险**：从 sqlite 迁移到 database-service 可能涉及历史数据兼容；建议提供一次性迁移脚本或明确“本轮不迁旧数据，只保证新写入路径正确”的策略。

> 备注（与你的当前阶段决策一致）：本轮实现允许 `untrusted` 的 service->hub->service 全量路由与接收方全量接受；本节风险项用于提示后续演进时的治理升级方向。

---

## 9. 输出物（Definition of Done）

完成本计划后，至少满足：
- `chat-server` 不再包含任何豆包直连实现与密钥使用；不再访问本地 sqlite/本地文件；所有外部能力经 Hub。
- `ai-doubao` 可作为标准 tool provider 被 Hub 路由（至少 TTS 同步工具闭环），并能被 Hub 流式代理支持（LLM/ASR）。
- 两个 service 均满足“文件/代码自洽”：不依赖 service 目录外的代码与本地文件资源；接入 Hub 作为平台网关后功能完整可用。
- 已删除所有确认未使用的文件与代码路径。

---

## 10. 已确认决策（你已拍板，本计划据此定稿）

1. **chat 会话 WS 的 tool_id**：确定使用 `app.chat.stream` 作为会话级流式工具的 tool_id。

2. **reliability 的来源与默认策略**：
   - reliability 必须来自 **Hub 内部机制**，不允许 service 通过 `manifest.json` 或 `/api/service/register` 修改；
   - 默认 reliability 全部为 `untrusted`；
   - 当前开发阶段：Hub 对所有 `service -> hub -> service` 消息均正常路由与处理（不因 `untrusted` 而拒绝）。

3. **service caller allowlist 与接收方拒绝策略（当前阶段）**：
   - 当前开发阶段：不对 `service -> hub` 的 tool 调用做额外 allowlist 限制；
   - 接收方 service 当前阶段也不做基于 reliability 的额外拒绝（全部接受 Hub 路由到达的调用）。
