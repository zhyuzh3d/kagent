# Hub 与 Chat-Server / File / AI-Doubao 工具机制对齐开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 00:54 CST  
> 范围：`hub/internal/{gateway,routing,supervisor,app}/`、`services/{file,ai-doubao,chat-server}/`、`pkg/{hubsvc,toolproto}/`、`scripts/`  
> 目标约束：Hub 与 Service 的交互只保留 tool 语义；路径级治理接口只能作为短期 shim，不得继续作为正式契约。

## 0. 结论

本轮重构的目标不是“给 chat-server 单点补丁”，而是把 `hub`、`file`、`ai-doubao`、`chat-server` 统一纳入同一套 Hub <-> Service tool 机制。

当前仓库的真实状态可以分成四档：

1. **Hub 侧已经具备 tool-first 基础**：`hub/internal/gateway/tool_handler.go` 已做 `AllowedCallerTypes` 校验，`hub/internal/supervisor/handler.go` 已接收服务注册工具清单，`hub/internal/supervisor/process_control.go` 也已开始通过 `service.lifecycle.*` 调用生命周期工具。
2. **file 基本已经对齐**：`file` 已有 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，注册和心跳也已走 `hub.governance.service.*` tool，剩余问题主要是旧路径兼容和历史残留清理。
3. **ai-doubao 处于半对齐状态**：业务工具已走 Hub tool 平面，但心跳仍改写到 `/api/service/heartbeat`，生命周期命名仍停留在 `ai-doubao.system.shutdown`，`AllowedCallerTypes` 仍未成为显式注册输入。
4. **chat-server 仍处于过渡态**：业务工具已通过 `/service/tool/exec` 接入 Hub，但还保留 `/healthz`、`/admin/shutdown` 和旧心跳路径改写逻辑，生命周期与权限元数据都没有完成 tool 化收口。

因此，本轮计划的核心方向是：

- Hub 侧补齐工具治理视图和生命周期收口。
- file 侧清理遗留路径，固化为参考实现。
- ai-doubao 侧补齐 caller 元数据并把心跳、生命周期统一到标准 tool 命名。
- chat-server 侧新增 lifecycle tools，清理路径级治理面，并补齐 caller 元数据。

---

## 1. 真实核验事实

以下事实已由当前仓库代码核验：

- `hub/internal/gateway/tool_handler.go` 在调用前会检查 `AllowedCallerTypes`，空值则默认放行。
- `hub/internal/routing/schema.go` 会从服务注册信息构建工具视图，但当前工具视图里还没有完整展示 caller 约束字段。
- `hub/internal/supervisor/handler.go` 已将 `hub.governance.service.register`、`hub.governance.service.heartbeat` 收为工具。
- `hub/internal/supervisor/process_control.go` 已通过 `service.lifecycle.health` 和 `service.lifecycle.shutdown` 操作服务生命周期，但仍保留 `/healthz` fallback。
- `services/file/cmd/file/main.go` 已直接处理 `service.lifecycle.health` 与 `service.lifecycle.shutdown`，并在注册工具转换里回填 `AllowedCallerTypes`，fallback 到 `ScopeSupport`。
- `services/file/cmd/file/main.go` 仍保留 `/api/service/heartbeat` 改写逻辑，属于历史兼容残留。
- `services/ai-doubao/cmd/ai-doubao/main.go` 已调用 `hub.governance.service.register` 与 `hub.governance.service.heartbeat`，但心跳仍改写到 `/api/service/heartbeat`。
- `services/ai-doubao/cmd/ai-doubao/main.go` 已存在 `ai-doubao.system.shutdown` 作为生命周期工具名，但尚未统一到 `service.lifecycle.shutdown`。
- `services/chat-server/cmd/chat-server/main.go` 已通过 `POST /service/tool/exec` 分发业务工具，但仍保留 `/healthz`、`/admin/shutdown` 和旧心跳路径改写逻辑。
- `services/chat-server/internal/app/service_manifest.go` 的工具描述结构仍保留 `ScopeSupport`，但当前 manifest 未系统输出 `AllowedCallerTypes`。

---

## 2. 设计原则

### 2.1 单一协议

跨 Hub 与 Service 的治理和能力调用都应优先通过 `tool_id`、`CallRequest`、`CallResponse` 和 `/api/tool/call` / `/service/tool/exec` 实现。

### 2.2 生命周期与业务面分离

业务工具只负责业务执行，生命周期工具只负责健康、关闭、状态查询。

### 2.3 权限前置

`AllowedCallerTypes` 必须成为 manifest 的显式输入，由 Hub 在网关层执行，Service 本地 handler 只做补充校验。

### 2.4 兼容层有明确终点

