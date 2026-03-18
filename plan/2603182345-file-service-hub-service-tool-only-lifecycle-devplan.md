# File Service Hub-Service 工具化生命周期对齐开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 00:27 CST  
> 范围：`services/file/`、`hub/internal/{gateway,supervisor,routing,app}/`、`pkg/{hubsvc,toolproto}/`、`scripts/`  
> 目标约束：Hub 与 Service 的交互只保留 tool 语义，不保留 Hub 直连 Service 的管理路径。

## 0. 结论

`services/file` 当前已经完成了基础的 Hub 接入，但生命周期治理仍然存在两类问题：

1. 语义层没有完全工具化，`file` 仍在使用旧的心跳路径改写逻辑，并保留 `admin/shutdown` 这类直连管理路径。
2. 权限和路由元数据没有完全接入 Hub 的标准 tool 体系，`ScopeSupport` 仍停留在 file 本地语义，未转化为 Hub 可执行的 `AllowedCallerTypes`。

最佳解决方案不是继续补兼容分支，而是直接收敛为“**Hub 只通过 Service 暴露的 tools 进行治理，Service 只通过 Hub 暴露的 tools 进行注册与心跳**”的单一通信模型。

因此，本计划的核心方向是：

- 服务端不再依赖 `/admin/shutdown`、`/healthz` 这类独立管理路径作为 Hub 对 Service 的治理面。
- `file` 需要补齐服务自身的生命周期 tool，例如 `service.lifecycle.health`、`service.lifecycle.shutdown` 或等价命名。
- Hub 的生命周期管理需要统一到通过 `/service/tool/exec` 调用这些 tool。当前代码已经出现了 tool 化 shutdown 的雏形，但命名与遗留路径还未统一收口。
- `file` 向 Hub 注册与心跳时，只能通过 Hub 的 tool 入口 `/api/tool/call`，不能再使用旧路径语义。

## 1. 核验事实

以下事实已在仓库代码中核验：

- `services/file/cmd/file/main.go` 在启动阶段会读取 bootstrap secret，并向 Hub 注册实例；当前注册结果只做 decode 校验，没有消费 Hub 返回的生命周期参数。
- `services/file/cmd/file/main.go` 仍包含 `buildHubHeartbeatURL()`，会把 Hub 注册地址改写到 `/api/service/heartbeat`，这与当前 Hub 的工具化治理入口不一致。
- `services/file/cmd/file/main.go` 仍显式暴露 `/admin/shutdown`，并由 Hub 侧通过 `BuildServiceControlURL(..., "/admin/shutdown")` 进行控制。
- `hub/internal/gateway/tool_handler.go` 已在网关层统一执行 caller 校验，且依据 `AllowedCallerTypes` 做提前拦截。
- `hub/internal/supervisor/handler.go` 已经把 `register`、`heartbeat`、`drain` 收敛成 `hub.governance.service.*` tools。
- `hub/internal/supervisor/lifecycle.go` 已开始通过 `/service/tool/exec` 调用服务侧 shutdown tool，当前命名为 `<service_id>.system.shutdown`，说明 shutdown 已进入 tool 语义，但尚未标准化。
- `hub/internal/supervisor/process_control.go` 与 `scripts/reset_db.sh` 仍保留 `/admin/shutdown` 路径调用，是本轮必须收敛的遗留入口。
- `hub/internal/gateway/hub_manifest.go` 已把 Hub 自身能力定义为标准 tool manifest，说明治理也应沿同一 tool 协议闭环。
- `services/file/internal/app/hub_builtins.go` 里 `file` 的工具描述仍使用 `ScopeSupport`，没有直接输出 Hub 所需的 `AllowedCallerTypes`。
- `services/file/internal/app/` 仍保留 `hub_platform.go`、`hub_builtins.go`、`session.go`、`pipeline.go`、`auth.go`、`llm.go`、`asr.go`、`tts.go`、`runtime_config.go` 等与 file 当前对外职责无关的历史模块。

## 2. 问题定义

### 2.1 生命周期通信不闭环

当前 `file` 的生命周期链路是“半工具化、半路径化”：

- Service -> Hub 走的是工具注册/心跳的意图。
- Hub -> Service 仍然混用路径控制面与工具语义。

这会导致两个问题：

1. 语义不统一，调用方无法只通过 tool 平面理解系统边界。
2. 生命周期控制不可组合，Hub 无法把服务停止、健康探测、重启判断都放进同一调度模型。

### 2.2 注册与权限元数据未完全标准化

`file` 当前对外工具已经基本成型，但 manifest 仍有两个缺口：

