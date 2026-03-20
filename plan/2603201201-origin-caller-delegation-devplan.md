# Origin Caller Delegation 开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-20 12:01 CST  
> 范围：`pkg/toolproto/`、`pkg/hubsvc/`、`hub/internal/{app,gateway,security}/`、`services/sql_db/`、`services/account/`、`services/chat_server/`、`services/surface_manager/`、`services/file_storage/`、`services/ai_doubao/` 以及其他经 Hub 二次调用其他 service 的业务 service  
> 目标约束：保留现有 `caller` 字段语义；新增 `origin_caller` 与 `origin_caller_token`，让 `web -> hub -> serviceA -> hub -> serviceB` 链路可以安全保留原始客户端身份上下文。

## 0A. 当前落地摘要

截至 2026-03-20 16:42 CST，本计划的第一轮基础实现已经落地，当前事实是：

1. `toolproto.Context` 已正式包含 `origin_caller` 与 `origin_caller_token`。
2. Hub 已能在入口保留现有 `caller` 语义，并在合法二次调用链路中恢复 `origin_caller`。
3. Hub 已为下游目标 service 重签发 `origin_caller_token`，并把 origin headers 纳入 protected headers 清洗与注入。
4. `sql_db` 已支持在显式传入 `scope_source=origin` 时按 `origin_caller` 的 `user` / `surface` scope 访问数据库；默认仍按当前 `caller` scope 处理。
5. `chat_server` 已接入这条路径，当前可通过 `service -> hub -> sql_db` 保留原始用户上下文。
6. `surface_manager`、`file_storage`、`account` 已补齐 origin caller 的读取或透传基础适配。

当前仍应坚持的主路径原则：

1. 首选推荐 `web -> hub -> service -> web`，也就是 Web/Page 直接调用目标业务 service。
2. `service -> hub -> service` 现在是正式支持的补充机制，但不应被当作默认推荐架构。
3. 当业务可以直接走 Web 到目标 service 时，不应优先设计成一个 service 间接调用另一个 service。

## 0. 计划结论

本轮不新增 `transport_caller` 字段。  
现有 `caller` 继续表示“当前这一次请求直连 Hub 的调用者”；新增：

- `origin_caller`
- `origin_caller_token`

整体语义调整为：

1. `caller`：每一跳都由 Hub 重新解析并覆盖，表示当前直连 Hub 的请求主体。
2. `origin_caller`：表示链路起点的原始客户端身份，只能由 Hub 首次提取或由 Hub 验证 `origin_caller_token` 后恢复。
3. `origin_caller_token`：Hub 签发并验证的委托凭证，防止 service 伪造 `origin_caller`。

目标不是让 service 转发用户 JWT，而是让 Hub 以自签发凭证方式把原始客户端上下文安全地延续到后续跳。  
这样 `serviceA -> hub -> sql_db` 可以在合法条件下恢复到原始 `user` / `surface` / `page` 作用域，而不是一律退化为 `service` 作用域。

这套机制不是只改 Hub 与 `sql_db` 就结束。  
原则上所有 service 都应适配并理解 `caller + origin_caller` 双身份上下文；其中 `account` 因为掌管用户身份、登录态与安全边界，必须做最深度的适配与复核。

## 1. 当前真实状态

以下事实已由代码核验：

1. Hub 在 `/api/tool/call` 中会根据当前 HTTP 请求身份重新构造 `caller` 并覆盖 `req.Context.Caller`，见 `hub/internal/gateway/tool_handler.go`。
2. Hub 当前不会保留上游请求体自带的 `caller` 作为“原始 caller”，而是直接覆盖成当前这跳识别出的主体。
3. Hub 转发前会清洗 `X-Caller-*` 头，见 `hub/internal/security/headers.go`，所以 service 无法靠自行注入 caller 头来向下游伪造身份。
4. `pkg/toolproto.Context` 当前只包含单一 `Caller` 字段，没有原始调用者字段。
5. `pkg/hubsvc` 当前只有 `X-Caller-*` 相关 helper，没有 origin caller 的 header 或 token helper。
6. `sql_db` 当前完全按 `caller` 决定 scope：
   - `user`
   - `surface`
   - `service`
7. `account`、`surface_manager`、`chat_server` 等 service 当前通过 Hub 二次调用下游工具时，主动提交的 `caller` 都是 `service` caller。

## 2. 问题定义

### 2.1 单一 `caller` 同时承担了两种不同语义

