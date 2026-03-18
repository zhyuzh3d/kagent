# Hub-Service 旧残留清理开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 01:30 CST  
> 范围：`hub/internal/{app,gateway,routing,supervisor}/`、`services/{account,database,surface-manager,file,ai-doubao,chat-server}/`、`services/*/internal/app/`、`webui/page/{account,chat}/`、`pkg/{hubsvc,toolproto}/`、`scripts/`  
> 目标约束：Hub 与 Service 的正式契约只保留 tool 语义；旧 HTTP 面、旧别名、旧兼容字段只能作为过渡 shim，且必须有明确删除条件。

## 0. 结论

仓库当前已经进入“主链路 tool 化、旧残留仍分散存在”的阶段。

1. `webui/page/account`、`webui/page/chat` 已经切到 `/api/tool/call` 和 `/api/tool/ws`，不是本轮清理对象。
2. Hub 侧治理工具、系统工具和路由视图已经工具化，但 `process_control.go` 仍保留 `/healthz` fallback。
3. `account`、`database`、`surface-manager`、`file`、`chat-server`、`ai-doubao` 仍保留不同形态的旧 HTTP 面、旧别名工具名或兼容字段。
4. 这些残留可以清，但必须按“先补正式 tool 契约，再删旧路径”的顺序推进，否则会把可用性、停机和探活链路一起删掉。

因此，本计划不是继续给每个服务单独打补丁，而是一次性列出**全部可清除旧残留**，并为每一项给出对应的收口方案。

---

## 1. 已核验事实

以下事实已由当前仓库代码核验：

- `webui/page/chat/tool-call.js`、`webui/page/account/index.html` 已通过 `/api/tool/call` 调用账号和聊天工具。
- `webui/page/chat/session-controller.js` 通过 `/api/tool/ws?tool_id=app.chat.stream` 连接聊天流式工具。
- `hub/internal/supervisor/handler.go` 已把 `hub.governance.service.register`、`hub.governance.service.heartbeat`、`hub.governance.service.drain` 收进工具平面。
- `hub/internal/gateway/hub_manifest.go` 已把 `hub.admin.*`、`hub.system.*`、`hub.governance.service.*` 定义为 Hub 内置工具。
- `hub/internal/supervisor/process_control.go` 目前仍会在 `service.lifecycle.health` 失败后回退到 `/healthz`。
- `services/account/cmd/account/main.go`、`services/database/cmd/database/main.go`、`services/surface-manager/cmd/surface-manager/main.go` 仍保留 `/healthz`、`/admin/shutdown` 和 `/api/service/heartbeat` 改写逻辑。
- `services/file/cmd/file/main.go` 已有 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，但仍保留 `/healthz`、`/service/info`、`/service/tools`。
- `services/chat-server/cmd/chat-server/main.go` 已有 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，但仍保留 `/healthz`、`/service/info`、`/service/tools`、`/admin/shutdown`。
- `services/ai-doubao/cmd/ai-doubao/main.go` 已有 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，但仍保留 `ai-doubao.system.health`、`ai-doubao.system.shutdown` 以及 `/v1/asr/stream`、`/v1/llm/ws`、`/v1/tts/synthesize`。
- `services/database/internal/app/hub_builtins.go`、`services/surface-manager/internal/app/hub_builtins.go` 仍主要使用 `ScopeSupport`，不是统一的 `AllowedCallerTypes` 输入。
- `hub/internal/app/hub_platform.go`、`services/file/internal/app/hub_platform.go`、`services/database/internal/app/hub_platform.go`、`services/surface-manager/internal/app/hub_platform.go` 都还保留 `ScopeSupport` 兼容归一化。

---

## 2. 可清除旧残留清单

### 2.1 Hub 侧残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `service.lifecycle.health` 失败后的 `/healthz` fallback | `hub/internal/supervisor/process_control.go` | Hub 仍然依赖路径级健康检查，导致“工具化已完成”的判断不彻底 | 先要求所有受管服务都提供 `service.lifecycle.health`，再删除 `BuildServiceControlURL(..., "/healthz")` 分支，只保留 tool 调用结果和 PID 状态作为判定依据 |

