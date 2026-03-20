# Hub Service 两阶段启动改进开发计划

> 时间：2026-03-21 00:20:25 CST  
> 主题：将 Hub 当前“一次拉起即 ready”的 service 启动机制，收敛为“进程启动/注册”与“依赖初始化/就绪”两阶段模型。  
> 范围：`hub/internal/{supervisor,gateway,routing,app}/`、`hub/config/services.json`、`pkg/toolproto/`、`pkg/hubsvc/`、`services/account/`、`services/sql_db/`、`services/surface_manager/`，以及其他存在跨 service 启动依赖的 service。  
> 依据：`hub/internal/supervisor/lifecycle.go`、`hub/internal/gateway/tool_handler.go`、`hub/internal/app/hub_platform.go`、`hub/config/services.json`、`services/account/internal/app/app.go`、`services/account/internal/app/store_client.go`、`services/surface_manager/cmd/surface_manager/main.go`、`services/surface_manager/internal/app/hub_store.go`、`services/sql_db/cmd/sql_db/main.go`、`doc/_instruction/core.md`、`doc/_instruction/structure.md`、本轮启动日志与仓库实时核验。

## 1. 结论

当前 Hub 的启动问题不是单一超时参数问题，而是启动协议不完整：

1. Hub 当前把“进程拉起 + register 成功”近似视为 `ready`。
2. 多个 service 在 `main()` 启动早期执行依赖型初始化，一旦依赖 service 尚未 ready，就直接报错退出。
3. 在并发启动场景下，这会稳定暴露为 `tool not found`、`service exited before ready`、实例误判 ready 等问题。

因此，本轮改进不应继续围绕 `sleep`、固定等待时间或局部串行化做补丁，而应引入明确的两阶段启动机制：

1. 第一阶段：`start/register`
2. 第二阶段：`init/ready`

Hub 只把完成第二阶段的 service 视为正式可路由实例。

## 2. 背景与当前问题

### 2.1 当前实现事实

根据实时代码核验，当前 `hub/internal/supervisor/lifecycle.go` 的 `StartAll` 负责拉起 service 并等待其注册成功，成功后直接把该 service 记为启动成功。  
各 service 目前对 `service.lifecycle.state.get` 的返回普遍直接自报 `status=ready`，但该 `ready` 并不区分：

1. 进程是否仅仅活着；
2. 是否已完成 register；
3. 是否已完成依赖型初始化；
4. 是否已具备正式业务工具能力。

### 2.2 本轮暴露出的真实问题链

本轮启动日志已证明存在以下竞态：

1. Hub 并发拉起 `sql_db`、`account`、`surface_manager` 等 service。
2. `account` 在 `services/account/internal/app/app.go` 的 `New(...)` 中立即调用 `EnsureSchema` 与 `GetOrCreateSigningKey`。
3. 这些动作通过 `services/account/internal/app/store_client.go` 调 Hub 工具 `storage.database.execute/query`。
4. `surface_manager` 在 `services/surface_manager/cmd/surface_manager/main.go` 启动早期立即执行 `store.EnsureSchema(...)`，并通过 `services/surface_manager/internal/app/hub_store.go` 调用 `storage.database.execute` / `storage.share.*`。
5. 当 `sql_db` 尚未 ready 且 Hub 路由表中还没有可用 provider 时，Hub 返回 `tool not found`。
6. `account` 与 `surface_manager` 因启动期依赖失败直接退出，Hub 最终记录 `ServiceFailed`。

这说明当前系统已经存在明确的跨 service 启动依赖，但协议层没有正式表达这种依赖。

### 2.3 问题根因

根因不是“并发启动本身错误”，而是：

1. 启动状态缺少 `registered` 与 `ready` 的正式区分。
2. 依赖型初始化仍放在 service 冷启动路径中。
3. Hub 没有显式依赖拓扑与 init 编排机制。
4. 路由层虽然已有 “no ready service instance” 的门槛，但启动侧仍把未完成初始化的实例过早纳入 ready 语义。

