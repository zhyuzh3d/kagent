# SQL 机制全面改进开发方案

> 文档类型：开发计划（devplan）  
> 时间：2026-03-20 10:55 CST  
> 范围：`services/sql_db/`、`hub/`、`services/surface_manager/`、`services/chat_server/`、`services/account/`、`pkg/sqlitedriver/`、项目说明文档  
> 目标约束：数据库能力统一收敛到 `sql_db` 工具面；除 `sql_db` 与经批准的 Hub 核心基础设施外，其他 service 不得直接使用 sqlite 驱动；不为旧模式保留兼容层。

## 0A. 当前落地摘要

截至 2026-03-20 16:42 CST，本方案相关的第一轮收敛已经落地：

1. `pkg/sqlitedriver` 已被移除，sqlite 驱动封装已收回 `hub` / `sql_db` 子项目内部。
2. `surface_manager` 已停止本地 sqlite 持久化，surface catalog、用户启用状态与日志查询均改为通过 Hub 调 `sql_db`。
3. `sql_db` 当前仍是唯一正式数据库工具服务，并继续直接持有 sqlite 实现。
4. `storage.share.*` 当前正式工具只有 `storage.share.read` 与 `storage.share.write`。
5. 为支持少量必要的二次调用链路，Hub 与相关 service 已补入 `origin_caller` / `origin_caller_token` 基础机制，`sql_db` 也已支持在显式 `scope_source=origin` 时按原始用户或 surface scope 选库。

当前仍应坚持的路径原则：

1. 首选推荐 `web -> hub -> service -> web`，由 Web/Page 直接调用目标业务 service。
2. `service -> hub -> service` 是支持的补充路径，不是默认推荐路径。
3. SQL 能力虽然统一收敛到 `sql_db`，但这不意味着鼓励业务 service 彼此层层间接转调。

## 0. 方案结论

本轮 SQL 机制改进应按“先定边界，再补 `sql_db` 契约，再迁业务 service，最后清理遗留共享驱动封装”的顺序推进。

最终目标不是新增一个数据库共享 SDK，而是把数据库能力彻底收敛为 `sql_db` 的正式工具面：

- `storage.database.query`
- `storage.database.execute`
- `storage.database.schema`
- `storage.share.read`
- `storage.share.write`

各业务 service 统一通过 Hub 工具平面调用这些工具；只有 `sql_db` 本身和经批准的 Hub 核心基础设施可以直连 sqlite。  
`pkg/hubsvc` 与 `pkg/toolproto` 继续作为跨 service 的唯一共享通信基础；sqlite 驱动封装不再允许作为共享公共包长期存在。

## 1. 当前真实状态

以下事实已由当前仓库代码核验：

1. `sql_db` 已经以工具形式提供 `storage.database.*` 与 `storage.share.*`，入口分发位于 `services/sql_db/cmd/sql_db/main.go`。
2. `sql_db` 当前仍直接 `sql.Open("sqlite", ...)`，这是数据库服务自身实现的一部分，见 `services/sql_db/internal/app/storage_services.go`。
3. `chat_server` 已通过 Hub 工具调用 `storage.database.query` / `storage.database.execute`，见 `services/chat_server/internal/app/hub_database_store.go`。
4. `account` 也已有基于 Hub 工具的数据库客户端实现，见 `services/account/internal/database/client.go`。
5. `surface_manager` 仍直接依赖 `pkg/sqlitedriver` 并持有本地 sqlite store，见 `services/surface_manager/internal/app/sqlite_store.go`。
6. `surface_manager` 同时已经存在经 Hub 调 `storage.database.*` 的探测链路，说明它具备转向 `sql_db` 的基础，见 `services/surface_manager/cmd/surface_manager/main.go`。
7. `hub` 当前仍有两处核心基础设施本地直连 sqlite：`hub/internal/app/user_store.go` 与 `hub/internal/app/startup_snapshot_store.go`。
8. `pkg/sqlitedriver/` 当前仍位于共享包目录，但实际检索只确认被 `surface_manager` 直接调用。

## 2. 问题定义

### 2.1 边界已明确，但代码尚未完全收敛

项目说明已经明确：

1. 数据库访问应收敛到 `sql_db`。
2. `hubsvc` / `toolproto` 是跨 service 共享例外。
3. sqlite 驱动封装属于子项目内部工具，不应继续放在共享包面。

但当前代码还没有完全执行这些规则。

### 2.2 `surface_manager` 是当前最主要的越界点

