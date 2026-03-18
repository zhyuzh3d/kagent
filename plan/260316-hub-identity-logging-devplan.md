# Hub 全局统一身份识别与增强日志 — 开发计划

- **基于 PRD**: `plan/260316-hub-unified-identity-and-logging-prd.md`
- **日期**: 2026-03-16
- **状态**: 待审核

---

## 1. 现状分析（代码级确认）

通过对 Hub 代码的逐文件核验，确认以下事实：

| 问题 | 文件 | 行 | 现象 |
|------|------|-----|------|
| JWT 双重解析 | `main.go` L946, `tool_handler.go` L299 | 两份独立的 `extractJWTClaims` 函数，逻辑完全相同 |
| loggingMux 后置解析 | `main.go` L190-193 | 在 `mux.ServeHTTP` **之后**才调用 `extractJWTClaims`，仅用于日志；每个请求做一次冗余 JWT 校验 |
| debug/log 无身份 | `main.go` L209-236 | 前端上报日志只打 `page:module:content`，完全缺失用户标识 |
| 多 Handler 重复鉴权 | `main.go` L721,736,753... | 每个需要鉴权的 Handler 各自调用 `extractJWTClaims` |
| 代码重复 | `main.go` + `tool_handler.go` | 两个包各自定义了一份完全相同的 `extractJWTClaims` |

---

## 2. 设计方案

### 2.1 Identity 实体（定义在 `hub/internal/app/identity.go`）

```go
type IdentityType string

const (
    IdentityUser      IdentityType = "USER"
    IdentityService   IdentityType = "SERVICE"
    IdentitySurface   IdentityType = "SURFACE"
    IdentityAnonymous IdentityType = "ANONYMOUS"
)

type Identity struct {
    Type IdentityType
    ID   string // user_id / service_id
    Name string // 展示名称（日志用，如 username）
}

// Context key + helper
type contextKey string
const IdentityCtxKey contextKey = "hub.identity"

func IdentityFromContext(ctx context.Context) Identity { ... }
func ContextWithIdentity(ctx context.Context, id Identity) context.Context { ... }
```

### 2.2 IdentityMiddleware（定义在 `hub/internal/app/identity.go`）

在 `main.go` 中将中间件插入 `loggingMux` 之前，使所有后续逻辑（包括 loggingMux 和全部 Handler）都能从 Context 获取身份。

```
HTTP request
  └─→ IdentityMiddleware（一次性 JWT 解析 → Context 注入）
       └─→ loggingMux（从 Context 读取 identity.Name）
            └─→ 各 Handler（从 Context 读取 identity，无需自行解析）
```

探测优先级：
1. Cookie `kagent_token` → JWT → `Identity{Type: USER, ID: user_id, Name: username}`
2. 无有效凭据 → `Identity{Type: ANONYMOUS, ID: "", Name: "anonymous"}`

> **注**: Service Token 和 Surface Token 不通过 HTTP middleware 传入 — 它们在 `tool_handler.go` 内部按 toolproto 协议处理，保持不变。

### 2.3 Logger 增强（修改 `hub/internal/app/logger.go`）

新增 Context-aware 日志函数：

```go
func InfofCtx(ctx context.Context, format string, args ...any) { ... }
func InfofCtxTag(ctx context.Context, tag string, format string, args ...any) { ... }
```

格式变为：
```
[2026-03-16 22:40:00] [INFO] [HUB] [zhyuzh] GET /api/threads
[2026-03-16 22:40:05] [INFO] [PAGE] [zhyuzh] chat:status_update:Selecting...
```

当 Context 中无 Identity 时 fallback 到无 `[USERNAME]` 格式（向后兼容）。

### 2.4 接口改造

#### `/api/debug/log`
从 `r.Context()` 取 Identity，日志自动补齐 `[Username]`。前端无需改动。

#### `loggingMux`
- 移除内部的 `extractJWTClaims` 调用。
- 改为从 `r.Context()` 读取 `Identity.Name`。

#### 各鉴权 Handler
- 可继续使用 `extractJWTClaims` 做精细鉴权判断（如 401 响应）。
- 但身份标识已可从 Context 获取，避免冗余解析。

#### `tool_handler.go`
- 删除其内部的 `extractJWTClaims` 函数副本。
- 改为引用 `main.go` 或 `app` 包中的统一实现。

---

## 3. 变更文件清单

### 3.1 核心变更

#### [NEW] `hub/internal/app/identity.go`
- 定义 `Identity` 结构、`IdentityType` 常量
- 定义 `IdentityCtxKey`、`IdentityFromContext`、`ContextWithIdentity`
- 实现 `IdentityMiddleware(authService *AuthService) func(http.Handler) http.Handler`

#### [MODIFY] `hub/internal/app/logger.go`
- 新增 `InfofCtx`、`InfofCtxTag`、`WarnfCtx`、`ErrorfCtx` 系列函数
- 内部 `formatLog` 新增 `identity` 参数支持

#### [MODIFY] `hub/cmd/hub/main.go`
- 在 `loggingMux` 外层包裹 `IdentityMiddleware`
- `loggingMux` 内移除 `extractJWTClaims` 调用，改从 Context 读取
- `/api/debug/log` Handler 从 Context 读取身份并注入日志
- 将 `extractJWTClaims` 函数移至 `app` 包，消除 `main.go` 中的本地定义

#### [MODIFY] `hub/internal/gateway/tool_handler.go`
- 删除本地 `extractJWTClaims` 函数
- 改为调用 `app.ExtractJWTClaims`

### 3.2 测试

#### [NEW] `hub/internal/app/identity_test.go`
- 测试 IdentityMiddleware 的三个分支：有效 JWT → USER、无效/过期 JWT → ANONYMOUS、无 Cookie → ANONYMOUS
- 测试 Context 注入与读取的幂等性

---

## 4. 实施步骤

| 阶段 | 步骤 | 说明 |
|------|------|------|
| 1 | 创建 `identity.go` | Identity 结构 + Context helper + Middleware |
| 2 | 修改 `logger.go` | 新增 Context-aware 日志 API |
| 3 | 修改 `main.go` | 中间件接入 + loggingMux 改用 Context + debug/log 增强 |
| 4 | 修改 `tool_handler.go` | 统一 `extractJWTClaims` 实现 |
| 5 | 创建 `identity_test.go` | 自动化测试 |
| 6 | 验证 | `go build` + `go test` + 部署验证 |

---

## 5. 验证计划

### 5.1 自动化测试

```bash
# 从项目根目录执行：
cd /Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent && go test ./hub/...
```

新增 `identity_test.go` 覆盖：
- 有效 JWT Cookie → Context 中 Identity.Type == USER，Name == username
- 无 Cookie → Context 中 Identity.Type == ANONYMOUS
- 无效 Cookie → Context 中 Identity.Type == ANONYMOUS

### 5.2 编译验证

```bash
cd /Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent && go build ./hub/...
```

### 5.3 运行时验证（部署后观察日志）

部署后观察控制台输出，确认：
1. 登录后访问 `/api/auth/me` → 访问日志显示 `[zhyuzh]-GET /api/auth/me [200] ...`
2. 前端发送 debug/log → 日志显示 `[INFO] [PAGE] [zhyuzh] chat:status_update:...`
3. 未登录访问 `/version` → 日志显示 `[anonymous]-...`

---

## 6. 风险与降级

- **性能**：JWT 解析从"每请求多次"变为"每请求一次"，性能只升不降。
- **兼容性**：不改变任何 API 接口签名或前端调用方式。
- **降级**：无有效 Token 时自动标记为 `ANONYMOUS`，不会阻断请求。
