# services/ai_doubao 全工具重建开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 03:51 CST  
> 范围：`services/ai_doubao/`、`hub/internal/{gateway,supervisor,transport}/`、`pkg/{hubsvc,toolproto}/`、`config/`、`hub/config/`  
> 目标约束：新子项目必须完整承载当前 `ai-doubao` 已对外提供的 tools 集合，且 Service 与 Hub 之间只保留 tool 语义。

## 0. 结论

现有 `services/ai-doubao` 已经具备工具化雏形，但目录命名、生命周期别名和运行配置仍停留在旧布局中。  
这次重建的目标不是重写业务能力，而是把当前已验证的能力迁移到新的 underscore 子项目 `services/ai_doubao`，并把 Hub 交互、manifest、运行时根目录和配置引用一次性收敛到同一条 tool-first 线路。

新子项目必须保留并正确注册以下工具：

- `ai.speech.asr`
- `ai.llm.stream`
- `ai.speech.tts`
- `service.lifecycle.health`
- `service.lifecycle.shutdown`

迁移窗口内可保留 `ai-doubao.system.health` / `ai-doubao.system.shutdown` 作为兼容别名，但它们不能再作为正式契约；正式契约必须统一到 `service.lifecycle.*`。

## 1. 真实核验事实

以下事实已由当前仓库代码核验：

- `services/ai-doubao/cmd/ai-doubao/main.go` 已通过 `POST /api/tool/call` 调用 `hub.governance.service.register` 与 `hub.governance.service.heartbeat`。
- `services/ai-doubao/cmd/ai-doubao/main.go` 已暴露 `POST /service/tool/exec` 与 `GET /service/tool/ws`。
- `services/ai-doubao/cmd/ai-doubao/main.go` 已在本地 dispatcher 中处理 `service.lifecycle.health`、`service.lifecycle.shutdown`，并保留旧别名。
- `services/ai-doubao/internal/app/service_manifest.go` 已支持 `AllowedCallerTypes`、`Streaming`、`WSPath`、`CapabilitiesRequired` 等 tool 元数据。
- `services/ai-doubao/internal/app/runtime_root.go` 仍以 `services/ai-doubao` 作为根目录候选，需要改为新子项目路径。
- `config/services.json` 与 `hub/config/services.json` 仍指向 `services/ai-doubao`，新子项目上线前必须同步切换。

## 2. 问题定义

### 2.1 目录与运行时边界不一致

服务实现已经工具化，但目录名仍是连字符风格。对当前仓库来说，这会让新旧实现并存、配置引用和运行时根目录解析容易发生漂移。

### 2.2 生命周期契约未完全收口

当前 `ai-doubao.system.*` 仍是实际存在的兼容名。它可以作为迁移桥梁，但不应继续扩散到新实现或新调用方。

### 2.3 配置与 Hub 侧引用仍绑定旧目录

Hub 的生命周期配置仍引用旧目录，若直接新增新子项目而不切换配置，Hub 不会自动拉起新实现。

## 3. 重建目标

### 3.1 对外目标

新子项目 `services/ai_doubao` 必须提供与当前 `ai-doubao` 一致的 tool 集合与服务语义：

- `ai.speech.asr`
- `ai.llm.stream`
- `ai.speech.tts`
- `service.lifecycle.health`
- `service.lifecycle.shutdown`

### 3.2 对内目标

新子项目内部需要保留清晰分层：

1. `cmd/` 负责启动、注册、tool dispatcher 和生命周期处理。
2. `internal/app/` 负责协议、配置、manifest、运行时根目录、日志、流式处理和各工具实现。
3. 运行时配置和 bootstrap secret 必须继续走 Hub 互信链路，不引入旁路控制面。

### 3.3 迁移约束

- 不新增 REST 控制面。
- 不把旧别名升级成新正式契约。
- 不修改 tool 协议的全局字段语义。
- 新目录上线前，旧目录保持只读参考，避免回退时失去可运行基线。

## 4. 实施分期

### Phase 1：新子项目骨架

任务：

1. 创建 `services/ai_doubao/` 目录结构，包含 `cmd/ai-doubao/main.go`、`config/`、`internal/app/`、`manifest.json`。
2. 将当前 ai-doubao 的启动参数、模型配置、tool dispatcher、WS 处理和注册逻辑迁移到新目录。
3. 将 `runtime_root.go`、`service_manifest.go`、`tool_protocol.go`、`ai_service_protocol.go`、`service_token.go` 等必要文件纳入新子项目。
4. 保证新目录的 import path 与文件路径一致，避免仍然引用旧目录包路径。

验收：

- 新目录可独立构建。
- `go build ./services/ai_doubao/...` 通过。
- `services/ai_doubao/manifest.json` 与启动参数一致。

### Phase 2：工具集完整对齐

任务：

1. 保留 `ai.speech.asr`、`ai.llm.stream`、`ai.speech.tts` 的输入输出语义。
2. 保留 `service.lifecycle.health`、`service.lifecycle.shutdown` 作为正式生命周期工具。
3. 迁移期间保留 `ai-doubao.system.health` / `ai-doubao.system.shutdown`，但仅作为向后兼容别名。
4. `AllowedCallerTypes`、`TimeoutMSDefault`、`Streaming`、`WSPath` 等 manifest 字段必须完整输出。

验收：

- Hub 的 route/manifest 视图能看到新目录对应的完整 tools。
- 调用方只通过 `/service/tool/exec` 与 `/service/tool/ws` 访问服务能力。

### Phase 3：Hub 与配置切换

任务：

1. 更新 `config/services.json` 与 `hub/config/services.json`，把 `ai-doubao` 的运行目录切换到 `services/ai_doubao`。
2. 更新 `hub/config/config.json` 中相关服务引用。
3. 视需要同步 `config/configx.json.example` 中的 `startCommand`，避免示例与真实目录不一致。
4. 确认 Hub 仍能通过 tool 方式完成注册、心跳和生命周期探测。

验收：

- Hub 拉起的实际服务目录是 `services/ai_doubao`。
- 旧目录不再是运行事实来源。

### Phase 4：旧目录收口

任务：

1. 在新子项目稳定后，逐步把旧 `services/ai-doubao` 降级为历史参考。
2. 清理任何仍然指向旧目录的脚本或文档引用。
3. 删除或冻结旧兼容别名的新增入口，避免继续扩散。

验收：

- 新增调用方不再依赖旧目录名。
- 生命周期正式契约仅剩 `service.lifecycle.*`。

## 5. 验证方案

1. `go build ./services/ai_doubao/...`
2. 启动后核验 `hub.governance.service.register` 与 `hub.governance.service.heartbeat`
3. 调用 `ai.speech.asr`、`ai.llm.stream`、`ai.speech.tts` 三类工具
4. 调用 `service.lifecycle.health` 与 `service.lifecycle.shutdown`
5. 检查 Hub 路由与审计记录是否显示新目录对应实例

## 6. 风险与约束

- 迁移时若先切换 Hub 配置而新目录未完成构建，服务将无法拉起。
- 兼容别名若保留过久，会把旧契约重新固化成事实。
- WS 工具和音频流工具对路径、超时与握手要求更敏感，迁移时必须逐项验证。

## 7. 信息来源

- `services/ai-doubao/cmd/ai-doubao/main.go`
- `services/ai-doubao/internal/app/service_manifest.go`
- `services/ai-doubao/internal/app/runtime_root.go`
- `config/services.json`
- `hub/config/services.json`
- `hub/config/config.json`
- `hub/internal/supervisor/process_control.go`