## 3. 本轮目标

### 3.1 目标

1. 将 Hub service 启动改造成正式的两阶段模型。
2. 让依赖型初始化脱离 service 冷启动主路径，改由 Hub 在合适时机编排。
3. 让 Hub 的治理视图、路由选择与启动快照都基于真实阶段，而不是模糊的 ready。
4. 在不破坏现有 `Hub + 多独立 Service + Tool 平面` 架构边界的前提下，提升启动稳定性与可解释性。

### 3.2 非目标

1. 本轮不改变前端页面、surface 包或 WebUI 交互模型。
2. 本轮不把 Hub 改造成业务初始化执行者；Hub 只负责编排，不接管 service 内部业务初始化逻辑。
3. 本轮不继续通过增加兼容层、隐式 fallback 或更长 sleep 来掩盖协议缺口。

## 4. 目标模型

## 4.1 生命周期阶段

建议在 Hub 与 service 的共同语义上引入以下阶段：

1. `starting`
   进程已拉起，但尚未完成 register。
2. `registered`
   service 已成功向 Hub 注册，生命周期工具可达，但业务能力未宣称 ready。
3. `initializing`
   Hub 正在编排该 service 的初始化阶段。
4. `ready`
   依赖初始化完成，业务工具可正式参与路由。
5. `failed`
   初始化失败，或进入不可恢复故障。
6. `draining`
   停机/排空中。
7. `stopped`
   已停止。

其中，本轮最关键的是把 `registered` 与 `ready` 分开。

## 4.2 新的正式启动流程

建议将 `StartAll` 的语义拆为两轮：

### Phase A：Start/Register

1. Hub 并发拉起全部 service 进程。
2. 每个 service 仅需完成最小启动：
   - 读配置
   - 启 HTTP server
   - 提供 lifecycle 工具
   - register 到 Hub
3. register 成功后，实例状态标为 `registered`，而不是 `ready`。

### Phase B：Init/Ready

1. Hub 根据显式依赖图分层推进初始化。
2. Hub 调用 service 的 `service.lifecycle.init`。
3. `init` 成功后，该实例状态切换为 `ready`。
4. `init` 失败则记为 `failed`，由 Hub 决定是否重试、标红或中止全局启动。

## 4.3 路由门禁

业务工具路由必须只选择 `ready` 实例。  
`registered` 或 `initializing` 实例仅暴露生命周期能力，不参与正式业务流量路由。

这与当前 `tool_handler.go` 中的 “no ready service instance” 语义一致，但需要把启动侧真实对齐到这一定义。

## 5. 协议与配置改造

### 5.1 新增正式 lifecycle 工具

建议为各 service 增加：

1. `service.lifecycle.init`

建议语义：

1. 由 Hub 调用；
2. 负责执行依赖型初始化；
3. 必须幂等；
4. 重复调用若已完成，应直接返回成功；
5. 返回结果应明确包含当前 init 状态、耗时与错误原因。

### 5.2 `service.lifecycle.state.get` 返回值收敛

当前多个 service 的 `state.get` 直接返回 `status=ready`。本轮应统一改为可表达上述阶段，例如：

1. `status`
2. `registered`
3. `initialized`
4. `healthy`
5. `ready`
6. `last_init_error`
7. `last_init_at_ms`

注意：`ready=true` 必须只在 init 成功后返回。

### 5.3 显式依赖声明

建议在 `hub/config/services.json` 中为每个 service 增加显式依赖，例如：

1. `depends_on`

首批明确依赖：

1. `account -> sql_db`
2. `surface_manager -> sql_db`

后续再按真实代码补充其他依赖，禁止继续依赖隐式启动顺序。

## 6. Hub 侧改造方案

### 6.1 Supervisor 生命周期改造

目标文件：

