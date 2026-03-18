# Account Service 重构开发计划（Tool-First + SSO + 公私钥）

> **文档类型**：开发计划（devplan）  
> **时间**：2026-03-18 20:37 CST  
> **范围**：`hub/`、`services/auth/` -> `services/account/`、`webui/page/account/`、`webui/page/chat/`、`config/services.json`、`hub/config/services.json`、`pkg/toolproto/`（若需协议扩展）、相关文档引用  
> **信息来源（可核验）**：  
> - As-Is 账号接口：Hub 内建 `/api/auth/*`（`hub/internal/gateway/system_handler.go` + `hub/cmd/hub/main.go`），JWT 逻辑（`hub/internal/app/auth.go` + `hub/internal/app/identity.go`）  
> - As-Is 前端依赖：`webui/page/account/index.html` 与 `webui/page/chat/index.html` 直接调用 `/api/auth/*`  
> - As-Is 服务治理：`config/services.json` 未包含 `auth`（`services/auth` 未纳入 lifecycle）  
> - Tool 协议形态：`pkg/toolproto/v1.go`（As-Is 的 `CallResponse` 无 Effects；To-Be 需要增加顶层 `effects` 以承载 cookie/header 操作）

---

## 0. 已达成一致的结论（本轮约束）

1. **目标必须切到 Tool-First**：账号管理从 Hub 迁出到 `services/account`，且 `account` 必须是标准 tool service，通过 Hub 的 `/api/tool/call` 对前端提供能力，不能例外。
2. **仅提供单点登录（SSO）**：同账号在新设备/新浏览器登录后，旧设备 JWT 必须“立即失效”（下一次请求即不可用）。
3. **公私钥签名**：account 持有私钥签发 token；Hub 仅持有公钥验签，不读取私钥。
4. **不做过度设计**：不引入 refresh token、session family、多端并存等复杂形态。
5. **不考虑其它 service 的旧模式**：`file/database/surface-manager` 的历史认证形态不作为本轮阻塞项；本轮只保证 account + hub + webui 闭环正确。

---

## 1. 现状问题（As-Is）

1. **Hub 仍在内建账号业务接口**：`/api/auth/register|login|logout|me` 在 Hub 内实现（`hub/internal/gateway/system_handler.go`），这与“账号能力应作为 service 被 Hub 治理”的目标冲突。
2. **WebUI 直接依赖 `/api/auth/*`**：`webui/page/account/index.html`、`webui/page/chat/index.html` 直接调用这些 endpoint，导致账号体系很难被纳入 tool 平面统一治理。
3. **`services/auth` 是历史遗留且不在治理链路**：`config/services.json`/`hub/config/services.json` 未纳入 `auth`；`services/auth/cmd/auth/main.go` 也不符合本仓库 tool service 形态（未实现 `/service/tool/exec` 作为主入口）。
4. **Tool 协议不直接支持 Set-Cookie**：`pkg/toolproto/v1.go` 的 `CallResponse` 没有 cookie 字段，因此“登录后写 cookie”需要明确落点与约定，避免出现“登录成功但浏览器未持久化登录态”。
   - To-Be：增加 **顶层 `effects`**，由 Hub 统一执行，且按 `service_id` 做强隔离（无需白名单/黑名单特判）。

---

## 2. 目标架构（To-Be）

### 2.1 分层职责

- **Hub**
  - 唯一入口与治理边界：路由、审计、生命周期治理
  - tool 网关：`POST /api/tool/call`（统一承接前端调用）
  - 身份解析：验签 + SSO 会话校验，然后注入 caller identity（`X-Caller-*`/Context）
  - Effects 执行：把 service 返回的 `effects`（set_cookie/set_header）转为真实 HTTP 响应头，并按 `service_id` 强隔离

- **Account Service（标准 tool service）**
  - 账号注册、登录、退出、改密
  - token 签发（私钥签名）与 SSO 会话状态维护（每用户只有 1 个 active session）
  - 以 `account.*` tool 形式对外提供能力，入口必须为 `/service/tool/exec`

- **Page（`page/account` + `page/chat`）**
  - 只做 UI 与交互：通过 tool 网关调用 `account.*` tools，不再直接 `fetch('/api/auth/*')`
  - 401 收敛：统一跳转 `/page/account/?redirect=...`

### 2.2 主链路（强约束）

`Page -> Hub (/api/tool/call) -> Account (/service/tool/exec)`

