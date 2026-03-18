# Hub-Service 简化身份识别（基于启动期 `.service_secret` 注入 + 运行期字符串匹配）

- 文档类型：开发计划（DevPlan）
- 创建时间：2026-03-18 20:35 CST
- 修订时间：2026-03-18 20:55 CST
- 范围：仅覆盖 **Hub <-> Service** 的最小身份识别；不约束浏览器请求；不考虑 Hub 重启（约定 Hub 重启即全部 Service 重启）。
- 依据（可核验）：
  - Hub 现有 service 注册入口：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go`（`/api/service/register` 为 loopback 限制）
  - 现有 `.service_secret` 与 `X-Hub-Service-Token`（HMAC）机制：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/hub_platform.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/hubsvc/session.go`
  - 现有 deploy 同步 service secret 到 `services/<svc>/run/.service_secret`：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh`

---

## 1. 背景与问题定义

目标是把 **Hub 与每个 Service 的“身份识别”** 作为系统基本要求：

- Service 必须能确认：收到的“需要被当作 Hub 内部调用”的请求，确实来自“最初启动我的那个 Hub”。
- Hub 必须能确认：收到的“需要被当作 Service 内部调用”的请求，确实来自“最初启动的那个 Service 实例”。

同时满足用户硬性约束：

- 不考虑历史兼容性：直接设计并切换到一套全新的 Hub <-> Service 身份互信机制（不保证与既有 `X-Hub-Service-Token`/HMAC 方案兼容）。
- 不约束浏览器请求（Hub 仍需对本地 Web 浏览器提供服务）。
- 不考虑 Hub 重启场景（约定 Hub 重启时 Service 也全部重启）。
- 身份校验必须 **绝对简单**、高频路径只做 **字符串匹配**（避免 HMAC/加解密/复杂解析）。
- 秘钥只通过 “Service 启动期的 `.service_secret` 文件” 注入；运行期不再通过其它通路分发秘钥。
- 秘钥落盘只用于启动注入；Service 完成注册后应清空/删除 `.service_secret`，降低磁盘驻留泄漏风险。
- “内存中秘钥泄露”不纳入威胁模型（不做对抗）。

> 备注：该方案的安全边界是“防止非预期来源伪造 Hub/Service 身份参与内部通信”，不是对抗同机高权限攻击者或内存窃取。

### 1.1 “全部请求”的精确定义（必须明确）

本计划中 “HS 互信机制影响 Hub 与 Service 互相的全部请求” 的含义是：

- **Hub -> Service**：Hub 通过反代/网关转发到 Service 的所有 HTTP 请求（包含普通 HTTP、SSE、WebSocket Upgrade）都必须注入 `X-Hub-Auth`，并且 Service 侧对所有受 Hub 调用的路由统一执行字符串匹配校验。
- **Service -> Hub**：Service 主动请求 Hub 的所有 “内部服务入口” 都必须携带 `X-Service-Auth`，并且 Hub 侧对这些入口统一执行 `RemoteAddr loopback` 前置拒绝 + 字符串匹配校验。

非本计划范围（不属于“Hub 与 Service 互相的请求”）：

- 浏览器/用户对 Hub 18080 的网页与 API 请求（仍按现有 JWT/匿名等逻辑处理；本方案不得把浏览器请求提升为 SERVICE/HUB 身份）。

---

## 2. 设计目标（DoD）

### 2.1 必须达成

1. 每个 Service 都拥有一组 **互相独立、随机、无关** 的身份秘钥（per-service）。
2. Hub->Service 调用：Service 在高频入口只做一次字符串匹配即可完成身份校验（失败直接拒绝）。
3. Service->Hub 调用：Hub 能在高频入口只做一次字符串匹配即可完成身份校验（失败直接拒绝），并把识别到的 Service 作为日志 `source`（或 Identity）输出。
4. `.service_secret` 文件仅用于启动注入：
   - Hub 写入到 `services/<service_id>/run/.service_secret`
   - Service 启动后读取到内存，并在注册成功后清空/删除该文件
5. **互信唯一可信源**：Hub/Service 双方对于“对端是否为 Hub/Service 身份”的判断，唯一可信来源是本方案定义的 header + token 字符串匹配结果；不得因为 `RemoteAddr`、`Endpoint`、端口号、或其它可伪造字段而把请求提升为 `SERVICE`/`HUB` 身份。
6. **防御性前置条件**：对 Hub 的 “Service 内部入口”（如 `/api/service/register`、`/api/service/heartbeat` 等）必须先满足 `RemoteAddr` 为 loopback（防御性拒绝），再进行 token 字符串匹配；loopback 只用于 **拒绝**，不得用于 **提升身份**。
7. 不依赖 Hub 的持久化 secret；不需要在 `data/` 下保留长期 `.service_secret`（本计划将原 HMAC 机制视为直接废弃/下线，不做兼容层）。
8. **强制头部清洗/覆盖**：Hub 对外接收浏览器请求时，必须确保“客户端自带的内部认证 header”不能透传并影响内部互信判定；Hub 在转发到 Service 前必须删除同名 header 并强制覆盖为 Hub 注入值（详见 §5.3）。

### 2.2 明确不做（Non-goals）

- 不提供抗重放、抗中间人、抗同机恶意进程读取文件等能力（除“尽快删除磁盘文件、0600 权限”外）。
- 不提供 Hub 重启后继续信任旧 Service 的能力（约定重启即全量重启）。
- 不试图把浏览器请求纳入“可信身份”（浏览器请求按现有用户认证/匿名处理，不参与本方案）。
- 不提供旧机制兼容：不保留 `X-Hub-Service-Token` 的兼容验签或双栈过渡逻辑。

---

## 3. 核心概念与术语

- **S2H Token**：Service->Hub 身份秘钥（仅 Hub 与该 Service 共享）。
- **H2S Token**：Hub->Service 身份秘钥（仅 Hub 与该 Service 共享）。
- **Bootstrap Secret File**：启动期 `.service_secret` 文件（只用于把 `S2H Token` / `H2S Token` / Hub 注册地址注入给 Service）。

> 两把 token 的目的：让两个方向的校验都能是“字符串匹配”，且避免一个方向的 token 被误用于另一个方向。

---

## 4. `.service_secret` 文件规范（启动期注入）

### 4.1 路径约定

- Hub 写入：`services/<service_id>/run/.service_secret`
- Service 读取：从自身 `run/.service_secret` 读取一次并载入内存
- Service 注册成功后：清空或删除自身 `run/.service_secret`

> 与当前仓库的运行目录约定保持一致（`services/<svc>/run/` 已用于可执行文件与运行清单）。

### 4.2 文件权限

- Hub 写入时强制 `0600`
- 内容尽量短小，避免在日志中打印

### 4.3 文件内容格式（极简 KV，一行一个）

```
SERVICE_ID=<service_id>
INSTANCE_ID=<instance_id>
HUB_REGISTER_URL=http://127.0.0.1:18080/api/service/register
S2H_TOKEN=<random_string>
H2S_TOKEN=<random_string>
ISSUED_AT_MS=<unix_milli>
EXPIRES_AT_MS=<unix_milli>
```

约束：

- `S2H_TOKEN`、`H2S_TOKEN` 必须随机、互不相同、跨 service 不相关。
- 推荐生成长度：至少 32 bytes 随机数编码为 `base64url` 或 `hex`（不影响运行期性能；运行期只是字符串 `==`）。
- `EXPIRES_AT_MS` 用于限制 “文件在磁盘上存在的窗口”，超时视为无效并触发重启/失败策略。

---

## 5. 运行期通信协议（仅字符串匹配）

### 5.1 Header 约定

#### Service -> Hub（所有需要被 Hub 识别为 Service 身份的请求）

- `X-Service-Id: <service_id>`
- `X-Service-Instance-Id: <instance_id>`
- `X-Service-Auth: <S2H_TOKEN>`

Hub 校验规则（高频路径）：

1. 前置：`RemoteAddr` 必须为 loopback（否则直接拒绝；该条件不用于提升身份）
2. 读取 `X-Service-Id`，定位该 service 的期望 `S2H_TOKEN`
3. 直接做 `token == expected` 字符串匹配
4. 可选：再做一次 `instance_id` 匹配（同样是字符串匹配）

校验成功后：

- Hub 将请求 Identity 识别为 `SERVICE/<service_id>`，用于日志 `source` 字段、审计字段等（替代或并行于现有 `X-Hub-Service-Token` 逻辑）。

#### Hub -> Service（所有需要被 Service 识别为 Hub 内部调用的请求）

- `X-Hub-Service-Id: <service_id>`（目标 service）
- `X-Hub-Service-Instance-Id: <instance_id>`（目标实例）
- `X-Hub-Auth: <H2S_TOKEN>`

Service 校验规则（高频路径）：

1. 从内存读取本 service 的 `H2S_TOKEN`
2. 直接做 `token == expected` 字符串匹配
3. 可选：校验 `X-Hub-Service-Id` 与自身一致（字符串匹配）

### 5.2 Token 的使用边界

- `X-Service-Auth` 只用于 Service->Hub 身份识别，禁止用于 Hub->Service
- `X-Hub-Auth` 只用于 Hub->Service 身份识别，禁止用于 Service->Hub

### 5.3 Header 清洗与覆盖（实现必须明确，否则会被误用）

#### 5.3.1 Hub 作为网关接收外部请求（浏览器/用户）

规则（对所有进入 Hub 的请求都适用）：

1. Hub **不得**因为请求中携带了 `X-Service-Auth`、`X-Hub-Auth`、`X-Service-Id` 等字段就把该请求判定为内部互信来源。
2. 对于需要走“内部服务入口”（Service->Hub）的路由，必须满足：
   - `RemoteAddr` loopback 前置条件
   - `X-Service-Auth` 字符串匹配成功
   - 以上两者缺一不可

#### 5.3.2 Hub 反代/转发到 Service（Hub->Service）

规则（对所有转发到 Service 的请求都适用，包含 WS/SSE）：

1. Hub 必须先删除下列 header（无论来路是否存在）：
   - `X-Hub-Auth`
   - `X-Hub-Service-Id`
   - `X-Hub-Service-Instance-Id`
2. Hub 必须再强制写入上述 header（覆盖注入）：
   - `X-Hub-Auth = <目标 service 的 H2S_TOKEN>`
   - `X-Hub-Service-Id = <目标 service_id>`
   - `X-Hub-Service-Instance-Id = <目标 instance_id>`
3. 任何实现路径（普通 HTTP、SSE、WS Upgrade）都必须遵守 1/2；若某类链路无法注入 header，则该链路不得绕过互信校验（应视为缺陷）。

---

## 6. 启动与注册时序（不考虑 Hub 重启）

### 6.1 Hub 启动阶段（Supervisor/生命周期）

对每个 `service_id`：

1. 生成 `instance_id`（或复用既有逻辑生成）
2. 生成随机 `S2H_TOKEN`、`H2S_TOKEN`
3. 写入 `services/<service_id>/run/.service_secret`（0600，带短 TTL）
4. 启动 service 进程
5. Hub 内存中建立映射：
   - `(service_id, instance_id) -> {S2H_TOKEN, H2S_TOKEN, issued_at, expires_at}`

### 6.2 Service 启动阶段

1. 读取并解析 `run/.service_secret`，将 `S2H_TOKEN`、`H2S_TOKEN`、`service_id`、`instance_id`、`register_url` 写入内存变量
2. 向 `HUB_REGISTER_URL` 发起 register：
   - 必须携带 `X-Service-Id` / `X-Service-Instance-Id` / `X-Service-Auth`
   - request body 仍使用现有 `SupervisorRegisterRequest`/manifest（保持协议主体不被破坏）
3. register 成功后：立即删除或清空 `run/.service_secret`
4. 后续所有对 Hub 的内部调用（需要被识别为 service）都携带 `X-Service-Auth`

### 6.3 Hub register handler

1. 前置：`RemoteAddr` 必须为 loopback（否则直接拒绝；该条件不用于提升身份）
2. 解析请求体得到 `service_id`、`instance_id`（以及 endpoint/manifest 等现有字段）
3. 校验 header：
   - `X-Service-Auth` 必须与 Hub 内存里该 `service_id/instance_id` 的 `S2H_TOKEN` 完全一致（字符串匹配）
4. 校验通过才允许完成注册、写入 registry、刷新路由等后续动作

> 说明：register 本身就是标准的 Service->Hub 通信，因此必须走 `X-Service-Auth` 校验。

---

## 7. 失败策略（必须简单、可预期）

- `.service_secret` 缺失 / 解析失败：service 立即退出（由 supervisor 重启）
- `.service_secret` 已过期：service 立即退出（由 supervisor 重启）
- register 被拒绝（token 不匹配等）：service 立即退出（由 supervisor 重启或按配置退避）
- Hub->Service 请求缺少/错误 `X-Hub-Auth`：service 返回 `403`
- Service->Hub 请求缺少/错误 `X-Service-Auth`：hub 返回 `403`，且日志中标记为匿名/未认证来源（不赋予 `SERVICE` identity）
- Service->Hub 请求 `RemoteAddr` 非 loopback：hub 返回 `403`（防御性拒绝，不参与身份判定）

---

## 8. 日志与审计要求（配合“source 字段”）

### 8.1 Hub 侧

- 在请求入口（Identity/Middleware）处完成识别：
  - 若 `X-Service-Auth` 校验通过：Identity = `SERVICE/<service_id>`
  - 否则：保持现有 USER/JWT 或 ANONYMOUS 分支
- 该识别结果是 Hub->Service / Service->Hub 互信的唯一可信源：后续路由、审计、权限分支不得再二次“猜测来源”（例如仅凭来源 IP/端口就把请求当作 service）。
- `RemoteAddr loopback` 仅允许作为“前置拒绝条件”，不得作为 `SERVICE` 身份的推断依据。
- 绝不把 `X-Service-Auth`、`X-Hub-Auth` 的内容写入日志（必要时打 `****`）

### 8.3 关于 18080 WebUI 请求的影响评估

- 本计划将 `RemoteAddr loopback` 作为前置条件 **仅应用于 Hub 的 Service 内部入口**（如 `/api/service/register`、`/api/service/heartbeat` 等），因此不会影响浏览器对 18080 WebUI 的正常访问与页面内 API 调用。
- 若未来把 loopback 前置条件错误地施加到“对用户/浏览器开放的公共路由”，会导致从非本机（或通过局域网 IP 访问）无法使用 Hub；此不属于本计划目标，实施时需严格限定适用路由范围。
- 本计划要求 Hub 在转发到 Service 前执行 header 清洗/覆盖（§5.3），因此浏览器即便主动携带 `X-Hub-Auth` 也不会影响 Hub->Service 的互信结果（不会造成“伪造内部调用”）。

### 8.2 Service 侧

- **所有 Hub 可能转发到达的入口**（包含 `/service/tool/exec`、ws/sse、health/admin 等）都必须在最前面做 `X-Hub-Auth` 字符串匹配；不得仅保护“部分高价值入口”，否则不满足“全部请求”要求。
- 同样避免输出 token 内容

---

## 9. 代码改造落点（仅列出，供实施）

> 本文档是 DevPlan，不在本轮执行编码；下列为预计改动点清单。

### 9.1 Hub 侧（预计）

- Supervisor 生命周期：为每个 service 生成 token 并写入 `services/<svc>/run/.service_secret`（新增 bootstrap 写入逻辑）
- `POST /api/service/register`：增加 `X-Service-Auth` 校验（在现有 loopback 限制之外的“身份门”）
- Hub Identity 中间件：新增识别 `X-Service-Auth` 的分支，将其注入为 `SERVICE` identity（用于日志 `source`）
- Hub 到 Service 的反代/网关：在转发请求时注入 `X-Hub-Auth`（按目标 service 选择对应 `H2S_TOKEN`）
- Header 安全：实现“清洗 + 覆盖注入”（§5.3），确保浏览器侧伪造 header 不会被透传并影响互信分支

### 9.2 Service 侧（预计）

- 启动入口：读取 `run/.service_secret`（KV 文件），加载到内存，register 成功后删除文件
- register client：对 Hub register 请求补齐 `X-Service-Auth`
- 全量入口：对所有 Hub 可能转发到达的路由统一校验 `X-Hub-Auth`（字符串匹配）

---

## 10. 验收标准（可执行）

1. 任意 Service 的 **所有 Hub 可达入口** 在缺少/错误 `X-Hub-Auth` 时稳定返回 `403`（且不会执行实际逻辑），包含普通 HTTP、SSE、WS Upgrade 链路。
2. Hub 端在缺少/错误 `X-Service-Auth` 时稳定返回 `403`（register/heartbeat/其它 service 内部入口），且不会把来源识别为 `SERVICE`。
3. 正常链路下：
   - service 启动 -> register 成功 -> `services/<svc>/run/.service_secret` 被删除/清空
   - Hub 日志 `source`/Identity 能稳定输出该 service 的 `service_id`
4. 多 service 并行启动时，token 互不相同，且 A 的 token 不能用于 B 的身份识别（字符串匹配失败）。
5. 浏览器请求不会因为携带伪造 `X-Hub-Auth`/`X-Service-Auth` 而获得内部身份或影响 Hub->Service 转发（header 被 Hub 清洗/覆盖）。

---

## 11. 风险清单（与约束对齐）

- 该方案本质是 per-service bearer secret，若 token 在传输/日志/抓包中泄漏，将被直接冒用；本计划通过“仅启动期落盘 + 禁止日志打印 + 0600 权限 + 尽快删除文件”降低概率，但不对抗高级本机攻击者。
- 若未来引入跨机器部署/非 loopback 通信，本方案需要补充传输层保护（TLS/mTLS）或更强的握手证明；但这超出本计划范围。