当前 `caller` 既被当作：

1. “谁在直接调用 Hub”
2. “这次业务真正代表谁”

这在单跳请求中问题不大，但在多跳链路里会冲突。

### 2.2 多跳链路中原始客户端身份会丢失

链路示例：

`web -> hub -> serviceA -> hub -> serviceB`

当前行为是：

1. 第一跳：Hub 识别到 `caller = user`
2. 第二跳：Hub 识别到 `caller = serviceA`
3. 原始用户身份不再进入 `serviceB`

这导致需要按用户 scope 访问 `sql_db` / `file_storage` 的链路无法正式表达。

### 2.3 不能让 service 直接回传用户 JWT 或自行声明原始 caller

如果允许 serviceA 直接带着用户 JWT 或明文 `origin_caller` 回到 Hub，会出现：

1. service 能伪造任意用户
2. 用户浏览器身份暴露给 service
3. Hub 的 caller 信任边界退化

因此必须由 Hub 自己签发和验证原始调用者凭证。

## 3. 目标模型

### 3.1 字段模型

保留现有字段：

- `caller`

新增字段：

- `origin_caller`
- `origin_caller_token`

推荐语义：

#### `caller`

当前直连 Hub 的调用者。

规则：

1. 每一跳都由 Hub 重新解析。
2. 任何请求体、header 中自带的旧 `caller` 都不可信。
3. Hub 识别完成后统一覆盖。

用途：

1. 表示谁在直接调用当前 service。
2. 用于 service-to-service 信任判断。
3. 用于内部工具调用限制。
4. 不再承担“链路起点原始用户是谁”的唯一表达职责。

#### `origin_caller`

链路起点的原始客户端身份。

可能值：

1. `user`
2. `surface`
3. `page`
4. `service`（用于 service 作为链路源头时）

规则：

1. 首次进入 Hub 时，由 Hub 从真实身份提取。
2. 后续跳只允许通过验证 `origin_caller_token` 恢复。
3. service 不能自行声明可信的 `origin_caller`。

用途：

1. 表示最初客户端上下文。
2. 用于数据 scope、对象归属、业务上下文和审计补充。
3. 不直接等于“业务授权已通过”。

#### `origin_caller_token`

Hub 签发的原始调用者委托凭证。

作用：

1. 证明 `origin_caller` 是 Hub 自己签发的，不是 service 伪造的。
2. 让 Hub 在下一跳恢复原始客户端上下文。

## 4. 核心规则

### 4.1 链路起点规则

当请求首次从外部进入 Hub：

1. Hub 正常解析当前调用者。
2. 写入 `caller`。
3. 同时把相同主体写入 `origin_caller`。
4. Hub 为该 `origin_caller` 签发 `origin_caller_token`。

示例：

`web -> hub`

结果：

- `caller = user`
- `origin_caller = user`
- `origin_caller_token = hub-signed(...)`

### 4.2 中继跳规则

当 `serviceA -> hub` 发起二次调用：

1. Hub 先按当前请求重新解析 `caller = serviceA`
2. Hub 再检查是否存在 `origin_caller_token`
3. 若 token 合法，则恢复 `origin_caller`
4. 若 token 不合法，则拒绝或降级处理

最终下发给 `serviceB` 的上下文：

- `caller = serviceA`
- `origin_caller = 原始 user/page/surface`

### 4.3 无 token 情况

如果二次调用没有携带 `origin_caller_token`：

1. 不允许 service 自己明文指定可信 `origin_caller`
2. Hub 应将 `origin_caller` 回退为当前 `caller`

也就是：

- `caller = serviceA`
- `origin_caller = serviceA`

### 4.4 token 不是用户 JWT

`origin_caller_token` 不等于用户 JWT。

要求：

1. 不暴露浏览器 JWT 给 service。
2. token 只用于 Hub 内部恢复 origin caller。
3. token 必须由 Hub 自己签发与验证。

### 4.5 所有 service 都应适配双身份上下文，但不能一刀切只看 `origin_caller`

本轮不应把规则写成“所有 service 统一只用 `origin_caller` 识别用户信息”。  
正确要求是：

1. 所有 service 都应能读取并理解 `caller` 与 `origin_caller` 两个可信字段。
2. 涉及数据 scope、业务归属、用户上下文的逻辑，应评估是否优先使用 `origin_caller`。
3. 涉及直接调用方信任、内部能力限制、service 间互调许可的逻辑，必须继续使用 `caller`。
4. 安全敏感 service 不得因为存在 `origin_caller` 就跳过自己的业务鉴权判断。