`/healthz`、`/admin/shutdown`、`/api/service/heartbeat` 只能作为短期 shim，必须在计划中定义删除条件。

---

## 3. Hub 侧改进计划

### 3.1 当前问题

Hub 侧已经具备 tool-first 基础，但还存在三类收口问题：

1. 路由视图没有完整呈现 caller 约束。
2. 生命周期控制仍保留 health/path fallback，旧路径还没有完全退出。
3. 部分脚本与辅助逻辑仍在引用传统控制面语义。

### 3.2 目标

Hub 侧最终应满足：

- 只把 `service.lifecycle.health` 和 `service.lifecycle.shutdown` 视为正式生命周期入口。
- 在 route/schema 视图中完整展示 `AllowedCallerTypes`、`CapabilitiesRequired`、`Streaming`、`WSPath`。
- 对旧路径只保留过渡兼容，不再将其当作正式契约。

### 3.3 具体任务

#### A. 工具元数据补强

1. 在 `hub/internal/routing/schema.go` 中把 `AllowedCallerTypes` 纳入 `ToolRegistryItem` 或等效元数据结构。
2. 确保 tool registry、service registry、audit view 能显示统一的 caller 语义。
3. 保持 `AllowedCallerTypes` 的规范化处理，避免空数组、重复值或大小写漂移。

#### B. 生命周期收口

1. 在 `hub/internal/supervisor/process_control.go` 中继续以 `service.lifecycle.shutdown` 作为首选停机路径。
2. 保留 `service.lifecycle.health` 作为首选可用性探测路径。
3. `/healthz` 仅作为短期 fallback，待 `file`、`ai-doubao`、`chat-server` 都完成生命周期 tool 化后移除。

#### C. 旧路径清理

1. 逐步去除对 `/admin/shutdown` 的正式依赖。
2. 逐步去除对 `/api/service/heartbeat` 的正式依赖。
3. 脚本和内部流程只允许在过渡期存在 shim 调用，不再新增旧路径。

### 3.4 验收标准

- Hub 的工具视图能明确展示 `chat-server`、`file`、`ai-doubao` 的 caller 约束。
- Hub 的生命周期管理不再依赖路径级正式契约。
- 旧路径只剩过渡 shim，没有新的正式调用方。

---

## 4. file 侧改进计划

### 4.1 当前状态

file 已经是四个对象里最接近目标状态的服务：

- 已使用 `hub.governance.service.register`。
- 已使用 `hub.governance.service.heartbeat`。
- 已提供 `service.lifecycle.health` 与 `service.lifecycle.shutdown`。
- `manifestTools()` 和 `toSupervisorTools()` 已支持 `AllowedCallerTypes`，并在空值时回填 `ScopeSupport`。

### 4.2 当前问题

file 的主要问题不是协议不对，而是过渡残留较多：

1. 仍然存在 `/api/service/heartbeat` 改写逻辑。
2. `ScopeSupport` 与 `AllowedCallerTypes` 双轨并存，主语义还不够清晰。
3. `services/file/internal/app` 里仍有较多历史复制件，增加边界噪音。

### 4.3 目标

file 最终应作为“参考级 storage service”存在，做到：

- 业务工具边界稳定。
- 生命周期 tool 稳定。
- caller 语义以 `AllowedCallerTypes` 为准。
- 旧路径只作为短期 shim。

### 4.4 具体任务

#### A. 收敛心跳语义

1. 停止生成 `/api/service/heartbeat` 作为正式注册地址。
2. 统一 `hub.governance.service.heartbeat` 的 tool 调用路径。
3. 保留最短过渡期后删除旧路径改写函数。

#### B. 固化 caller 语义

1. 保持 `AllowedCallerTypes` 为主，`ScopeSupport` 仅作兼容输入。
2. 对 `storage.file.*`、`storage.blob.*`、`storage.database.*` 统一 caller 约束输出。
3. 保持 blob GC 这类服务级工具的 caller 限制明确可审计。

#### C. 清理历史残留

1. 按职责收缩 `services/file/internal/app`。
2. 删除或迁出与当前 storage/blob/lifecycle 无关的复制件。
3. 避免 file 再产生“平台复制层”。

### 4.5 验收标准

- file 不再生成 `/api/service/heartbeat`。
- file 的 manifest 以 `AllowedCallerTypes` 为主。
- file 只保留必要的 lifecycle shim，历史残留显著减少。

---

## 5. ai-doubao 侧改进计划

### 5.1 当前状态

ai-doubao 已经具备业务工具接入 Hub 的基础，但还没有完成标准 tool 语义的闭环：

- 注册工具仍走 Hub tool 入口。
- 心跳仍改写到 `/api/service/heartbeat`。
- 生命周期工具名仍使用 `ai-doubao.system.shutdown`。
- 工具注册转换尚未统一输出 `AllowedCallerTypes`。

