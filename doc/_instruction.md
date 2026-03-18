# Kagent 项目技术说明（doc/_instruction.md）

## 1. 项目概览

`kagent` 是一个基于 **Hub + 多独立 Service** 架构构建、面向本地多进程运行形态的 AI 交互与工具平台。系统通过 “前端驱动交互（Client‑Driven） + Hub 统一治理（Gateway/Governance） + Service 原子化执行（Tool Providers）” 的协作分层，实现能力可插拔、调用可审计、边界可控。

### 1.1 核心架构模型
本项目遵循 **“交互在前端，治理在 Hub，执行在 Service”** 的协作原则：
- **Page (表现层 & 决策者)**：承载用户交互，负责高频判定（如 VAD、打断）并编排工具调用请求。
- **Hub (平台内核 & 隔离边界)**：系统唯一入口与枢纽。负责认证鉴权、路由治理、工具代理、审计与多进程生命周期管控。
- **Service (能力执行层)**：独立的原子能力提供者（如 AI 逻辑）或业务协调者（如 Chat 逻辑）。通过统一的工具协议（Tool Protocol）接受 Hub 调度。

---

## 2. 核心框架设计理念

本节只强调**核心理念（白皮书 260316 的“架构哲学”）**，并在每条理念后给出“当前项目实现的对应落点（以代码为准）”。这些理念用于长期演进与合规判定：任何新增能力/新增服务/新增 Surface 都应尽量不破坏这些不变量。

### 2.1 逻辑路径优先（Logic > Physics）
跨模块协作必须面向 **`tool_id` 逻辑标识**（例如 `ai.llm.stream`、`app.chat.stream`），而不是面向某个固定端口、进程、或某个 service 的私有 URL。端口/进程只是运行时载体，不应成为业务依赖的“接口契约”。
- 实现落点：Hub 工具网关 `POST /api/tool/call`（`hub/internal/gateway/tool_handler.go`）与路由选择（`hub/internal/routing/*`）；流式工具网关 `GET /api/tool/ws?tool_id=...`（`hub/internal/gateway/tool_handler.go`）。

### 2.2 Client‑Driven 交互模型（交互仲裁在 Page）
交互节奏（开始/停止录音、打断/继续、何时触发调用）应由 Page 作为单一事实源仲裁；后端更多承担“被动执行”与“可观测返回”。这能避免把高频交互逻辑绑定到后端进程时序与网络抖动。
- 实现落点：WebUI 入口与页面组织（`webui/page/chat/`、`webui/page/surface/`），Hub 静态资源托管与默认跳转（`hub/cmd/hub/main.go`）。

### 2.3 治理权收敛于 Hub（唯一网关 + 可治理）
Hub 是系统唯一入口与治理边界：服务发现、路由选择、审计、身份/权限、生命周期编排都应收敛在 Hub；Service 不应绕过 Hub 与其它 Service 直接互连。
- 实现落点：Service 注册与心跳（`/api/service/*`，`hub/cmd/hub/main.go`）；工具调用审计与路由记录（`hub/internal/observability/*` + `hub/internal/routing/*`）。

### 2.4 “工具平面”统一：原子工具（HTTP）与流式工具（WS）
平台对外能力收敛为两类工具形态，并统一由 Hub 暴露：
- **原子工具**：`POST /api/tool/call`（Hub 负责路由 + 鉴权/审计 + 转发到 Service 的 `POST /service/tool/exec`）。
- **流式工具**：`GET /api/tool/ws?tool_id=...`（Hub 负责路由 + 受保护 header 注入 + WS 隧道反代到 Service 的 `GET /service/tool/ws`，并由 Service 按 `tool_id` 分发）。
- 实现落点：HTTP 工具网关与 WS 代理（`hub/internal/gateway/tool_handler.go`）；Streaming 工具的 `ws_path` 配置（各 Service manifest 的 `ws_path` 字段，如 `services/chat-server/internal/app/service_manifest.go`）。

