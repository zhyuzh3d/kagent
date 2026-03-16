# Hub + Service Platform 全量重构开发计划

- 日期：2026-03-15
- 文档类型：开发计划（Dev Plan）
- 状态：拟执行
- 范围：后端大重构为 `Hub + 多独立 service` 架构，并同步规划前端 `page/surface` 分层配套改造
- 依据：
  - `plan/260314-hub-services-refactor-ana.md`
  - `plan/260314-hub-services-minimal-devplan.md`
  - `doc/_instruction.md`
  - 当前代码实现：`main.go`、`internal/session.go`、`internal/sqlite_store.go`、`internal/auth.go`、`internal/surface_catalog.go`、`internal/surfacefs.go`、`internal/blob_service.go`、`cmd/ai-doubao/main.go`、`webui/page/chat/*`、`webui/page/surface/*`
  - 2026-03-15 本轮对话中用户最终确认的目标、边界与修正意见

---

## 1. 计划结论

本次重构的目标不是“在现有单体上继续补一点 service 化”，而是将当前后端正式重构为：

- `Hub`：本地统一入口、路由代理、监管者、安全边界与控制面中心
- `chat server`：聊天应用后端实现服务，承接当前绝大多数会话业务逻辑
- 多个独立 service：按单一职责提供工具集合，由 Hub 聚合并固化路由

最终目标链路定义为：

`chat app -> Hub -> chat server -> Hub -> 其它 service`

其中：

1. `Hub` 应尽可能薄，只负责统一入口、service 注册与监管、工具清单汇总、路由表固化、鉴权、能力校验、审计、限流、静态 web 服务。
2. `chat server` 不再是“编排器”概念，而是完整聊天应用后端实现，承接项目、线程、消息、流式对话、surface 事件接入等业务逻辑。
3. 所有工具都隶属于某个 service；工具唯一标识统一采用三层逻辑路径，如 `storage.file.read`、`app.chat.project_create`。
4. `category/type/tool` 是工具自身三层分类；完整展示路径可带上 service 所属层，如 `service/storage/file/read`。
5. `page` 和 `surface` 是前端两级结构：
   - `page` 是宿主页面层，直接连接 Hub，拥有用户身份
   - `surface` 是独立业务单元，可被任意 `page` 加载，但不允许脱离 `page` 直接取得系统能力，也不允许自行加载其它 `surface`

本计划以“整体平台化重构”为目标，分阶段实施，确保可迁移、可回退、可观测。

---

## 2. 用户已确认的关键原则

以下原则视为本计划固定约束，后续开发不得偏离：

### 2.1 Hub 角色

- Hub 是本地统一网关与监管层
- Hub 近乎透传，不承载具体业务实现
- Hub 保留：
  - service supervisor
  - tools registry
  - 固化路由表
  - 用户鉴权与 capability 校验
  - 审计与观测
  - 静态 web 文件服务
  - service 管理 API

### 2.2 工具体系

- 所有工具都隶属于某个 service
- `tool_id` 使用三层逻辑路径，例如：
  - `storage.file.read`
  - `storage.database.query`
  - `app.chat.project_create`
- 完整展示路径可写为：
  - `service/storage/file/read`
  - `service/app/chat/project_create`
- `category` 不是 service 名称，多个 service 可以提供相同 `category/type/tool` 的工具
- Hub 根据路由评分选择 provider，并固化为路由表

### 2.3 路由表与评分

- Hub 不在每次请求时动态重算路由
- 路由表只在以下时机重算：
  - Hub 启动
  - service/工具列表变化
  - 工具不可用
  - 新工具加入
  - 用户手动刷新
- 候选选择不只看 `reliability`，还综合：
  - 成功率
  - 延迟
  - 历史表现
  - 人工 override

### 2.4 service 身份

- `service_id` 采用语义化稳定标识，用于真实逻辑与持久化绑定
- `service_name` 只用于显示
- `instance_id` 表示运行时唯一实例
- 平台目标是避免同一 `service_id` 同时存在多个活动实例

### 2.5 存储与授权

- `file` 与 `database` 都遵循 scope 隔离思路
- 默认支持 `data/user` 级用户基本存储
- service 内部请求允许使用 `data/service/<service_id>/...`
- surface 默认只能访问自己的 scope
- 只做明确隔离，不追求强安全沙箱

