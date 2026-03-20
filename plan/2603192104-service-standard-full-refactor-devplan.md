# Service Standard Full Refactor Dev Plan

## 1. 计划结论

本轮重构按“先协议基座，后治理入口，再改各 service，最后收口 WebUI”的顺序推进。核心目标不是继续堆兼容层，而是把 `pkg/toolproto` 与 `pkg/hubsvc` 收敛成唯一协议来源，让 `hub` 成为唯一治理入口，让 `file_storage` / `sql_db` 成为唯一文件与 sqlite 基础能力来源，并把 `account` / `ai_doubao` / `chat_server` / `surface-manager` 与 `webui/page/*` 全部改造成通过 Hub 工具平面协作的标准形态。

## 2. 当前真实差距

### 2.1 标准缺口

1. 全仓未发现 `service.lifecycle.state.get` 与 `service.lifecycle.drain` 的实现检索结果，当前最低生命周期契约不完整。
2. `pkg/toolproto` 当前只覆盖基础请求/响应和 supervisor 注册结构，缺少标准文档要求的完整 tool 元数据表达：`description`、`input_schema`、`output_schema`、`protocol`、`hub_only`、`hub_auth_required`、`has_effects`、`risk_lv`、`timeout_ms_default`、`scope_support` 等字段未统一收敛。
3. `pkg/hubsvc` 仅提供 bootstrap secret 和 header 帮助函数，尚未收敛成标准的 Hub-Service 可信接入与 service 侧注册/鉴权辅助层。
4. `hub/internal/gateway/hub_manifest.go` 目前只注册简化版 `toolproto.ServiceTool`，tool 元数据治理信息不完整。
5. 多个 service 仍保留各自私有协议定义，违背“基础设施统一、去除冗余”的目标。

### 2.2 冗余与越界证据

1. `services/chat_server/internal/app/tool_protocol.go`
2. `services/ai_doubao/internal/app/tool_protocol.go`
3. `services/chat_server/internal/app/service_manifest.go`
4. `services/ai_doubao/internal/app/service_manifest.go`

以上文件重复定义了 `CallRequest` / `ServiceTool` / `SupervisorRegisterRequest` / manifest 描述结构，应由 `pkg/toolproto` 提供。

### 2.3 直接违反标准边界的实现

1. `services/surface-manager/internal/app/sqlite_store.go` 直接使用 `kagent/pkg/sqlitedriver` 和 sqlite 连接，不符合“数据库操作通过 Hub 调用 `sql_db`”的要求。
2. `services/surface-manager/internal/app/surfacefs.go` 直接执行文件写入，不符合“文件/blob 统一通过 Hub 调用 `file_storage`”的要求。
3. `services/chat_server/internal/app/hub_database_store.go` 与 `services/chat_server/internal/app/hub_tool_client.go` 已部分通过 Hub 调用其他能力，但仍需核对是否彻底移除自身对 provider/存储的重复实现。

## 3. 实施顺序

### Phase 1：协议与可信接入基座

1. 重构 `pkg/toolproto`
2. 重构 `pkg/hubsvc`
3. 为 `hub`、各 service 补齐统一生命周期与 register/heartbeat/state 协议

### Phase 2：Hub 治理平面

1. Hub manifest/tool registry 全量切换到统一协议元数据
2. Hub 路由、caller/capability 门槛、effects 回写、治理统计与运行快照逻辑对齐新协议
3. Hub supervisor 对 `service.lifecycle.health` / `state.get` / `shutdown` / `drain` 做标准化编排

### Phase 3：基础能力 service

1. 重构 `services/file_storage`
2. 重构 `services/sql_db`
3. 提供完整、标准、可复用的文件/blob 与 sqlite 工具集

### Phase 4：应用/编排 service

1. 重构 `services/account`
2. 重构 `services/ai_doubao`
3. 重构 `services/chat_server`
4. 重构 `services/surface-manager`

### Phase 5：WebUI 收口

1. `webui/page/account`
2. `webui/page/chat`
3. `webui/page/surface`

全部统一指向 `/api/tool/call` 与 `/api/tool/ws`，并使用来自标准 service 的 tool_id。

## 4. 模块级计划

### 4.1 `pkg/toolproto`

目标：

1. 成为唯一 tool 协议与 tool 元数据定义来源。
2. 提供统一请求/响应、WS frame、tool manifest、service manifest、register/heartbeat、lifecycle state/result、effects、风险等级和 schema helpers。
3. 提供标准化校验与规范化逻辑，消除各 service 自有 `tool_protocol.go`。

实施：

1. 扩充 `ServiceTool` 为标准文档字段全集。
2. 新增 service manifest/tool descriptor 结构。
3. 新增 lifecycle state/result、state snapshot、heartbeat payload 的公共结构。
4. 新增 helper：tool id 校验、caller type 归一化、schema clone、allowed caller/all 语义处理、effect/meta 填充。
5. 回收 `chat_server` / `ai_doubao` 等私有协议文件，改为直接依赖 `pkg/toolproto`。

