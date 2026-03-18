# Structure

本文件只描述当前仓库的关键结构、模块职责和主要接口，便于 agent 定位文件和理解代码分层。

## 1. 当前目录结构

> 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage`。

```text
kagent/                                         # 仓库根目录
├── AGENTS.md                                   # 自动化协作与文档维护总规则
├── README.md                                   # 项目入口说明，是否为最新待确认
├── config/                                     # 运行配置（公共配置与生命周期配置）
│   ├── config.json                             # Page / Chat 公共配置
│   └── services.json                           # Hub 生命周期配置（当前与 hub/config/services.json 一致）
├── hub/                                        # Hub 进程与治理代码
│   ├── cmd/hub/main.go                         # Hub 启动入口与工具/静态路由装配
│   ├── config/                                 # Hub 本地配置副本
│   │   ├── config.json                         # Hub 管理 service 列表等
│   │   ├── services.json                       # Hub 生命周期配置
│   │   └── configx.json.example                # 配置样例
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
│   │   └── manifest.json                       # 服务运行清单
│   ├── ai-doubao/                              # AI Provider
│   │   ├── cmd/ai-doubao/main.go               # AI 服务入口与工具处理、生命周期别名
│   │   └── manifest.json                       # 服务运行清单
│   ├── chat-server/                            # 业务编排服务
│   │   ├── cmd/chat-server/main.go             # Chat 服务入口与 tool/WS 承载
│   │   └── manifest.json                       # 服务运行清单
│   ├── database/                               # 存储 / 数据库能力
│   │   ├── cmd/database/main.go                # 数据库服务入口
│   │   └── manifest.json                       # 服务运行清单
│   ├── file/                                   # 文件 / blob 能力
│   │   ├── cmd/file/main.go                    # 文件服务入口与 tool 承载
│   │   └── manifest.json                       # 服务运行清单
│   └── surface-manager/                        # Surface 扫描与 capability/session
│       ├── cmd/surface-manager/main.go         # Surface Manager 入口
│       └── manifest.json                       # 服务运行清单
├── webui/                                      # Web UI 静态资源
│   ├── page/                                   # 宿主页面
│   │   ├── account/index.html                  # 账号页面
│   │   ├── chat/                               # Chat 页面脚本与资源
│   │   └── surface/                            # Surface 页面脚本与资源
│   └── surface/                                # 插件化 UI
│       ├── buildin/                            # 内置 Surface
│       └── demo-unsafe.html                    # 示例页面（用途待确认）
├── pkg/                                        # 共享公共包
│   ├── hubsvc/                                 # Hub <-> Service 互信与 bootstrap secret
│   ├── sqlitedriver/                           # sqlite 驱动封装
│   └── toolproto/                              # 工具协议结构
├── scripts/                                    # 构建 / 启动 / 重置脚本
│   ├── deploy.sh                               # 构建并启动 Hub
│   ├── reset_db.sh                             # 停止进程并清理 data/
│   └── ...                                     # 其他烟测与辅助脚本
├── data/                                       # 运行态数据
├── run/                                        # PID 与运行态临时文件
├── logs/                                       # 日志目录（用途待确认）
├── log.txt                                     # Hub 统一日志
├── bin/                                        # 历史二进制与 symlink
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
- `hub/internal/app/` 提供认证、身份、日志、运行时配置、烟测与路径解析等基础能力。
- `hub/internal/gateway/` 汇总 `tool_handler.go`、`admin_handler.go`、`system_handler.go` 和 `middleware.go`。
- `hub/internal/supervisor/` 负责注册、心跳、拉起、停止、重启和进程跟踪。
- `hub/internal/routing/` 决定某个 `tool_id` 选哪个 service instance。
- `hub/internal/security/` 负责 protected headers 清洗与 Hub<->Service 互信 header 注入。

### 2.2 account

- 对外提供 `account.auth.register/login/logout/me/password_change`。
- 对 Hub 暴露 `account.system.keys.get` 和 `account.session.dump_active`。
- 负责 token 签发、账户公钥分发和单会话状态维护。
- 通过 `svc.account.token` 与 Hub 协同完成登录态同步。

### 2.3 chat-server

- 对外提供 `app.chat.project_*`、`app.chat.thread_*` 和 `app.chat.stream`。
- 以 HTTP + WS 形式承载与 Page 的会话交互，并通过 `POST /service/tool/exec` 和 `GET /service/tool/ws` 接入 Hub 的 tool 平面。
- 通过 Hub 工具网关访问 `ai.*`、`storage.*` 等外部能力。
- 已实现 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，但仍保留 `/healthz` 与 `/admin/shutdown` 兼容入口。
- 使用 Hub 提供的数据库/存储适配层而不是直接绕过 Hub。