不允许出现：

- `Page` 直连 `Account`
- `Account` 对前端暴露 page-facing `/api/account/*` 作为主入口

---

## 3. Token（公私钥）与 SSO 立即失效设计

### 3.1 Token 结构（最小字段）

Token（JWT-like）payload（JSON）至少包含：

- `user_id`：用户唯一标识
- `username`：展示名（可选，但建议保留）
- `sid`：session_id（SSO 核心字段）
- `iat_ms`：签发时间
- `exp_ms`：过期时间（可取 30 天；“踢下线”不依赖过期）
- `kid`：签名 key id（用于公钥轮换）

token 格式建议保持为：`<base64url(payload_json)>.<base64url(signature)>`  
签名算法建议使用 Ed25519（或同等级别非对称签名算法）。

### 3.2 公钥分发与轮换（不改 Supervisor 协议的可落地方案）

由于 `SupervisorRegisterRequest` 不承载公钥字段，本轮采用“工具自描述拉取”：

1. account 提供内部工具：`account.system.keys.get`  
   - 输出：`[{kid, alg, public_key}]`
2. Hub 在 account ready 后拉取并缓存 keyring（kid -> pubkey）
3. Hub 验签时按 `kid` 选取公钥
4. 轮换策略：
   - account 可并行保留新旧 `kid`
   - 新签发 token 使用新 `kid`
   - Hub keyring 同步后即可同时验签新旧 token
   - 旧 key 下线窗口至少覆盖旧 token 的最长 `exp_ms`

### 3.3 SSO 立即失效（单会话）的最小状态机

account 在其存储域内维护每个用户的 **唯一 active `sid`**：

- `register`：创建用户并生成 `sid`，签发带 `sid` 的 token
- `login`：生成新 `sid` 覆盖旧 `sid`，签发带新 `sid` 的 token（实现“新设备登录踢掉旧设备”）
- `logout`：将 `sid` 置空（或写入无效值），使 token 校验失败
- `password_change`：更新密码并强制生成新 `sid`（旧 token 立即失效）

### 3.4 Hub 侧的验签与 SSO 校验（避免递归调用）

约束：Hub 的身份解析发生在处理 `/api/tool/call` 之前，不能在中间件里“再走一次 `/api/tool/call`”去问 account，否则会递归。

推荐落地：

1. **验签与解析在 Hub 本地完成**
   - 从 cookie `svc.account.token` 取 token
   - 解码 payload，按 `kid` 从 keyring 取公钥验签
2. **SSO 校验使用 Hub 自己维护的 `user_id -> sid` 表**
   - Hub 在处理 `account.auth.register/login/logout/password_change` 的成功结果时同步更新该表
   - 校验规则：`token.sid == hub_sso[user_id]`，否则 401
3. **Hub 重启恢复**
   - Hub 启动后，account ready 时，Hub 通过 Hub->Service 内部调用（直接调用 account 的 `/service/tool/exec`，不经 `/api/tool/call`）执行 `account.session.dump_active`，恢复 `user_id -> sid`
   - 若恢复失败：Hub 对需要身份的请求返回 401，并输出明确诊断日志（而不是 silent accept）

这套机制能保证：新设备 login 时 sid 立刻被覆盖，旧 token 下一次请求立即失败。

---

## 4. Tool 设计（account 必须严格遵循 tool 管理机制）

### 4.1 Tool 列表（建议）

对前端开放（可经 Hub 网关调用）：

- `account.auth.register`（匿名允许）
- `account.auth.login`（匿名允许）
- `account.auth.logout`（需要登录）
- `account.auth.me`（需要登录）
- `account.auth.password_change`（需要登录）

仅供 Hub 内部编排调用（不对前端开放）：

- `account.system.keys.get`
- `account.session.dump_active`

### 4.2 Effects（顶层字段，统一 cookie/header 操作机制）

目标：service 不直接触达浏览器，但可以通过 **CallResponse 顶层 `effects`** 声明“希望 Hub 在对浏览器响应时执行的副作用”，Hub 以统一规则执行并完成 service 之间隔离。

#### 4.2.1 协议形态（To-Be）

在 `pkg/toolproto/v1.go` 的 `CallResponse` 增加：

- `effects`（顶层，可选）：
  - `set_cookies[]`
  - `set_headers[]`

约束：**effects 放在顶层，不塞进 result**，使 Hub 可以通用处理 effects 而不解析业务 payload。