### 2.6 配置体系

- 每个 service 自带独立配置目录：
  - `config/config.json`
  - `config/configx.json`
  - `config/configx.json.example`
- `configx.json` 一律不得提交到 Git
- 敏感配置分散到各自 service 中，不再集中在单体配置里

### 2.7 surface / page 前端关系

- `surface` 是独立单元，不属于某个具体 page
- 任意 `/page/*` 页面可加载 `surface`
- 非 `page` 层页面不得加载 `surface`
- `surface` 自身不能加载其它 `surface`
- `webui/page/surface/admin.html` 作为统一 surface 管理台

### 2.8 surface 授权

- 采用持久化签名根密钥
- 每次 surface 加载重新签发 `surface_session_token`
- capability token 短时有效，并绑定 surface session
- 必须支持多 tab、多 page、多 surface 并发加载

---

## 3. 当前项目现状与重构出发点

### 3.1 当前单体里已经存在的“准 Hub”能力

当前 `main.go` 已经承担大量 Hub 职责：

- 统一 HTTP API 与 `/ws`
- JWT 登录与用户鉴权
- 项目/线程 CRUD
- 配置读写
- surface 列表与启停
- surfacefs
- blob
- admin service API
- 静态资源服务

结论：

- 当前主程序不是“完全没有 Hub”，而是“Hub 与业务逻辑高度混杂”
- 本次重构不是从零设计，而是要把现有主程序拆出清晰边界

### 3.2 当前最重的业务耦合点

当前 `Session` 仍承接大量业务：

- ASR 生命周期
- `trigger_llm`
- `interrupt`
- turn 编排
- 历史同步
- action report 汇总
- surface state change 入流
- continuation 编排

结论：

- 这些逻辑不应继续保留在 Hub
- 它们应迁移到 `chat server`

### 3.3 当前已存在的 service 化雏形

当前仓库已经有：

- `cmd/ai-doubao/main.go`
- `internal/ai_service_manager.go`
- `internal/service_provider_factory.go`

说明：

- `ai-doubao` 已经被局部抽成独立 service
- 但当前仍是“单例特判”，不是通用 service 平台

本次重构要把这部分提升为通用 service 注册、身份、路由、管理体系。

---

## 4. 目标总体架构

## 4.1 分层结构

### A. 前端 page 层

负责：

- 页面级用户身份
- 与 Hub 建立 HTTP/WS 连接
- 承载 `chat app`
- 承载 `surface admin`
- 管理和加载 `surface`

典型页面：

- `webui/page/chat/*`
- `webui/page/surface/index.html`
- `webui/page/surface/admin.html`

### B. 前端 surface 层

负责：

- 独立业务界面与局部业务状态
- 注册自身 action
- 接收 page 转授的 session 与 capability
- 通过 page / Hub 间接使用存储能力

限制：

- 不能直接取得用户级全局权限
- 不能直接加载其它 surface
- 不能脱离 `/page/*` 宿主直接运行系统能力链路

### C. Hub 层

负责：

- 统一 HTTP/WS 入口
- service 注册、身份管理、健康检查、生命周期控制
- tools registry 汇总
- 路由表固化
- 用户 auth、service auth、surface auth
- capability / quota / audit
- 静态 web 文件服务
- service 管理 API

不负责：

- 聊天业务规则
- project/thread/message 的业务解释
- provider 具体实现
- surface 的具体业务逻辑

### D. 业务 service 层

首批目标 service：

- `chat server`
- `ai-doubao`
- `file`（内含 blob）
- `database`
- `surface-manager`
- `auth`

后续可扩展：

- `storage-ext`
- 其它 AI provider
- 官方 sys service

---

## 5. service 身份、注册与实例管理

## 5.1 基本概念

需要区分三个概念：

- `service_id`：稳定语义化身份，用于逻辑绑定、配置目录、持久化统计、路由与审计
- `service_name`：纯显示名称，可重名，不参与逻辑
- `instance_id`：运行时实例唯一标识，用于本次进程存活周期

### 5.1.1 `service_id` 设计要求