### 4.6 `account` 是重点改造模块，不是普通适配模块

`account` 掌管：

1. 用户身份
2. 登录态
3. token / session
4. 对外身份写回

因此它不能只做字段兼容，而要深度复核：

1. 哪些能力允许 service 合法代表用户发起。
2. 哪些能力必须只接受原始用户直连 Hub。
3. `caller` 与 `origin_caller` 冲突时应如何处理。
4. 登录态、副作用写回、session 绑定是否允许 delegation。

结论上，`account` 必须作为重点 service 单独设计和验证。

### 4.7 `allowed_caller_types` 必须作为 account 边界的第一层硬约束

`account` 的最佳默认策略不是“先放开再在内部判断”，而是：

1. 默认只接受客户端调用。
2. 只有极少数明确需要的工具才允许 `service` caller。

因此本轮应把 `allowed_caller_types` 作为第一层边界收紧 account：

1. `account.auth.register`
2. `account.auth.login`
3. `account.auth.logout`
4. `account.auth.me`
5. `account.auth.password_change`

以上工具默认应保持客户端向导：

- `anonymous`
- `user`

而非面向普通 service 开放。

service caller 白名单应只保留明确的内部治理或基础设施工具，例如：

1. `service.lifecycle.*`
2. `account.system.keys.get`
3. `account.session.dump_active`

若未来确实需要新增“service 代表用户调用 account”的能力，应单独定义新工具或显式放开某个工具，而不是把整个 `account.auth.*` 面整体对 service 开放。

## 5. token 设计建议

### 5.1 最小 payload

建议至少包含：

1. `origin_caller`
2. `issued_at_ms`
3. `expires_at_ms`
4. `issued_for_service_id`
5. `request_id`
6. `trace_id`

### 5.2 建议能力限制

可选增加：

1. `allow_redelegate`
2. `allowed_tool_prefixes`
3. `allowed_scope_types`

### 5.3 实现方式

本轮不要求必须采用标准 JWT 库。  
Hub 自签名的轻量 token 即可，只要满足：

1. service 不能伪造
2. Hub 能无状态验证
3. Hub 能从 token 恢复 `origin_caller`

建议采用“payload + HMAC 签名”的简单结构。  
如果后续需要更强可扩展性，再演进为标准 JWT。

## 6. 代码改造范围

### 6.1 `pkg/toolproto`

目标：

1. 为请求上下文正式引入 `origin_caller` 与 `origin_caller_token`

任务：

1. 扩展 `Context` 结构
2. 补充相关 JSON tag
3. 更新 Normalize/Clone/helper 逻辑，避免新字段在规范化时丢失

验收：

1. `toolproto.Context` 可稳定携带：
   - `caller`
   - `origin_caller`
   - `origin_caller_token`

### 6.2 `pkg/hubsvc`

目标：

1. 提供 origin caller token 的读写 helper

任务：

1. 新增 token header/字段 helper
2. 新增 signer / verifier helper
3. 新增 caller merge helper，明确：
   - `caller` 从当前直连请求识别
   - `origin_caller` 从 token 恢复

验收：

1. service 侧可以透传 `origin_caller_token`
2. 但不能自行伪造可信 `origin_caller`

### 6.3 `hub/internal/app`

目标：

1. 在身份识别层明确区分“当前 caller”与“原始 caller”

任务：

1. 保持现有 `IdentityMiddleware` 对当前请求主体的识别逻辑
2. 新增 origin caller token 签发/验证能力
3. 管理 Hub 自身用于签发 token 的密钥来源

验收：

1. Hub 可为链路起点请求生成 `origin_caller_token`
2. Hub 可在二次调用时验证并恢复 `origin_caller`

### 6.4 `hub/internal/gateway/tool_handler.go`

目标：

1. 把单 caller 覆盖逻辑改为“重建 caller + 恢复 origin_caller”

任务：

1. 当前识别出的主体继续覆盖 `req.Context.Caller`
2. 若请求没有合法 `origin_caller_token`：
   - `req.Context.OriginCaller = req.Context.Caller`
3. 若请求有合法 token：
   - 从 token 恢复 `req.Context.OriginCaller`
4. 在转发到下游 service 时，同时注入：
   - `caller`
   - `origin_caller`
   - `origin_caller_token`

验收：

1. `web -> hub -> serviceA` 下游可看到：
   - `caller = user`
   - `origin_caller = user`
