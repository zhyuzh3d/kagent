# Service Name Underscore Unification Dev Plan

## 1. 计划结论

本次改造应按“先冻结目标命名表，再改治理配置与运行工件，再改各 service 内部标识，最后做文档与验证收口”的顺序执行，避免出现目录名、`service_id`、二进制名、`run/` 工件名和 Hub 路由名短时间失配。目标不是只改 `services/surface-manager` 一处目录，而是把仓库内 service 标识全面收敛到统一的 underscore 体系，并确保 Hub、脚本、manifest、注册/心跳、自报信息、代码标识和测试期望全部一致。

本轮建议固定的目标 service 名称如下：

| 现状 | 目标 |
| --- | --- |
| `account` | `account` |
| `ai-doubao` | `ai_doubao` |
| `chat-server` | `chat_server` |
| `file` | `file_storage` |
| `database` | `sql_db` |
| `surface-manager` | `surface_manager` |

## 2. 当前真实现状

### 2.1 目录层

1. 当前 `services/` 第一层目录已部分切换为 underscore：`account`、`ai_doubao`、`chat_server`、`file_storage`、`sql_db`。
2. `services/surface-manager` 仍是连字符目录，尚未与其余 service 的目录命名风格一致。
3. 当前每个 service 下都存在 `run/` 目录，说明运行工件路径已经成为改名影响面的一部分，而不是单纯源码目录重命名。

### 2.2 治理配置与运行工件层

1. [`hub/config/services.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/services.json) 仍将 `service_id` 配成 `ai-doubao`、`chat-server`、`file`、`database`、`surface-manager`，且 `surface-manager` 的 `dir` 也还是 `services/surface-manager`。
2. [`scripts/deploy.sh`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh) 使用 `service_id` 同时驱动 `./<sdir>/cmd/<sid>` 和 `run/${sid}-latest`，因此 `service_id` 变更会直接影响构建入口目录名和产物名。
3. Hub 默认配置 [`hub/cmd/hub/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go) 仍内嵌旧 service 名，包括：
   - `serviceDirs`
   - 默认 TCP endpoint map
   - 各种 `*-service-url` flag 名与说明文字

### 2.3 Manifest / 注册 / 自报层

1. `services/ai_doubao/manifest.json` 的 `service_id` 仍是 `ai-doubao`。
2. `services/chat_server/manifest.json` 的 `service_id` 仍是 `chat-server`。
3. `services/file_storage/manifest.json` 的 `service_id` 仍是 `file`。
4. `services/sql_db/manifest.json` 的 `service_id` 仍是 `database`。
5. `services/surface-manager/manifest.json` 的 `service_id` 仍是 `surface-manager`。
6. 各 service 主入口还会把这些旧值继续用于：
   - bootstrap `service_id` 校验
   - 默认 `instance_id` 前缀
   - register / heartbeat payload
   - `service.lifecycle.*` 响应体
   - `/service/info` 或类似自报信息中的 `ServiceID` / `Provider`
   - 日志前缀与错误信息

### 2.4 代码与测试层