- `ScopeSupport` 只描述 file 本地语义，没有被 Hub 直接用于 caller 校验。
- `AllowedCallerTypes` 未作为 file 对 Hub 的显式注册字段输出，导致权限模型只能在 file 本地 handler 兜底。

### 2.3 历史残留过多

`services/file/internal/app` 中保留了大量与当前 storage/blob 职责无关的代码，这些代码会持续制造边界漂移，并让后续治理工具化改造更难收敛。

## 3. 推荐方案

### 3.1 统一原则

本次改造只接受一个原则：

- **Hub 只能调用 Service 提供的 tools。**
- **Service 只能调用 Hub 提供的 tools。**
- **路径级管理接口不再作为 Hub-Service 间的长期契约。**

在这个原则下：

- Service 向 Hub 的注册、心跳、上报状态，全部通过 `hub.governance.service.register`、`hub.governance.service.heartbeat` 等 Hub tools 完成。
- Hub 向 Service 的停止、健康探测、必要的生命周期查询，全部通过 Service 自身暴露的 lifecycle tools 完成。
- `admin/shutdown` 不再作为 Hub 调用 Service 的正式通道。

### 3.2 目标生命周期工具

建议 `file` 补齐如下工具语义：

- `service.lifecycle.health`：返回当前实例是否可用、实例信息、时间戳。
- `service.lifecycle.shutdown`：接收 Hub 的关闭请求，执行优雅停机并退出。
- 如有必要，可补充 `service.lifecycle.status` 或 `service.lifecycle.pre_stop`，但只在确有业务需要时引入。

这些工具应通过 `services/file/cmd/file/main.go` 的统一 tool dispatcher 暴露，而不是新增一套独立的管理 REST 路径。

### 3.3 Hub 侧改造方式

Hub 侧需要把 lifecycle 逻辑从“路径调用”改成“tool 调用”：

- `hub/internal/supervisor/lifecycle.go` 不再构造 `.../admin/shutdown`。
- `hub/internal/supervisor/process_control.go` 不再依赖 `BuildServiceControlURL(..., "/admin/shutdown")` 作为停止手段。
- 健康探测也应尽量改为调用 Service 的 lifecycle tool，而不是独立 `/healthz` 路径。

### 3.4 file 侧改造方式

`file` 需要收敛为纯 storage/blob 服务：

- 保留 `storage.file.*`、`storage.blob.*` 以及必要的 lifecycle tools。
- 去掉注册后基于旧路径的心跳 URL 改写逻辑。
- 将 manifest 中的 caller 约束输出为 Hub 可直接消费的 `AllowedCallerTypes`。
- 删除或迁出与当前职责无关的历史模块，避免入口和实现边界继续污染。
- 统一服务 shutdown tool 的命名，避免 `system.shutdown` 这类临时名继续扩散到 file 之外。

## 4. 实施分期

### Phase 1：协议和入口收口

目标：先把通信面统一到 tool 协议，禁止新旧路径并存继续扩散。

任务：

1. 将 `file` 向 Hub 的心跳调用收敛到 `/api/tool/call`，调用目标 tool 设为 `hub.governance.service.heartbeat`。
2. 停止使用 `/api/service/heartbeat` 这类旧路径改写逻辑。
3. 在 `file` 的工具注册数据里补齐 `AllowedCallerTypes` 的输出映射，不再把权限约束停留在 `ScopeSupport`。

验收：

- `file` 不再生成 `/api/service/heartbeat`。
- `file` 的注册与心跳都通过 Hub tool 入口完成。
- Hub 侧可直接依据 manifest 限制 caller 类型。

### Phase 2：Service 生命周期工具化

目标：把 Hub 对 Service 的控制完全收束到 tool 调用，并清理旧路径与临时命名。

任务：

1. 为 `file` 增加 `service.lifecycle.health` 工具。
2. 为 `file` 增加 `service.lifecycle.shutdown` 工具，或将现有 shutdown 语义统一到该命名。
3. 让 `shutdown` 工具负责优雅关闭 HTTP server、释放资源并退出进程。
4. 将 Hub 现有的 `<service_id>.system.shutdown` 临时命名迁移到统一的生命周期命名。
5. 移除 `hub/internal/supervisor/process_control.go`、`scripts/reset_db.sh` 等处的 `/admin/shutdown` 直连调用。

验收：

- Hub 不再依赖 Service 的 `/admin/shutdown`。
- `StopServiceRegistration`、`BroadcastServiceShutdown` 及相关停止流程都能通过 tool 完成。
- 服务被停止时，审计和注册状态仍可正确落到 Hub 的治理模型中。

### Phase 3：Manifest 与路由元数据修正