- 全局唯一
- 稳定
- 不依赖显示名称
- 一旦确定，不随每次启动变化

建议形式：

- 由 manifest 固定声明
- 使用规范化语义名，例如：
  - `chat-server`
  - `ai-doubao`
  - `file`
  - `database`
  - `surface-manager`
  - `auth`

### 5.1.2 `instance_id` 设计要求

- 每次启动生成随机唯一值
- Hub 以此识别“当前活跃实例”

## 5.2 单实例约束

平台目标是：

- 同一 `service_id` 同一时刻只允许一个活跃实例参与路由

建议策略：

1. service 启动后必须向 Hub 注册
2. Hub 记录：
   - `service_id`
   - `instance_id`
   - `pid`
   - `started_at`
   - `socket/endpoint`
   - `manifest_hash`
   - `build_hash`
3. 若同一 `service_id` 又注册了新实例：
   - 默认拒绝新实例进入可用态
   - 标记为冲突
   - 在 service 管理台显示冲突告警
   - 由用户手工处理

这样可以避免“两个相同 service 同时工作”导致的路由混乱。

## 5.3 注册换取身份 token

所有 service 必须遵循：

1. 启动后先注册
2. 注册成功后获得 `service_session_token`
3. 后续调用 Hub 任意内部工具时都必须带 token
4. 没有 token 的请求一律拒绝

建议注册流程：

1. service 读取本地 manifest 与 config
2. service 向 Hub 发起 `register`
3. Hub 校验：
   - `service_id`
   - `instance_id`
   - manifest
   - 当前冲突状态
   - 可见性/启用状态
4. Hub 返回：
   - `service_session_token`
   - token 过期时间
   - 当前路由/状态摘要

### 5.3.1 性能要求

热路径不应做重型鉴权。建议：

- 注册阶段做完整校验
- 后续请求只做内存 token 查表
- 若使用长连接，可将 token 绑定到连接上下文

这样性能成本极低，可满足本地高频调用。

---

## 6. manifest 与工具声明规范

## 6.1 manifest 目标

每个 service 必须通过 manifest 向 Hub 声明：

- 基本信息
- 配置结构
- 提供的工具
- 依赖的工具类型
- 可见性
- reliability
- 默认运行要求

## 6.2 manifest 建议字段

```json
{
  "service_id": "chat-server",
  "service_name": "Chat Server",
  "version": "1.0.0",
  "build_hash": "xxxx",
  "reliability": "trusted",
  "visibility": "public",
  "entry": "bin/service/chat-server",
  "config_schema_version": 1,
  "provides": [],
  "requires": []
}
```

### 6.2.1 `reliability` 枚举

建议采用用户确认过的多级：

- `trusted`
- `verified`
- `unverified`
- `risky`
- `high_risk`

说明：

- `reliability` 影响路由评分，但不是绝对值
- Hub 仍需结合真实运行数据打分

## 6.3 工具声明字段

每个工具至少声明：

- `tool_id`
- `category`
- `type`
- `tool`
- `description`
- `input_schema`
- `output_schema`
- `side_effect`
- `capabilities_required`
- `timeout_ms_default`
- `streaming`
- `scope_support`

示例：

```json
{
  "tool_id": "app.chat.project_create",
  "category": "app",
  "type": "chat",
  "tool": "project_create",
  "description": "create chat project",
  "input_schema": {},
  "output_schema": {},
  "side_effect": "write",
  "capabilities_required": ["user.auth"],
  "timeout_ms_default": 5000,
  "streaming": "none",
  "scope_support": ["user"]
}
```

## 6.4 同工具多 provider

多个 service 可提供相同 `tool_id`。Hub 只记录工具候选集，不直接实时切换。

候选集持久化时应至少记录：

- `tool_id`
- `service_id`
- `reliability`
- `success_rate`
- `p95_latency_ms`
- `last_error_rate`
- `manual_weight`
- `enabled`

---

## 7. 路由表与工具选择

## 7.1 固化路由表原则

Hub 对每个 `tool_id` 生成确定性 binding：

- `tool_id -> provider service_id`

只在以下事件触发重算：

- Hub 启动
- service 注册/退出
- 工具新增/删除
- 工具健康失效
- 用户手工刷新

