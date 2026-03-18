# File Service Hub-Service 重构开发计划

> **文档类型**：开发计划（devplan）  
> **时间**：2026-03-18 22:37 CST  
> **范围**：`services/file/`、`hub/internal/{gateway,routing,supervisor,app}/`、`pkg/toolproto/`、按需检查 `webui/`  
> **信息来源（可核验）**：  
> - `services/file/cmd/file/main.go`：当前 file 入口只初始化 `ScopedFileService`、`BlobService`，注册 `/service/tool/exec`，并分发 `storage.file.*` / `storage.blob.*`  
> - `services/file/internal/app/*.go`：存在一批未被入口引用的残留模块，包括 `auth.go`、`surfacefs.go`、`session.go`、`pipeline.go`、`llm.go`、`asr.go`、`tts.go`、`runtime_config.go`、`sqlite_store.go`、`operation_log.go`、`hub_platform.go`、`hub_builtins.go` 等  
> - `hub/internal/gateway/hub_manifest.go`：Hub 内部能力已经以标准 tool manifest 形式对外建模，不再依赖零散接口心智  
> - `hub/internal/gateway/tool_handler.go`：Hub 已实现标准 tool 路由、caller 注入、HubAuth 转发、WS 代理与 `effects` 落地  
> - `hub/internal/supervisor/handler.go`、`hub/internal/supervisor/lifecycle.go`：Hub 已实现 service 注册、心跳、启动、停止与运行态清单装配  
> - `hub/internal/routing/engine.go`、`hub/internal/routing/schema.go`：Hub 以 service manifest 的 `Provides` 构建 tool 路由与实例选择  
> - `hub/internal/app/hub_platform.go`：Hub 侧已统一 builtin tools 与注册工具目录，file 不应再维护平台复制件  
> - `pkg/toolproto/v1.go`、`pkg/toolproto/supervisor.go`：tool / supervisor 协议已支持 `allowed_caller_types`、`streaming`、`ws_path`、`effects` 等字段  
> - `webui/page/chat/tool-call.js`、`webui/page/surface/admin.html`：前端统一经 `/api/tool/call` 调用 tool；目前未发现 file 专属直连入口

## 0. 结论

`services/file` 的重构目标不是“补齐一个自己的平台层”，而是直接收敛成一个遵循 Hub 标准 tool 体系的纯服务模块。

新 Hub 已经把内部 service/tool 管理统一到标准 manifest 和 `/api/tool/call` 链路，所以 file 侧不应再引入新的 Hub 兼容分支，也不应保留 file 本地的 AI/service 协议心智。

本次重构的判断原则：

1. 对外只保留 `storage.file.*` 与 `storage.blob.*`。
2. 对内只保留文件与 blob 的真实领域逻辑，残留模块直接删除或迁出。
3. manifest、权限、caller 限制、工具描述全部按 Hub 当前标准 tool 格式对齐。
4. `webui/` 只做条件性适配，不预设必须改动；若不存在 file 专属硬编码，则不动前端。

---

## 1. Hub 侧对接事实

### 1.1 Hub 已统一为标准 tool 平面

新的 Hub 不是“多个零散接口拼接”的状态，而是“内部能力也按 tool 统一建模”的状态。

关键事实：

- `hub/internal/gateway/hub_manifest.go` 已把 Hub 内部能力组织成标准 tool manifest。
- `hub/internal/gateway/tool_handler.go` 统一处理 `/api/tool/call`，包含 caller 识别、头部清洗、HubAuth 注入、目标 service 选择、结果写回和 `effects` 执行。
- `hub/internal/routing/engine.go` 仍然以 `ServiceManifest.Provides` 进行路由和实例选择。

这意味着 file 侧只需要把自身工具声明清楚，Hub 会按统一格式接入，不需要 file 自己去发明一套平台协议。

### 1.2 Service 注册链路已经标准化

`hub/internal/supervisor/handler.go` 与 `hub/internal/supervisor/lifecycle.go` 已经把 service 注册、心跳、启动、停止与运行态清单装配收敛成统一链路。

对 file 来说，目标不是创建新的治理接口，而是：

