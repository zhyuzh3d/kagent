# Structure

本文件只描述当前仓库的关键结构、模块职责和主要接口，便于 agent 定位文件和理解代码分层。

## 1. 当前目录结构

> **原则**：`hub/` 与 `services/*` 目录均视为相互独立的 Golang 项目（逻辑层面），除 `pkg/` 协议共享外，不得产生跨项目的文件跨层依赖。

> 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage`。

```text
kagent/                                         # 仓库根目录
├── AGENTS.md                                   # 自动化协作与文档维护总规则
├── README.md                                   # 项目入口说明，是否为最新待确认
├── hub/                                        # Hub 进程与治理代码
│   ├── cmd/hub/main.go                         # Hub 启动入口与工具/静态路由装配
│   ├── config/                                 # Hub 自身配置目录
│   │   ├── services.json                       # Hub 生命周期配置唯一事实源
│   │   ├── config.json                         # Hub 通用配置，启动时会按空 JSON 语义加载
│   │   ├── configx.json                        # Hub 敏感配置，启动时会按空 JSON 语义加载
│   │   └── configx.json.example                # Hub 敏感配置样例
│   └── internal/                               # Hub 私有实现
│       ├── app/                                # 认证、身份、日志、运行时、烟测等基础能力
│       ├── gateway/                            # Tool / Admin / System 网关与 middleware
│       ├── observability/                      # 审计与可观测存储
│       ├── protocol/                           # 协议文档
│       ├── routing/                            # 工具路由引擎
│       ├── security/                           # 头部清洗与互信 header 注入
│       ├── supervisor/                         # 注册、心跳、生命周期与进程管控
│       └── transport/                          # Hub 调用 Service 的传输层客户端
├── services/                                   # 多独立 Service（各自独立进程）
│   ├── account/                                # 账号与会话服务
│   │   ├── cmd/account/main.go                 # 账号服务入口与工具处理
│   │   ├── config/                             # 账号服务配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│   │   ├── internal/app/                       # 账号服务应用装配、HTTP tool 入口、鉴权与存储适配
│   │   ├── run/                                # Hub 可读写的运行态工件
│   │   └── manifest.json                       # 服务运行清单
│   ├── ai_doubao/                              # AI Provider 子项目，承载当前 ai_doubao tools
│   │   ├── cmd/ai_doubao/main.go               # AI 服务入口与 tool/WS 承载
│   │   ├── config/                             # AI 配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│   │   ├── run/                                # Hub 可读写的运行态工件
│   │   └── manifest.json                       # 服务运行清单
│   ├── chat_server/                            # 业务编排服务新子项目
│   │   ├── cmd/chat_server/main.go             # Chat 服务入口与 tool/WS/配置工具承载
│   │   ├── config/                             # Chat 配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│   │   ├── run/                                # Hub 可读写的运行态工件
│   │   └── manifest.json                       # 服务运行清单
│   ├── file_storage/                           # 文件 / blob 能力新子项目
│   │   ├── cmd/file_storage/main.go            # 文件服务入口与 tool 承载
│   │   ├── config/                             # 文件服务配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│   │   └── manifest.json                       # 服务运行清单
│   ├── sql_db/                                 # 数据库 / 共享存储能力新子项目
│   │   ├── cmd/sql_db/main.go                  # 数据库服务入口
│   │   ├── config/                             # 数据库服务配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│   │   └── manifest.json                       # 服务运行清单
│   ├── surface_manager/                        # Surface 扫描与 capability/session
│   │   ├── cmd/surface_manager/main.go         # Surface Manager 入口
│   │   ├── config/                             # Surface Manager 配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│   │   └── manifest.json                       # 服务运行清单
│   └── autogui/                                # 桌面自动化服务
│       ├── cmd/autogui/main.go                 # Autogui 入口与桌面控制工具承载
│       ├── config/                             # Autogui 配置目录（含 config.json / configx.json / configx.json.example，启动最早阶段加载）
│       └── manifest.json                       # 服务运行清单
├── webui/                                      # Web UI 静态资源
│   ├── page/                                   # 宿主页面
│   │   ├── account/index.html                  # 账号页面
│   │   ├── chat/                               # Chat 页面脚本与资源
│   │   ├── service/                            # Service/admin 治理页面与配置/文件编辑 UI
│   │   └── surface/                            # Surface 页面脚本与资源 (含 lib/ 逻辑库与 components/ 组件)
│   └── surface/                                # 插件化 UI
│       ├── buildin/                            # 内置 Surface
│       └── demo-unsafe.html                    # 示例页面（用途待确认）
├── pkg/                                        # 共享公共包（当前稳定共享面仅 hubsvc / toolproto）
│   ├── hubsvc/                                 # Hub <-> Service 互信与 bootstrap secret
│   └── toolproto/                              # 工具协议结构
├── scripts/                                    # 构建 / 启动 / 重置脚本
│   ├── deploy.sh                               # 构建并启动 Hub
│   ├── reset_db.sh                             # 停止进程并清理 data/
│   └── ...                                     # 其他烟测与辅助脚本
├── data/                                       # 运行态数据
├── run/                                        # PID 与运行态临时文件
├── log.txt                                     # Hub 统一日志
├── plan/                                       # 规划 / 设计 / 分析文档
├── doc/                                        # 项目说明、开发日志与说明子文档
│   ├── _instruction.md                         # 说明入口页
│   ├── _instruction/                           # 说明专题子文档
│   │   ├── core.md                             # 核心理念与开发规范
│   │   ├── structure.md                        # 目录结构与模块职责
│   │   └── glossary.md                         # 术语表
│   ├── _devlog.md                              # 开发日志入口
│   ├── _devlog/                                # 按日拆分的开发日志
│   │   └── _devlog-260318.md                   # 当前日志分片（历史产物）
│   └── devnote.md                              # 说明用途待确认
└── ref/                                        # 参考资料
```

## 2. 核心模块职责

### 2.1 Hub

- 统一入口与治理边界，托管 WebUI 静态资源并提供默认跳转。
- 暴露 Tool 入口，并把 `hub.governance.service.*`、`hub.admin.*`、`hub.system.*` 作为内置工具注册到同一条工具平面。
- 负责身份识别、header 清洗、路由选择、审计记录和生命周期编排。
- `hub/internal/app/` 提供认证、身份、日志、PID 快照、烟测与路径解析等基础能力。Hub 是身份验证的唯一物理边界。
- Hub 当前的启动快照使用 sqlite 持久化，默认与用户态 Hub 库共用 [`data/hub/users.db`](../../data/hub/users.db)；`hub.internal.app.StartupSnapshotStore` 在该库内维护 `hub_startup_snapshots` 表。
- Hub 当前会在启动阶段确保 `hub/` 与各受管 service 至少存在 `config/`、`config.json`、`configx.json`、`configx.json.example`，并按空 JSON 语义加载 Hub 自身 `config.json` / `configx.json`。
- `hub/internal/gateway/` 汇总 `tool_handler.go`、`admin_handler.go`、`system_handler.go` 和 `middleware.go`，负责 Caller 身份识别与注入。
- `hub/internal/supervisor/` 负责注册、心跳、DAG 启动编排、停止、重启和进程跟踪；依赖图事实当前来自各 service `run/manifest.json` 的 `depends_on`，不再来自 `hub/config/services.json`。
- `hub/internal/routing/` 决定某个 `tool_id` 选哪个 service instance。
- `hub/internal/security/` 负责 protected headers 清洗与 Hub<->Service 互信 header 注入。
- Hub 当前已提供 `hub.system.state.get`，用于返回治理视图下的 service、tool 与 route 运行态快照。
- Hub 当前还提供 `hub.admin.service.config.*` 与 `hub.admin.service.files.*` 工具；前者是 `config.json` / `configx.json` 的快捷入口，后者是 service 工作区文件浏览与通用写入入口。

### 2.2 account

- 对外提供 `account.auth.register/login/logout/me/password_change`。
- 对 Hub 暴露 `account.system.keys.get` 和 `account.session.dump_active`。
- 负责 token 签发、账户公钥分发和单会话状态维护。
- 当前实现已收拢到 `services/account/internal/app/`，由单一 app 包承载启动装配、`/service/tool/exec` 入口、业务处理与通过 Hub 调 `sql_db` 的存储客户端。
- 账号 schema 与签名密钥初始化当前已完全收回到 service 进程内部，完成后才会 register，不再依赖 Hub 反向调用 `service.lifecycle.init`。
- `services/account/config/` 当前在启动最早阶段按统一 helper 读取 `config.json` 与 `configx.json`，空文件会被视为 `{}`。
- 通过 `svc.account.token` 与 Hub 协同完成登录态同步。

### 2.3 chat_server

- 对外提供 `app.chat.project_*`、`app.chat.thread_*`、`app.chat.config.get`、`app.chat.config.update` 和 `app.chat.stream`。
- 以 HTTP + WS 形式承载与 Page 的会话交互，并通过 `POST /service/tool/exec` 和 `GET /service/tool/ws` 接入 Hub 的 tool 平面。
- 通过 Hub 工具网关访问 `ai.*`、`storage.*` 等外部能力。
- 当前已支持在 `service -> hub -> sql_db` 这类二次调用链路中透传 `origin_caller`，用于按原始用户 scope 访问聊天数据；但这类链路是补充能力，不是首选交互主路径。
- Chat 页面配置当前通过 Hub 转发到 Chat Server 工具读取与更新，不再由 Hub 自身直接维护配置读写接口。
- 已实现 `service.lifecycle.health`、`service.lifecycle.state.get` 和 `service.lifecycle.shutdown`，但仍保留 `/healthz` 与 `/admin/shutdown` 兼容入口。
- 使用 Hub 提供的数据库/存储适配层而不是直接绕过 Hub。

### 2.4 ai_doubao

- 对外提供 `ai.speech.asr`、`ai.llm.stream`、`ai.speech.tts`。
- 提供 HTTP 原子工具和 WS 流式工具，并通过 `POST /service/tool/exec` 与 `GET /service/tool/ws` 接入 Hub。
- 启动入口当前会先按统一 helper 加载 `config.json` / `configx.json`，随后继续读取 `services/ai_doubao/config/configx.json` 的模型配置。
- 已实现 `service.lifecycle.health`、`service.lifecycle.state.get` 和 `service.lifecycle.shutdown`，同时保留 `ai_doubao.system.health`、`ai_doubao.system.shutdown` 作为兼容别名。
- 对 `/service/tool/exec` 与 `/service/tool/ws` 校验 Hub 注入的互信 headers。

### 2.5 sql_db / file_storage / surface_manager / autogui

- `sql_db`：核心数据库服务。负责整站数据的持久化读写，提供 `storage.database.*` 与 `storage.share.read/write`。**核心契约：数据库能力正式收敛到 `sql_db` 工具面；默认按当前 `caller` 锁定 User/Surface/Service scope，在显式传入 `scope_source=origin` 且存在合法 `origin_caller` 时，可按原始 `user` / `surface` scope 访问。** 当前 sqlite 落盘根目录是仓库根下 `data/`，并按 scope 拆分为 `data/user/<user_id>/`、`data/user/<user_id>/surface/<surface_id>/`、`data/service/<service_id>/`；每个 scope 默认数据库文件名为 `kagent.db`，也可通过 `db_name` 覆盖。
- `file_storage`：提供 `storage.file.*`、`storage.blob.*` 以及 `service.lifecycle.*` 能力；当前已补入 `service.lifecycle.state.get`，并与 `sql_db` 一样支持在显式需要时按 `origin_caller` 解析 `user` / `surface` scope。
- `surface_manager`：负责 Surface catalog 扫描、session/capability 颁发，以及 `ui.surface.*` 相关工具；当前文件能力通过 Hub 转调 `storage.file.*`，Surface catalog、用户启用状态与日志查询也已改为通过 Hub 调 `sql_db` 的 `storage.database.*` / `storage.share.*`，不再直连本地 sqlite；其内部 store 初始化与 catalog 预热当前也已收回到 service 自启动阶段。
- `autogui`：桌面自动化服务。当前提供鼠标、键盘、截图与 `service.lifecycle.*` 工具，已被纳入 `hub/config/services.json` 的受管服务列表。
- `sql_db`、`file_storage`、`surface_manager` 与 `autogui` 当前都已在启动最早阶段按统一 helper 加载 `config.json` / `configx.json`，并在各自 `run/.service_pid` 中记录进程用于下次自清理。

### 2.6 webui

- `webui/page/` 是宿主页面，当前包含 `account`、`chat`、`service`、`surface` 四类入口。
- `webui/surface/` 是插件化 UI 与内置 Surface 资源目录。
- `webui/page/service/admin.html` 与相关脚本当前承载服务治理界面，可查看 service 详情、编辑 `config.json` / `configx.json` / `manifest.json`，并通过文件浏览页签读写任意工作区文件。
- 页面逻辑与 Hub 的工具平面、Surface 能力和账号态联动。

### 2.7 共享包

- `pkg/toolproto/`：工具请求/响应、caller、context、effects、service tool 声明与 supervisor 协议。
- `pkg/hubsvc/`：Hub<->Service bootstrap secret、互信 header 与会话协商工具。
- 当前 `pkg/` 不再承载 sqlite 驱动注册或数据库打开逻辑；这类实现性工具已收回 `hub` / `sql_db` 各自目录内。

## 3. 关键接口与运行契约

### 3.1 Tool 网关

- `POST /api/tool/call`：Hub 原子工具入口，负责 request 规范化、caller 识别、路由、审计与 effects 落地。
- `GET /api/tool/ws?tool_id=...`：Hub 流式工具入口，按 `tool_id` 和 `ws_path` 反代到目标 service。

### 3.2 Service 工具入口

- `POST /service/tool/exec`：Service 原子执行入口。
- `GET /service/tool/ws`：Service 流式执行入口。
- Service 侧必须校验 Hub 注入的互信 headers。

### 3.3 Governance / System Tool IDs

- `hub.governance.service.register`
- `hub.governance.service.heartbeat`
- `hub.governance.service.drain`
- `hub.admin.services.list`
- `hub.admin.routes.get`
- `hub.admin.routes.bind`
- `hub.admin.audits.list`
- `hub.admin.tool.probe`
- `hub.admin.service.get`
- `hub.admin.service.start`
- `hub.admin.service.stop`
- `hub.admin.service.restart`
- `hub.admin.service.drain`
- `hub.admin.service.rebind`
- `hub.admin.service.disable`
- `hub.admin.service.enable`
- `hub.admin.service.manifest.get`
- `hub.admin.service.manifest.update`
- `hub.admin.service.config.get`
- `hub.admin.service.config.update`
- `hub.admin.service.config.restore_default`
- `hub.admin.service.files.list`
- `hub.admin.service.files.read`
- `hub.admin.service.files.write`
- `hub.admin.service.build`
- `hub.admin.service.generate`
- `hub.system.version.get`
- `hub.system.smoke.test`
- `hub.system.report_log`
- `hub.system.health`
- `hub.system.state.get`
- `hub.system.shutdown`

这些能力都通过 `POST /api/tool/call` 暴露，`hub/internal/gateway/hub_manifest.go` 里是当前的主注册清单。

### 3.4 Service 侧契约

- `POST /service/tool/exec`：Service 原子执行入口。
- `GET /service/tool/ws`：Service 流式执行入口。
- Service 侧必须校验 Hub 注入的互信 headers。
- `service.lifecycle.health`：Hub 首选的可用性探测工具；`/healthz` 只作为兼容 fallback。
- `service.lifecycle.state.get`：Hub 当前已在 `account`、`ai_doubao`、`chat_server`、`file_storage`、`sql_db`、`surface_manager`、`autogui` 落地，用于返回 service 自报运行态。
- `service.lifecycle.shutdown`：Hub 首选的停机工具；`/admin/shutdown` 仅是 `chat_server` 的兼容入口。
- `ai_doubao` 还保留 `ai_doubao.system.health` 和 `ai_doubao.system.shutdown` 作为生命周期别名。
- 所有受管 service 当前都会在启动最早阶段通过 `pkg/hubsvc/project_config.go` 完成 `config/` 目录补齐，并读取 `config.json` / `configx.json`；空文件按 `{}` 处理。
- Hub 当前不再调用 `service.lifecycle.init`；service 若需要初始化依赖，必须在自己 register 前完成。

### 3.5 运行态工件

- 各 service 的运行清单位于 `services/<svc>/manifest.json`。
- Hub 拉起 service 时会使用 `services/<svc>/run/manifest.json` 与 `services/<svc>/run/.service_secret`。
- 各 service 当前还会在 `services/<svc>/run/.service_pid` 记录自己的最近进程信息，用于下一次自启动时清理旧实例；这一步不再由 Hub 统一维护。
- Service 命名规范：`services/<svc>` 文件夹名、`manifest/service_id`、Hub 注册名、默认 `instance_id` 前缀、`run/<svc>-latest` 产物名，以及代码内表达“自身 service 名”的常量/变量/类型命名，必须统一使用同一个 underscore 版本 `<svc>`。
- 当前仓库中的 `hub/` 与所有受管 `services/<svc>/` 目录下都已存在 `config/`、`config.json`、`configx.json` 与 `configx.json.example`；默认空配置统一按 `{}` 处理。
- Hub 的 service 配置自动补齐逻辑与自定义 service 脚手架生成逻辑当前都已对齐到上述三文件标准。
- `pkg/hubsvc.LoadProjectConfigFiles(projectRoot, files)` 当前把传入文件名限制为 `config/` 目录下的相对路径，并返回 `filename -> {result, err}` 的结果映射；`err.kind` 会区分 `missing` 与 `load`。
- `chat_server` 的公开运行配置当前直接持久化到 `services/chat_server/config/config.json`，不再经平台级 `user_custom_config.json` 覆盖。
- `ai_doubao` 的私有模型配置当前固定落在 `services/ai_doubao/config/configx.json`。
- Hub 当前的启动快照里 `registered` 已直接等于 `ready`；若依赖缺失、环依赖或上游失败，下游会被显式标记为 `skipped`。
- Hub 默认 sqlite 路径由 `hub/cmd/hub/main.go` 的 `-sqlite-path` 控制，当前默认值是 `data/hub/users.db`；该库当前同时承载 Hub 用户态相关存储与 `hub_startup_snapshots` 启动快照表。
- `sql_db` 的 sqlite 文件不固定为单库，而是以 `data/` 为根目录按 caller scope 分目录落盘；默认文件名是 `kagent.db`，传入 `db_name` 时会在对应 scope 目录下生成 `<db_name>.db`。
- `scripts/deploy.sh` 负责构建 `*-latest` 可执行文件并启动 Hub。
- `scripts/reset_db.sh` 负责停止进程并清理 `data/`。

### 3.6 头部与身份

- Hub 注入 caller headers：`X-Hub-Request-Id`、`X-Hub-Trace-Id`、`X-Caller-Type`、`X-Caller-User-Id`、`X-Caller-Service-Id`、`X-Caller-Surface-Id`、`X-Caller-Reliability`。
- Hub 当前还会注入 origin delegation headers：`X-Origin-Caller-Type`、`X-Origin-Caller-User-Id`、`X-Origin-Caller-Service-Id`、`X-Origin-Caller-Surface-Id`、`X-Origin-Caller-Token`。
- Hub<->Service 互信 header：`X-Service-Id`、`X-Service-Instance-Id`、`X-Service-Auth`、`X-Hub-Service-Id`、`X-Hub-Service-Instance-Id`、`X-Hub-Auth`。
- 转发前会清洗 protected headers，避免浏览器伪造内部身份字段。
- 语义上，`caller` 仍表示当前直连 Hub 的请求主体；`origin_caller` 表示链路起点主体。平台支持 `service -> hub -> service` 链路保留 `origin_caller`，但架构上仍首选 `web -> hub -> service -> web` 的直接调用路径。

### 3.7 调用副作用与调用权限

- `toolproto.CallResponse` 支持顶层 `effects.set_cookies` 和 `effects.set_headers`。
- `toolproto.ServiceTool` 支持 `allowed_caller_types`、`streaming`、`ws_path`、`capabilities_required` 等声明。
- Hub 在网关层统一执行 caller type 校验、路由与副作用写入。

## 4. 运行与开发验证

1. 构建与并行部署：`./scripts/deploy.sh`
2. 重置环境：`./scripts/reset_db.sh`
3. 健康核验：调用 `hub.system.smoke.test` 工具
4. 日志分析：查看 `log.txt` 或浏览器控制台中的 `[PAGE]` / `[SURF]` 标签日志

---

**文档更新时间**：2026-03-21 12:29:00 CST

**信息来源**：`find hub -maxdepth 3 -type d -name config -o -type f -name 'config*.json'`、`find services -maxdepth 3 \( -type d -name config -o -type f -name 'config*.json' \)`、`pkg/hubsvc/project_config.go`、`hub/config/services.json`、`hub/cmd/hub/main.go`、`hub/internal/app/hub_platform.go`、`hub/internal/gateway/admin_handler.go`、`hub/internal/gateway/admin_service_tools.go`、`hub/internal/gateway/hub_manifest.go`、`hub/internal/supervisor/admin_ops.go`、`services/account/cmd/account/main.go`、`services/account/internal/app/app.go`、`services/ai_doubao/cmd/ai_doubao/main.go`、`services/chat_server/cmd/chat_server/main.go`、`services/chat_server/internal/app/runtime_config.go`、`services/file_storage/cmd/file_storage/main.go`、`services/sql_db/cmd/sql_db/main.go`、`services/surface_manager/cmd/surface_manager/main.go`、`services/autogui/cmd/autogui/main.go`、`webui/page/service/admin.html`、`webui/page/service/admin.js`、`webui/page/service/components/logic.js`、`webui/page/service/components/render.js`。