## 7.2 排序因子

建议打分因素：

1. `manual override`
2. `enabled`
3. `reliability`
4. `success_rate`
5. `latency`
6. `error_rate`
7. `近期冷却状态`

## 7.3 结果持久化

建议至少维护两张表：

- `tool_provider_stats`
- `tool_binding_table`

并允许用户在 service 管理台查看：

- 候选列表
- 当前绑定目标
- 绑定原因
- 手工刷新入口

---

## 8. chat server 设计

## 8.1 角色定义

`chat server` 是聊天应用的后端实现服务，而不是单纯 orchestrator。

它承接：

- 项目管理
- 线程管理
- 消息历史
- 实时流式会话
- action report 入流
- surface state change 入流
- continuation / followup
- 聊天应用业务规则

## 8.2 category 与工具命名

`chat server` 提供的工具统一使用：

- `category = app`
- `type = chat`

典型工具：

- `app.chat.project_list`
- `app.chat.project_create`
- `app.chat.project_update`
- `app.chat.project_delete`
- `app.chat.thread_list`
- `app.chat.thread_create`
- `app.chat.thread_update`
- `app.chat.thread_delete`
- `app.chat.thread_move`
- `app.chat.history_fetch`
- `app.chat.stream_start`
- `app.chat.stream_stop`
- `app.chat.turn_start_listen`
- `app.chat.turn_commit`
- `app.chat.turn_interrupt`
- `app.chat.action_report`
- `app.chat.surface_state_change`
- `app.chat.config_change`

## 8.3 迁移来源

`chat server` 的首批逻辑迁移自当前：

- `internal/session.go`
- `internal/pipeline.go`
- `internal/sqlite_store.go` 中与 chat 直接相关的部分
- 部分 `main.go` 中的 chat/project/thread/history API

## 8.4 对外关系

对前端：

- 仍由 Hub 暴露统一 `/ws` 与 `/api/chat/*`

对内部：

- 调 `ai-doubao`
- 调 `surface-manager`
- 调 `file`
- 调 `database`
- 调 `auth`

---

## 9. AI service 设计：`ai-doubao`

## 9.1 角色

仅负责 provider 适配，不负责聊天业务。

## 9.2 工具

- `ai.speech.asr`
- `ai.llm.stream`
- `ai.speech.tts`

## 9.3 迁移原则

- 保持现有流式语义
- 保持现有前端行为兼容
- 保持 `chat server` 可回退到本地直连实现作为故障兜底

## 9.4 配置拆分

Doubao 的配置拆入其自身目录：

- `services/ai-doubao/config/config.json`
- `services/ai-doubao/config/configx.json`
- `services/ai-doubao/config/configx.json.example`

---

## 10. file service（含 blob）设计

## 10.1 角色

统一提供：

- 路径式文件能力
- 不可变 blob 能力

首阶段不单独拆 blob 为独立 service，而是作为 `file` service 内第二组工具。

## 10.2 工具集合

### 10.2.1 文件工具

- `storage.file.read`
- `storage.file.write`
- `storage.file.delete`
- `storage.file.exists`
- `storage.file.stat`
- `storage.file.list`
- `storage.file.mkdir`
- `storage.file.rename`
- `storage.file.copy`

### 10.2.2 blob 工具

- `storage.blob.put`
- `storage.blob.get`
- `storage.blob.sign_url`
- `storage.blob.gc`

## 10.3 scope 设计

统一支持三类 scope：

- `user`
- `surface`
- `service`

路径建议：

- `data/user/<user_id>/...`
- `data/user/<user_id>/surface/<surface_id>/...`
- `data/service/<service_id>/...`

## 10.4 访问规则

### 浏览器 page 请求

- 默认只允许 `user` scope

### surface 请求

- 默认只允许自己的 `surface` scope
- 由 page/Hub 转授 capability

### service 请求

- 默认只允许 `service` scope
- 若要访问 `user/surface` scope，必须由 Hub 注入代表身份

## 10.5 blob 与 file 的边界

- `file`：路径式、可变、目录语义
- `blob`：句柄式、不可变、带 TTL 与签名 URL

实现上先同 service，接口上清晰分组，为将来独立拆分保留空间。

