# services/chat_server 全工具重建开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 03:51 CST  
> 范围：`services/chat_server/`、`hub/internal/{gateway,supervisor,transport}/`、`pkg/{hubsvc,toolproto}/`、`config/`、`hub/config/`、`webui/page/chat/`  
> 目标约束：新子项目必须完整承载当前 `chat-server` 已对外提供的 tools 集合，并继续通过 Hub 的 tool 平面访问外部 AI / storage 能力。

## 0. 结论

`chat-server` 已经是一个以 tool 为中心的编排服务，但目录名、配置引用和若干兼容路径仍停留在旧布局。  
新子项目 `services/chat_server` 的目标是把当前验证过的能力完整迁移过去，同时去掉“目录名是旧风格、运行时仍绑定旧路径”的割裂感。

新子项目必须保留的工具集合为：

- `app.chat.project_list`
- `app.chat.project_create`
- `app.chat.project_update`
- `app.chat.project_delete`
- `app.chat.thread_list`
- `app.chat.thread_create`
- `app.chat.thread_update`
- `app.chat.thread_delete`
- `app.chat.stream`
- `service.lifecycle.health`
- `service.lifecycle.shutdown`

## 1. 真实核验事实

以下事实已由当前仓库代码核验：

- `services/chat-server/cmd/chat-server/main.go` 已通过 `POST /api/tool/call` 调用 `hub.governance.service.register` 与 `hub.governance.service.heartbeat`。
- `services/chat-server/cmd/chat-server/main.go` 已暴露 `POST /service/tool/exec` 与 `GET /service/tool/ws`。
- `services/chat-server/cmd/chat-server/main.go` 已在 dispatcher 中处理 `service.lifecycle.health` 与 `service.lifecycle.shutdown`。
- `services/chat-server/internal/app/service_manifest.go` 已声明完整的 `app.chat.*` 工具与生命周期工具。
- `services/chat-server/internal/app/runtime_root.go` 仍以 `services/chat-server` 作为根目录候选。
- `services/chat-server/internal/app/config.go` 强制 `ai_service.mode` 为 `service`，说明该服务运行时依赖 Hub 作为 tool 协议入口。
- `webui/page/chat/` 仍是当前前端交互层，与 `chat-server` 的工具接口联动。

## 2. 问题定义

### 2.1 目录与调用边界不统一

当前实现已经 tool 化，但目录命名与运行配置仍是旧布局。这样会让“新实现”和“旧实现”长期并存，增加维护成本。

### 2.2 生命周期契约虽然存在，但还需要迁移到新目录

`service.lifecycle.*` 已是正式工具名，但新子项目上线前，Hub 配置和运行根目录必须同步，否则生命周期工具再正确也无法落地。

### 2.3 业务工具对外依赖较多

`chat-server` 不是纯存储服务，它会通过 Hub 访问 `ai.*`、`storage.*` 等外部能力，因此迁移时必须保持 Hub 互信头、caller 头和 WebSocket 代理链路完整。

## 3. 重建目标

### 3.1 对外目标

新子项目应完整提供当前 chat 业务工具，并保留流式会话能力：

- 项目与线程管理
- 流式聊天能力
- 生命周期 health / shutdown

### 3.2 对内目标

1. `cmd/` 负责注册、tool dispatcher、WS 入口和生命周期。
2. `internal/app/` 负责协议、配置、manifest、store 访问和 Hub tool 客户端。
3. 依赖 Hub 的数据库与存储能力时，必须通过工具平面，不得新增直连旁路。

### 3.3 迁移约束

- 保留当前调用语义，不改变工具名。
- 不把 `webui/page/chat` 直接耦合到新目录名，前端只依赖 Hub 暴露的接口。
- 旧目录保持参考，迁移成功后再降级处理。

## 4. 实施分期

### Phase 1：新子项目骨架

任务：

1. 创建 `services/chat_server/` 目录结构并迁移当前 `cmd/chat-server/main.go`、`config/`、`internal/app/`、`manifest.json`。
2. 调整 `runtime_root.go` 以识别 `services/chat_server`。
3. 确保包路径、资源路径和运行时根目录全部切换到 underscore 新目录。

验收：

- `go build ./services/chat_server/...` 通过。
- 新目录能独立启动并读取配置。

### Phase 2：工具集与生命周期对齐

任务：

1. 保留全部 `app.chat.*` 工具。
2. 保留 `app.chat.stream` 的 WS 入口。
3. 保留 `service.lifecycle.health` / `service.lifecycle.shutdown`。
4. 检查 `AllowedCallerTypes`、`Streaming`、`WSPath`、`CapabilitiesRequired` 等 manifest 字段是否完整输出。

验收：

- Hub 侧注册视图与 route 视图可正确展示 chat 工具。
- 通过 tool 平面能完成所有业务和生命周期调用。

### Phase 3：Hub 与配置切换

任务：

1. 更新 `config/services.json` 与 `hub/config/services.json`，把 `chat-server` 的目录切换到 `services/chat_server`。
2. 同步 `hub/config/config.json` 中的服务引用。
3. 检查 `config/configx.json.example` 中的启动命令是否仍指向旧目录。
4. 验证 `ai_service.mode=service` 场景下，chat-server 仍能正确通过 Hub 访问 AI / storage 能力。

验收：

- Hub 拉起的新目录服务可用。
- 前端页面无须感知目录迁移。

### Phase 4：旧目录收口

任务：

1. 逐步冻结旧 `services/chat-server`，避免继续扩散新变更。
2. 清理任何仍依赖旧目录名的路径拼接。
3. 对不再需要的兼容入口进行删除或降级说明。

验收：

- 新调用方只依赖 `services/chat_server`。
- 生命周期与业务工具均只通过 tool 平面对外。

## 5. 验证方案

1. `go build ./services/chat_server/...`
2. 启动后验证 `app.chat.project_*` 和 `app.chat.thread_*`
3. 验证 `app.chat.stream` 的 WS 连接
4. 验证 `service.lifecycle.health` 与 `service.lifecycle.shutdown`
5. 验证 chat-server 调用 Hub 时仍能访问 AI / storage 工具

## 6. 风险与约束

- WS 会话和多轮状态最容易在迁移中出问题，必须优先验证。
- 若前端静态资源或运行配置仍引用旧目录，会导致启动成功但行为不完整。
- 任何新增的直连接口都与当前架构目标冲突。

## 7. 信息来源

- `services/chat-server/cmd/chat-server/main.go`
- `services/chat-server/internal/app/service_manifest.go`
- `services/chat-server/internal/app/runtime_root.go`
- `services/chat-server/internal/app/config.go`
- `webui/page/chat/*`
- `config/services.json`
- `hub/config/services.json`
- `hub/config/config.json`