### 2.5 零信任与“头部卫生”（Header Hygiene）
身份不能靠端口、RemoteAddr、“看起来像”来推断，必须靠**互信 header + 严格校验**。同时 Hub 必须对来自浏览器的请求做 protected headers 清洗，避免客户端伪造内部身份字段影响后端。
- 实现落点：受保护 headers 清洗（`hub/internal/security/headers.go`）；Hub->Service 注入 `X-Hub-Auth`/caller headers（`hub/internal/gateway/tool_handler.go`）；Service->Hub 注入 `X-Service-Auth`（`pkg/hubsvc/session.go`；各 service 注册/调用 Hub 时使用）。

### 2.6 生命周期与编排收敛（Hub‑Centric Orchestration）
多进程系统的“启动/停止/冲突清理/Ready 判定/（可选）可用性探测”应由 Hub 收敛管理；部署脚本尽量只做构建与拉起 Hub 本身，减少脚本承载的治理复杂度。
- 实现落点：Hub 生命周期管理器（`hub/internal/supervisor/lifecycle.go`）；Hub 启动后拉起服务与自动冒烟（`hub/cmd/hub/main.go`）；部署脚本的极简化（`scripts/deploy.sh`）。

### 2.7 数据与文件边界（Service 不直接触达用户数据）
Service 的文件系统边界应尽量收敛到自身目录；对用户数据/共享存储的读写应通过 Hub 的工具平面完成，以便统一审计、能力隔离与授权闭环。
- 实现落点：`chat-server` 使用 Hub 工具客户端走 `POST /api/tool/call`（`services/chat-server/internal/app/hub_tool_client.go`）；存储类工具族（`storage.*`/`ui.surface.*` 等）在 `database/file/surface-manager` service 中实现（各 service 的 `cmd/*/main.go` + `internal/app/*`）。

### 2.8 可观测与审计（可追溯、可定位、可回放）
所有跨边界调用都应携带 `request_id/trace_id`、caller identity，并在 Hub 侧形成可检索的审计日志；业务事件通过虚拟工具统一上报，避免“分散打印”导致链路断裂。
- 实现落点：caller/trace 注入（`hub/internal/security/headers.go`）；统一上报 `hub.system.report_log`（`hub/internal/app/hub_platform.go` + Hub 路由实现位于 `hub/cmd/hub/main.go`）。

---

## 3. 项目详细结构说明

