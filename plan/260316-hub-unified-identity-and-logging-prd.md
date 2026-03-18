# Hub 全局统一身份识别与增强日志系统设计文档 (PRD/Design)

- **文档类型**：产品需求与技术设计文档 (PRD/Design)
- **目标版本**：v1.2 (2026-03-16)
- **核心价值**：达成身份透明感知，消除“匿名”日志，建立可追溯的审计基座。

---

## 1. 现状痛点分析 (As-Is)

目前 Hub 的身份识别采用的是“按需解析”模式：
- **`loggingMux` 中间件**：在记录访问日志时，会为了打日志而临时解析一次 JWT。
- **`/api/debug/log` 接口**：直接打印前端传来的 `body.Content`，完全没有尝试校验调用者身份。
- **后果**：
  1. **审计断链**：开发者在控制台看到 `[PAGE]` 错误时，无法立即判定是哪个用户的页面报错。
  2. **逻辑冗余**：如果后续业务逻辑需要鉴权，每个 Handler 都要重复执行“解析 Cookie -> 校验 JWT”的动作。

---

## 2. 目标设计理念 (To-Be)

引入 **“身份透明注入 (Identity Injection)”** 机制，确立以下原则：
1. **统一提取**：身份识别动作在请求进入 Mux 之前的中间件层一次性完成。
2. **上下文绑定**：将识别出的 `Identity` 对象与 `r.Context()` 绑定，向下游传递。
3. **声明式消费**：业务 Handler 与 Logger 仅需向 Context “声明”所需身份信息，无需关心提取细节。

---

## 3. 技术架构设计

### 3.1 Identity 身份实体结构
在 `hub/internal/app` 中定义标准身份结构：

```go
type Identity struct {
    Type     string // USER, SERVICE, SURFACE, ANONYMOUS
    ID       string // 唯一 ID (如 user_8f2a, ai-doubao)
    Name     string // 展示名称 (如 zhyuzh, Hub-Admin)
    Scope    string // 权限作用域
}
```

### 3.2 统一身份中间件 (IdentityMiddleware)
在 `hub/cmd/hub/main.go` 中重构请求链，加入 `IdentityMiddleware`：

1. **多源探测**：
   - 检查请求头 `X-Hub-Service-Token` (内部服务调用)。
   - 检查 Cookie `kagent_at` / JWT (终端用户调用)。
   - 检查 `Capability-Token` (Surface 插件调用)。
2. **环境注入**：
   ```go
   // 定义 Context Key
   type contextKey string
   const IdentityKey contextKey = "identity"

   // 注入逻辑
   identity := DetectIdentity(r)
   ctx := context.WithValue(r.Context(), IdentityKey, identity)
   next.ServeHTTP(w, r.WithContext(ctx))
   ```

### 3.3 Context-Aware 增强日志 (Unified Logger)
升级 `hub/internal/app/logger.go`，支持从 Context 中提取身份标识：

- **日志格式标准**：
  `[TIMESTAMP] [LEVEL] [TAG] [USERNAME] Content`
- **示例输出**：
  - `[2026-03-16 22:40:00] [INFO] [HUB] [zhyuzh] GET /api/threads`
  - `[2026-03-16 22:40:05] [INFO] [PAGE] [zhyuzh] chat:Sidebar:Selecting first project...`

---

## 4. 关键接口改造计划

### 4.1 `/api/debug/log` (前端上报接口)
- **改造前**：仅打印 `page:module:content`。
- **改造后**：利用中间件注入的 Context，在日志中自动补齐 `[USERNAME]`。
- **收益**：前端任何报错均可精确追溯到具体用户。

### 4.2 `loggingMux` (访问日志中间件)
- **改造前**：手动调用 `extractJWTClaims`。
- **改造后**：直接从 `r.Context()` 获取身份。
- **收益**：性能提升，代码整洁。

---

## 5. 安全与权衡

- **降级策略**：如果请求未携带任何有效令牌，身份标记为 `ANONYMOUS`，日志显示 `[anonymous]`。
- **敏感信息屏蔽**：日志中仅记录 `Name` 或缩短后的 `ID`，严禁记入 Token 原始值。
- **性能开销**：由于 JWT 校验涉及 RSA/HMAC 运算，将其收敛到中间件层“仅执行一次”是性能最优解。

---

## 6. 开发路线图 (Roadmap)

1. **阶段 1 (结构定义)**：在 `pkg/toolproto` 或 `internal/app` 定义 `Identity` 协议模型。
2. **阶段 2 (中间件实现)**：在 Hub 中落地 `IdentityMiddleware` 并替换旧有的 `loggingMux` 解析逻辑。
3. **阶段 3 (日志库升级)**：扩展 `app.Infof` 系列函数，支持接收上下文并打印身份标签。
4. **阶段 4 (全链路回归)**：验证 User、Service 来源请求的日志是否均能正确携带标识。

---

## 7. 完成标准 (DoD)

- [ ] Hub 控制台输出的所有请求日志（无论是网关注入还是前端上报）均包含 `[USERNAME]` 标识。
- [ ] 移除 `main.go` 中重复的 JWT 解析逻辑。
- [ ] `/api/debug/log` 接口在不改动前端参数的情况下，后端能自动通过 Context 关联到用户。

---
**核准**: Antigravity (AI Architect)
**日期**: 2026-03-16 23:31 CST