1. `ai_doubao`、`chat_server`、`file_storage`、`sql_db`、`surface-manager` 的 `cmd/*/main.go` 中存在大量旧 service 名硬编码。
2. [`services/sql_db/internal/app/hub_builtins.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/internal/app/hub_builtins.go) 仍把多个 service 视图项写成旧名，如 `file`、`database`、`surface-manager`、`chat-server`、`ai-doubao`。
3. Hub 测试仍使用旧名，例如 [`hub/internal/supervisor/registry_test.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/registry_test.go) 和 [`hub/internal/routing/schema_test.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/routing/schema_test.go) 中仍以 `database` 为 service 断言值。
4. 运行时路径、import path、目录名、二进制名和字符串常量已经交织，说明这不是简单的批量替换任务，必须分层推进。

## 3. 目标范围

### 3.1 必须统一的标识面

1. `services/` 第一层目录名。
2. `hub/config/services.json` 中的 `service_id` 与 `dir`。
3. 所有 `services/*/manifest.json` 中的 `service_id`、必要时 `service_name`。
4. `scripts/deploy.sh` 依赖的 `cmd/<svc>` 目录名、构建输出名 `run/<svc>-latest`、`run/manifest.json` 路径。
5. `services/<svc>/run/.service_secret`、`run/manifest.json` 等运行工件访问路径。
6. service register / heartbeat / state / shutdown 等 payload 中的 `service_id` 与 `instance_id` 前缀。
7. Hub 内部保存、查询、路由、转发和暴露给外部的 service 标识。
8. 代码中的文件名、类型名、结构体名、变量名、日志文案、常量名中凡是表达 service 名语义的部分。

### 3.2 本轮不建议主动改动的部分

1. 工具命名空间如 `storage.file.*`、`storage.database.*` 不必因为 service 名统一而同步重命名。
2. 非 service 语义的领域词，例如 Go 标准库 `database/sql`、业务意义上的 `surface manager` UI class name，不应机械替换。
3. 仅在历史 devlog 中作为历史记录出现的旧名，不应篡改历史；如果后续要同步文档，应以“新增说明”而非改写历史方式处理。

## 4. 实施策略

### Phase 1：冻结目标命名表与替换边界

1. 先建立单一映射表，作为全仓唯一真值：
   - `ai-doubao -> ai_doubao`
   - `chat-server -> chat_server`
   - `file -> file_storage`
   - `database -> sql_db`
   - `surface-manager -> surface_manager`
2. 对每个旧名做一次“语义归类”：
   - service id
   - 目录名
   - `cmd/<svc>` 入口名
   - 二进制名
   - `instance_id` 前缀
   - import path
   - 普通展示文案
3. 先列出允许保留旧词的白名单，避免误伤：
   - `storage.file.*`
   - `storage.database.*`
   - `database/sql`
   - 非 service 标识含义的前端 CSS class / 文案

### Phase 2：目录与构建入口统一

1. 将 `services/surface-manager` 重命名为 `services/surface_manager`。
2. 同步调整其 `cmd` 入口目录与 import path，目标应与部署脚本约定一致。
3. 核对现有 `cmd/<svc>` 目录是否也需要统一成 underscore 版本：
   - `services/ai_doubao/cmd/ai-doubao` 是否改为 `cmd/ai_doubao`
   - `services/chat_server/cmd/chat-server` 是否改为 `cmd/chat_server`
   - `services/file_storage/cmd/file` 是否改为 `cmd/file_storage`
   - `services/sql_db/cmd/database` 是否改为 `cmd/sql_db`
   - `services/surface_manager/cmd/surface-manager` 是否改为 `cmd/surface_manager`
4. 这一步必须与 [`scripts/deploy.sh`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh) 和 Hub lifecycle 配置一起修改，否则构建会立即断裂。

### Phase 3：治理配置、manifest 与 run 工件统一

1. 修改 [`hub/config/services.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/services.json)：
   - `service_id` 全部切到 underscore
   - `dir` 指向实际目录名
2. 修改各 service 的 `manifest.json`：
   - `service_id` 与目录名一致
   - `entry.args` 中涉及自身配置/运行目录的路径与目录名一致
3. 核对所有 `run/` 访问路径：
   - `services/<svc>/run/.service_secret`
   - `services/<svc>/run/manifest.json`
   - `services/<svc>/run/${svc}-latest`
4. 明确 `run/<svc>` 中的 `<svc>` 采用“service 逻辑名”还是“cmd 入口名”；本次需求要求它与自身项目文件夹名一致，因此最终应统一为目录名。

### Phase 4：Hub 侧 service 识别统一

1. 修改 [`hub/cmd/hub/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go) 中的：
   - `serviceDirs`
   - endpoint map
   - `flag` 名与说明文字
2. 核对 Hub 其余模块里凡是按 service id 做索引、比较、选择、注入 header 或暴露状态的逻辑：
   - `hub/internal/app/hub_platform.go`
   - `hub/internal/supervisor/*`
   - `hub/internal/routing/*`
   - `hub/internal/security/*`
   - `hub/internal/gateway/*`
3. 统一后需保证 Hub 对外提供的状态、路由、审计、治理工具看到的 service id 也全部变成 underscore。

### Phase 5：各 service 内部标识统一

1. 逐个 service 处理 `cmd/*/main.go` 内的硬编码旧名：
   - bootstrap 校验值
   - 默认实例名前缀
   - register/heartbeat payload
   - state/self-info payload
   - lifecycle alias
   - 日志信息
2. 逐个 service 处理 `internal/` 下的命名：
   - 文件名
   - 结构体名
   - 常量名
   - 变量名
   - helper 名
3. 保持“只改 service 标识语义，不改业务领域词”的边界，避免把 `database` 这种领域概念误改成 `sql_db`。
4. 对 `surface_manager` 额外核对 import path、路径拼接和默认 sqlite 文件名中是否含旧 service 名。

### Phase 6：测试、脚本与文档收口

1. 修改所有基于旧 service id 的单元测试与断言。
2. 修改部署/烟测脚本中对 service 名、二进制名、pid 文件或治理 payload 的依赖。
3. 若本轮实际实施后需要同步说明文档，应更新：
   - [`doc/_instruction/structure.md`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md)
   - [`doc/_instruction/core.md`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md)
   - 追加 [`doc/_devlog.md`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_devlog.md)
4. 历史 plan / devlog 不追溯批量改写，只在新文档中说明“服务命名统一已完成，旧记录保留历史名”。

## 5. 重点风险

1. `scripts/deploy.sh` 当前把 `service_id` 同时用于 `cmd/<sid>` 和 `run/${sid}-latest`；如果只改 `service_id` 不改入口目录，会直接导致构建失败。
2. `surface-manager` 同时涉及目录重命名、import path 变更、运行路径拼接和 Hub 配置修改，是本轮最高风险点。
3. `file -> file_storage`、`database -> sql_db` 不只是字符替换，而是“旧通用词”切换到“新项目名”；误替换概率高于 `chat-server -> chat_server`。
4. Hub 与 service 之间的 bootstrap secret、header 认证和 register/heartbeat 依赖精确的 `service_id` / `instance_id`，一旦中间态不一致，会出现服务注册失败或心跳被拒。
5. 测试和脚本中的旧名若漏改，可能导致功能代码正确但自动化回归持续失败。

## 6. 建议的开发拆分

### 6.1 先做机械一致性层

1. 目录名
2. `cmd/<svc>` 入口名
3. `manifest.json`
4. `hub/config/services.json`
5. `scripts/deploy.sh`
6. service 主入口中的 bootstrap / register / heartbeat / instance prefix

### 6.2 再做语义命名层

1. service 自报信息中的 `ServiceID` / `ServiceName` / `Provider`
2. Hub 内部默认 endpoint key、状态视图、路由断言
3. 类型名、变量名、日志文案、辅助函数名

### 6.3 最后做验证与文档层

1. 全仓旧 service 标识残留检索
2. 编译级回归
3. 生命周期/注册链路验证
4. 说明文档与开发日志同步

## 7. 验收标准

1. `services/` 第一层目录中不再存在 `surface-manager`，统一为 `surface_manager`。
2. 每个 service 的 `service_id`、目录名、`cmd/<svc>` 名、`run/${svc}-latest` 名、`run/.service_secret` 路径约定一致。
3. Hub 生命周期配置、默认 endpoint map、register/heartbeat/state/shutdown payload 中的 service id 全部改成 underscore 版本。
4. Hub 提供给外部的 service 视图、路由与治理信息中，不再出现旧 service 名。
5. 每个 service 内部凡是表达“自身 service 名”的文件名、类名、变量名、常量名与日志标识都已统一。
6. `rg` 检索旧名后，仅允许剩余：
   - 历史 devlog / 旧 plan 中的历史文本
   - 明确声明保留兼容的 alias
   - 非 service 语义的领域词或标准库引用
7. 通过与本次改动直接相关的编译和运行验证。

## 8. 建议验证方案

1. 残留检索：
   - `rg -n "ai-doubao|chat-server|surface-manager|\\bfile\\b|\\bdatabase\\b" hub services scripts pkg webui`
   - 对 `file`、`database` 的结果做人工二次甄别，排除领域词误报。
2. 编译级验证：
   - `go test -run '^$' ./hub/... ./services/... ./pkg/...`
3. 部署链路验证：
   - 执行 `./scripts/deploy.sh`
   - 确认各 service 被成功构建到各自 `run/${svc}-latest`
4. 生命周期验证：
   - 确认 register 成功
   - 确认 heartbeat 正常
   - 确认 Hub `state` / `services.list` 类视图返回 underscore 名称
5. 重点验证 `surface_manager`：
   - 目录重命名后 import path 正常
   - `run/.service_secret` 与 `run/manifest.json` 正常读写
   - Hub 能拉起并识别为 `surface_manager`

## 9. 推荐实施顺序

1. 先改 `hub/config/services.json` 与 `scripts/deploy.sh` 的约定，使“配置真值”和“构建真值”先一致。
2. 再改 `services/surface-manager -> services/surface_manager` 及各 service `cmd/<svc>` 入口名。
3. 再改各 `manifest.json` 和 `cmd/*/main.go` 中的 `service_id` / `instance_id` / 路径常量。
4. 再改 Hub 内部旧 service id 常量、默认 endpoint 和测试断言。
5. 最后做全仓残留检索、编译回归、部署验证和文档同步。

---

**计划时间**：2026-03-20 10:14:01 CST

**计划范围**：`services/account`、`services/ai_doubao`、`services/chat_server`、`services/file_storage`、`services/sql_db`、`services/surface-manager`、`hub/`、`scripts/deploy.sh`、相关测试与说明文档

**依据**：

1. [`doc/_instruction.md`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction.md)
2. [`doc/_instruction/structure.md`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md)
3. [`hub/config/services.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/services.json)
4. [`hub/cmd/hub/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go)
5. [`scripts/deploy.sh`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh)
6. [`services/ai_doubao/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/manifest.json)
7. [`services/chat_server/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/manifest.json)
8. [`services/file_storage/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/file_storage/manifest.json)
9. [`services/sql_db/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/manifest.json)
10. [`services/surface-manager/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/surface-manager/manifest.json)
11. [`services/sql_db/internal/app/hub_builtins.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/internal/app/hub_builtins.go)
12. [`hub/internal/supervisor/registry_test.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/registry_test.go)
13. [`hub/internal/routing/schema_test.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/routing/schema_test.go)