### 2.2 `account` 残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `/healthz` | `services/account/cmd/account/main.go` | 外部探活和运维仍可绕开 Hub 直接打服务 | 增加 `service.lifecycle.health` 工具后删除 HTTP `/healthz`，健康检查统一改为 `hub.system.smoke.test` / `service.lifecycle.health` |
| `/admin/shutdown` | `services/account/cmd/account/main.go` | 服务停机仍存在直连管理面 | 增加 `service.lifecycle.shutdown` 工具并由 Hub 通过 `/service/tool/exec` 调用，删除 `/admin/shutdown` |
| `buildHubHeartbeatURL()` -> `/api/service/heartbeat` | `services/account/cmd/account/main.go` | 注册后心跳仍受旧路径语义约束 | 让心跳固定调用 `POST /api/tool/call`，目标 tool 为 `hub.governance.service.heartbeat`，删除路径改写函数 |
| `hub_only` / `healthzRequested` 兼容分支 | `services/account/cmd/account/main.go` | 让服务行为依赖一个“伪健康参数”，增加协议噪音 | 在 `service.lifecycle.health` 稳定后删除该分支，改为真实 lifecycle tool 响应 |

### 2.3 `database` 残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `/healthz` | `services/database/cmd/database/main.go` | 直接探活绕过 Hub | 用 `service.lifecycle.health` 替代，删除 HTTP 健康面 |
| `/service/info`、`/service/tools` | `services/database/cmd/database/main.go` | 服务自身继续暴露 introspection 面，Hub 的 route/schema 视图不再是唯一事实源 | 将这些信息统一由 Hub admin tool 输出，删除服务侧 introspection HTTP 面 |
| `/admin/shutdown` | `services/database/cmd/database/main.go` | 仍然存在直连停机面 | 增加 `service.lifecycle.shutdown` 并由 Hub 调用，删除 `/admin/shutdown` |
| `buildHubHeartbeatURL()` -> `/api/service/heartbeat` | `services/database/cmd/database/main.go` | 心跳仍被旧路径绑定 | 改为直接走 `hub.governance.service.heartbeat` tool 调用，删除重写逻辑 |
| `hub_only` / `healthzRequested` 兼容分支 | `services/database/cmd/database/main.go` | 让工具执行面仍含路径兼容分支 | 用 lifecycle tools 替换 sentinel 逻辑，删除该分支 |
| `ScopeSupport` 仅兼容元数据 | `services/database/internal/app/hub_builtins.go`、`services/database/internal/app/hub_platform.go` | caller 约束仍以旧字段为中心，Hub 需要回填兼容 | 为所有 tool 补齐 `AllowedCallerTypes`，再删除 `ScopeSupport` 字段与回填逻辑 |

### 2.4 `surface-manager` 残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `/healthz` | `services/surface-manager/cmd/surface-manager/main.go` | 直接探活绕过 Hub | 以 `service.lifecycle.health` 替代并删除 HTTP 健康面 |
| `/service/info`、`/service/tools` | `services/surface-manager/cmd/surface-manager/main.go` | 服务侧 introspection 与 Hub 元数据视图重复 | 统一由 Hub admin tool 输出，删除服务侧 introspection HTTP 面 |
| `/admin/shutdown` | `services/surface-manager/cmd/surface-manager/main.go` | 仍可直连停机 | 增加 `service.lifecycle.shutdown` 并由 Hub 调用，删除 `/admin/shutdown` |
| `buildHubHeartbeatURL()` -> `/api/service/heartbeat` | `services/surface-manager/cmd/surface-manager/main.go` | 仍使用旧路径做心跳 | 改为 `/api/tool/call` + `hub.governance.service.heartbeat`，删除路径改写逻辑 |
| `hub_only` / `healthzRequested` 兼容分支 | `services/surface-manager/cmd/surface-manager/main.go` | 让执行面仍依赖 sentinel | 在 lifecycle tool 稳定后删除 |
| `ScopeSupport` 仅兼容元数据 | `services/surface-manager/internal/app/hub_builtins.go`、`services/surface-manager/internal/app/hub_platform.go` | caller 约束仍停留在旧字段 | 全量补齐 `AllowedCallerTypes`，移除 `ScopeSupport` |