2. `serviceA -> hub -> serviceB` 下游可看到：
   - `caller = serviceA`
   - `origin_caller = user`

### 6.5 `hub/internal/security`

目标：

1. 补齐 `origin_caller` / token 的 header 清洗与注入规则

任务：

1. 将 `X-Origin-Caller-*`、`X-Origin-Caller-Token` 加入 protected headers
2. 新增注入 helper
3. 确保浏览器和外部请求不能伪造这些头

验收：

1. 外部请求中的 origin caller 相关头一律被清洗
2. 只有 Hub 转发时才会写入

### 6.6 `services/sql_db`

目标：

1. 在合法 delegation 场景下优先按 `origin_caller` 选 scope

任务：

1. 更新 `storage.database.*` 的 caller 解析规则：
   - 若存在合法 `origin_caller` 且类型是 `user` / `surface`，优先使用它
   - 否则按当前 `caller`
2. `storage.share.write` 仍需谨慎，默认建议继续以当前 `caller=service` 为准

原因：

1. `share.write` 语义本身偏 service 共享，不宜轻易切到 user 作用域
2. `storage.database.*` 才是最明确需要 origin scope 的链路

验收：

1. `serviceA` 代表用户访问 `storage.database.*` 时，可落到用户 scope
2. 无 token 时仍退回 service scope

### 6.7 所有业务 service 的通用适配要求

目标：

1. 所有 service 都能够读取可信的 `caller + origin_caller`
2. 所有存在二次调用链路的 service 都能够透传 `origin_caller_token`

适用范围：

1. `account`
2. `chat_server`
3. `surface_manager`
4. `file_storage`
5. `ai_doubao`
6. 后续任何经 Hub 二次调用其他 service 的模块

任务：

1. 梳理本 service 内哪些逻辑应使用 `caller`
2. 梳理哪些逻辑应改为优先使用 `origin_caller`
3. 对所有 Hub 二次调用点补充 `origin_caller_token` 透传
4. 避免把 `origin_caller` 当作“授权已经通过”的替代品

验收：

1. service 能同时读取 `caller` 和 `origin_caller`
2. serviceA 无需持有用户 JWT
3. serviceA 只转交 Hub 签发的 token

### 6.8 `services/account`

目标：

1. 成为 origin delegation 模型下最严格的身份边界实现
2. 清晰区分“谁直接调用了 account”与“这次调用最初代表谁”

任务：

1. 梳理所有 account 工具，按安全级别分组：
   - 允许 delegation
   - 只允许原始 user 直连
   - 只允许 service caller
2. 先用 `allowed_caller_types` 把 account 工具面按客户端/服务端分层收紧。
3. 默认原则：`account.auth.*` 不对普通 service 开放；只保留明确白名单的 `service.lifecycle.*` 与 `account.system.*` 服务侧工具。
4. 如确有 delegation 需求，应优先设计新的专用工具，而不是直接放开现有认证工具。
5. 明确 `caller` 与 `origin_caller` 的优先级：
   - 身份上下文可参考 `origin_caller`
   - 直接调用信任和内部管理操作必须看 `caller`
6. 审查登录、登出、密码修改、session dump、keys get 等工具的 delegation 规则
7. 审查 cookie/effects 回写场景是否允许 service 代表用户触发
8. 补充 account 自身对下游 Hub 调用时的 `origin_caller_token` 透传

验收：

1. account 不接触用户 JWT 的跨 service 传播
2. account 能正确区分当前调用 service 与原始客户端身份
3. account 的 `allowed_caller_types` 与真实安全边界一致
4. account 的安全敏感工具 delegation 规则明确且有测试覆盖

## 7. 请求与 header 设计建议

### 7.1 上下文字段

建议在 `toolproto.Context` 中新增：

1. `origin_caller`
2. `origin_caller_token`

### 7.2 Hub 注入头

建议新增：

1. `X-Origin-Caller-Type`
2. `X-Origin-Caller-User-Id`
3. `X-Origin-Caller-Service-Id`
4. `X-Origin-Caller-Surface-Id`
5. `X-Origin-Caller-Token`

说明：

1. 这些头只作为 Hub <-> Service 间的传输载体
2. 外部请求进 Hub 时必须被清洗

### 7.3 service -> hub 回传方式

建议优先通过 `toolproto.Context.origin_caller_token` 回传；  
header 只作为 Hub -> Service 注入载体，不把“service 自行填 header”变成正式 API。