### 5.2 当前问题

1. 生命周期命名不统一，和 Hub 的 `service.lifecycle.*` 体系不一致。
2. 心跳仍依赖旧路径语义，迁移还不彻底。
3. 权限元数据不完整，Hub 只能默认放行或依赖运行时兜底。

### 5.3 目标

ai-doubao 最终应作为标准 AI provider service 存在：

- 心跳统一通过 `hub.governance.service.heartbeat`。
- 生命周期统一到 `service.lifecycle.health`、`service.lifecycle.shutdown`。
- 工具注册必须显式输出 caller 约束。

### 5.4 具体任务

#### A. 心跳改造

1. 将 `buildHubHeartbeatURL()` 这类路径改写逻辑移除出正式链路。
2. 让心跳请求直接调用 `POST /api/tool/call`。
3. 心跳 tool 目标固定为 `hub.governance.service.heartbeat`。

#### B. 生命周期命名收敛

1. 将 `ai-doubao.system.shutdown` 迁移或别名为 `service.lifecycle.shutdown`。
2. 补齐 `service.lifecycle.health`，作为 Hub 首选探测入口。
3. 仅在迁移窗口内保留旧工具名别名。

#### C. manifest 标准化

1. 让 `toSupervisorToolsFromDescriptors()` 输出 `AllowedCallerTypes`。
2. 保留当前业务工具的实际调用策略，但写入显式声明。
3. 对流式工具继续保留 `Streaming`、`WSPath`、`TimeoutMS` 等字段。

### 5.5 验收标准

- ai-doubao 不再依赖 `/api/service/heartbeat` 作为正式心跳路径。
- lifecycle 工具名与 Hub 统一到 `service.lifecycle.*`。
- Hub 的 tool registry 能读取 ai-doubao 的 caller 约束。

---

## 6. chat-server 侧改进计划

### 6.1 当前状态

chat-server 已完成业务工具接入，但治理面仍然明显过渡化：

- 业务工具已经通过 `/service/tool/exec` 接入。
- 仍保留 `/healthz` 与 `/admin/shutdown`。
- 仍存在 `/api/service/heartbeat` 的旧路径改写。
- manifest 结构仍以 `ScopeSupport` 为过渡语义。

### 6.2 当前问题

1. lifecycle tools 尚未纳入统一命名。
2. caller 元数据没有标准化输出。
3. 旧路径与 tool 语义并存，后续会继续放大边界漂移。

### 6.3 目标

chat-server 最终应作为标准业务编排 service 存在：

- 业务工具只通过 tool 平面暴露。
- 生命周期工具统一到 `service.lifecycle.*`。
- caller 约束必须出现在注册清单中。

### 6.4 具体任务

#### A. 生命周期工具补齐

1. 增加 `service.lifecycle.health`。
2. 增加 `service.lifecycle.shutdown`。
3. 让 `shutdown` tool 负责优雅关闭 HTTP server、WS session 和相关资源。

#### B. 旧路径收口

1. 将 `/healthz` 降级为短期 shim。
2. 将 `/admin/shutdown` 降级为短期 shim。
3. 去除 `buildHubHeartbeatURL()` 这种旧路径改写依赖。

#### C. manifest 标准化

1. 为 `app.chat.*` tool 明确补齐 `AllowedCallerTypes`。
2. 若当前业务确实只允许用户调用，先统一声明为 `["user"]`，再视需要细化。
3. `ScopeSupport` 只允许作为兼容输入，不再作为主语义。

### 6.5 验收标准

- chat-server 不再依赖 `/api/service/heartbeat` 作为正式治理路径。
- chat-server 的 manifest 能被 Hub 直接消费 caller 约束。
- chat-server 具备明确的 lifecycle tool 闭环。

---

## 7. 分阶段实施顺序

### Phase 1：统一协议与元数据

目标：先把所有 service 的 manifest 语义统一起来，再收紧 Hub。

任务：

1. `file`：保持现有 `AllowedCallerTypes` 输出，作为参考实现。
2. `ai-doubao`：补齐 `AllowedCallerTypes` 输出和生命周期 tool 命名。
3. `chat-server`：补齐 `AllowedCallerTypes`，新增 `service.lifecycle.*`。
4. Hub：扩展 route/schema 展示 caller 约束。

验收：

- 三个 service 的注册清单都能向 Hub 提供可消费的 caller 元数据。
- Hub 侧工具视图能展示统一字段。

### Phase 2：心跳迁移

目标：先统一 Service -> Hub 的治理通信。

任务：