目标：让 `file` 的 manifest 成为 Hub 可直接消费的标准输入。

任务：

1. 将 `file` 的 tool 描述从 `ScopeSupport` 语义升级为 Hub 可直接执行的 `AllowedCallerTypes`。
2. 为 `storage.file.*`、`storage.blob.*` 统一输出 caller 限制、超时、流式标识等元信息。
3. 如有需要，补充 `service.lifecycle.*` 的 caller 限制，并与 Hub 的调用侧命名保持一致。

验收：

- Hub 的 route schema 能直接展示 `file` 的真实可调用面。
- `file` 的权限约束不再只存在于本地 handler 里。

### Phase 4：历史残留清理

目标：收缩 `services/file/internal/app` 到真正需要的 storage/blob/lifecycle 代码面。

任务：

1. 删除或迁出与当前 file 职责无关的残留模块。
2. 只保留服务入口、存储领域逻辑、blob 逻辑、生命周期工具和必要的辅助函数。
3. 清理与 Hub 平台复制件相关的代码，避免再次出现“file 自己再造一个 Hub”的结构。

验收：

- `services/file/internal/app` 不再包含明显的账号、chat、AI provider、平台复制件残留。
- file 的目录边界和职责边界一致。

## 5. 验证方案

### 5.1 启动链路

验证 `scripts/deploy.sh` 后，Hub 能拉起 `file` 并完成：

- 服务注册
- 心跳稳定
- 工具路由可用

### 5.2 生命周期链路

验证以下场景：

1. Hub 通过 tool 调用启动对 `file` 的健康探测。
2. Hub 通过 tool 调用关闭 `file`。
3. `file` 停止后，Hub 的注册和路由状态同步更新。
4. `file` 重启后，注册与路由恢复正常。

### 5.3 权限链路

验证 `AllowedCallerTypes` 生效：

- 不允许的 caller 在 Hub 网关层被提前拦截。
- `file` 本地不再承担唯一权限防线。

### 5.4 回归范围

至少覆盖：

- `storage.file.*`
- `storage.blob.*`
- `hub.governance.service.register`
- `hub.governance.service.heartbeat`
- `hub.governance.service.drain`
- `service.lifecycle.health`
- `service.lifecycle.shutdown`

## 6. 风险与约束

### 6.1 迁移风险

- 如果先移除旧路径，但新 lifecycle tools 尚未落地，Hub 会失去停止服务的能力。
- 如果 heartbeat 先改到 tool 语义，但注册/服务心跳参数未对齐，启动阶段可能出现误判和退出。
- 如果 shutdown 工具命名先变更，但 Hub 侧 `lifecycle.go`、`process_control.go`、`reset_db.sh` 未同步，系统会出现“双命名并存”的治理裂缝。

### 6.2 兼容性约束

本计划不建议长期保留旧路径作为正式接口。

如果必须设置过渡期，也只能把旧路径当成内部 shim，并明确其最终删除时间，不能继续把它写成正式契约。

### 6.3 实施顺序

必须按以下顺序推进：

1. 先收口 Service -> Hub 的心跳与注册到 tool 语义。
2. 再把 Hub -> Service 的 shutdown / health 迁移到 service tools。
3. 同步清理 Hub 内部仍存在的临时 shutdown 命名与旧路径调用。
4. 最后清理 `file` 的历史残留。

## 7. 完成标准

满足以下条件时，认为本轮计划完成：

1. `file` 不再使用 `/api/service/heartbeat` 这类旧路径语义。
2. Hub 不再通过 `/admin/shutdown` 直连 Service。
3. `file` 已补齐生命周期 tools，并能被 Hub 调用。
4. Hub 停止 service 时只保留 tool 语义，不再依赖 `/admin/shutdown` 或临时命名分叉。
5. `AllowedCallerTypes` 已成为 file 对 Hub 的有效 manifest 输入。
6. `services/file/internal/app` 已明显收缩，历史残留被清理或迁出。
7. 启动、注册、心跳、探测、停止、重启的全链路 smoke 通过。

## 8. 待确认事项

1. `service.lifecycle.health` 与 `service.lifecycle.shutdown` 的最终 tool 命名是否要统一到 `service.*` 前缀，还是保留 `storage.*` 体系内的生命周期子命名。
2. Hub 侧当前已经出现 `<service_id>.system.shutdown` 的临时命名，是否统一收敛为 `<service_id>.lifecycle.shutdown` 或其他单一规范。
3. Hub 侧停止服务时，是否需要在 tool 调用返回后立即退出，还是允许一个可配置的优雅关闭窗口。
4. `file` 内部是否需要保留任何对外可见的兼容路径，如果保留，保留多久。