> ⚠️ 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage`。

```text
kagent/                                        # 仓库根目录
├── AGENTS.md                                  # 项目内协作与文档维护总规范
├── README.md                                  # 项目入口说明（待确认是否为最新）
├── kagent                                     # Hub 可执行文件（`scripts/deploy.sh` 构建产物）
├── hub/                                       # Hub：唯一网关与治理中心
│   ├── cmd/hub/main.go                         # Hub 进程入口：路由组装、生命周期、AutoSmokeTest
│   ├── config/                                 # Hub 运行配置（与根 config/ 可能存在镜像）
│   │   ├── config.json                         # Hub 管理 service 列表等（当前被 deploy.sh 读取）
│   │   └── services.json                       # Hub 生命周期配置（当前被 Hub 读取）
│   └── internal/                               # Hub 私有实现：gateway/security/routing/supervisor/transport 等
├── services/                                  # 多独立 Service（各自独立进程，暴露 `/service/tool/*`）
│   ├── ai-doubao/                              # AI provider：提供 `ai.*`（含 WS 流式工具）
│   ├── chat-server/                            # 业务编排：提供 `app.chat.*`（含 `app.chat.stream` WS）
│   ├── database/                               # 存储/数据库能力（提供 `storage.*` 等工具族，详见代码）
│   ├── file/                                   # 文件与 blob 能力（提供 `storage.*` 等工具族，详见代码）
│   ├── surface-manager/                        # Surface 扫描与 capability/session（提供 `ui.surface.*` 等）
│   └── auth/                                   # Auth Service 合约占位（目录存在；当前未纳入 lifecycle 管理）
├── webui/                                     # Web UI（Page 与 Surface 的静态资源）
│   ├── page/                                   # 宿主页面（chat/surface/account）
│   └── surface/                                # 插件化 UI（buildin/extension/custom）
├── pkg/                                       # 共享公共包（Hub 与部分 service 复用）
│   ├── toolproto/                              # 工具协议结构（CallRequest/CallResponse、Supervisor 协议等）
│   ├── hubsvc/                                 # Hub<->Service bootstrap/互信 header 工具
│   └── sqlitedriver/                           # sqlite 驱动封装（供 Hub/服务使用）
├── config/                                    # 运行配置（WebUI/public config、lifecycle config 等）
│   ├── config.json                             # Page/Chat 公共配置（前端/会话参数等）
│   └── services.json                           # Hub 生命周期配置（与 hub/config/services.json 当前内容相同）
├── scripts/                                   # 脚本：构建/启动/重置
│   ├── deploy.sh                               # 构建 Hub+services 并启动 Hub（不再负责拉起 services）
│   └── reset_db.sh                             # 停止进程并清理 `data/`（支持 all/users 两种模式）
├── data/                                      # 运行态数据（用户数据/Hub 状态/Blob 等）
├── run/                                       # PID 与运行态临时文件（按需生成）
├── logs/                                      # 日志目录（用途待确认）
├── log.txt                                    # Hub 统一日志（deploy.sh 会重置并 tail）
├── bin/                                       # 版本化历史二进制（包含 symlink 指向最新版本）
├── plan/                                      # 规划/设计/分析类文档（PRD/DevPlan/ANA/DevReport）
├── doc/                                       # 项目说明与开发日志（权威快照与追加记录）
└── ref/                                       # 参考资料（例如 doubao 相关文档）
```

---

## 4. 核心模块职责图谱

### 4.1 Hub (The Coordinator)
- **统一入口**：托管 WebUI 静态资源并提供默认跳转；对外只暴露 Hub（`hub/cmd/hub/main.go`）。
- **工具网关**：`POST /api/tool/call`（原子工具）与 `GET /api/tool/ws`（流式工具 WS 隧道）（`hub/internal/gateway/tool_handler.go`）。
- **身份与安全边界**：JWT 解析 + caller 注入；protected headers 清洗；Hub<->Service 互信注入（`hub/internal/security/headers.go` + `pkg/hubsvc/session.go`）。
- **服务治理与生命周期**：Service 注册/心跳（`/api/service/*`）+ LifecycleManager 拉起/停止/重启（`hub/internal/supervisor/*`）。
- **路由与审计**：按 `tool_id` 选择实例、记录审计与统计（`hub/internal/routing/*` + `hub/internal/observability/*`）。
- **冒烟验证**：启动后自动执行端到端 smoke test（`hub/internal/app/smoke.go` + `hub/cmd/hub/main.go`）。

### 4.2 chat-server (The Orchestrator)
- **对外提供 `app.chat.*`**：项目/线程管理工具 + 流式会话入口 `app.chat.stream`（`services/chat-server/internal/app/service_manifest.go`）。
- **会话 WS 承载**：对 Hub 暴露 `GET /service/tool/ws`，由 Hub 反代后与 Page 建立会话（`services/chat-server/cmd/chat-server/main.go`）。
- **外部能力访问经由 Hub**：通过 Hub 工具网关调用 `ai.*`、`storage.*` 等（`services/chat-server/internal/app/hub_tool_client.go` + `services/chat-server/internal/app/hub_database_store.go`）。

### 4.3 ai-doubao (The Atomic Provider)
- **对外提供 `ai.*`**：`ai.speech.asr`、`ai.llm.stream`、`ai.speech.tts`（包含 WS 流式工具）（`services/ai-doubao/cmd/ai-doubao/main.go`）。
- **工具入口受 Hub 保护**：`/service/tool/exec` 与 `/service/tool/ws` 校验 Hub 注入的互信 headers（`services/ai-doubao/cmd/ai-doubao/main.go`）。

### 4.4 database / file / surface-manager（能力服务）
- **database**：提供 `storage.database.*` 等存储相关工具（具体以 `services/database/cmd/database/main.go` 与其内置工具清单为准）。
- **file**：提供 `storage.file.*`/`storage.blob.*` 等文件与 blob 相关工具（具体以 `services/file/cmd/file/main.go` 与其内置工具清单为准）。
- **surface-manager**：负责 surface catalog 扫描与 capability/session 相关工具（具体以 `services/surface-manager/cmd/surface-manager/main.go` 与其内置工具清单为准）。

---

## 5. 协议、接口与关键机制（以“Tool 平面”为中心）

### 5.1 Tool 网关（外部只看 Hub）
- **原子工具入口**：`POST /api/tool/call`（Hub 解析 `toolproto.CallRequest`，转发到目标 Service 的 `POST /service/tool/exec`，返回 `toolproto.CallResponse`）。
- **流式工具入口**：`GET /api/tool/ws?tool_id=<...>`（Hub 按 tool descriptor 的 `ws_path` 反代到目标 Service 的 `GET /service/tool/ws`）。

### 5.2 Service 工具入口（内部只信 Hub）
- **原子执行入口**：`POST /service/tool/exec`（Service 必须校验 Hub 注入的互信 headers）。
- **流式执行入口**：`GET /service/tool/ws`（Service 必须校验 Hub 注入的互信 headers，并按 `tool_id` 分发流式会话）。

### 5.3 身份、互信与受保护 headers
- **caller headers（Hub 注入）**：
  - `X-Hub-Request-Id` / `X-Hub-Trace-Id`
  - `X-Caller-Type`（user/service/surface/anonymous）
  - `X-Caller-User-Id` / `X-Caller-Service-Id` / `X-Caller-Surface-Id`
  - `X-Caller-Reliability`
- **Hub<->Service 互信（启动期 secret 注入 + 运行期字符串匹配）**：
  - Service -> Hub：`X-Service-Id` / `X-Service-Instance-Id` / `X-Service-Auth`
  - Hub -> Service：`X-Hub-Service-Id` / `X-Hub-Service-Instance-Id` / `X-Hub-Auth`
- **头部卫生**：Hub 在转发前会清洗 protected headers，确保浏览器侧伪造的内部认证 header 不会透传（`hub/internal/security/headers.go`）。

### 5.4 日志撰写规范 (Standardized Logging)
- **输出标签（SOURCE）**：由 Hub 根据请求身份动态标记：
  - `[HUB]`：Hub 内部产生的信息。
  - `[SERVICE-ID]`：由独立 Service（通过 `X-Service-Auth` 被识别）发起的调用审计。
  - `[PAGE]`：由前端宿主页面（User JWT 认证）上报。
  - `[SURF]`：由独立 Surface 插件上报。
- **三类日志架构（Prefixes）**：
  - **`System:Internal:`**：Hub 自身的生命周期、配置加载、服务管理。
  - **`System:HTTP:`**：网关 HTTP 请求流水审计（含 Method/Path/Status/Duration）。
  - **`Service:Report:`**：由 Service 或前端通过 `hub.system.report_log` 虚拟工具主动上报的业务事件。
- **格式范例**：
  - 系统审计：`[TS] [INFO] [HUB] [zhyuzh] System:HTTP:POST /api/tool/call [200] (1.2ms)`
  - 业务上报：`[TS] [INFO] [HUB] [zhyuzh] Service:Report:Module:Action:Description`
- **原则**：屏蔽高频无意义心跳，确保所有上报日志具有明确的身份追踪。

### 5.5 进程生命周期与编排契约
- **deploy.sh 极简化**：负责构建 `./kagent`（Hub）与各 service 的 `services/<svc>/run/<svc>-latest` + `services/<svc>/run/manifest.json`，然后启动 Hub 并 tail `log.txt`（`scripts/deploy.sh`）。
- **Hub 生命周期管理**：Hub 启动后读取 `config/services.json`（默认）并拉起 service；拉起前写入 `services/<svc>/run/.service_secret` 用于启动期互信注入（`hub/internal/supervisor/lifecycle.go`）。
- **AutoSmokeTest**：Hub 在拉起服务后自动运行 smoke test（`hub/internal/app/smoke.go` + `hub/cmd/hub/main.go`）。
- **优雅退出**：`POST /admin/shutdown`（loopback-only）触发 Hub 级联 stop（`hub/cmd/hub/main.go`）；deploy.sh 对 Hub 给 3s 宽限期，超时强杀（`scripts/deploy.sh`）。
- **启动预清理（Port Preemption）**：Hub 启动前尝试清理占用端口的旧实例（`hub/internal/app/*` + `hub/cmd/hub/main.go`）。

---

## 6. 最近关键变更摘要（以代码为准）
- Hub 内置 LifecycleManager：启动后由 Hub 拉起 service，并将启动期 `.service_secret` 注入到 `services/<svc>/run/`（`hub/internal/supervisor/lifecycle.go`）。
- Tool WS 网关语义升级：`/api/tool/ws` 支持 `tool_id` 路由，并按 tool descriptor 的 `ws_path` 反代到目标 service（`hub/internal/gateway/tool_handler.go`）。
- 部署脚本收敛：`scripts/deploy.sh` 仅做构建 + 拉起 Hub + tail 日志；服务治理/自测转移到 Hub（`scripts/deploy.sh` + `hub/cmd/hub/main.go`）。

---

## 7. 项目常用术语表 (Glossary)

| 术语 | 定义 & 职责 | 状态 |
| :--- | :--- | :--- |
| **Hub** | 中心网关，控制面枢纽 | Active |
| **Service** | 独立进程的能力/业务单元 | Active |
| **Page** | 前端宿主页面，决策中心 | Active |
| **Surface** | 插件化 UI 模块，受 capability 令牌限制资源访问 | Active |
| **Tool ID** | 能力的唯一逻辑标识（如 `ai.speech.asr`） | Active |
| **CallRequest / CallResponse** | 工具调用请求/响应结构（Hub 网关与 Service 入口的共同协议） | Active |
| **Bootstrap Secret** | 启动期 `.service_secret` 文件：注入 `S2H_TOKEN/H2S_TOKEN` 与 register URL | Active |
| **X-Service-Auth** | Service->Hub 内部调用身份凭证（字符串匹配） | Active |
| **X-Hub-Auth** | Hub->Service 内部调用身份凭证（字符串匹配） | Active |
| **Identity (Context)** | 经过认证的调用者实体（含 USER/SERVICE/SURFACE/ANONYMOUS） | Active |
| **hub.system.report_log** | 内建虚拟工具，用于统一全平台日志上报 | Active |
| **SmokeTester** | Hub 内建的自监测模块，负责自动运行全链路冒烟测试 | Active |
| **Capability Token** | 授权 Surface 访问特定目录/API 的带作用域 (Scope) 令牌（以 surface-manager 实现为准） | Active |
| **hub_only** | Hub 主动发起的探测/内部调用标记（写入 `CallRequest.context.meta.hub_only`） | Active |

---

## 8. 运行与开发验证 (DoD)

1. **构建与并行部署**：`./scripts/deploy.sh`（执行构建、启动并在最后自动进入 `tail -n 0 -F` 实时输出模式，仅展示后续增量）。
2. **重置环境**：`./scripts/reset_db.sh`（停止所有进程并清空 `data/` 隐私数据）。
3. **健康核验**：观察 Hub 日志中 `System:Internal:AutoSmokeTest` 的输出结果。
4. **日志分析**：`tail -F log.txt` 或在 Browser 控制台查看 `[PAGE]/[SURF]` 标签日志。

---

## 9. 待确认事项（需要进一步核验/收敛）
1. 配置单一事实源：deploy.sh 读取 `hub/config/config.json`，Hub 默认读取 `config/services.json`；两处目前内容一致，但需要明确“长期以谁为准”。
2. `services/auth/` 的定位：目录与 manifest 存在，但当前未纳入 lifecycle 管理与启动链路；认证逻辑目前在 Hub 内实现（需在文档中明确边界与后续策略）。
3. Service “完全独立可拷走运行”的理念与当前实现差距：部分 service 仍复用 `pkg/*`（例如 `pkg/hubsvc`）；需要在后续计划中明确是否继续收敛或允许复用。
4. `Inverse Heartbeat` 的落地范围与验收：协议字段存在，但是否形成统一机制仍需核验与收敛。

---

**文档更新时间**：2026-03-18 16:15 CST  
**信息来源**：对照白皮书理念（`plan/260316-kagent-architecture-philosophy-ana.md`）并以仓库真实代码/脚本为准（重点核验：`hub/cmd/hub/main.go`、`hub/internal/gateway/tool_handler.go`、`hub/internal/supervisor/lifecycle.go`、`scripts/deploy.sh`、`services/ai-doubao/cmd/ai-doubao/main.go`、`services/chat-server/cmd/chat-server/main.go`）。