### 2.5 `file` 残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `/healthz` | `services/file/cmd/file/main.go` | Hub 以外仍可探活 | 以 `service.lifecycle.health` 替换，删除 HTTP `/healthz` |
| `/service/info`、`/service/tools` | `services/file/cmd/file/main.go` | 服务侧仍有独立 introspection 面 | 将能力视图统一收口到 Hub route/schema，删除这两个 HTTP 面 |
| `ScopeSupport` fallback | `services/file/cmd/file/main.go`、`services/file/internal/app/hub_builtins.go`、`services/file/internal/app/hub_platform.go` | caller 语义存在双轨（AllowedCallerTypes + ScopeSupport） | 保持 `AllowedCallerTypes` 为唯一正式输入，删除 `ScopeSupport` 回填和相关字段 |

### 2.6 `chat-server` 残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `/healthz` | `services/chat-server/cmd/chat-server/main.go` | 可绕过 Hub 探活 | 以 `service.lifecycle.health` 替换并删除 `/healthz` |
| `/service/info`、`/service/tools` | `services/chat-server/cmd/chat-server/main.go` | 服务侧 introspection 与 Hub 管理视图重复 | 统一通过 `hub.admin.routes.get` / `hub.admin.services.list` 获取，删除 HTTP introspection 面 |
| `/admin/shutdown` | `services/chat-server/cmd/chat-server/main.go` | 直连停机面仍存在 | 用 `service.lifecycle.shutdown` 替换，删除 `/admin/shutdown` |
| `hub_only` / `healthzRequested` 兼容分支 | `services/chat-server/cmd/chat-server/main.go` | 让执行路径带有旧探活语义 | 在生命周期和探活工具化完成后删除 |
| `ScopeSupport` 字段残留 | `services/chat-server/internal/app/service_manifest.go` | manifest 结构仍保留旧兼容字段 | 删除 `ScopeSupport` 字段，只保留 `AllowedCallerTypes` |

### 2.7 `ai-doubao` 残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `/v1/asr/stream`、`/v1/llm/ws`、`/v1/tts/synthesize` | `services/ai-doubao/cmd/ai-doubao/main.go` | 外部仍可绕过 `service/tool/*` 直接访问旧业务接口 | 将所有消费方切到 `/service/tool/exec`、`/service/tool/ws`，确认无外部依赖后删除旧 `v1` 面 |
| `ai-doubao.system.health`、`ai-doubao.system.shutdown` | `services/ai-doubao/cmd/ai-doubao/main.go` | 生命周期命名与 Hub 的 `service.lifecycle.*` 不一致 | 删除旧别名，只保留 `service.lifecycle.health`、`service.lifecycle.shutdown` |
| `hub_only` / `healthzRequested` 兼容分支 | `services/ai-doubao/cmd/ai-doubao/main.go` | 工具执行面仍含旧 sentinel | 在 lifecycle tool 稳定后删除 |
| `ScopeSupport` 字段残留 | `services/ai-doubao/internal/app/service_manifest.go` | manifest 结构仍保留旧兼容字段 | 删除 `ScopeSupport` 字段，只保留 `AllowedCallerTypes` |

### 2.8 共享元数据残留

| 残留项 | 现状文件 | 影响 | 解决方案 |
| --- | --- | --- | --- |
| `ScopeSupport` 兼容字段与归一化 | `hub/internal/app/hub_platform.go`、`services/file/internal/app/hub_platform.go`、`services/database/internal/app/hub_platform.go`、`services/surface-manager/internal/app/hub_platform.go` | 旧 caller 语义仍被当作输入来源，导致 `AllowedCallerTypes` 不能成为唯一事实源 | 先让所有服务清单补齐 `AllowedCallerTypes`，再删除 `ScopeSupport` 字段和 `normalizeToolDescriptor()` 中的兼容归一化 |
| `ScopeSupport` 只出现在 builtins 中的旧写法 | `services/database/internal/app/hub_builtins.go`、`services/surface-manager/internal/app/hub_builtins.go` | 权限模型仍停留在旧字段，Hub 需要依赖兼容回填 | 为这些 builtins 补齐 `AllowedCallerTypes`，再清掉 `ScopeSupport` |

---

## 3. 推荐清理顺序

### 3.1 第一阶段：先补正式生命周期工具

1. 先在 `account`、`database`、`surface-manager`、`file`、`chat-server`、`ai-doubao` 中确认 `service.lifecycle.health` / `service.lifecycle.shutdown` 的完整实现。
2. 确保 Hub 通过 `/service/tool/exec` 调用停机和探活，不再依赖路径级控制面。
3. 同步补齐自动化测试，确保生命周期工具在每个服务中返回稳定结构。

