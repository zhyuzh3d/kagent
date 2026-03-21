# SQLite Design

## 文档元信息

- 更新时间：2026-03-21 12:20 CST
- 适用范围：当前仓库 `hub/`、`services/*` 与仓库根 `data/` 下的 SQLite 使用机制。
- 结论原则：以真实代码、当前磁盘实库和可复现查询为准；无法确认的内容明确标注。
- 本文依据：
  - 代码入口与 schema：`hub/internal/app/*.go`、`services/sql_db/internal/app/*.go`、`services/account/internal/app/store_client.go`、`services/chat_server/internal/app/hub_database_store_*.go`、`services/surface_manager/internal/app/hub_store*.go`
  - 当前实库扫描：`find data -name '*.db' | sort`
  - 当前实库 schema：`sqlite3 <db> ".schema"`、`SELECT ... FROM sqlite_master`

## 1. 总体机制

### 1.1 谁可以直接持有 sqlite 驱动

- 当前仓库里，只有 `hub/internal/app` 和 `services/sql_db/internal/app` 直接导入 `modernc.org/sqlite` 并调用 `sql.Open(...)`。
- 对应入口：
  - `hub/internal/app/sqlite_open.go`
  - `services/sql_db/internal/app/sqlite_open.go`
- 这与 `doc/_instruction/core.md` 中“除 `sql_db` 与获准的 Hub 基础设施外，其他 service 不直接持有 sqlite 驱动”的边界一致。

### 1.2 两条数据库主链

1. Hub 本地基础设施库
   - Hub 直接打开 `data/hub/users.db`。
   - 当前主链实际只用于启动快照持久化。
   - 代码中仍保留一套 legacy `UserStore`，也指向同一文件，但当前没有活跃调用点。

2. sql_db 统一数据库服务
   - `sql_db` 自己直接开 sqlite，并把库文件根目录固定在仓库根 `data/`。
   - 其他业务 service 不自己开库，而是通过 Hub 转调 `storage.database.query` / `storage.database.execute` / `storage.database.schema`。
   - `account`、`chat_server`、`surface_manager` 当前都走这条链。

### 1.3 sql_db 的路径分流规则

`services/sql_db/internal/app/storage_services.go` 当前按 caller scope 把数据库文件路由到不同目录：

- `user` scope -> `data/user/<user_id>/`
- `surface` scope -> `data/user/<user_id>/surface/<surface_id>/`
- `service` scope -> `data/service/<service_id>/`

库名规则：

- 未显式传 `db_name` 时，默认库名是 `kagent.db`
- 显式传 `db_name` 时，自动补 `.db` 后缀
- 最终文件名会落在对应 scope 根目录下

这意味着 `sql_db` 不是“单库模式”，而是“按作用域切库 + 按需命名”的多库模式。

### 1.4 打开参数与连接策略

- Hub 直连 sqlite 的两个 store 都会：
  - `SetMaxOpenConns(1)`
  - `PRAGMA journal_mode=WAL`
  - `PRAGMA foreign_keys=ON`
- `sql_db` 打开任意 scope 库时会：
  - `SetMaxOpenConns(1)`
  - `PRAGMA journal_mode=WAL`
- 当前业务 schema 基本没有外键约束定义，所以 `sql_db` 侧没有额外打开 `foreign_keys=ON`。

### 1.5 谁在建表

- Hub 本地表由 Hub 启动时初始化。
- `account` 在自身初始化阶段通过 `storage.database.execute` 建表。
- `chat_server` 在 `NewHubDatabaseStore(...)` 初始化阶段通过 `storage.database.execute` 建表。
- `surface_manager` 在自身初始化阶段通过 `storage.database.execute` / `storage.share.write` 建表或写共享记录。
- `surface_probe` 仅由 `ui.surface.db_roundtrip` 调试工具按需创建，不属于常规业务主表。

## 2. 当前磁盘上已落盘的 SQLite 库

截至 2026-03-21 当前工作区 `data/` 下可见的 `.db` 文件有：

- `data/hub/users.db`
- `data/service/account/account.db`
- `data/service/surface_manager/surface_manager.db`
- `data/service/_share/_share.db`
- 多个 `data/user/<user_id>/kagent.db`

当前没有发现已落盘的 surface-scope 数据库：