补充：`effects` 作为 **JSON body 的一部分**随 `CallResponse` 返回；Hub 只需要在 `/api/tool/call` 的响应路径解析一次并在写回浏览器前执行，不需要对“每个普通路由请求”做额外解析。

#### 4.2.2 Cookie 隔离规则（无白名单/黑名单）

service 返回的 set_cookie 只允许提供“短 name key”，例如 `name="token"`，**不得**提供完整 cookie 名。

Hub 将其补全为：

- 完整 cookie 名：`svc.<service_id>.<name>`
- 例如 account 写入登录态：`svc.account.token`

并由 Hub 统一收敛 cookie 属性（建议默认值）：

- `Path=/`（避免同名不同 Path 的多 cookie 混淆）
- `SameSite=Strict`
- `HttpOnly=true`
- `Secure` 按部署形态决定（本地 http 可为 false；https 必须为 true）
- `Domain` 一律不允许（host-only）
- `MaxAge` 限制在合理范围内（例如不超过 30 天）

删除 cookie：`max_age_sec = -1`（或等价字段）。

#### 4.2.3 Header 隔离规则（可选）

同样采用“短 name key”，Hub 补全为：

- `X-Svc-<service_id>-<name>`（例如 `X-Svc-account-Session`）

Hub 自己写的头统一用 `X-Hub-<name>`，与 service 空间自然隔离。

#### 4.2.4 account 的具体约定（不泄露 token 到 JSON）

- `account.auth.login/register`：
  - `effects.set_cookies += {name:"token", value:"<signed_token>", max_age_sec:<...>}`
  - `result` 返回 `user_id/username/sid/exp_ms`（可选；Hub 也可从 token payload 中解析，但保留字段便于诊断与 smoke）
- `account.auth.logout/password_change`：
  - `effects.set_cookies += {name:"token", value:"", max_age_sec:-1}`

Hub 收到 effect 后写入/清除 `svc.account.token`，并据 `user_id/sid` 更新 `hub_sso`。

#### 4.2.5 Hub 解析与性能（建议实现）

- Hub 本来就必须解析 `CallResponse` 才能把 `ok/result/error` 返回给前端；增加 `effects` 的额外成本仅是解码少量字段与设置响应头。
- 为避免额外分配，Hub 可以把 `result` 解为 `json.RawMessage`（或延迟解码），只把 `effects` 解为结构体；仅当 `effects` 非空时执行 cookie/header 写入逻辑。

### 4.3 匿名访问与权限（去除“白名单式特判”）

为避免在 Hub 中硬编码匿名白名单，本轮建议把“caller 要求”作为 **tool 描述的一部分**（由 service 在注册时上报，Hub 以统一规则执行），例如：

- `allowed_caller_types`：`["anonymous"] | ["user"] | ["service"] ...`

account 的 `register/login` 声明允许 anonymous，其它 `account.*` 声明仅允许 user。

---

## 5. 开发实施路径（Phases）

### Phase 1：新建 `services/account`（标准 tool service）

任务：

- 新建 `services/account/cmd/account/main.go`，实现标准 service 入口与注册/心跳
- 新建 `services/account/manifest.json`，声明 `account.*` tools
- 实现 account 最小存储：
  - 用户表（username + password_hash）
  - 当前 sid（SSO）
  - keyring（私钥持久化 + 公钥导出）
- 实现 `account.auth.register/login/logout/me/password_change`
- 实现内部工具：
  - `account.system.keys.get`
  - `account.session.dump_active`

验收：

- `account` 可被 Hub lifecycle 拉起并注册
- 经 Hub `/api/tool/call` 能完成注册/登录/登出/我是谁 的闭环
- 同一用户二次登录会覆盖 sid

### Phase 2：Hub 接入（tool 网关 + 身份解析 + SSO）

任务：

- 将 `account` 加入 `config/services.json` 与 `hub/config/services.json`
- tool 网关基于 tool 描述执行统一权限策略（例如 `allowed_caller_types`），避免在 Hub 内做“白名单式特判”
- Hub 在 account ready 后拉取 keyring：调用 `account.system.keys.get`
- Hub 增加 `hub_sso (user_id -> sid)` 并实现恢复：调用 `account.session.dump_active`
- Hub 身份解析迁移：
  - 从 Hub 内建 HMAC secret 迁移为 account 公钥验签
  - 加入 SSO sid 校验