`surface_manager` 既管理 surface catalog，又保留本地 sqlite schema、事务和查询逻辑。  
这直接违反了“业务 service 不得直连 sqlite”的新边界。

### 2.3 共享包层仍残留实现性工具

`pkg/sqlitedriver/` 的存在会继续诱导其他 service 直接拿 sqlite 当本地实现细节使用。  
如果不收口，架构规则很容易再次失效。

### 2.4 Hub 本地存储需要被视为特批基础设施，而不是例外失控

Hub 的本地 sqlite 路径允许保留，但必须收敛成少量核心基础设施：

1. 不扩散到普通业务功能。
2. 不反向依赖 `sql_db` 形成启动环。
3. 不把“Hub 可以直连 sqlite”误读成“任何模块都可以申请例外”。

### 2.5 旧模式不能再继续兼容

本轮不是围绕旧代码做兼容层，而是直接把旧 service、旧前端调用习惯和旧内部实现迁到新的 tool 边界。  
凡是旧模式与新边界冲突，直接改旧模式本身。

## 3. 目标架构

### 3.1 统一原则

1. 数据库能力统一通过 `sql_db` 的工具面提供。
2. 业务 service 不持有 sqlite 文件路径、不导入 sqlite 驱动、不新增本地数据库封装。
3. `hubsvc` 与 `toolproto` 是跨 service 的唯一共享基础设施。
4. sqlite 驱动封装只允许作为 `hub` / `sql_db` 自身目录内的内部实现。
5. 新边界落地时，不保留长期兼容层。

### 3.2 职责划分

#### `services/sql_db`

负责：

1. scope 解析与物理落盘目录管理。
2. SQL 执行与 schema 查询。
3. `storage.share.*` 共享记录能力。
4. 数据库参数校验、错误模型和调用约束。

不负责：

1. 应用层对象语义。
2. 各业务 service 的领域逻辑包装。
3. 为每个 service 再导出一个新的共享 SDK。

#### `hub`

负责：

1. caller 身份识别与注入。
2. 路由 `storage.database.*` / `storage.share.*` 到 `sql_db`。
3. 对 `sql_db` 的生命周期管理、探活与治理视图。
4. 保留少量特批核心本地数据库能力。

不负责：

1. 替业务 service 生成数据库领域 API。
2. 为旧本地 sqlite 模式兜底兼容。

#### 业务 service（`surface_manager` / `chat_server` / `account` 等）

负责：

1. 直接调用 Hub 工具平面的 `storage.database.*` / `storage.share.*`。
2. 自己维护 SQL 语义与业务对象映射。
3. 删除本地 sqlite 依赖与文件路径心智。

不负责：

1. 管理 sqlite 驱动。
2. 自己维护数据库落盘路径与数据库实例。

## 4. 是否需要“通用存储客户端”

结论：不新增一个跨 service 共享的数据库客户端层。

理由：

1. `sql_db` 的正式边界就是工具面，继续抽一个共享 `pkg/dbsvc` 会在 `sql_db` 之外再造一层半公开存储 SDK，边界会再次变糊。
2. 你已经明确 `pkg/` 里只有 `hubsvc` / `toolproto` 作为通信基础例外；数据库调用助手如果进入 `pkg/`，会破坏这条规则。
3. 真正需要统一的是工具契约，而不是额外的共享客户端。

执行原则：

1. 跨 service 共享层面，只保留 `hubsvc` / `toolproto`。
2. 各 service 可以在自己目录内保留极薄的私有 helper，用于减少重复拼装 Hub tool call；但这类 helper 不进入 `pkg/`，不形成新的共享基础设施层。
3. 若某类工具调用模式高度重复，优先在 `sql_db` 工具契约本身补齐参数与语义，而不是在外部再包一层公共 SDK。

## 5. 改造分期

### Phase 1：边界与目录收口

目标：

1. 把“谁能直连 sqlite、谁不能”变成明确、可执行的代码边界。
2. 为后续删除 `pkg/sqlitedriver` 做准备。

任务：

1. 盘点全仓 `sql.Open("sqlite"...`、`modernc.org/sqlite`、`pkg/sqlitedriver` 的真实调用面。
2. 确认白名单仅保留：
   - `services/sql_db/**`
   - `hub/**` 中经批准的核心基础设施路径
3. 在项目说明中固化边界和例外说明。
4. 冻结新增业务 service 直连 sqlite 的可能性。

产出：

1. 文档边界已更新。
2. 白名单清单可供后续 lint / CI 使用。