- `find data/user -path '*/surface/*/*.db'` 返回空

这说明 `surface` scope 是代码支持的机制，但当前样本数据里尚未实际生成对应 `.db` 文件。

## 3. 各库设计与表职责

### 3.1 `data/hub/users.db`

#### 当前活跃用途

- 由 `hub/cmd/hub/main.go` 通过 `-sqlite-path` 默认指向 `data/hub/users.db`
- 当前主链用法是 `hub/internal/app/startup_snapshot_store.go`
- 当前实库中只有一张业务表：`hub_startup_snapshots`

#### 表说明

| 表名 | 来源 | 作用 |
| --- | --- | --- |
| `hub_startup_snapshots` | `hub/internal/app/startup_snapshot_store.go` | 保存 Hub 启动后的治理快照，用于 `LoadLatest()` 读取最近一次启动状态。字段里 `payload_json` 保存完整快照 JSON。 |

#### 索引

- `idx_hub_startup_snapshots_created_at`
  - 按 `created_at_ms DESC` 取最近快照

#### 特别说明

- `hub/internal/app/user_store.go` 里仍定义了一套 `users` 表 schema：
  - `users(user_id, username, password_hash, created_at_ms, updated_at_ms)`
- 但当前仓库没有 `NewUserStore(...)` 的活跃调用点。
- 当前实库 `data/hub/users.db` 也没有 `users` 表。
- 因此，`UserStore` 应视为当前未接入主链的 legacy 代码，而不是当前运行事实。

### 3.2 `data/service/account/account.db`

#### 当前活跃用途

- 由 `services/account/internal/app/store_client.go` 固定使用 `db_name = "account.db"`
- 由 `account` service 以 `service` scope 调 `sql_db`
- 落盘路径因此固定为 `data/service/account/account.db`
- `services/account/internal/app/app.go` 在初始化时先 `EnsureSchema()`，再 `GetOrCreateSigningKey()`

#### 表说明

| 表名 | 来源 | 作用 |
| --- | --- | --- |
| `users` | `services/account/internal/app/store_client.go` | 账号主表，保存平台账号的 `user_id`、`username`、密码哈希以及创建/更新时间。 |
| `active_sessions` | `services/account/internal/app/store_client.go` | 当前生效会话表，按 `user_id` 只保留一条 `sid`，用于单会话校验和踢旧会话。 |
| `signing_keys` | `services/account/internal/app/store_client.go` | Account Service 的 Ed25519 签名密钥表，保存 `kid`、算法、公钥、私钥和创建时间；用于签发和轮换账号 token。 |

#### 索引

- `idx_account_users_username`
  - 用户名查找索引，用于登录和重复用户名校验

### 3.3 `data/service/surface_manager/surface_manager.db`

#### 当前活跃用途

- 由 `services/surface_manager/internal/app/hub_store.go` 固定使用 `db_name = "surface_manager.db"`
- `surface_manager` 以 `service` caller 身份通过 Hub 调 `sql_db`
- 落盘路径固定为 `data/service/surface_manager/surface_manager.db`
- 启动时会执行 `EnsureSchema()`

#### 表说明

| 表名 | 来源 | 作用 |
| --- | --- | --- |
| `user_surfaces` | `services/surface_manager/internal/app/hub_store_schema.go` | 用户与 surface 的启用状态表；主键是 `(user_id, surface_id)`，记录某个用户是否启用了某个 surface。 |
| `surface_logs` | `services/surface_manager/internal/app/hub_store_schema.go` | surface 侧日志/消息表，记录 `surface_id` 维度的消息类别、消息类型、正文、原始 payload 和语义时间字段。 |

#### 索引

- `idx_surface_user_surfaces_user`
  - 按用户查询 surface 开关状态
- `idx_surface_logs_surface_time`
  - 按 `surface_id + created_at_ms DESC` 取最近日志

#### 按需调试表

| 表名 | 来源 | 状态 | 作用 |
| --- | --- | --- | --- |
| `surface_probe` | `services/surface_manager/cmd/surface_manager/tool_http_handler.go` 中的 `ui.surface.db_roundtrip` | 当前实库未发现 | 仅用于调试 roundtrip：验证 `surface_manager -> hub -> sql_db` 的服务侧数据库写读链路。 |

