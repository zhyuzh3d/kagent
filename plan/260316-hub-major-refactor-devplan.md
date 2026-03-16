# 260316 Hub 大重构开发计划（Tool 路由方案 A / Auth 内嵌 Hub / UDS 优先）

- 文档类型：开发计划（devplan）
- 更新时间：2026-03-16 11:13 CST
- 范围：`hub/`、`services/*`、`webui/`（含 surface）
- 依据：
  - PRD：`plan/260316-hub-service-platform-architecture-prd.md`
  - 现状实现（抽样证据）：
    - Hub 同时存在硬编码代理与本地业务 Handler：`hub/cmd/hub/main.go`、`hub/cmd/hub/service_proxy.go`
    - Hub 内置大量业务/存储/认证实现与 SQLite schema：`hub/internal/app/*`
    - 多个 service 当前仍以旧 REST 形态提供能力（如 file/database/chat-server 等）：`services/*/cmd/*/main.go`
  - 本次对话中已确认的架构决策（见“共识与约束”）

---

## 0. 共识与约束（本计划的“不可变前提”）

### 0.1 动态路由闭环：选择方案 A，且不兼容旧接口
- 统一以 `tool_id` 为主键进行路由、鉴权、审计与治理。
- 强制把旧有 REST/WS 接口整体改写为新协议，不提供旧模式兼容层（开发过程允许短暂双栈以便迁移，但最终形态必须删除旧接口与旧路由）。

### 0.2 安全：Hub 完全管控授权，不外泄密钥与签发能力
- 服务只信 Hub：服务端不验证用户 JWT、不持有 JWT 签名密钥、不签发任何 token。
- `server/service/user/surface` 的授权与 token 生命周期由 Hub 统一签发与验证。
- Surface Manager 与 Service Manager 定位为“应用级管理服务”，只做列表/元数据管理，不参与任何权限授予。

### 0.3 数据：所有 DB 操作必须经由 database-service
- 除 database-service 外，任何模块不得直接打开/写入 DB 文件。
- DB 相关密钥/敏感配置只允许存在于 `services/database/config/configx.*`（或等价敏感配置载体）中。
- 每个 service 原则上只操作自己的数据库；另设 `_share` 库：
  - 任意 service 可写入“自己的条目”（强约束：`service_id == caller_service_id`），可公共读取。
  - 写入约束必须由 database-service 强制执行（应用层校验 + DB 侧约束/触发器兜底）。

### 0.4 传输：必须同时支持 HTTP/HTTPS/WebSocket/Unix Domain Socket
- 对外：Hub 作为统一入口，提供 `http/https/ws`。
- 对内：Hub 到 service 的默认传输优先采用 `unix domain socket`（UDS），并允许在开发/调试场景回退到 loopback TCP。

---

## 1. 目标架构概述（重构完成后的“最终形态”）

### 1.1 角色与边界

**Hub（平台内核）**
- `Gateway`：静态资源托管；统一入口；WebSocket 终止与桥接（如需要）。
- `Security`：统一身份与 token（用户 JWT、service session token、surface session/capability token）签发与验证；把“调用方身份”注入到下游。
- `Supervisor`：service 注册/冲突/心跳/prepare-start/级联退出。
- `Routing`：基于 `tool_id -> service_id` 的绑定表进行路由；内置评分/熔断/降级策略；提供运维与可观测。

**Services（原子能力提供者）**
- 只提供“工具执行接口”（Tool Executor），不对外暴露用户态 REST；不校验用户 JWT。
- 通过 Hub 注入身份（如 `X-User-ID`）与 Hub 颁发的 service token 来区分 caller。
- 读写 DB 一律通过 database-service 的工具调用，不直连 DB 文件。

**Surface（webui/surface 插件）**
- 通过 Hub 统一入口加载与运行。
- 不具备直接访问用户级数据的能力；仅能在 Hub 授予 capability 的范围内，通过工具协议调用能力。

**Surface Manager / Service Manager（应用级管理服务）**
- 仅维护“列表与元数据”（增删改查、状态上报、日志聚合等），不参与授权与 token 发放。

### 1.2 统一工具协议（方案 A 的核心）

**对外接口（Hub）**
- `POST /api/tool/call`：非流式工具调用（请求包含 `tool_id`、`args`、`context`）。
- `WS /api/tool/ws`（或等价端点）：流式工具调用与事件回传（支持长链路、渐进输出）。