- 提供完整 manifest；
- 提供标准 tool 描述；
- 让 Hub 侧按 `AllowedCallerTypes`、`CapabilitiesRequired`、`Streaming`、`WSPath` 等字段直接接入。

### 1.3 协议边界应直接落在 `pkg/toolproto`

`pkg/toolproto/v1.go` 与 `pkg/toolproto/supervisor.go` 已经覆盖 file 所需的关键字段，包括：

- `CallRequest.Context.Caller`
- `CallResponse.Effects`
- `ServiceTool.AllowedCallerTypes`
- `ServiceTool.Streaming`
- `ServiceTool.WSPath`

因此，file 重构不应再引入 `tool_protocol.go` 之类的本地协议复制件；如需额外约束，只保留 file 自己的 manifest/schema helper，协议基底直接复用 `pkg/toolproto`。

---

## 2. 当前 file 现状

### 2.1 线上真正生效的部分

`services/file/cmd/file/main.go` 当前只承担这些职责：

- 读取 bootstrap secret，与 Hub 建立互信。
- 初始化 `ScopedFileService` 和 `BlobService`。
- 装配并注册 file 的 service manifest。
- 暴露 `/healthz`、`/service/info`、`/service/tools`、`/service/tool/exec`、`/admin/shutdown`。
- 在 `/service/tool/exec` 内分发：
  - `storage.file.read`
  - `storage.file.write`
  - `storage.file.list`
  - `storage.file.delete`
  - `storage.file.exists`
  - `storage.file.stat`
  - `storage.file.mkdir`
  - `storage.file.rename`
  - `storage.file.copy`
  - `storage.blob.put`
  - `storage.blob.get`
  - `storage.blob.sign_url`
  - `storage.blob.gc`

### 2.2 明确的历史残留

`services/file/internal/app` 里存在一批与当前对外职责无关的残留文件：

- 账号/JWT：`auth.go`
- Surface / 会话：`surfacefs.go`、`session.go`
- Chat / pipeline：`pipeline.go`、`message_types.go`、`context_meta.go`、`assistant_envelope.go`
- AI provider：`llm.go`、`asr.go`、`tts.go`、`provider_factory.go`、`runtime_config.go`
- 运行态 / 存储辅助：`sqlite_store.go`、`operation_log.go`
- 平台复制件：`hub_platform.go`、`hub_builtins.go`
- 过渡协议：`ai_service_protocol.go`、`protocol.go`

这些文件不应继续被视为 file 服务边界的一部分。

### 2.3 当前结构问题

file 现在的问题不是缺工具，而是边界不干净：

1. 对外只有 storage 工具。
2. 对内却残留平台级、会话级、AI 级逻辑。
3. 入口与实现之间没有严格分层，和新 Hub 的标准 tool 服务形态不一致。

---

## 3. 重构目标

### 3.1 对外目标

file 只作为一个清晰的 storage tool service 存在：

- `storage.file.read`
- `storage.file.write`
- `storage.file.list`
- `storage.file.delete`
- `storage.file.exists`
- `storage.file.stat`
- `storage.file.mkdir`
- `storage.file.rename`
- `storage.file.copy`
- `storage.blob.put`
- `storage.blob.get`
- `storage.blob.sign_url`
- `storage.blob.gc`

新增能力也必须按同一 tool 模式扩展，不回退到旧的残留结构。

### 3.2 对内目标

file 内部只保留三个清晰层次：

1. **入口层**：负责 bootstrap、manifest 装配、注册、健康检查、shutdown。
2. **协议/调度层**：负责 service manifest、tool schema、caller 权限、tool dispatch。
3. **领域层**：负责文件路径约束、scope 解析、blob 存储、元数据、签名和 GC。

### 3.3 架构约束

- 不保留兼容接口。
- 不保留 file 本地的 Hub 平台复制件。
- 不保留账号、Surface、chat、AI provider 残留代码。
- 不引入 file 直连前端的旁路接口。
- 对外调用只走 Hub `/api/tool/call`。

---

## 4. Hub 对 file 的治理要求

### 4.1 注册时只声明真实能力

file 注册到 Hub 时，`Provides` 只能包含真实可执行工具，不得再出现平台级伪工具或历史残留工具。