验收：

1. 全仓不再保留重复的 `CallRequest` / `ServiceTool` / `SupervisorRegisterRequest` 私有定义。
2. 各 service manifest/tool 注册均可直接复用 `pkg/toolproto`。

### 4.2 `pkg/hubsvc`

目标：

1. 成为 Hub-Service 可信接入的唯一基础设施来源。
2. 提供 bootstrap secret、header 注入/校验、Hub tool call/register helper、service 侧 request guard 与 lifecycle helper。

实施：

1. 规范 bootstrap secret 读写与错误模型。
2. 新增统一的 service register helper，避免各服务重复拼装 `hub.governance.service.register` 调用。
3. 新增 service 侧 Hub 认证校验与 caller 注入提取 helper。
4. 新增 service state/lifecycle 响应 helper，减少主入口重复样板。

验收：

1. `services/*/cmd/*/main.go` 不再各自复制 register/auth/context 提取逻辑。

### 4.3 `hub`

目标：

1. 完整承载 service 标准中的治理职责。
2. 统一维护 session、routing overlay、tool 统计与退出快照。

实施：

1. Hub manifest 全量补齐 tool 元数据。
2. supervisor register/heartbeat/session 合并逻辑明确区分：service 自报事实、Hub 观察结果、Hub 治理结果。
3. 路由层完整消费 `allowed_caller_types`、`capabilities_required`、`hub_only`、`hub_auth_required`、`streaming`、`ws_path`。
4. 统一生命周期调用顺序：`health`、`state.get`、`drain`、`shutdown`，并保留兼容 fallback 但不作为正式主契约。
5. 退出前把 Hub 与全部 service/tool 运行态快照写入数据库。

验收：

1. `hub` 对所有标准 service 的 register/heartbeat/lifecycle 调度一致。
2. 管理与运行态视图中的 tool/service 元数据完整。

### 4.4 `services/file_storage`

目标：

1. 成为唯一文件/blob 基础能力 service。
2. 对外只暴露标准工具，不让其他模块直接碰文件/blob。

实施：

1. 统一 manifest/tool descriptor，补齐 `state.get`，必要时补 `drain`。
2. 梳理 `storage.file.*`、`storage.blob.*` 工具边界、caller/scope 语义与风险等级。
3. 提供供其他 service 通过 Hub 调用的稳定输入输出结构。

### 4.5 `services/sql_db`

目标：

1. 成为唯一 sqlite 基础能力 service。
2. 负责 caller scope、隔离与基础 SQL/记录能力。

实施：

1. 统一 manifest/tool descriptor，补齐 `state.get`。
2. 整理数据库工具集合，明确用户/Surface/Service 作用域。
3. 为其他 service 提供标准工具化数据访问入口，禁止其他模块再直连 sqlite。

### 4.6 `services/account`

目标：

1. 仅负责账号应用层工具。
2. 业务鉴权基于 Hub 注入的受信身份执行。

实施：

1. 与 `webui/page/account` 对齐所需工具面。
2. 明确 cookie/effects 与 session/token 工具边界。
3. 补齐标准生命周期与 manifest/tool 元数据。

### 4.7 `services/ai_doubao`

目标：

1. 只保留基础 AI provider 工具。
2. 移除与 chat 应用层耦合的冗余逻辑。

实施：

1. 删除私有 tool protocol/manifest 重复定义，切换至共享协议。
2. 明确 `ai.llm.*`、`ai.speech.*` 的流式/非流式契约、风险等级和副作用声明。
3. 补齐 `state.get`，必要时提供 `drain` 以支持流式连接优雅退出。

### 4.8 `services/chat_server`

目标：

1. 成为 AI 聊天应用层工具集。
2. AI/存储能力全部通过 Hub 调用其他 service 获得。

实施：

1. 清理私有协议与 provider 重复层。
2. 统一 chat 项目/线程/消息/流式会话工具。
3. 所有 AI 调用通过 Hub 调用 `ai_doubao`，所有存储调用通过 Hub 调用 `sql_db`/`file_storage`。

### 4.9 `services/surface-manager`

目标：

1. 成为 surface catalog/session/capability 工具集。
2. 数据库、文件、AI 相关能力全部通过 Hub 转调其他标准 service。

实施：

1. 删除直连 sqlite 的 `sqlite_store` 路径。
2. 删除直接文件写入与 blob 管理逻辑，改用 `file_storage` 工具。
3. 重新整理 `ui.surface.*` 工具集，与 `webui/page/surface` 对齐。

## 5. 验证策略