### 3.4 `data/service/_share/_share.db`

#### 当前活跃用途

- 由 `services/sql_db/cmd/sql_db/bootstrap_runtime.go` 的 `storage.share.write` / `storage.share.read` 按需创建
- 固定 target：
  - `Scope = "service"`
  - `ServiceID = "_share"`
  - `DBName = "_share.db"`
- 所有 service 间共享记录都汇总在这一库中

#### 表说明

| 表名 | 来源 | 作用 |
| --- | --- | --- |
| `share_records` | `services/sql_db/cmd/sql_db/bootstrap_runtime.go` | 跨 service 共享记录表。用 `(namespace, category, service_id, key)` 作为唯一业务键，`value_json` 保存 JSON 值，`visibility` 表示可见性。当前 `surface_manager` 会通过 `storage.share.write/read` 使用这张表保存 surface catalog 等共享数据。 |

#### 约束

- 主键：`id`
- 业务唯一约束：`UNIQUE(namespace, category, service_id, key)`

### 3.5 `data/user/<user_id>/kagent.db`

#### 当前活跃用途

- 由 `chat_server` 通过 `storage.database.*` 使用
- `services/chat_server/internal/app/hub_database_store_client.go` 调用时显式传 `scope_source = "origin"`
- 这意味着当 `chat_server` 代表某个用户工作时，最终实际落到原始用户的 `user` scope
- 未传 `db_name`，所以默认文件名是 `kagent.db`
- 每个用户一库，路径模式为 `data/user/<user_id>/kagent.db`

#### 表说明

| 表名 | 来源 | 作用 |
| --- | --- | --- |
| `users` | `services/chat_server/internal/app/hub_database_store_schema.go` | Chat 侧用户存在性锚点表。当前只记录 `user_id` 与创建时间，用于确保用户级会话空间已初始化。 |
| `projects` | `services/chat_server/internal/app/hub_database_store_schema.go` | 聊天项目表。每个项目挂在某个用户下，记录标题、创建时间、最近活跃时间、语义时间字段与排序序号。 |
| `threads` | `services/chat_server/internal/app/hub_database_store_schema.go` | 项目内线程表。每个线程属于一个 `project_id` 和 `user_id`，记录标题、时间和排序序号。 |
| `messages` | `services/chat_server/internal/app/hub_database_store_schema.go` | 聊天消息主表。保存 turn 内消息序号、角色、内容、action JSON、payload JSON、completion/interruption 状态和多套语义时间字段，是当前聊天历史的核心事实表。 |
| `sqlite_sequence` | SQLite 自带 | `messages.id INTEGER PRIMARY KEY AUTOINCREMENT` 生成的内部序列表，不是业务表。 |

#### 索引

- `idx_messages_scope`
  - 按 `(user_id, project_id, thread_id, id)` 读取线程消息窗口
- `idx_projects_user`
  - 按用户读取项目列表与排序
- `idx_threads_project`
  - 按项目读取线程列表与排序

### 3.6 `data/user/<user_id>/surface/<surface_id>/kagent.db`

#### 机制状态

- 这是 `sql_db` 的正式支持路径之一
- 当 caller 被解析为 `surface` scope 且未指定 `db_name` 时，会落到该路径
- 当前代码没有给这条路径预设固定 schema
- 当前工作区数据里也没有实际落盘文件

#### 当前结论

- 这是“机制支持但尚未观察到实库”的路径
- 哪些表会出现，取决于未来具体哪个 service 在 `surface` scope 下执行了哪些 `CREATE TABLE`

### 3.7 其他按需命名数据库

除上面已观察到的固定库名外，`sql_db` 还支持任意 `db_name` 覆盖，因此理论上还可能出现：

- `data/user/<user_id>/<custom>.db`
- `data/user/<user_id>/surface/<surface_id>/<custom>.db`
- `data/service/<service_id>/<custom>.db`

当前仓库内已知的命名实例只有：

- `account.db`
- `surface_manager.db`
- `_share.db`
- 默认 `kagent.db`

## 4. 表级总表

下表只列当前代码中已明确定义过用途的表：

