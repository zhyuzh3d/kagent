# services/file_storage 重建开发计划

> 文档类型：开发计划（devplan）  
> 时间：2026-03-19 03:51 CST  
> 范围：`services/file_storage/`、`hub/internal/{gateway,supervisor,routing}/`、`pkg/toolproto/`、`config/`、`hub/config/`  
> 目标约束：新子项目必须作为当前 `file` 的替代实现，且只保留 storage / blob / lifecycle 相关工具。

## 0. 结论

`file` 当前已经非常接近 tool-only storage service，但目录下仍混有旧命名、兼容入口和历史残留。  
新子项目 `services/file_storage` 的目标是把当前验证过的文件与 blob 能力重新整理成一个干净、可替换、可由 Hub 直接调度的 storage 服务。

新子项目必须保留的工具集合为：

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
- `service.lifecycle.health`
- `service.lifecycle.shutdown`

## 1. 真实核验事实

以下事实已由当前仓库代码核验：

- `services/file/cmd/file/main.go` 已通过 `hub.governance.service.register` 与 `hub.governance.service.heartbeat` 完成服务注册与心跳。
- `services/file/cmd/file/main.go` 已在 `POST /service/tool/exec` 中分发所有 `storage.file.*` 与 `storage.blob.*` 工具。
- `services/file/internal/app/hub_builtins.go` 已把 file 工具声明成标准 service manifest。
- `services/file/cmd/file/main.go` 当前仍保留 `/healthz`、`/service/info`、`/service/tools` 和 `/admin/shutdown`，这些属于迁移期兼容面。
- `services/file/internal/app/runtime_root.go` 仍以 `services/file` 为根目录候选。
- Hub 侧 `hub/internal/supervisor/process_control.go` 已优先通过 `service.lifecycle.*` 控制服务。

## 2. 问题定义

### 2.1 对外语义已清晰，但目录边界仍旧

当前 file 已经是标准 storage service 的样子，但目录名和旧兼容入口仍让它看起来像“过渡实现”。

### 2.2 兼容入口过多

`/healthz`、`/service/info`、`/service/tools`、`/admin/shutdown` 的存在本身不是问题，但它们不能继续作为新实现的正式契约。

### 2.3 旧实现残留较多

旧 `services/file/internal/app` 中存在平台复制件和不相关模块。新子项目不能继承这些噪音。

## 3. 重建目标

### 3.1 对外目标

`services/file_storage` 只提供真实的 storage / blob 能力，并保持 Hub 可调度的 lifecycle tool：

- `storage.file.*`
- `storage.blob.*`
- `service.lifecycle.*`

### 3.2 对内目标

1. `cmd/` 只做 bootstrap、tool dispatcher、注册、健康与停机。
2. `internal/app/` 只保留文件路径约束、scope 解析、blob 存储、manifest helper 和必要的工具实现。
3. 不再保留平台复制件、账号逻辑、chat 逻辑或 AI provider 逻辑。

### 3.3 迁移约束

- 对外工具名不改。
- 先保证可替换，再逐步删除旧目录。
- 兼容入口只允许作为过渡，不允许成为新设计的默认终点。

## 4. 实施分期

### Phase 1：新子项目骨架

任务：

1. 创建 `services/file_storage/` 目录结构，迁移现有 file 服务入口与必要内部实现。
2. 调整 `runtime_root.go`，让其识别 `services/file_storage`。
3. 保留 `manifest.json`、运行配置和数据根目录约束。

验收：

- 新目录可以独立构建。
- `go build ./services/file_storage/...` 通过。

### Phase 2：工具集与 manifest 收敛

任务：

1. 保留所有 `storage.file.*` 和 `storage.blob.*` 工具。
2. 保留 `service.lifecycle.health` 与 `service.lifecycle.shutdown`。
3. 统一输出 `AllowedCallerTypes`，由 Hub 在网关层执行 caller 限制。
4. 清理 manifest 中的 `ScopeSupport` 过渡语义，必要时仅作兼容输入，不作为主语义。

验收：

- Hub 侧 route/schema 能显示 new service 的真实 tools。
- 权限约束不再只停留在本地 handler。

### Phase 3：Hub 与配置切换

任务：

1. 更新 `config/services.json` 与 `hub/config/services.json`，将 `file` 的目录切换到 `services/file_storage`。
2. 检查 `hub/config/config.json` 中的服务引用是否需要同步。
3. 验证 Hub 通过 `service.lifecycle.health` 和 `service.lifecycle.shutdown` 调度新子项目。

验收：

- Hub 能按新目录拉起 file 替代实现。
- 旧目录不再是运行事实来源。

### Phase 4：历史残留清理

任务：

1. 删除或迁出与当前职责无关的旧模块。
2. 移除正式依赖中的 `/admin/shutdown` 和 `/service/info` / `/service/tools` 路径语义。
3. 保留最小兼容面后，再逐步清空旧目录引用。

验收：

- `services/file_storage/internal/app` 只剩 storage / blob / lifecycle 相关代码。
- 新增调用方不会被旧路径带偏。

## 5. 验证方案

1. `go build ./services/file_storage/...`
2. 启动后验证 `storage.file.*` 与 `storage.blob.*`
3. 验证 `service.lifecycle.health` 与 `service.lifecycle.shutdown`
4. 验证 Hub 对 file 新目录的注册、心跳与停机

## 6. 风险与约束

- file service 是共享存储边界，路径 root 或权限检查变动会直接影响调用方。
- 若 `AllowedCallerTypes` 与 Hub 预期不一致，调用会被提前拦截。
- 删除旧兼容路径前必须确认 Hub 侧不再依赖它们。

## 7. 信息来源

- `services/file/cmd/file/main.go`
- `services/file/internal/app/hub_builtins.go`
- `services/file/internal/app/runtime_root.go`
- `hub/internal/supervisor/process_control.go`
- `config/services.json`
- `hub/config/services.json`
- `hub/config/config.json`