### 3.2 第二阶段：删除服务侧旧 HTTP 面

1. 先删 `/service/info`、`/service/tools` 这类 introspection 面，避免双事实源。
2. 再删 `/healthz` 和 `/admin/shutdown`。
3. 最后删 `ai-doubao` 的 `/v1/*` 直连业务接口。

### 3.3 第三阶段：清掉旧别名和 sentinel 分支

1. 删 `ai-doubao.system.*` 旧别名。
2. 删所有服务中 `hub_only` / `healthzRequested` 的兼容分支。
3. 删 `buildHubHeartbeatURL()` / `/api/service/heartbeat` 改写逻辑，统一使用 `hub.governance.service.heartbeat`。

### 3.4 第四阶段：清掉 `ScopeSupport`

1. 先在所有 manifest 和 builtins 中补齐 `AllowedCallerTypes`。
2. 再删除 `ScopeSupport` 字段及 `normalizeToolDescriptor()` 中的兼容归一化。
3. 最后同步更新 `hub/internal/routing/schema.go`、`hub/internal/app/hub_platform.go`、说明文档与测试快照。

---

## 4. 验收标准

1. `rg -n "/api/service/heartbeat|/admin/shutdown|/service/info|/service/tools|/v1/asr/stream|/v1/llm/ws|/v1/tts/synthesize|ai-doubao\\.system\\.(health|shutdown)|ScopeSupport|healthzRequested|hub_only" services hub webui -S` 不再命中任何正式路径或正式字段残留，最多只剩明确标注为 shim 的注释。
2. Hub 的服务存活与停机流程只依赖 `service.lifecycle.health`、`service.lifecycle.shutdown` 和 `hub.governance.service.*`。
3. `webui/page/account`、`webui/page/chat` 仍保持 `/api/tool/call` / `/api/tool/ws`，不回退到旧 HTTP 直连。
4. `hub/internal/routing/schema.go`、`hub/internal/gateway/hub_manifest.go` 的工具视图只输出 `AllowedCallerTypes`，不再依赖 `ScopeSupport`。
5. 所有服务的注册、心跳、停机、探活都能通过同一套 tool 平面完成，并且 `go test ./...` 与一次完整 smoke 验证通过。

---

## 5. 风险与约束

1. 不能先删 `healthz` 再补 `service.lifecycle.health`，否则 Hub 会失去最后的可用性探测兜底。
2. 不能先删 `ScopeSupport` 再统一 `AllowedCallerTypes`，否则旧清单会丢失 caller 约束信息。
3. `ai-doubao` 的 `/v1/*` 需要先确认是否还有外部直连依赖，再删除；如果还有依赖，必须先给出迁移窗口。
4. `chat-server` 的 `/admin/shutdown` 如果还被脚本或运维手工流程使用，必须先迁移到 Hub 的 `hub.system.shutdown` 再删。
5. `database`、`surface-manager` 的 `ScopeSupport` 目前还是旧权限模型输入，必须在 Hub schema 与服务注册全部切到 `AllowedCallerTypes` 后再清理。

---

**文档更新时间**：2026-03-19 01:30 CST

**信息来源**：`hub/cmd/hub/main.go`、`hub/internal/app/hub_platform.go`、`hub/internal/gateway/hub_manifest.go`、`hub/internal/gateway/system_handler.go`、`hub/internal/supervisor/handler.go`、`hub/internal/supervisor/process_control.go`、`hub/internal/routing/schema.go`、`services/account/cmd/account/main.go`、`services/database/cmd/database/main.go`、`services/database/internal/app/hub_builtins.go`、`services/database/internal/app/hub_platform.go`、`services/surface-manager/cmd/surface-manager/main.go`、`services/surface-manager/internal/app/hub_builtins.go`、`services/surface-manager/internal/app/hub_platform.go`、`services/file/cmd/file/main.go`、`services/file/internal/app/hub_builtins.go`、`services/file/internal/app/hub_platform.go`、`services/chat-server/cmd/chat-server/main.go`、`services/chat-server/internal/app/service_manifest.go`、`services/ai-doubao/cmd/ai-doubao/main.go`、`services/ai-doubao/internal/app/service_manifest.go`、`webui/page/account/index.html`、`webui/page/chat/tool-call.js`、`webui/page/chat/session-controller.js`、`webui/page/chat/index.html`。