1. `hub/internal/supervisor/lifecycle.go`
2. `hub/internal/supervisor/registry.go`
3. `hub/internal/supervisor/handler.go`
4. `hub/internal/app/hub_platform.go`

改造方向：

1. `StartAll` 改为：
   - 并发 `start/register`
   - 再按依赖拓扑执行 `init`
2. 启动快照结构补充阶段字段，而不是只保留 `Ready bool`。
3. 对实例状态从“单一 ready/failure”扩展为分阶段状态。
4. register 成功不再自动等于 `StartupServiceOutcome.Ready=true`。

### 6.2 依赖拓扑编排

目标：

1. 对 `depends_on` 做校验与拓扑排序。
2. 同层依赖并发 init，不同层串行推进。
3. 对依赖未 ready 的 service，禁止提前进入 init。

约束：

1. 检测循环依赖并直接报配置错误。
2. 缺失依赖 service 时直接启动失败，不做静默降级。

### 6.3 路由层与治理视图对齐

目标文件：

1. `hub/internal/gateway/tool_handler.go`
2. `hub/internal/routing/`

改造方向：

1. 路由选择实例时，只接受 `ready`。
2. `registered` / `initializing` 状态不参与业务工具路由。
3. 管理接口与系统状态接口要能清楚显示每个 service 当前阶段。

### 6.4 启动日志与快照收敛

当前日志里的：

1. `ServiceReady`
2. `ServiceFailed`
3. `Lifecycle:Done: ready=X total=Y`

都需要升级为两阶段语义，例如区分：

1. `Registered`
2. `InitBegin`
3. `InitReady`
4. `InitFailed`

启动快照也应同步记录：

1. register 是否成功；
2. init 是否成功；
3. 失败发生在哪个阶段；
4. 依赖等待耗时与 init 耗时。

## 7. Service 侧改造方案

### 7.1 总原则

各 service 的 `main()` 冷启动路径只负责最小启动，不再承担依赖型初始化。  
依赖型初始化统一迁移到 `service.lifecycle.init`。

### 7.2 `sql_db`

`sql_db` 是基础依赖 service，本轮要求：

1. 尽量保持冷启动简单且快速；
2. register 后即可很快进入 `ready`；
3. 如无额外依赖，可让 `service.lifecycle.init` 成为轻量幂等空操作或最小自检。

### 7.3 `account`

当前需迁移出冷启动路径的内容：

1. `EnsureSchema`
2. `GetOrCreateSigningKey`

改造目标：

1. `New(...)` / `Run()` 不再因 `sql_db` 尚未 ready 而直接退出。
2. `service.lifecycle.init` 负责 schema 与 signing key 初始化。
3. init 成功前，业务工具不应被正式放流。

### 7.4 `surface_manager`

当前需迁移出冷启动路径的内容：

1. `store.EnsureSchema(...)`
2. 与 Hub 存储相关的依赖型初始化
3. 首次 catalog sync（是否保留在 init 由实现阶段细化）

改造目标：

1. 先完成最小 server 启动与 register。
2. 再由 `service.lifecycle.init` 处理依赖 `sql_db` 的初始化。
3. init 完成后再宣布 `ready`。

### 7.5 其他 service

其余 service 也要统一检查：

1. 是否在 `main()` 或 `New(...)` 中立即调用其他 service tool；
2. 是否存在“依赖失败即直接退出”的冷启动路径；
3. 是否应迁移到 `service.lifecycle.init`。

优先排查：

1. `chat_server`
2. `file_storage`
3. `autogui`
4. `ai_doubao`

## 8. 推荐实施顺序

### Phase 1：协议与状态机落地

1. 扩展 Hub 内部 service 状态模型。
2. 扩展 `service.lifecycle.state.get` 统一结构。
3. 设计并落地 `service.lifecycle.init` 协议。
4. 为 `hub/config/services.json` 增加 `depends_on`。

验收：