注册描述需要完整补齐：

- `AllowedCallerTypes`
- `CapabilitiesRequired`
- `Streaming`
- `WSPath`（file 当前应为空）
- `TimeoutMSDefault`

### 4.2 caller 权限必须由 Hub 统一生效

Hub 已经支持在 tool 调用前做 caller type 检查，因此 file 应把权限声明写进 manifest，而不是在入口中自定义平台分支。

建议的 caller 策略：

- `storage.file.*`：允许 `user`、`surface`、`service`
- `storage.blob.put/get/sign_url`：允许 `user`
- `storage.blob.gc`：允许 `service`

### 4.3 不需要流式能力

file 当前没有 WebSocket 工具诉求：

- `streaming` 默认应为 `false` 或 `none`
- `ws_path` 不应出现在 file manifest 中

---

## 5. 重构方案

### Phase 1：协议与 manifest 收敛

目标是把 file 的工具协议面收干净，并对齐 Hub 的标准 tool 形态。

任务：

1. 删除 `services/file/internal/app/ai_service_protocol.go` 和 `services/file/internal/app/protocol.go` 这类过渡协议文件的职责。
2. 不再创建 `tool_protocol.go` 作为 file 本地协议复制件。
3. 新增专用的 `service_manifest.go`，明确输出：
   - `service_id`
   - `service_name`
   - `version`
   - `reliability`
   - `visibility`
   - `provides`
4. 为每个 tool 定义 `allowed_caller_types`、`capabilities_required`、`timeout_ms_default`。
5. 如需额外 schema 约束，只保留 file 自己的 schema/helper，不复制 Hub 协议。

验收：

- file 只声明自己的 storage 工具。
- Hub 的服务/路由/工具清单里，file 只呈现 storage 能力。

### Phase 2：领域逻辑重建

目标是把真正有价值的文件能力从历史残留中剥离出来。

任务：

1. 保留并整理 `ScopedFileService`，作为 scoped filesystem 的唯一实现。
2. 保留并整理 `BlobService`，作为 blob 元数据和数据文件的唯一实现。
3. 新增统一的 tool dispatcher，按 `tool_id` 显式分发，不再把业务逻辑写进 `main.go`。
4. 把路径约束、scope 解析、caller 到 storage scope 的映射单独抽成 policy/helper。
5. 统一错误映射到 `toolproto.ErrorCode*`，让 Hub 能稳定做状态转换。

建议的内部文件方向：

- `service_manifest.go`
- `tool_dispatch.go`
- `storage_scope.go`
- `file_store.go`
- `blob_store.go`
- `storage_policy.go`

验收：

- 所有 `storage.file.*` 与 `storage.blob.*` 都有明确 handler。
- 任一 handler 都不依赖 session、LLM、ASR、TTS、Surface token 或 Hub 平台逻辑。

### Phase 3：残留代码清理

目标是删除 file 内部所有不再参与服务职责的文件。

建议删除或迁出的文件组：

- `auth.go`
- `surfacefs.go`
- `session.go`
- `pipeline.go`
- `llm.go`
- `asr.go`
- `tts.go`
- `runtime_config.go`
- `sqlite_store.go`
- `operation_log.go`
- `context_meta.go`
- `assistant_envelope.go`
- `message_types.go`
- `provider_factory.go`
- `protocol.go`
- `ai_service_protocol.go`
- `hub_platform.go`
- `hub_builtins.go`

验收：

- `services/file/internal/app` 只保留 file service 真正需要的代码。
- `rg` 不再找到 file 入口对上述残留文件的引用。

### Phase 4：入口与 Hub 对齐

目标是把 file 的入口做成标准、薄、纯装配的 service 入口。

任务：

1. 保留 `cmd/file/main.go` 作为唯一入口，但把业务逻辑下沉到内部包。
2. 注册流程继续走 `.service_secret` + Hub register。
3. 注册 payload 只包含 file 的真实 manifest 与工具列表。
4. `/service/info` 和 `/service/tools` 的输出与 manifest 保持一致。
5. 明确健康检查与 shutdown 行为，保持最小化。
6. Hub 侧不新增 file 专属兼容分支；若标准字段已覆盖需求，则直接按统一链路接入。

