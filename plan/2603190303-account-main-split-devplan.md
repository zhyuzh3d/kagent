# Account Main 拆分开发计划

> **文档类型**：开发计划（devplan）  
> **时间**：2026-03-19 03:03 CST  
> **范围**：`services/account/cmd/account/main.go`、`services/account/internal/bootstrap/`、`services/account/internal/business/`、`services/account/internal/adaptation/`、`services/account/internal/database/`、`services/account/manifest.json`、必要时联动 `hub/internal/gateway/tool_handler.go` 与 `hub/internal/supervisor/process_control.go`  
> **信息来源（可核验）**：  
> - 当前 `account` 入口：`services/account/cmd/account/main.go` 已同时承担启动、注册、心跳、tool 分发、账号业务、存储、生命周期兼容入口  
> - 当前 `Hub` 身份与 tool 路由：`hub/internal/app/identity.go`、`hub/internal/gateway/tool_handler.go`  
> - 当前 `database` 能力：`storage.database.query/execute/schema` 与 `storage.share.read/write`，可承载 account 的持久化需求  
> - 项目说明：`doc/_instruction/core.md`、`doc/_instruction/glossary.md`

## 0. 本轮共识

1. `account` 不再自证用户身份，真正的身份验证在 Hub。
2. `account` 只负责利用 `effects` 管理 Cookie/JWT 的写入与清理。
3. `account` 的所有持久化全部改走 `database` service 提供的 tool，不在 service 内直接持有 sqlite 业务逻辑。
4. 暂不引入事务、加密、复杂迁移框架或额外抽象，先做最小可行拆分。
5. 拆分目标不是“目录好看”，而是把 `main.go` 缩成装配层，让职责边界稳定下来。

## 1. 现状判断

`services/account/cmd/account/main.go` 当前混合了以下职责：

1. 启动参数解析、appRoot 识别、bootstrap secret 加载。
2. 向 Hub 注册、心跳守护和 shutdown 守卫。
3. `/service/tool/exec` 的 HTTP 适配。
4. `account.auth.*`、`service.lifecycle.*`、`account.system.*` 的 tool 分发。
5. 账号业务逻辑：
   - 注册
   - 登录
   - 登出
   - 查询当前用户
   - 修改密码
   - token 签发
   - active sid 维护
6. 本地 sqlite 存储。

这说明当前文件已经不适合继续加功能，应该先拆分职责再继续演进。

## 2. 目标结构

建议拆成 3 个核心包加 1 个存储适配包：

1. `bootstrap`
   - 负责进程启动装配、Hub 注册、心跳守护、退出守卫、bootstrap secret 生命周期。
2. `business`
   - 负责账号领域逻辑、token 生成、密码校验、session 规则、tool 业务处理。
3. `adaptation`
   - 负责 HTTP/tool 协议适配，接收 `/service/tool/exec`，把请求转给 `business`，把 `effects` 和响应写回去。
4. `database`
   - 负责把 account 的持久化动作翻译成 Hub -> database tool 调用。

`cmd/account/main.go` 只保留装配和启动，不再承载业务实现。

## 3. 拆分原则

1. `bootstrap` 只处理运行时，不处理业务。
2. `business` 只处理账号规则，不直接处理 HTTP。
3. `adaptation` 只处理协议，不做领域判断。
4. `database` 只处理存储，不知道业务语义。
5. 任何一个包都不应反向依赖 `main.go`。
6. `account` 允许通过 `effects` 管理 Cookie，但不在 JSON result 里泄露必须保密的登录态。

## 4. 文件级拆分计划

### 4.1 `bootstrap`

建议承接的内容：

1. `detectAppRoot`
2. bootstrap secret 读取
3. `registerToHub`
4. `postHubToolCall`
5. 心跳守护 `startHubHeartbeatGuard`
6. service 启动参数解析后的装配逻辑
7. shutdown 回调和 server close 流程

输出目标：

1. 提供一个 `Bootstrap` 或 `App` 对象。
2. 负责把 `business`、`adaptation`、`database` client 拼起来。
3. 负责在 Hub 注册成功后启动心跳。

### 4.2 `business`

建议承接的内容：

1. `account.auth.register`
2. `account.auth.login`
3. `account.auth.logout`
4. `account.auth.me`
5. `account.auth.password_change`
6. `service.lifecycle.health`
7. `service.lifecycle.shutdown`
8. token claims 结构和签发逻辑
9. 密码 hash / verify
10. session/sid 生成与更新规则

输出目标：

1. 提供一个统一的 `HandleTool(...)` 或 `Execute(...)`。
2. 输入是规范化后的 tool request 和 caller context。
3. 输出是 `toolproto.CallResponse`，其中 Cookie/JWT 通过 `effects` 描述。