**对内接口（Service）**
- `POST /service/tool/exec`：服务统一工具执行入口（Hub 转发调用）。
- `WS /service/tool/ws`（可选）：服务侧流式执行入口（如需要端到端 WS；或改为 Hub 端 WS + Hub->service HTTP streaming）。

### 1.3 传输适配（HTTP/HTTPS/WS/UDS）
- Hub 实现一个“上游连接抽象”：
  - `tcp+http(s)`：`http://127.0.0.1:PORT`
  - `uds+http`：`unix:///abs/path/service.sock`
- 统一转发层对上层路由透明：路由只关心 `service_id` 与其 endpoint 描述，不关心传输细节。

### 1.4 安全模型（服务只信 Hub）
- 用户：Hub 持有 JWT 签名密钥；仅 Hub 解析与校验 JWT。
- 服务：service 启动注册后，Hub 签发 `service_session_token`；服务间调用时由 Hub 统一注入/透传服务身份；下游只校验“请求来自 Hub”与“service token 有效”。
- Surface：Hub 直接签发 surface session 与 capability token；能力边界由 Hub 验证并在转发层强制执行。

### 1.5 数据与 `_share` 库
- 每个 service：拥有独立数据库（命名/路径由 database-service 统一管理）。
- `_share`：由 database-service 管控的共享库：
  - 写：必须带 service 身份；写入行必须属于该 service（强约束）。
  - 读：公共读取，建议至少按 `namespace/category` 分区，避免未来出现“共享库变垃圾场/泄露场”。

---

## 2. 三阶段实施计划（对应你提出的 1/2/3 步）

> 贯穿原则：每阶段都必须给出“可验收”的 DoD；任何拆分都要配套“切流量策略”与“回滚策略”（至少在开发阶段）。

### Phase 1：职责迁移与 Hub 纯粹化（先把“多出来的功能”从 Hub 清走）

**目标**
- Hub 停止承载业务与存储实现，只保留平台内核四件套：Gateway/Security/Supervisor/Routing。
- 全部对外能力切换到统一 `tool` 协议（A 方案），旧接口在最终合并时删除。
- database-service 成为所有 DB 操作的唯一入口。

**主要工作包**
1. 定义并冻结“工具协议 v1”
   - 请求/响应 envelope、错误码、trace_id、超时、幂等键、流式事件格式、权限上下文（user/service/surface）。
2. 建立“服务工具执行入口”规范
   - 所有 service 实现 `/service/tool/exec`（与可选流式入口）。
3. 把旧 REST/WS 改写为工具协议（不做兼容）
   - Chat：把 `projects/threads/history/stream/...` 改为工具调用（只保留必要的会话型 WS 语义，但由工具协议承载）。
   - File/Blob：工具化（读写/列表/删改/Blob Put/Get/SignURL 等）。
   - Database：工具化（Query/Execute/Schema/迁移/共享库读写等）。
   - Surface Manager / Service Manager：工具化（仅列表管理与元数据，不含授权）。
4. Hub 内置 Auth（并删除 auth-service 形态）
   - 把用户注册/登录/登出/JWT 签发等作为 Hub 的 Security 子模块对外提供工具（而不是 auth-service 的 REST）。
5. Hub 移除本地存储/SQLite/Blob/SurfaceFS 业务实现
   - 任何原本通过 `hub/internal/app/sqlite_store.go`、`hub/internal/app/blob_service.go`、`hub/internal/app/storage_services.go`、`hub/internal/app/surface_catalog.go` 实现的能力，必须迁移到对应 service。
   - Hub 内不得再直接打开 DB 文件；Hub 需要的持久化（用户/配置/平台元数据等）必须通过 database-service 以“Hub 身份”访问（或以保留的本地安全文件持久化密钥）。

**关键 DoD（阶段验收）**
- Hub 对外仅保留：
  - 静态资源托管
  - 统一工具入口（HTTP + WS）
  - Supervisor 管理入口（注册/心跳/prepare-start/级联退出/路由管理）
- `hub/` 目录中不再出现：
  - 业务域 SQLite schema、聊天域消息模型、blob/file/db 的本地实现
  - 硬编码 `classifyProxyRoute` 类型的路径路由（可暂留但必须不再承载核心流量，且计划在 Phase 2 删除）
- 任何 DB 写入操作都可追溯到 database-service（从依赖与调用链层面证明）。