| 库 | 表 | 当前状态 | 作用 |
| --- | --- | --- | --- |
| `data/hub/users.db` | `hub_startup_snapshots` | 活跃 | Hub 启动治理快照 |
| `data/hub/users.db` | `users` | legacy 代码定义，当前实库无此表 | 旧 Hub 本地账号表 |
| `data/service/account/account.db` | `users` | 活跃 | 平台账号与密码哈希 |
| `data/service/account/account.db` | `active_sessions` | 活跃 | 单用户当前活动 `sid` |
| `data/service/account/account.db` | `signing_keys` | 活跃 | Account token 签名密钥 |
| `data/service/surface_manager/surface_manager.db` | `user_surfaces` | 活跃 | 用户启用的 surface 状态 |
| `data/service/surface_manager/surface_manager.db` | `surface_logs` | 活跃 | surface 运行消息与日志 |
| `data/service/surface_manager/surface_manager.db` | `surface_probe` | 调试按需创建，当前实库无此表 | `ui.surface.db_roundtrip` 探针 |
| `data/service/_share/_share.db` | `share_records` | 活跃 | service 间共享 JSON 记录 |
| `data/user/<user_id>/kagent.db` | `users` | 活跃 | Chat 用户锚点 |
| `data/user/<user_id>/kagent.db` | `projects` | 活跃 | Chat 项目 |
| `data/user/<user_id>/kagent.db` | `threads` | 活跃 | Chat 线程 |
| `data/user/<user_id>/kagent.db` | `messages` | 活跃 | Chat 消息与 action/payload 历史 |
| `data/user/<user_id>/kagent.db` | `sqlite_sequence` | SQLite 内部表 | AUTOINCREMENT 序列元数据 |

## 5. 当前实现中的几个关键判断

### 5.1 Hub 本地用户库已退出主链

- 代码中仍保留 `hub/internal/app/user_store.go`
- 但当前 `hub/cmd/hub/main.go` 只初始化：
  - `AuthService`
  - `HubPlatform`
  - `StartupSnapshotStore`
- 没有初始化 `UserStore`
- 当前实库 `data/hub/users.db` 也只包含 `hub_startup_snapshots`

因此当前账号主数据的真实事实源已经迁到 `data/service/account/account.db`，而不是 Hub 本地库。

### 5.2 Chat 数据是“用户一库”而不是“全局单库”

- `chat_server` 查询/执行数据库时都显式传 `scope_source = "origin"`
- `sql_db` 会据此恢复原始用户 caller
- 未指定 `db_name` 时默认落在 `data/user/<user_id>/kagent.db`

这意味着聊天项目、线程、消息数据天然按用户分库隔离。

### 5.3 Surface 的“目录扫描共享事实”和“用户启用状态”不在同一个表

- `surface_manager.db` 里的 `user_surfaces` / `surface_logs` 是 service 私有库
- 跨 service 共享事实则走 `_share.db` 中的 `share_records`

因此 `surface_manager` 当前采用“私有 service 库 + 平台共享库”双轨存储，而不是把所有数据都放进单一 `surface_manager.db`。

## 6. 待确认事项

- 当前没有发现任何活跃调用会创建 `data/user/<user_id>/surface/<surface_id>/kagent.db`；该路径目前只能确认“机制存在”，还不能确认具体业务表设计。
- `hub/internal/app/user_store.go` 是明确的 legacy 候选代码，但在正式删除前，仍应以调用点扫描和启动验证为准。

## 7. 证据清单

- Hub sqlite 入口：`hub/internal/app/sqlite_open.go`
- Hub 启动快照表：`hub/internal/app/startup_snapshot_store.go`
- Hub legacy 用户表：`hub/internal/app/user_store.go`
- sql_db scope 路由与开库：`services/sql_db/internal/app/storage_services.go`
- sql_db 共享表：`services/sql_db/cmd/sql_db/bootstrap_runtime.go`
- account schema：`services/account/internal/app/store_client.go`
- chat schema：`services/chat_server/internal/app/hub_database_store_schema.go`
- chat 走 origin user scope：`services/chat_server/internal/app/hub_database_store_client.go`
- surface_manager schema：`services/surface_manager/internal/app/hub_store_schema.go`
- surface_manager 共享记录调用：`services/surface_manager/internal/app/hub_store_client.go`
- surface probe 调试表：`services/surface_manager/cmd/surface_manager/tool_http_handler.go`