### 2.4 ai-doubao

- 对外提供 `ai.speech.asr`、`ai.llm.stream`、`ai.speech.tts`。
- 提供 HTTP 原子工具和 WS 流式工具，并通过 `POST /service/tool/exec` 与 `GET /service/tool/ws` 接入 Hub。
- 已实现 `service.lifecycle.health` 和 `service.lifecycle.shutdown`，同时保留 `ai-doubao.system.health`、`ai-doubao.system.shutdown` 作为兼容别名。
- 对 `/service/tool/exec` 与 `/service/tool/ws` 校验 Hub 注入的互信 headers。

### 2.5 database / file / surface-manager

- `database`：提供 `storage.database.*` 以及部分 `storage.share.*` 能力。
- `file`：提供 `storage.file.*`、`storage.blob.*` 以及 `service.lifecycle.*` 能力，manifest 通过 `AllowedCallerTypes` 与 `ScopeSupport` 兼容输出 caller 约束。
- `surface-manager`：负责 Surface catalog 扫描、session/capability 颁发，以及 `ui.surface.*` 相关工具。

### 2.6 webui

- `webui/page/` 是宿主页面，当前包含 `account`、`chat`、`surface` 三类入口。
- `webui/surface/` 是插件化 UI 与内置 Surface 资源目录。
- 页面逻辑与 Hub 的工具平面、Surface 能力和账号态联动。

### 2.7 共享包

- `pkg/toolproto/`：工具请求/响应、caller、context、effects、service tool 声明与 supervisor 协议。
- `pkg/hubsvc/`：Hub<->Service bootstrap secret、互信 header 与会话协商工具。
- `pkg/sqlitedriver/`：sqlite 驱动封装。

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
- `hub.system.version.get`
- `hub.system.config.get`
- `hub.system.smoke.test`
- `hub.system.report_log`
- `hub.system.health`
- `hub.system.shutdown`

这些能力都通过 `POST /api/tool/call` 暴露，`hub/internal/gateway/hub_manifest.go` 里是当前的主注册清单。

### 3.4 Service 侧契约

- `POST /service/tool/exec`：Service 原子执行入口。
- `GET /service/tool/ws`：Service 流式执行入口。
- Service 侧必须校验 Hub 注入的互信 headers。
- `service.lifecycle.health`：Hub 首选的可用性探测工具；`/healthz` 只作为兼容 fallback。
- `service.lifecycle.shutdown`：Hub 首选的停机工具；`/admin/shutdown` 仅是 chat-server 的兼容入口。
- `ai-doubao` 还保留 `ai-doubao.system.health` 和 `ai-doubao.system.shutdown` 作为生命周期别名。

### 3.5 运行态工件

- 各 service 的运行清单位于 `services/<svc>/manifest.json`。
- Hub 拉起 service 时会使用 `services/<svc>/run/manifest.json` 与 `services/<svc>/run/.service_secret`。
- `scripts/deploy.sh` 负责构建 `*-latest` 可执行文件并启动 Hub。
- `scripts/reset_db.sh` 负责停止进程并清理 `data/`。

### 3.6 头部与身份

- Hub 注入 caller headers：`X-Hub-Request-Id`、`X-Hub-Trace-Id`、`X-Caller-Type`、`X-Caller-User-Id`、`X-Caller-Service-Id`、`X-Caller-Surface-Id`、`X-Caller-Reliability`。
- Hub<->Service 互信 header：`X-Service-Id`、`X-Service-Instance-Id`、`X-Service-Auth`、`X-Hub-Service-Id`、`X-Hub-Service-Instance-Id`、`X-Hub-Auth`。
- 转发前会清洗 protected headers，避免浏览器伪造内部身份字段。

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

**文档更新时间**：2026-03-19 01:18 CST

**信息来源**：`rg --files` 的实时目录扫描、`hub/cmd/hub/main.go`、`hub/internal/gateway/hub_manifest.go`、`hub/internal/gateway/admin_handler.go`、`hub/internal/gateway/system_handler.go`、`hub/internal/gateway/tool_handler.go`、`hub/internal/routing/schema.go`、`hub/internal/supervisor/lifecycle.go`、`hub/internal/supervisor/process_control.go`、`pkg/toolproto/v1.go`、`pkg/toolproto/supervisor.go`、`services/ai-doubao/cmd/ai-doubao/main.go`、`services/chat-server/cmd/chat-server/main.go`、`services/file/cmd/file/main.go`、`services/*/manifest.json`、`config/services.json`、`hub/config/services.json`。