**风险与缓解**
- 一次性改协议会导致大面积联调失败：用“按 tool_id 分批替换”的方式推进，但最终合并前删除旧路径。
- Hub 依赖 database-service 的可用性会提升：为 database-service 设计最小启动依赖与自检；Hub 启动阶段需要明确“数据库服务未就绪时哪些能力不可用”。

### Phase 2：Hub 完全重构（拆包、重写核心、实现平台应有能力）

**目标**
- 将 Hub 从“巨型 main.go + 混合业务遗留”重构为清晰可维护的内核。
- 完整实现方案 A：动态 `tool_id` 路由、评分、熔断、审计、可观测闭环。
- 完整实现传输适配：HTTP/HTTPS/WS/UDS 的可配置统一转发。

**建议的 Hub 内部模块拆分（示意）**
- `gateway/`：外部 HTTP/HTTPS/WS server、静态资源、连接管理。
- `security/`：JWT、service token、surface token 的签发/验证；将身份注入请求上下文。
- `supervisor/`：注册、冲突、心跳、prepare-start、级联退出；状态持久化。
- `routing/`：工具清单、绑定表、评分器、策略（manual bind、优选、降级、熔断）。
- `transport/`：UDS/TCP client 适配；统一重试/超时/连接池。
- `observability/`：结构化日志、trace_id、统计上报、审计落盘策略。

**关键 DoD**
- Hub 的路由只以 `tool_id` 驱动；不存在 URL 前缀白名单路由成为主路径。
- `RecordToolCall`（或等价统计）来自真实转发链路，能够驱动路由评分策略生效。
- UDS 与 TCP endpoint 可互换，且不影响上层路由与鉴权逻辑。
- Supervisor 行为具备可验证性：prepare-start/级联退出/心跳均有自动化验证脚本或集成测试覆盖。

### Phase 3：Hub 目录清理与最终精简（删冗余、删遗留、完成最终形态）

**目标**
- 从 `hub/` 目录彻底删除所有遗留、重复、不可达、无人使用的文件与逻辑。
- 使 `hub/` 成为“平台内核”而非“共享业务库”。

**工作项**
- 全量扫描 `hub/`：
  - 无引用文件、仅为兼容保留的 no-op、重复实现（特别是 message/sqlite/legacy helper 一类）。
- 删除与合并：
  - 删除不可达 handler、未使用 internal/app 文件、旧 proxy 分类器、旧协议结构。
- 文档/验收同步：
  - 更新（或新增）面向开发者的“Hub API 与工具协议说明”（建议放在 `doc/`，但本阶段至少在计划中明确产物位置）。

**关键 DoD**
- `hub/` 目录内不包含任何 service 业务域模型（chat、file、db、surface-manager 的业务实现均不在 Hub）。
- Hub 可独立作为平台内核运行，且通过统一工具协议完成核心链路。
- 代码与目录结构可被新人快速理解：模块边界清晰、无“历史遗留副本”。

---

## 3. 里程碑与验收清单（建议）

### 3.1 里程碑（按结果而非按时间）
- M1：工具协议 v1 冻结 + 1~2 个 service 跑通（UDS + HTTP 均可）
- M2：全量能力迁移完成，Hub 不再承载本地业务实现
- M3：Hub 内核重构完成，动态路由评分闭环生效
- M4：Hub 目录清理完成，最终形态稳定

### 3.2 验收（每次合并前必须满足的硬指标）
- 路由：所有功能调用都经过 `tool_id` 路由；不存在旧 REST 路径作为主路径。
- 安全：服务不持有 JWT/DB 密钥；Surface/Service/User 授权全部由 Hub 管控。
- 数据：除 database-service 外无任何 DB 文件访问；`_share` 写入约束强制可证明。
- 传输：同一 service 能切换 UDS/TCP endpoint，且不影响功能与权限判定。
- 生命周期：prepare-start、心跳、级联退出有自动化验证（至少脚本级）。

---

## 4. 待确认事项（实施前需明确）
- 工具协议 v1 是否要求“强类型 schema”（输入/输出 JSON Schema）与版本演进策略。
- 流式工具统一使用 WS 还是允许 HTTP streaming（两者对前端与 Hub 实现复杂度不同）。
- Hub 作为“平台身份”访问 database-service 的鉴权手段：
  - 仅靠 UDS 文件权限 + 回环限制
  - 还是引入平台级 token（不外泄，但需持久化与轮换）

