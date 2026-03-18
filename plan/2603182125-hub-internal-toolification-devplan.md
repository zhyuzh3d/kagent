# Hub 内部功能工具化与架构收归开发计划 (260318-hub-toolification-devplan)

## 1. 背景与目标

### 1.1 背景
当前 Hub 项目正处于架构升级阶段，原有的应用层账号管理（登录/注册）正剥离至独立的 `account-service`。Hub 正在演进为纯粹的“逻辑网关”与“治理中心”。目前 Hub 内部仍散落着大量通过原生 REST API 暴露的管理（Admin）、治理（Supervisor/Governance）和系统（System）接口。

### 1.2 目标
*   **API 归一化**：彻底取消散落在 Hub 中的原生 API 路径（`/api/admin/*`, `/api/service/*` 等）。
*   **全量工具化 (Logic > Physics)**：将所有 Hub 内部功能收敛为以 `hub.*` 为命名空间的逻辑工具，统一通过 `POST /api/tool/call` 和 `GET /api/tool/ws` 调度。
*   **统一治理层**：实现 Hub 内部功能的统一鉴权、审计记录、频率控制和副作用（Effects）处理。
*   **适配新身份机制**：全面接入 `account-service` 下发的用户公钥同步机制，实现 Hub 的“无状态验证”与“身份平面下沉”。

---

## 2. 核心技术设计

### 2.1 Hub 作为“虚拟服务（Virtual Service）”
Hub 将不再被视为特殊的路由容器，而是在逻辑上注册为一个 Service 实例。
*   **ServiceID**: `hub`
*   **InstanceID**: `builtin-hub`
*   **自描述清单 (Hub Manifest)**：在内存中维护一个静态的 `ServiceManifest`，声明 Hub 提供的所有能力：
    *   `hub.admin.*`（运维管理族）
    *   `hub.governance.*`（服务治理族）
    *   `hub.system.*`（平台系统族）

### 2.2 内部分发器 (Internal Dispatcher) 机制
不再使用 HTTP 反向代理调用自身，而是通过内存函数分发实现：
1.  **ToolHandler 改造**：在 `routing.Engine.Select` 选中 `ServiceID == "hub"` 时，直接拦截并转交 `InternalDispatcher`。
2.  **函数签名约定**：
    *   **原子调度**：`type InternalToolFunc func(ctx context.Context, req toolproto.CallRequest) (toolproto.CallResponse, error)`
    *   **流式调度**：`type InternalWSToolFunc func(ctx context.Context, conn *websocket.Conn, req toolproto.CallRequest) error`

### 2.3 身份平面下沉 (Identity Lowering)
Hub 作为系统唯一入口，在 Tool 调度的最前端完成身份解析：
*   **JWT 校验**：基于从 `account-service` 同步的公钥（RS256/EdDSA）进行验证。
*   **Context 注入**：将解析出的 `Identity`（包含 UserID, Role, CallerType）注入 `r.Context()`。
*   **权限切片 (Zero Trust)**：内部工具函数不再执行复杂的解析逻辑，直接通过 `IdentityFromContext(ctx)` 获取调用者信息并检查权限位。

---

## 3. 详细命名空间与命名规划

| 类别 | Tool ID | 对应原逻辑 | 访问限制 (RABC/Context) |
| :--- | :--- | :--- | :--- |
| **Admin** | `hub.admin.services.list` | `/api/admin/services` | Admin Only |
| | `hub.admin.routes.get` | `/api/admin/services/routes` | Admin Only |
| | `hub.admin.routes.bind` | `/api/admin/services/bind` | Admin Only |
| | `hub.admin.audits.list` | `/api/admin/services/audits` | Admin Only |
| | `hub.admin.tool.probe` | `/api/admin/services/tool-probe` | Admin Only |
| **Governance** | `hub.governance.service.register` | `/api/service/register` | Service Secret Required |
| | `hub.governance.service.heartbeat` | `/api/service/heartbeat` | Service Auth Required |
| | `hub.governance.service.drain` | `/api/service/drain` | System/Service |
| **System** | `hub.system.version.get` | `/version` | Public |
| | `hub.system.config.get` | `/api/config` | Admin |
| | `hub.system.smoke.test` | `/api/system/smoke-test` | Loopback Only |
| | `hub.system.report_log` | `/api/debug/log` | Dynamic (User/Svc) |

---

## 4. 实施阶段计划

### 第一阶段：基础设施建设
1.  **Hub Manifest 定义**：编写 `hub/internal/gateway/hub_manifest.go`。
2.  **Internal Registry 实现**：在 `ToolHandler` 中增加内存注册表与分发逻辑。
3.  **身份平面改造**：将 `resolveCaller` 的执行时机前移到路由选择之前，并完成 Context 注入。

### 第二阶段：治理与管理功能迁移
1.  **重构 Admin 逻辑**：将 `admin_handler.go` 逻辑重构为 `InternalToolFunc`，注册至 `hub.admin`。
2.  **重构 Governance 逻辑**：将 `supervisor/handler.go` 的注册/心跳逻辑重构为 `InternalToolFunc`，注册至 `hub.governance`。
    *   *注：此步骤需同步更新 Service 端 SDK（pkg/hubsvc），改为调用工具端口。*

### 第三阶段：系统接口与 Websocket 适配
1.  **系统接口收口**：迁移版本、配置管理及烟雾测试逻辑。
2.  **流式工具实现**：实现管理日志订阅、性能监控等基于 WS 的内建工具。

### 第四阶段：端点清理与终态收拢
1.  **清理 main.go**：移除所有不再需要的 API 路由。
2.  **强制性验证**：所有未定义在 Manifest 中的 Hub 接口请求，统一返回 `TOOL_NOT_FOUND`。
3.  **DoD 验证**：通过 `hub.admin.tool.list` 能查看到 Hub 自身的所有工具，且调用均经过审计。

---

## 5. 风险与对策

*   **性能考量**：虽然内建工具路径比原生 REST 多了一层路由解析，但由于是内存直接调用，省去了 HTTP 序列化与传输损耗，总体性能应持平或更优。
*   **服务引导风险**：Service 注册是系统启动的关键路径。需确保 `hub.governance` 具备极高的稳定性，并支持在 Hub 尚未完全 Ready 时的特殊处理逻辑。
*   **账号服务同步**：Hub 对 `account-service` 的公钥同步存在延迟风险。需实现 `ForceRefresh` 机制或 WebSocket 实时推送，确保会话状态一致。

---

**文档维护状态**：
*   **版本**：v1.0.0
*   **作者**：Antigravity
*   **更新时间**：2026-03-18 21:25 CST
*   **状态**：等待执行规划确认
