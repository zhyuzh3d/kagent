# services/sql_db 重建开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 03:51 CST  
> 范围：`services/sql_db/`、`hub/internal/{gateway,supervisor,routing}/`、`pkg/toolproto/`、`config/`、`hub/config/`  
> 目标约束：新子项目必须作为当前 `database` 的替代实现，且只保留数据库与共享存储相关工具。

## 0. 结论

当前 `database` 服务已经是一个明确的 storage/database 工具服务，但目录名与运行配置仍是旧布局。  
新子项目 `services/sql_db` 的目标是把数据库工具能力和共享存储能力重新整理成一个独立、可替换、可通过 Hub tool 平面调度的实现。

新子项目必须保留的工具集合为：

- `storage.database.query`
- `storage.database.execute`
- `storage.database.schema`
- `storage.share.read`
- `storage.share.write`
- `service.lifecycle.health`
- `service.lifecycle.shutdown`

## 1. 真实核验事实

以下事实已由当前仓库代码核验：

- `services/database/cmd/database/main.go` 已暴露 `POST /service/tool/exec`。
- `services/database/cmd/database/main.go` 已分发 `storage.database.*` 与 `storage.share.*` 工具。
- `services/database/cmd/database/main.go` 仍保留 `/healthz`、`/service/info`、`/service/tools`、`/admin/shutdown` 和 `/api/service/heartbeat` 改写逻辑。
- `services/database/internal/app/hub_builtins.go` 已声明数据库与共享存储工具的 manifest。
- `services/database/internal/app/runtime_root.go` 仍以 `services/database` 为根目录候选。
- Hub 侧生命周期管理已可通过 `service.lifecycle.*` 完成控制。

## 2. 问题定义

### 2.1 目录与运行时边界仍旧

数据库能力已经工具化，但目录名和运行态仍与旧服务保持一致，不利于替代实现的独立演进。

### 2.2 旧兼容路径仍然存在

`/admin/shutdown` 与心跳路径改写逻辑仍在，说明服务还没有完全收口到新架构的正式契约。

### 2.3 共享存储职责需要显式边界

`storage.share.*` 与 `storage.database.*` 必须在 manifest、权限和 dispatcher 三个层面同时明确，否则 Hub 端很难稳定治理。

## 3. 重建目标

### 3.1 对外目标

`services/sql_db` 只提供数据库查询、执行、schema 与共享存储读写能力，并保留标准生命周期工具：

- `storage.database.*`
- `storage.share.*`
- `service.lifecycle.*`

### 3.2 对内目标

1. `cmd/` 只做启动、注册、tool dispatcher 和生命周期。
2. `internal/app/` 只保留数据库执行、共享存储、manifest helper 和必要的运行时根目录逻辑。
3. 不把旧 `database` 目录中的过渡语义继续复制到新实现。

### 3.3 迁移约束

- 工具名不改。
- 先切换目录，再清理旧实现。
- 兼容路径只允许短期存在，不允许继续扩散。

## 4. 实施分期

### Phase 1：新子项目骨架

任务：

1. 创建 `services/sql_db/` 目录结构。
2. 迁移当前 `database` 服务的入口、manifest、store 逻辑和必要辅助代码。
3. 调整 `runtime_root.go`，让其识别 `services/sql_db`。

验收：

- `go build ./services/sql_db/...` 通过。
- 新目录可独立启动并读取运行时数据。

### Phase 2：工具集与生命周期对齐

任务：

1. 保留所有 `storage.database.*` 与 `storage.share.*` 工具。
2. 保留 `service.lifecycle.health` / `service.lifecycle.shutdown`。
3. 统一 manifest 输出 `AllowedCallerTypes`、`Streaming`、`WSPath` 等字段的真实值。
4. 清理新实现中的 `/healthz`、`/service/info`、`/service/tools`、`/admin/shutdown` 正式依赖。

验收：

- Hub 侧能把 sql_db 视为一个标准 tool service。
- 调用方只通过 tool 平面使用数据库能力。

### Phase 3：Hub 与配置切换

任务：

1. 更新 `config/services.json` 与 `hub/config/services.json`，把 `database` 的目录切换到 `services/sql_db`。
2. 同步 `hub/config/config.json` 中相关引用。
3. 验证 Hub 通过 tool 平面完成注册、心跳和生命周期控制。

验收：

- Hub 拉起的新目录服务可用。
- 旧目录不再是运行事实来源。

### Phase 4：旧目录收口

任务：

1. 冻结旧 `services/database`，避免继续新增能力。
2. 逐步删除或迁出兼容路径与心跳改写逻辑。
3. 只保留正式 tool 契约的实现面。

验收：

- 新调用方只依赖 `services/sql_db`。
- 兼容入口不再是正式契约的一部分。

## 5. 验证方案

1. `go build ./services/sql_db/...`
2. 验证 `storage.database.query`
3. 验证 `storage.database.execute`
4. 验证 `storage.database.schema`
5. 验证 `storage.share.read` 与 `storage.share.write`
6. 验证 `service.lifecycle.health` 与 `service.lifecycle.shutdown`

## 6. 风险与约束

- SQL 执行链路最容易出现权限和参数边界问题，迁移后必须做回归验证。
- 若 `storage.share.write` 的 caller 限制与 Hub 预期不一致，会影响写入路径。
- 删除旧兼容路径前必须确认生命周期工具已稳定。

## 7. 信息来源

- `services/database/cmd/database/main.go`
- `services/database/internal/app/hub_builtins.go`
- `services/database/internal/app/runtime_root.go`
- `hub/internal/supervisor/process_control.go`
- `config/services.json`
- `hub/config/services.json`
- `hub/config/config.json`