验收：

- `main.go` 只负责装配和 HTTP 入口。
- file 不再承载任何平台级治理逻辑。

### Phase 5：按需检查 webui

当前没有证据表明 webui 直接依赖 file 专属接口，因此前端适配只做条件检查，不预设一定要改。

任务：

1. 搜索 webui 中是否存在对 file 旧接口、旧路径或旧命名的硬编码依赖。
2. 如果某个页面需要 file 存储能力，统一改为经 Hub 调 `storage.file.*` / `storage.blob.*`。
3. 如果 webui 里没有 file 专属假设，则不做前端改动。
4. 如需展示说明文案，只改描述，不引入直连 file 的新旁路。

优先检查：

- `webui/page/chat/`
- `webui/page/surface/`

验收：

- 前端不直连 file。
- 需要 file 能力的页面只通过 `/api/tool/call` 消费工具。

---

## 6. 关键文件清单

### 6.1 重点保留并重构

- `services/file/cmd/file/main.go`
- `services/file/internal/app/storage_services.go`
- `services/file/internal/app/blob_service.go`
- `services/file/internal/app/runtime_root.go`
- `services/file/manifest.json`

### 6.2 重点新建或改名

- `services/file/internal/app/service_manifest.go`
- `services/file/internal/app/tool_dispatch.go`
- `services/file/internal/app/storage_scope.go`
- `services/file/internal/app/file_store.go`
- `services/file/internal/app/blob_store.go`
- `services/file/internal/app/storage_policy.go`

### 6.3 重点删除

- `services/file/internal/app/auth.go`
- `services/file/internal/app/surfacefs.go`
- `services/file/internal/app/session.go`
- `services/file/internal/app/pipeline.go`
- `services/file/internal/app/llm.go`
- `services/file/internal/app/asr.go`
- `services/file/internal/app/tts.go`
- `services/file/internal/app/runtime_config.go`
- `services/file/internal/app/sqlite_store.go`
- `services/file/internal/app/operation_log.go`
- `services/file/internal/app/context_meta.go`
- `services/file/internal/app/assistant_envelope.go`
- `services/file/internal/app/message_types.go`
- `services/file/internal/app/provider_factory.go`
- `services/file/internal/app/protocol.go`
- `services/file/internal/app/ai_service_protocol.go`
- `services/file/internal/app/hub_platform.go`
- `services/file/internal/app/hub_builtins.go`

---

## 7. 验收标准

1. file 仅对外提供 storage 相关 tool 能力。
2. file 的 manifest、工具定义、caller 限制与 Hub 的路由/注册模型完全一致。
3. `services/file/internal/app` 中不再保留与当前职责无关的残留代码。
4. Hub 侧 `tool_handler` 能按 manifest 正确路由 file 工具，并按 caller 类型限制访问。
5. `webui` 仅在发现 file 专属硬编码时才需要调整，否则不改。
6. 通过编译和最小烟测验证后，file 应表现为一个独立、干净、可治理的服务模块。

---

## 8. 风险控制

- **最大风险**：清理残留时误删仍被间接引用的 helper。
  - 控制方式：先用 `rg` 确认调用链，再逐步删除，保持每步可编译。
- **第二风险**：manifest 和 Hub 注册字段不一致，导致路由看得到工具但无法正确调用。
  - 控制方式：先对齐 `toolproto.ServiceTool` 和 file 的 manifest/schema，再动 service 实现。
- **第三风险**：误判 webui 为必改，造成不必要的前端变更。
  - 控制方式：先检索 file 专属硬编码，再决定是否调整前端。

---

## 9. 计划依据

本计划基于以下事实推导：

- 新 Hub 已经把 service 治理、注册、路由、caller 注入和副作用写回收敛到统一 tool 链路。
- file 当前唯一有效的对外职责只是 storage/file/blob 工具能力。
- file 内部存在大量与当前职责无关的历史残留，且这些残留没有来自入口的实际调用路径。
- 因此，file 的正确方向不是继续叠加兼容层，而是直接收敛成一个标准 tool service。