### Phase 2：增强 `sql_db` 工具契约

目标：

1. 让 `sql_db` 成为足够稳定、足够完整的唯一数据库能力入口。
2. 减少业务 service 迁移时反复改工具契约。

任务：

1. 复核 `storage.database.query`、`execute`、`schema` 的参数与返回结构。
2. 明确并统一以下语义：
   - `scope`
   - `user_id`
   - `surface_id`
   - `service_id`
   - `db_name`
   - `query`
   - `args`
3. 明确错误模型：
   - 参数错误
   - scope 错误
   - SQL 执行错误
   - 调用权限错误
4. 复核 `storage.share.read/write` 是否仍有语义缺口；缺口应优先补到工具契约本身。
5. 评估是否需要新增少量数据库工具，但新增工具也必须直接属于 `sql_db` 工具面，而不是外置 SDK。

验收：

1. `sql_db` 工具契约足以覆盖现有业务 service 迁移。
2. 业务 service 不需要依赖额外共享数据库客户端。

### Phase 3：迁移 `surface_manager`

目标：

1. 去掉 `surface_manager` 本地 sqlite。
2. 让其全部数据库访问通过 Hub -> `sql_db`。

任务：

1. 找出 `sqlite_store.go` 承担的全部职责：
   - surface catalog
   - user_surfaces
   - logs / messages
   - 其他关系表
2. 按现有方法边界，把每个本地 SQL 操作改成 Hub tool call：
   - 建表
   - 查询
   - 写入
   - 事务型更新
3. 删除 `surface_manager` 对 `pkg/sqlitedriver` 的依赖。
4. 删除 `surface_manager` 内部对 sqlite 文件路径的配置和假设。
5. 同步改 `surface_manager` 的相关前端/调用路径，不保留旧本地模式分支。

验收：

1. `surface_manager` 不再 import `pkg/sqlitedriver`。
2. `surface_manager` 不再直接 `sql.Open(...)`。
3. `ui.surface.*` 主链路只通过 Hub 工具平面访问数据库与文件能力。

### Phase 4：清理 `pkg/sqlitedriver` 并内聚驱动封装

目标：

1. 把 sqlite 驱动封装迁回子项目内部。
2. 删除共享实现性工具。

任务：

1. 在 `services/sql_db` 自身目录内保留或重建内部 sqlite 打开 helper。
2. 在 `hub` 自身目录内保留或重建内部 sqlite 打开 helper，仅供核心基础设施使用。
3. 删除 `pkg/sqlitedriver/`。
4. 全仓替换残余引用。

验收：

1. `pkg/sqlitedriver/` 不再存在。
2. `hub` / `sql_db` 之外没有 sqlite 驱动注册或打开逻辑。

### Phase 5：收口 Hub 例外边界

目标：

1. 保留 Hub 本地数据库能力，但把范围控制到最小。

任务：

1. 明确 `hub/internal/app/user_store.go` 与 `startup_snapshot_store.go` 的基础设施属性。
2. 禁止把新的业务数据持久化继续堆进 Hub 本地库。
3. 复核 Hub 是否还有其他潜在本地数据库扩张点。
4. 在文档与代码注释层明确：Hub 本地数据库是“特批基础设施”，不是通用模式。

验收：

1. Hub 本地 sqlite 范围可枚举、可说明。
2. Hub 不出现新的业务层数据库直连实现。

### Phase 6：建立防回流约束

目标：

1. 防止后续再出现业务 service 直连 sqlite。

任务：

1. 增加仓库检查脚本或 CI 规则，扫描：
   - `services/**` 中的 `modernc.org/sqlite`
   - `services/**` 中的 `sql.Open("sqlite"...`
   - `services/**` 中的 `pkg/sqlitedriver`
2. 对白名单路径放行：
   - `services/sql_db/**`
   - `hub/**` 批准路径
3. 把这条检查纳入常规验证。

验收：

1. 新增越界实现会被自动检查阻断。

## 6. 关键设计决策

### 6.1 不做旧模式兼容

本轮不保留以下类型的长期兼容：

1. `surface_manager` 新旧两套 sqlite 路径并存
2. 前端按旧本地模式和新 tool 模式双分支运行
3. 为旧 schema、旧调用路径长期保留双写或双读

允许的仅是短时迁移脚本或一次性数据搬运，不允许长期共存逻辑。

### 6.2 SQL 语义仍由业务 service 自己负责

统一到 `sql_db` 不意味着 `sql_db` 替业务 service 提供领域 API。  
例如：