1. `file`：删除旧 heartbeat URL 改写的正式依赖。
2. `ai-doubao`：把心跳切到 `/api/tool/call` 的 `hub.governance.service.heartbeat`。
3. `chat-server`：把心跳切到 `/api/tool/call` 的 `hub.governance.service.heartbeat`。
4. Hub：保留旧路径兼容只到迁移窗口结束。

验收：

- 三个 service 都以 tool 语义完成注册与心跳。

### Phase 3：生命周期统一

目标：把 Hub 对 Service 的控制完全收束到 lifecycle tools。

任务：

1. `file`：保留并验证 `service.lifecycle.health` / `service.lifecycle.shutdown`。
2. `ai-doubao`：把 `ai-doubao.system.shutdown` 收敛到 `service.lifecycle.shutdown`。
3. `chat-server`：新增 `service.lifecycle.health` / `service.lifecycle.shutdown`。
4. Hub：停机、探测、广播停止统一走 tool 调用。

验收：

- Hub 不再需要把路径级控制面作为正式契约。

### Phase 4：历史残留清理

目标：删掉旧 shim 和边界噪音。

任务：

1. 逐步删除 `/admin/shutdown`。
2. 逐步删除 `/api/service/heartbeat`。
3. 收缩 `services/*/internal/app` 中的历史复制件。
4. 清理脚本和文档中的旧路径正式表述。

验收：

- 仅保留 tool 语义正式契约。
- 旧路径不存在新的正式调用方。

---

## 8. 验证方案

### 8.1 启动与注册

1. 运行 `scripts/deploy.sh`。
2. 验证 Hub 能拉起 `file`、`ai-doubao`、`chat-server`。
3. 验证三者都能成功注册并进入稳定心跳。

### 8.2 工具路由

1. 验证 Hub 工具视图能看到三者的 `AllowedCallerTypes`。
2. 验证网关层对不允许 caller 的工具调用能提前拦截。
3. 验证流式工具仍可正确透传 WS 路由。

### 8.3 生命周期

1. 验证 Hub 对 `file`、`ai-doubao`、`chat-server` 的 `service.lifecycle.health`。
2. 验证 Hub 对三者的 `service.lifecycle.shutdown`。
3. 验证停止后注册状态和路由状态能同步收敛。

### 8.4 回归范围

至少覆盖：

- `hub.governance.service.register`
- `hub.governance.service.heartbeat`
- `service.lifecycle.health`
- `service.lifecycle.shutdown`
- `storage.file.*`
- `storage.blob.*`
- `app.chat.*`
- `ai.*`

---

## 9. 风险与约束

### 9.1 迁移风险

- 如果先严格收紧 Hub 的 caller 约束，而 `ai-doubao`、`chat-server` 还没补齐 `AllowedCallerTypes`，工具链会先出现拒绝调用。
- 如果先删除旧路径 shim，而心跳和生命周期工具还没完全落地，Hub 会失去停止和探测能力。
- 如果只改 Service 不改 Hub 的 schema 和视图，治理视图会继续不一致。

### 9.2 兼容性约束

本计划不建议长期保留旧路径。

如必须保留过渡层，只能以内部 shim 形式存在，并写清删除条件和截止时间。

### 9.3 实施顺序约束

必须按以下顺序推进：

1. 先补齐 Service 侧 manifest 和生命周期 tool。
2. 再切 Service -> Hub 的心跳到 tool 语义。
3. 再收紧 Hub 侧治理视图与路径 fallback。
4. 最后清理旧路径和历史残留。

---

## 10. 完成标准

满足以下条件时，认为本轮重构完成：

1. `file`、`ai-doubao`、`chat-server` 都能以 tool 语义完成注册、心跳和生命周期治理。
2. Hub 不再把 `/admin/shutdown` 和 `/api/service/heartbeat` 作为正式契约。
3. 三个 service 的 manifest 都能稳定输出 `AllowedCallerTypes`。
4. Hub 的 route/schema/tool 视图能完整展示 caller 约束。
5. `service.lifecycle.health` 与 `service.lifecycle.shutdown` 成为 Hub 管理 service 的统一入口。
6. 旧路径只剩短期 shim，没有新的正式调用方。
7. 启动、注册、心跳、探测、停止、重启的全链路 smoke 通过。

---

## 11. 待确认事项

1. `ai-doubao.system.shutdown` 是否直接替换为 `service.lifecycle.shutdown`，还是保留一段显式别名窗口。
2. `chat-server` 的 `app.chat.*` 默认 caller 策略是否统一为 `user`，还是需要保留更细粒度的 caller 分层。
3. `file` 的 `/healthz` 与 `/admin/shutdown` 是否需要保留任何兼容窗口。
4. Hub 的 route/schema 是否需要把 `AllowedCallerTypes` 作为强展示字段，还是只在管理视图显示。
5. 旧路径 shim 的删除日期是否需要单独写入 devlog 或迁移清单。

