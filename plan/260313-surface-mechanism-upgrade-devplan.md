# Surface 机制全面升级开发计划（DevPlan）

- 文档时间：2026-03-13 12:05 CST
- 对应需求：`plan/260313-surface-mechanism-prd.md`
- 计划类型：开发计划（`-devplan`）
- 执行方式：按本计划分阶段直接落地代码并完成验证

## 1. 目标与范围

### 1.1 总目标
将当前 chat 页的“单 demo、硬编码 counter surface”升级为“manifest 驱动、可扫描入库、可启停管理、可文件隔离、可 token 授权、可多 surface 动态调度”的统一机制，并保持现有 action/report 对话流可追溯。

### 1.2 本次必须完成
1. 启动扫描 `webui/surface/{buildin,ext,custom}` 并将结果入库。
2. 后端提供 `GET /api/surfaces`、`POST /api/surfaces/{surface_id}/enable`。
3. 退役 `surface_states` 作为系统依赖；surface 状态通过“surface 自存 + 对话流事件”承载。
4. 新增 surfacefs（读/写/列/删 + 静态签名 URL），强约束路径到 `data/users/<uid>/surface_data/<surface_id>/`。
5. chat 页主 surface 模块支持动态 `get_surfaces/open_surface/close_surface/surface.get_state/surface.call.*`。
6. counter surface 迁移为标准包（`manifest.json + entry`），并接入 surfacefs 状态持久化。
7. 提供最小 host 能力代理链路（至少 `flash` 可验证），并可通过 action_report/ops 追溯。

### 1.3 边界与假设
1. `ext/custom` 默认 `enabled=0`，`buildin` 默认 `enabled=1`（冲突/无效除外）。
2. 本期不做在线安装/卸载，仅做启动扫描。
3. 当前单机用户模式保持不变（`--user-id` 所对应用户视角），不引入完整账号系统。
4. 对于 “清理并重建 surface 数据目录” 弹窗流程，本期先提供后端能力和前端基础入口，不扩展历史消息级清理。

## 2. 现状差距（相对 PRD）
1. 前端 `surface-bridge.js` 仅支持固定 counter，`get_surfaces/open_surface/close_surface` 都是硬编码。
2. 后端无 surface manifest 扫描与可管理清单 API。
3. 数据库仍存在并依赖 `surface_states`。
4. surfacefs 与 token 能力边界尚未落地。
5. counter 仍是 demo html 内嵌 manifest，不是标准 manifest 包结构。

## 3. 分阶段实施计划

## Phase A：后端基础设施（扫描、入库、API、surfacefs）
### A1. 数据模型与迁移
1. SQLite 新增 `surfaces`、`user_surfaces` 表。
2. 停止 session 逻辑写入 `surface_states`；旧表不再被业务读取。
3. 保持消息流与 action_report 持久化逻辑不回退。

### A2. manifest 扫描与校验
1. 扫描三类目录下一级包目录的 `manifest.json`。
2. 校验规则：UUID、entry 存在且防越界、version/min_supported_version 可比较。
3. 处理 `surface_id` 冲突：同批冲突全部标记 conflict 且不可用。
4. 扫描结果 upsert 到 `surfaces`，并同步 `user_surfaces` 默认启用状态。

### A3. surfaces API
1. `GET /api/surfaces`：返回当前用户视角清单（含 enabled/status/error）。
2. `POST /api/surfaces/{surface_id}/enable`：更新用户启用状态。
3. 仅 `enabled=true && status=ok` 进入可用集（用于 get_surfaces 结果）。

### A4. surfacefs
1. 引入 session token 与 capability token 基础模型。
2. 落地 `read/write/list/delete` 四类能力接口。
3. 落地静态资源签名访问 `GET /surfacefs/static/<surface_id>/<path>?st=...`。
4. 路径安全：禁止目录穿越与跨 surface 访问。

## Phase B：会话链路与提示词去硬编码
### B1. Session 改造
1. 移除 `handleStateChange/handleActionResult` 内对 `surface_states` 的 upsert。
2. 保留对话流事件入库与 ops 记录。

### B2. action 元数据通用化
1. 通用 surface_id 推断逻辑，不再硬编码 `counter`。
2. 保持 followup/report 流程兼容。

### B3. LLM 提示词
1. 将固定 counter action 文案升级为通用 surface action 规范。
2. 保持强约束：JSON envelope、followup 规则、先 `get_surfaces` 再 `open_surface`。

## Phase C：前端 mainsurface 重构与 chat 接入
### C1. surface bridge 重构
1. 维护 surface registry（来自 `/api/surfaces`）。
2. 支持多 surface iframe 生命周期与 action registry。
3. `dispatchAction` 支持动态路由与可用性校验。

### C2. 管理 UI
1. 在 chat 页 surface 面板展示清单与状态。
2. 提供 enabled 开关，错误/冲突高亮。

### C3. Host 能力代理
1. 面向 surface 提供 `flash/chat/tts/asr/isr` 能力入口。
2. 至少完成 `flash` 闭环（surface 调用 -> page 代理 -> 后端 action_report 可追溯）。

## Phase D：counter 标准化迁移
### D1. 包结构迁移
1. 新建 `manifest.json`（UUID id、version、entry）。
2. `entry` 页面改造为标准运行时注册 action。

### D2. 状态持久化
1. counter 通过 surfacefs 读写 `state.json` 实现恢复。
2. 状态变化继续发 `state_change` 写入对话流。

### D3. 兼容退役
1. chat 页不再依赖旧 demo 硬编码路径。
2. 旧 demo 文件保留仅作历史兼容，不作为默认链路。

## 4. 验证计划
1. 单元测试：
   - manifest 校验与冲突处理；
   - surfaces/user_surfaces 入库与查询；
   - surfacefs 路径安全与 token 校验。
2. 集成验证：
   - 启动后 `GET /api/surfaces` 返回正确；
   - enable 开关持久化；
   - chat 中 `get_surfaces/open_surface/close_surface` 正常；
   - counter 状态可重启恢复；
   - flash host 调用可在后端消息/ops 中追溯。
3. 构建验证：
   - `go test ./...`
   - `go build -buildvcs=false ./...`

## 5. 风险与应对
1. 迁移期行为不一致风险：先保留旧文件但切断默认入口，保证新链路唯一。
2. token 设计过重风险：采用最小可用 capability 模型，先保证边界正确再扩展权限粒度。
3. action 动态化导致兼容问题：保留常用 alias，并在 bridge 层提供统一 canonical 解析。
4. 前端改动面大：优先保持 `action-engine -> reportActionRecord -> ws` 协议不变，降低联动风险。

## 6. 完成定义（DoD）
1. PRD 第 16 节 1-7 项全部在代码层可验证。
2. 关键接口均可被实际调用并返回可预期结果。
3. 相关测试与构建通过，无编译回归。
4. chat 页不再依赖硬编码 counter demo 逻辑。