1. 单元测试优先覆盖 `pkg/toolproto`、`pkg/hubsvc`、Hub 路由/生命周期、关键 service 辅助层。
2. 集成验证至少覆盖注册、caller 路由、生命周期、chat/account/surface 页面核心工具调用。
3. 使用 `go test ./...` 和必要的分模块测试。
4. 若部署链路可用，再执行 `scripts/deploy.sh` + tool smoke。

## 6. 风险与处理

1. 当前多个 service 的主入口内联了大量业务与协议样板，重构时要先抽共性 helper，再迁业务，避免一次性全量重写造成回归。
2. `surface-manager` 改造成纯 Hub 工具调用后，其本地 sqlite/file 逻辑会被大幅替换，属于高影响区域，需要分步骤迁移。
3. `webui/page/chat`、`surface` 依赖流式工具；WS tool id、路径和最终事件语义必须与 service 同步更新。

## 7. 完成标准

1. `pkg/toolproto` 和 `pkg/hubsvc` 成为唯一协议/可信接入基础设施来源。
2. `hub` 与全部目标 service 满足 `_service_standard.md` 的最低生命周期、register/heartbeat、tool 元数据和治理边界要求。
3. `surface-manager`、`chat_server` 等不再直连 sqlite/file/provider 私有实现，而是通过 Hub 工具平面调用标准 service。
4. `webui/page/account`、`chat`、`surface` 全部请求都指向 Hub 的正确工具集。
5. 通过与改动直接相关的自动化验证；无法验证的部分需在交付中明确说明。

## 8. 当前进展

### 8.1 已完成

1. `pkg/toolproto` 已补入 `protocol`、`timeout_ms_default`、`scope_support`、`hub_only`、`hub_auth_required`、`has_effects`、`risk_lv`、`lifecycle state` 等共享结构与 helper。
2. `pkg/hubsvc` 已补入统一的 Hub tool call URL/helper、caller 提取与 lifecycle meta 辅助逻辑。
3. `chat_server` 与 `ai_doubao` 已删除完整私有协议定义，改为共享协议别名与共享 manifest 结构。
4. `account`、`ai_doubao`、`chat_server`、`file_storage`、`sql_db`、`surface-manager` 已补齐 `service.lifecycle.state.get`。
5. `hub` 已补入 `hub.system.state.get`，并补强 Hub 内置工具元数据。
6. `surface-manager` 的 `ui.surface.fs_*` 主链路已改为“先校验 capability，再通过 Hub 调 `storage.file.*`”，相关本地文件 IO 代码已从 `surfacefs.go` 主链路中移除。
7. WebUI 已复核 `account`、`chat`、`surface` 页面工具请求，当前仍统一指向 `/api/tool/call` 与 `/api/tool/ws`，未发现直接打 service 私有地址的页面主链路。

### 8.2 未完成

1. `surface-manager` 仍保留 `sqlite_store.go` 与 `surface_catalog.go` 的本地 sqlite catalog/logs 路径，尚未彻底改成通过 Hub 调 `sql_db`。
2. `surface-manager` 因上一条未完成，相关冗余文件还不能安全删除。
3. `hub` 的退出前全量治理快照写库仍未按标准完全补齐到“Hub + 全部 service/tool 统计”的最终形态。
4. `service.lifecycle.drain` 仍未在目标 service 中系统化落地。

### 8.3 当前验证结果

1. 已通过编译级验证：
   `GOCACHE=$PWD/.cache/go-build go test -run '^$' ./pkg/... ./hub/internal/... ./hub/cmd/hub ./services/account/internal/... ./services/account/cmd/account ./services/ai_doubao/internal/app ./services/ai_doubao/cmd/ai-doubao ./services/chat_server/internal/app ./services/chat_server/cmd/chat-server ./services/file_storage/internal/app ./services/file_storage/cmd/file ./services/sql_db/internal/app ./services/sql_db/cmd/database ./services/surface-manager/internal/app ./services/surface-manager/cmd/surface-manager`
2. 常规 `go test` 在当前环境下仍会遇到 `hub/internal/transport` 的本地监听限制，因此本轮主要完成了编译级而非完整运行级回归。

---

**计划时间**：2026-03-19 21:04:08 CST

**计划范围**：`pkg/hubsvc`、`pkg/toolproto`、`hub/`、`services/file_storage`、`services/sql_db`、`services/account`、`services/ai_doubao`、`services/chat_server`、`services/surface-manager`、`webui/page/account`、`webui/page/chat`、`webui/page/surface`

**依据**：

1. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
2. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`
3. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`
4. `rg -n "service.lifecycle.state.get|service.lifecycle.drain" hub services` 结果为空
5. `rg -n "type CallRequest struct|type ServiceTool struct|type SupervisorRegisterRequest struct|type BootstrapSecret struct" services hub pkg`
6. `rg -n "sqlite|os\\.WriteFile|/api/tool/ws|/api/tool/call" services/chat_server services/surface-manager services/account webui/page`