---

## 11. database service 设计

## 11.1 角色

为用户、surface、service 提供数据库能力，并由 Hub 锁定实际 DB 范围。

## 11.2 工具集合

- `storage.database.query`
- `storage.database.insert`
- `storage.database.update`
- `storage.database.delete`
- `storage.database.execute`
- `storage.database.schema`

## 11.3 DB scope 设计

建议逻辑库映射：

- 用户默认库：`data/user/<user_id>/kagent.db`
- surface 库：`data/user/<user_id>/surface/<surface_id>/<db_name>.db`
- service 库：`data/service/<service_id>/<db_name>.db`

## 11.4 锁定原则

调用方不直接传文件路径，只传逻辑目标：

- `scope`
- `db_name`
- `surface_id`（若适用）

Hub 负责映射到真实路径。

## 11.5 风险控制

用户可直接使用 database 工具，但仍需满足最小隔离：

- 不能越过 Hub 指定的 DB scope
- 不能伪造其它 user/surface/service 的 DB

本计划不追求强安全 SQL 沙箱，但要保证库范围隔离准确。

---

## 12. auth service 设计

## 12.1 角色

统一承接：

- 用户认证
- service 认证
- surface session 与 capability 认证
- token 签发与审计

## 12.2 工具集合

### 用户 auth

- `security.auth.user_register`
- `security.auth.user_login`
- `security.auth.user_logout`
- `security.auth.user_me`
- `security.auth.user_password_change`

### service auth

- `security.auth.service_register`
- `security.auth.service_issue_token`
- `security.auth.service_verify_token`
- `security.auth.service_revoke_token`

### surface auth

- `security.auth.surface_session_issue`
- `security.auth.surface_capability_issue`
- `security.auth.surface_verify`

### audit

- `security.auth.audit_query`

## 12.3 密钥与 token 策略

### 根密钥

- 持久化保存
- 用于签发用户 JWT / service token / surface token

### 用户密码

- 升级为 `argon2id` 或 `bcrypt`
- 不再沿用当前轻量 hash 方案

### surface token

- `surface_session_token`：每次 surface 加载时新发
- `capability_token`：短期、绑定 session、绑定 scope
- 根签名密钥持久化，确保多 tab、多 page、重载场景稳定

## 12.4 为什么不能每次重启都重置全部 secret

若所有 secret 都随机重置，会导致：

- 多 tab 失效
- 页面刷新导致旧 capability 全失效
- surface 管理页和聊天页无法稳定共存
- 异步资源下载/动作回报中断

因此应采用：

- 持久化签名根
- 短时 session token
- 更短时 capability token

---

## 13. surface manager 设计

## 13.1 角色

后端 `surface-manager` 负责：

- surface 包扫描
- manifest 管理
- 可用性状态
- 启用/禁用
- session token 与 capability 辅助信息
- surface 元数据查询

前端 page 仍负责真正的 iframe 生命周期与运行时宿主。

## 13.2 工具集合

- `ui.surface.catalog_list`
- `ui.surface.get`
- `ui.surface.enable_set`
- `ui.surface.session_issue`
- `ui.surface.capability_issue`
- `ui.surface.runtime_status`
- `ui.surface.rescan`
- `ui.surface.rebind`

## 13.3 与 chat server 的关系

- `surface-manager` 提供 surface 注册与元信息
- `chat server` 只把 surface 事件与 action 结果纳入聊天流
- `page` 层负责实际加载与宿主交互

---

## 14. 前端 page / surface 设计

## 14.1 page 层职责

page 层是 Hub 的直接客户端，负责：

- 用户会话
- page 自身 UI
- surface 宿主
- 为 surface 申请和转授授权

## 14.2 surface 层职责

surface 负责：

- 独立 UI 与局部状态
- 注册 action
- 响应 page 宿主转发的消息

## 14.3 重要限制

- `surface` 不能脱离 `page` 获得系统能力
- `surface` 不能直接加载其它 `surface`
- 非 `/page/*` 页面不能加载 `surface`

## 14.4 页面规划

保留并重构：

- `webui/page/chat/`：聊天页
- `webui/page/surface/index.html`：独立 surface 测试/加载页
- `webui/page/surface/admin.html`：统一 surface 管理台