理由：

1. 减少 service 手写 header 的歧义
2. 明确上下文属于 tool protocol 的一部分

## 8. 实施分期

### Phase 1：协议与 token 基座

1. 改 `pkg/toolproto`
2. 改 `pkg/hubsvc`
3. 补 token 签发/验证 helper

### Phase 2：Hub 网关改造

1. 改 `tool_handler`
2. 改 header 清洗/注入
3. 打通单跳和二跳 caller/origin_caller 行为

### Phase 3：`sql_db` 消费 origin caller

1. 更新 scope 解析逻辑
2. 验证 user/surface delegation 场景

### Phase 4：全 service 适配双身份上下文

1. 先改 `account`
2. 再改 `chat_server`
3. 再改 `surface_manager`
4. 再改其他存在二次调用链路的 service

### Phase 5：验证与防回归

1. 增加相关单测/集成测试
2. 覆盖单跳与多跳场景

## 9. 验证策略

### 9.1 单跳

`web -> hub -> serviceA`

验证：

1. `caller = user`
2. `origin_caller = user`

### 9.2 二跳

`web -> hub -> serviceA -> hub -> serviceB`

验证：

1. 到 serviceA：
   - `caller = user`
   - `origin_caller = user`
2. 到 serviceB：
   - `caller = serviceA`
   - `origin_caller = user`

### 9.3 无 token 二跳

`serviceA -> hub -> serviceB`

验证：

1. `caller = serviceA`
2. `origin_caller = serviceA`

### 9.4 `sql_db` scope

验证：

1. 有合法 token 时，`storage.database.*` 使用 `origin_caller`
2. 无 token 时，退回 `caller`

### 9.5 `account` 行为

验证：

1. `account.auth.*` 默认不接受普通 service caller
2. 允许 delegation 的 account 工具可正确消费 `origin_caller`
3. 不允许 delegation 的 account 工具不会因为有 `origin_caller` 就放行
4. account 的副作用写回规则与现有安全边界一致

### 9.6 其他 service 适配

验证：

1. `chat_server`、`surface_manager` 等可同时看到可信 `caller + origin_caller`
2. 各自的内部使用逻辑与职责边界一致

## 10. 风险与控制

### 10.1 把 `origin_caller` 当成“已授权身份”

风险：

1. service 误把 `origin_caller` 当成权限已通过的结论

控制：

1. 文档明确：`origin_caller` 只提供上下文，不替代业务授权判断

### 10.2 token 作用域过大

风险：

1. 一个 token 被 service 滥用于超出原始链路的操作

控制：

1. token 中加入过期时间
2. 必要时加入 service/tool 限制

### 10.3 过度修改当前 caller 行为

风险：

1. 改坏现有服务间调用

控制：

1. 保持 `caller` 语义不变
2. 所有新增行为只放到 `origin_caller` 与 token 机制中

### 10.4 service 误用 `origin_caller`

风险：

1. 开发者把所有用户相关判断一律切到 `origin_caller`
2. 导致 service 间调用信任边界被忽略

控制：

1. 明确要求所有 service 同时理解 `caller` 与 `origin_caller`
2. 在关键 service 尤其是 `account` 中做专项复核

## 11. 完成标准

满足以下条件才算本轮机制完成：

1. 保留现有 `caller` 字段，不新增 `transport_caller`
2. `toolproto.Context` 已支持 `origin_caller` 与 `origin_caller_token`
3. Hub 每一跳都会重算 `caller`
4. Hub 能通过 token 恢复可信 `origin_caller`
5. `sql_db` 在合法 delegation 场景下能按 `origin_caller` 选 scope
6. 所有相关 service 已完成 `caller + origin_caller` 双身份上下文适配
7. `account` 已完成深度安全边界更新
8. service 不需要接触用户 JWT
9. service 不能伪造可信 `origin_caller`

## 12. 信息来源

1. `pkg/toolproto/v1.go`
2. `pkg/hubsvc/session.go`
3. `hub/internal/gateway/tool_handler.go`
4. `hub/internal/security/headers.go`
5. `hub/internal/app/identity.go`
6. `services/sql_db/cmd/sql_db/main.go`
7. `services/sql_db/internal/app/hub_builtins.go`
8. `services/account/internal/database/client.go`
6. `services/sql_db/cmd/sql_db/main.go`
7. `services/sql_db/internal/app/hub_builtins.go`
8. `services/account/internal/database/client.go`