1. 不改业务初始化逻辑时，系统仍可编译。
2. Hub 能区分 `registered` 与 `ready`。

### Phase 2：Hub 启动编排改造

1. `StartAll` 改为两阶段。
2. 实现依赖拓扑与分层 init。
3. 启动日志、快照与管理视图同步升级。

验收：

1. 即使 service 已 register，未 init 成功前也不会被视为 `ready`。
2. 路由不会把业务流量打到未 ready 实例。

### Phase 3：首批 service 迁移

1. 迁移 `sql_db`
2. 迁移 `account`
3. 迁移 `surface_manager`

验收：

1. 并发拉起全量 service 时，不再出现 `account` / `surface_manager` 因 `tool not found` 冷启动退出。
2. `sql_db` ready 后，Hub 再推进依赖它的 service init。

### Phase 4：全面收口

1. 排查其他 service 冷启动依赖。
2. 统一各 service 的 lifecycle 状态输出。
3. 补齐测试与说明文档。

验收：

1. 启动日志可解释；
2. 启动失败能明确定位到 `register` 或 `init` 阶段；
3. 不再依赖隐式启动顺序。

## 9. 测试与验证设计

### 9.1 单元/编译级验证

1. `go test -run '^$' ./hub/... ./services/account/... ./services/sql_db/... ./services/surface_manager/...`
2. 覆盖 supervisor、routing、tool gateway 相关包。

### 9.2 集成启动验证

关键验证场景：

1. `./scripts/deploy.sh` 首次启动通过。
2. 重复执行 `./scripts/deploy.sh`，不会因启动竞态导致 service 冷启动失败。
3. `sql_db` 人为延迟 ready 时，`account` / `surface_manager` 不退出，只处于 `registered` 或 `initializing`。
4. `sql_db` init 失败时，依赖它的 service 被明确阻断并呈现正确失败阶段。

### 9.3 路由验证

1. 未 ready 的 service 不参与业务工具路由。
2. lifecycle 工具仍可调用。
3. 管理接口能看到完整阶段变化。

## 10. 风险与控制

### 10.1 风险：协议扩展影响面广

`state.get`、supervisor、routing、service 自报状态都会受影响。

控制：

1. 先落状态机与 Hub 内部支持；
2. 再逐个迁移 service；
3. 保持新增字段向后兼容，但不继续扩散旧的“注册即 ready”语义。

### 10.2 风险：service 内部实现分散

不同 service 对“初始化”的定义不同。

控制：

1. 统一原则：凡依赖其他 service 的初始化，全部迁出冷启动路径。
2. 先迁明确依赖 `sql_db` 的 service，再扩展到其他场景。

### 10.3 风险：Hub 过度侵入业务初始化

如果 Hub 直接编排细粒度业务对象，会越界。

控制：

1. Hub 只调用 service 级 `service.lifecycle.init`。
2. service 内部若还有更细初始化，由 service 自己管理。

## 11. 完成标准

满足以下条件，才算本计划完成：

1. Hub 已正式区分 `registered` 与 `ready`。
2. `service.lifecycle.init` 已成为标准协议的一部分。
3. `hub/config/services.json` 已可表达真实启动依赖。
4. `account`、`surface_manager` 等依赖型 service 已迁出冷启动依赖初始化。
5. Hub 路由只向 `ready` 实例分发正式业务工具。
6. 并发启动全量 service 时，不再因依赖 service 尚未 ready 而出现 `tool not found` 冷启动退出。
7. 启动日志、治理视图与启动快照都能准确反映 register/init/ready 各阶段。

## 12. 计划范围与后续文档关系

本计划是 Hub service 启动机制优化的主计划，后续若进入实现，应优先按本计划拆出子计划或直接进入 `dev` 执行。  
若实现过程中发现某个 service 需要独立大改，可再补充针对该 service 的专项 devplan，但不得偏离本计划中“两阶段启动、显式依赖、Hub 编排 service 级 init”的核心边界。