---

## 15. `webui/page/surface/admin.html` 设计

## 15.1 角色定位

该页面是 `page` 层的 surface 管理控制台，不是 surface 本身。

## 15.2 页面模块

建议分为以下区域：

### A. Registry 区

显示：

- `surface_id`
- `name`
- `version`
- `status`
- `enabled`
- `type`

### B. Duplicates / Warnings 区

显示：

- 重复 `service_id`
- manifest 冲突
- surface 包异常
- 重名警告

### C. Permissions 区

显示：

- 该 surface 当前 session
- 最近签发 capability
- scope
- 过期时间

### D. Runtime 区

显示：

- 是否已加载
- ready 状态
- 最近 state_change
- 最近 action_result

### E. Storage 区

显示：

- 当前 surface 对应用户目录
- 当前 surface 默认 DB
- 读写 scope 摘要

### F. Debug / Admin 区

提供：

- `rescan`
- `rebind`
- 查看最近日志
- 打开/关闭 surface

## 15.3 后端 API 建议

- `GET /api/surfaces`
- `GET /api/surfaces/:id`
- `POST /api/surfaces/:id/enable`
- `POST /api/surfaces/:id/session-token`
- `POST /api/admin/surfaces/rescan`
- `POST /api/admin/surfaces/rebind`
- `GET /api/admin/surfaces/:id/runtime`
- `GET /api/admin/surfaces/:id/logs`

---

## 16. 配置目录与仓库布局建议

## 16.1 总体布局

建议引入：

```text
kagent/
├── hub/                          # Hub 主程序源码
├── services/
│   ├── chat-server/
│   ├── ai-doubao/
│   ├── file/
│   ├── database/
│   ├── surface-manager/
│   └── auth/
├── webui/
│   ├── page/
│   │   ├── chat/
│   │   └── surface/
│   └── surface/
├── bin/
│   └── service/
└── data/
```

## 16.2 每个 service 目录

```text
services/<svc>/
├── cmd/<svc>/
├── internal/
├── config/
│   ├── config.json
│   ├── configx.json
│   └── configx.json.example
└── manifest.json
```

## 16.3 配置约束

- `config.json`：常规配置
- `configx.json`：敏感配置，不入库
- `configx.json.example`：完全脱敏示例

---

## 17. 审计、观测与统计

## 17.1 Hub 必须记录

- service 注册/退出
- service health
- tool 调用时间线
- 路由目标
- 成功/失败
- 耗时
- 参数摘要
- 结果摘要
- blob 元信息

## 17.2 持久化建议

建议至少分层：

- 高价值结构化表：路由统计、service 状态、工具绑定
- JSONL operation 日志：详细事件流

## 17.3 统计用途

这些数据将用于：

- 生成路由评分
- 管理台展示
- 故障排查
- 人工 pin 决策

---

## 18. 开发阶段拆分

## 18.1 Phase 0：契约冻结与结构准备

目标：

- 冻结本计划中的核心命名与边界
- 明确 `service_id / service_name / instance_id`
- 明确 manifest 结构
- 明确 page/surface 权限分层

产出：

- service manifest 规范
- routing table 数据结构
- auth token 规范
- file/database scope 规范

完成标准：

- 名称体系不再摇摆
- 可以开始编码而不反复改定义

## 18.2 Phase 1：Hub 控制面最小闭环

目标：

- 实现通用 service 注册
- 实现 `service_session_token`
- 实现基础 registry
- 实现单实例冲突处理
- 实现工具聚合
- 实现静态绑定路由表

完成标准：

- Hub 不再只认识 `ai-doubao`
- 任一 service 能注册并出现在管理视图

## 18.3 Phase 2：chat server 落地

目标：

- 将当前 `Session` / `Pipeline` / chat history 主逻辑迁入 `chat server`
- Hub 对前端保持现有兼容入口
- `chat app -> Hub -> chat server` 跑通

完成标准：

- 聊天主链路不再依赖 Hub 内部业务实现

## 18.4 Phase 3：ai-doubao 接入通用平台

目标：

- 将现有 `ai-doubao` 纳入通用注册模型
- 由 `chat server` 通过 Hub 路由到 `ai-doubao`