1. `chat_server` 仍自己决定 `messages`、`projects`、`threads` 的 SQL 和对象映射。
2. `surface_manager` 仍自己决定 `surfaces`、`user_surfaces` 的 schema 和更新策略。
3. `sql_db` 只提供执行这些 SQL 所需的统一工具面与 scope 管理。

### 6.3 Hub 不替代 `sql_db`

Hub 只负责治理、路由和身份，不提供第二套数据库抽象。  
这样可以避免：

1. Hub 和 `sql_db` 出现职责重复。
2. 业务 service 面向两套数据库入口编程。
3. 后续数据问题定位时边界不清。

## 7. 实施顺序建议

建议按以下顺序落地：

1. 完成文档与边界冻结。
2. 先补 `sql_db` 工具契约缺口。
3. 先改 `surface_manager`，因为它是当前主要越界点。
4. 再清理 `pkg/sqlitedriver`，把驱动封装迁回 `hub` / `sql_db` 内部。
5. 最后补自动检查，防止回流。

这样做的原因是：

1. 先删 `pkg/sqlitedriver` 会让 `surface_manager` 改造无落点。
2. 先迁 `surface_manager`，可以直接验证 `sql_db` 工具面是否足够。
3. 最后再加检查，避免在改造中途自己被规则卡住。

## 8. 风险与应对

### 8.1 `surface_manager` SQL 面积较大

风险：

1. 其本地 store 不是单点，而是一整套 catalog/logs 存储。
2. 粗暴替换容易漏掉初始化、重置、事务更新语义。

应对：

1. 先列方法清单，再逐个迁移。
2. 先做编译级收口，再做运行级验证。

### 8.2 `sql_db` 工具契约可能不够用

风险：

1. 迁移到一半发现需要新的工具参数或错误模型。

应对：

1. 在 Phase 2 先做契约复核。
2. 缺什么就直接补 `sql_db` tool，而不是临时造共享客户端层。

### 8.3 Hub 例外边界容易被滥用

风险：

1. 一旦不写清楚，后续可能把更多业务存储塞回 Hub。

应对：

1. 明确白名单。
2. 代码检查只对批准路径放行。

## 9. 验证与验收

### 9.1 静态验证

1. `rg` 检查 `services/**` 中是否仍存在 sqlite 直连。
2. 核对 `pkg/sqlitedriver/` 是否已删除。
3. 核对 `surface_manager` 是否仅通过 Hub 调 `storage.database.*`。

### 9.2 编译验证

1. `go test -run '^$' ./services/sql_db/...`
2. `go test -run '^$' ./services/surface_manager/...`
3. `go test -run '^$' ./services/chat_server/...`
4. `go test -run '^$' ./services/account/...`
5. `go test -run '^$' ./hub/...`

### 9.3 运行级验收

1. 通过 Hub 调用 `storage.database.schema`
2. 验证 `chat_server` 数据路径可读写
3. 验证 `surface_manager` catalog / session 相关主链路可读写
4. 验证 Hub 本地用户库与启动快照不受影响

## 10. 完成标准

满足以下条件才算本轮 SQL 机制改进完成：

1. `sql_db` 成为唯一正式数据库工具服务。
2. `surface_manager` 不再直连 sqlite。
3. `hub` 仅保留经批准的核心本地 sqlite 基础设施路径。
4. `pkg/sqlitedriver/` 被移除，驱动封装收回 `hub` / `sql_db` 内部。
5. 不新增跨 service 共享数据库客户端层。
6. 自动检查能阻止业务 service 再次直连 sqlite。
7. 相关前端和旧调用模式已同步迁到新边界，没有长期兼容分支残留。

## 11. 信息来源

1. `doc/_instruction/core.md`
2. `doc/_instruction/structure.md`
3. `doc/_note.md`
4. `services/sql_db/cmd/sql_db/main.go`
5. `services/sql_db/internal/app/hub_builtins.go`
6. `services/sql_db/internal/app/storage_services.go`
7. `services/chat_server/internal/app/hub_database_store.go`
8. `services/account/internal/database/client.go`
9. `services/surface_manager/internal/app/sqlite_store.go`
10. `services/surface_manager/cmd/surface_manager/main.go`
11. `hub/internal/app/user_store.go`
12. `hub/internal/app/startup_snapshot_store.go`
13. `plan/2603190351-sql_db-rebuild-devplan.md`
14. `plan/2603192104-service-standard-full-refactor-devplan.md`