- Hub tool 网关实现登录态落盘：
  - 统一解析 `effects` 并执行（set_cookie/set_header）
  - 按 `service_id` 自动补全 `svc.<service_id>.*` 的 cookie 名称，形成天然隔离
  - account 产生的登录态最终落到 `svc.account.token`，Hub 仅执行写入并负责验签/SSO 校验

验收：

- 前端只走 tool 网关即可完成账号闭环
- 新设备登录后旧设备 token 下一次请求立即 401（SSO 立即失效）

### Phase 3：WebUI 全切 Tool

任务：

- `webui/page/account/index.html`：将 `/api/auth/*` 调用改为 tool 调用 `account.auth.*`
- `webui/page/chat/index.html`：鉴权检查与 logout 全切 tool 调用；401 统一跳转 `/page/account`

验收：

- 全仓 `rg "/api/auth/" webui/page` 无残留
- 账号入口与守卫稳定

### Phase 4：清理历史实现

任务：

- 删除 `services/auth/` 全目录
- 删除 Hub 内建 `/api/auth/*` handler 与相关依赖（完成迁移后再删）

验收：

- 仓库不再存在 `services/auth`
- Hub 不再暴露 `/api/auth/*`（或已不再被任何页面引用）

### Phase 5：Smoke 同步改造与回归

任务（更新 `hub/internal/app/smoke.go`）：

- 用 `/api/tool/call` 调用 `account.auth.register/login/me`（通过 effects 从响应头获取/更新 `svc.account.token`）
- SSO 场景：
  - login1：读取响应的 `Set-Cookie` 得到 `svc.account.token=token1`
  - login2：读取响应的 `Set-Cookie` 得到 `svc.account.token=token2`（覆盖 token1）
  - 携带 `Cookie: svc.account.token=token1` 调用 `account.auth.me`（或任意受保护 tool）必须 401

验收：

- `go test ./...` 或等价验证通过
- smoke 覆盖注册/登录/我是谁/SSO 立即失效

---

## 6. 文件级重构清单

### 6.1 新增

- `services/account/cmd/account/main.go`
- `services/account/internal/app/*`（auth/session/store/keyring 等）
- `services/account/manifest.json`
- `services/account/config/*`

### 6.2 迁移或重写

- Hub：
  - `hub/internal/app/identity.go`（验签逻辑迁移 + SSO 校验）
  - `hub/internal/gateway/tool_handler.go`（Effects 顶层处理 + cookie/header 隔离补全 + 更新 sid + tool 权限策略）
  - `hub/internal/app/smoke.go`（走 tool 验证）
- 配置：
  - `config/services.json`
  - `hub/config/services.json`
- WebUI：
  - `webui/page/account/index.html`
  - `webui/page/chat/index.html`

### 6.3 删除

- `services/auth/`（全量删除）
- Hub 内建 `/api/auth/*`（完成迁移后删除）

---

## 7. 风险与控制

风险：

1. **Effects 隔离规则不严格**：导致 service 之间可以互相覆盖 cookie/header。
2. **Effects 与 Hub 执行策略不一致**：导致“登录成功但 cookie 未落盘”。
3. **Hub 重启后 SSO 表未恢复**：导致错误 accept/reject token。
4. **kid/keyring 未同步**：验签失败导致全站 401。

控制：

- Hub 统一补全 `svc.<service_id>.*`，禁止 service 自指定完整 cookie 名；并拒绝包含 `.`/空白/控制字符的 name key
- 把 effects schema（set_cookie/set_header）作为硬契约写进协议与测试
- Hub 启动编排：account ready 后先拉 keys + dump sid，再对外提供受保护能力
- kid 未命中时明确拒绝并打印诊断日志（避免静默错误）

---

## 8. 验收标准（Definition of Done）

1. 前端账号相关动作全部通过 `/api/tool/call` 调用 `account.*` tools（不再调用 `/api/auth/*`）。
2. 注册/登录后 cookie `svc.account.token` 生效，页面刷新不要求重复登录。
3. 新设备/新浏览器登录后，旧设备 token 下一次请求立即 401（SSO 立即失效）。
4. account 作为标准 tool service 纳入 lifecycle 管理，且所有账号能力均通过 tool 平面暴露。
5. `services/auth/` 被完全删除；Hub 内建 `/api/auth/*` 被移除或已无引用。