### 4.3 `adaptation`

建议承接的内容：

1. `http.Handler` 的 `/service/tool/exec`
2. request body decode
3. `toolproto.NormalizeRequest`
4. caller 注入
5. `hub_only` context 标记
6. response 写回
7. `effects` 解析和写回前落地

输出目标：

1. 只负责协议适配，不含业务判断。
2. 处理 `account.system.keys.get` 和 `account.session.dump_active` 的访问门禁时，只做调用前的上下文准备，不写业务存储逻辑。

### 4.4 `database`

建议承接的内容：

1. 用户表的 CRUD
2. active session 的 CRUD
3. signing key 的存取
4. 必要 schema 初始化

实现方式：

1. 通过 Hub 调 `storage.database.query`
2. 通过 Hub 调 `storage.database.execute`
3. 必要时通过 `storage.database.schema` 做 schema 自检

不做的内容：

1. 不加事务封装。
2. 不加额外加密层。
3. 不做领域级账号校验。

## 5. 数据访问映射

### 5.1 `register`

业务步骤：

1. 检查用户名和密码长度。
2. 写入用户记录。
3. 生成新 `sid`。
4. 写入 active session。
5. 签发 token。
6. 通过 `effects` 写入登录 cookie。

数据库调用：

1. `storage.database.execute`：插入用户
2. `storage.database.execute`：写 active session
3. `storage.database.execute`：必要时写 signing key

### 5.2 `login`

业务步骤：

1. 按用户名查询用户。
2. 验证密码。
3. 生成新 `sid` 覆盖旧 `sid`。
4. 签发新 token。
5. `effects` 写 cookie。

数据库调用：

1. `storage.database.query`：查用户
2. `storage.database.execute`：更新 active session

### 5.3 `logout`

业务步骤：

1. 根据 caller 用户身份清理 active session。
2. `effects` 删除 cookie。

数据库调用：

1. `storage.database.execute`：删除 active session

### 5.4 `password_change`

业务步骤：

1. 查用户。
2. 验证旧密码。
3. 更新密码 hash。
4. 生成新 `sid`。
5. `effects` 删除旧 cookie 或重新下发新 cookie，按最终业务决定。

数据库调用：

1. `storage.database.query`：查用户
2. `storage.database.execute`：更新密码 hash
3. `storage.database.execute`：更新 active session

### 5.5 `keys.get` / `dump_active`

业务步骤：

1. 仅供 Hub 内部同步。
2. 返回公钥列表和活跃 session 列表。

数据库调用：

1. `storage.database.query`：查 signing keys
2. `storage.database.query`：查 active sessions

## 6. 推荐迁移顺序

### Phase 1：先抽 `bootstrap`

目标：

1. 把启动、注册、心跳从 `main.go` 拿出去。
2. 让 `main.go` 先短下来。

验收：

1. `main.go` 只剩装配和 `ListenAndServe`。
2. 注册和心跳行为与当前一致。

### Phase 2：再抽 `database`

目标：

1. 把 sqlite 逻辑替换成 database tool 调用。
2. 保留当前表结构和字段语义。

验收：

1. `account` 不再直接打开本地 sqlite。
2. 用户、session、key 的读写都能通过 database tool 完成。

### Phase 3：再抽 `business`

目标：

1. 把账号业务从 HTTP handler 中拆出来。
2. 让业务逻辑变成可单测的纯 Go 逻辑。

验收：

1. `business` 可以直接输入 tool request 输出 tool response。
2. 业务分支不再散落在 `main.go`。

### Phase 4：最后抽 `adaptation`

目标：

1. 把 `/service/tool/exec` 变成薄适配层。
2. 统一处理 effects 写回。

验收：

1. `main.go` 基本只做 wiring。
2. tool 处理链路清晰，HTTP 只是入口协议。

## 7. 风险与边界

1. `database` tool 没有事务，account 的多步写入只能先按最小实现落地。
2. 拆分时不要把 `effects` 逻辑塞回 business，否则会重新长成大文件。
3. `bootstrap` 和 `adaptation` 的边界要清楚，前者管进程，后者管协议。
4. 迁移期间要保持 tool ID、cookie 名和 Hub 同步逻辑稳定，避免影响现有登录态。

## 8. 验收标准

1. `services/account/cmd/account/main.go` 缩短为装配层。
2. account 的持久化全部走 Hub -> database tool。
3. account 只通过 `effects` 管理 Cookie/JWT。
4. Hub 仍然是唯一身份认证边界。
5. 关键路径能通过构建或最小测试验证。

## 9. 备注

本计划只定义拆分路径，不改实现。  
等拆分进入执行阶段时，再按 `bootstrap -> database -> business -> adaptation` 的顺序逐步提交代码变更。