完成标准：

- AI provider service 化从特例变成平台标准能力

## 18.5 Phase 4：file / database / auth service

目标：

- 统一 `file+blob`
- 统一 `database`
- 统一 `auth`
- page/surface/service 三类 scope 全部跑通

完成标准：

- 聊天页与 surface 页都能在新体系下稳定访问存储与鉴权链路

## 18.6 Phase 5：surface-manager 与管理台

目标：

- 完整 surface manager
- `webui/page/surface/admin.html`
- 稳定的 surface session/capability 策略

完成标准：

- 多 tab、多 page、多 surface 并发稳定
- 可通过管理台完成 surface 运维与调试

---

## 19. 迁移策略

## 19.1 原则

- 先兼容、再切换、最后清理
- 任何阶段都保留可回退路径

## 19.2 迁移顺序

建议顺序：

1. 先做 Hub 控制面与注册体系
2. 再迁移 `chat server`
3. 再接入 `ai-doubao`
4. 再迁移 `file/database/auth`
5. 最后重构 `surface-manager` 与管理台

## 19.3 回退机制

每个阶段都要允许：

- service 下线回退
- 路由表刷新回退
- provider 直连回退

---

## 20. 测试与验收

## 20.1 核心验收项

### Hub

- service 注册成功
- 冲突实例被阻止
- 路由表生成正确
- 手工刷新可用

### chat server

- 项目/线程/历史/流式对话不回归
- interrupt / continuation 正常

### auth

- 用户登录/登出/鉴权正常
- service token 正常
- surface session / capability 正常

### file/database

- user/surface/service 三类 scope 正确隔离
- 无法越权访问其它 scope

### surface

- 多 tab 并发稳定
- page 刷新后可重新授权
- 独立 surface 管理页可正常运作

## 20.2 建议测试分层

- 单元测试：manifest、路由评分、scope 解析、token 校验
- 集成测试：Hub 与 service 注册、单实例约束、路由表刷新
- 端到端测试：chat 页面、surface 页面、管理台

---

## 21. 风险与对应策略

## 21.1 风险：Hub 重新变胖

原因：

- 业务逻辑迁移不彻底

策略：

- 严格把会话业务迁入 `chat server`
- Hub 只做代理、鉴权、审计与控制面

## 21.2 风险：命名体系再次混乱

策略：

- 固化 `service_id / service_name / instance_id / tool_id`
- 不允许再混用 slash 与 dot 作为逻辑主键

## 21.3 风险：surface token 体系不稳定

策略：

- 持久化根密钥
- session token 短时
- capability 更短时
- 多 page 并发专项测试

## 21.4 风险：scope 实现错误导致越权

策略：

- file 与 database 共用同一套 scope 解析模型
- 不允许调用方直接传真实路径

## 21.5 风险：service 冲突处理影响可用性

策略：

- 冲突实例默认不纳入路由
- 管理台可见、可人工处理

---

## 22. 本计划的实际落地优先级

若按真实工程效率排序，建议先做：

1. manifest / service 注册 / session token / 单实例约束
2. chat server 边界抽离
3. ai-doubao 接入通用 service 框架
4. auth 统一化
5. file+blob 与 database scope 化
6. surface-manager 与 `admin.html`

---

## 23. 完成标准（DoD）

当本次“大重构”完成时，应同时满足：

1. Hub 不再承载聊天业务实现，只保留控制面、代理与安全边界
2. `chat server` 成为聊天应用的业务后端实现
3. 所有工具都来自已注册 service，并由 Hub 固化路由
4. service 必须注册并换取 token 后才能调用 Hub 工具
5. file/database 完整支持 `user/surface/service` 三类 scope
6. surface session/capability 授权在多 tab、多 page 场景下稳定可用
7. `webui/page/surface/admin.html` 能完成 surface 管理、调试与基础运维
8. 各 service 拥有独立配置目录与敏感配置边界
9. 现有 chat 页面、surface 页面和核心对话链路无行为回退

---

## 24. 下一步建议

本计划落盘后，建议紧接着产出以下两份细化文档：

1. `service manifest + auth token + routing table` 详细设计
2. `surface manager + admin.html` 详细产品/接口设计

