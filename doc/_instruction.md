# 项目说明（doc/_instruction.md）

## 1. 项目概览
`kagent` 已完成 `Hub + 多服务` 运行架构重构：
- Hub 负责统一入口、路由代理、service 注册管理、静态资源托管。
- 服务侧已独立可执行：`chat-server`、`ai-doubao`、`auth-service`、`file-service`、`database-service`、`surface-manager`。
- 前端 `page/chat` 与 `page/surface` 均通过 Hub 访问后端能力。

当前真实状态（2026-03-16 00:06 CST 核验）：
1. 旧单体入口已移除：根目录 `main.go`、`cmd/*`、`internal/*` 不再存在。
2. Hub 已接入路径级 service 代理：`/api/auth/*`、`/api/storage/file/*`、`/api/storage/database/*`、`/api/surfaces*`、`/api/surfacefs/*`、`/surfacefs/static/*` 转发至对应服务。
3. `scripts/deploy.sh` 已支持 7 进程统一构建/拉起/健康检查。
4. `scripts/reset_db.sh` 已支持停止全部服务并全量清空 `data/*`。

## 2. 当前目录结构（关键层级）
> 已忽略噪音目录：`.git`、`node_modules`、`dist`、`build`、`.next`、`coverage`。

```text
kagent/                                                      # 仓库根目录
├── hub/                                                     # Hub 子项目
│   ├── cmd/hub/                                             # Hub 入口与路由层
│   │   ├── main.go                                          # Hub 主进程
│   │   ├── chat_proxy.go                                    # chat HTTP/WS 代理
│   │   └── service_proxy.go                                 # auth/file/db/surface 路由代理
│   └── internal/app/                                        # Hub 自有依赖代码
├── services/                                                # 独立服务子项目
│   ├── chat-server/
│   │   ├── cmd/chat-server/main.go                          # chat 业务服务入口
│   │   └── internal/app/                                    # chat 服务依赖代码
│   ├── ai-doubao/
│   │   ├── cmd/ai-doubao/main.go                            # AI 服务入口
│   │   └── internal/app/                                    # AI 服务依赖代码
│   ├── auth/
│   │   ├── cmd/auth/main.go                                 # 认证服务入口
│   │   └── internal/app/                                    # 认证服务依赖代码
│   ├── file/
│   │   ├── cmd/file/main.go                                 # 文件/Blob 服务入口
│   │   └── internal/app/                                    # 文件服务依赖代码
│   ├── database/
│   │   ├── cmd/database/main.go                             # 数据库存储服务入口
│   │   └── internal/app/                                    # 数据库服务依赖代码
│   └── surface-manager/
│       ├── cmd/surface-manager/main.go                      # surface 管理服务入口
│       └── internal/app/                                    # surface 服务依赖代码
├── webui/
│   ├── page/chat/                                           # chat 页面与 sidebar/WS 模块
│   ├── page/surface/                                        # surface 页面与 admin 管理台
│   └── surface/                                             # 内置 surface 资源
├── scripts/
│   ├── deploy.sh                                            # 7 服务部署脚本
│   └── reset_db.sh                                          # 全量重置脚本
├── config/                                                  # Hub 公共配置
├── data/                                                    # 运行数据目录（可 reset 清空）
├── doc/                                                     # 项目说明与开发日志
├── plan/                                                    # 计划与分析文档
│   ├── 260315-hub-service-platform-full-refactor-devplan.md  # 全量重构开发计划
│   └── 260316-hub-service-platform-architecture-prd.md       # 架构改进需求描述 (PRD)
├── go.mod                                                   # Go 模块定义
└── version.json                                             # 版本单一事实源
```

## 3. 核心模块职责
1. `hub/cmd/hub/main.go`
- 统一入口 `/version`、`/api/config`、`/api/admin/services*`、`/ws` 与静态资源。
- 通过 `service_proxy` 将 auth/file/database/surface 请求转发到独立服务。
- 通过 `chat_proxy` 将 chat HTTP/WS 转发到 `chat-server`。
- **日志记录与过滤**：集中处理前端 `debug/log` 上报，支持 `PAGE/SURF` 标签；静默过滤高频访问日志（如 `/debug/log` 接口）。

2. `services/auth/cmd/auth/main.go`
- 提供 `/api/auth/register|login|logout|me`。
- 负责用户 JWT 签发与解析。

3. `services/chat-server/cmd/chat-server/main.go`
- 提供 `/api/projects*`、`/api/threads*`、`/ws`。
- 强制 `ai_service.mode=service`，并依赖 ai-service 健康。

4. `services/ai-doubao/cmd/ai-doubao/main.go`
- 提供 `/v1/asr/stream`、`/v1/llm/stream`、`/v1/tts/synthesize`。

5. `services/file/cmd/file/main.go`
- 提供 `/api/storage/file/*` 与 `/api/blob/*`。

6. `services/database/cmd/database/main.go`
- 提供 `/api/storage/database/*`。

7. `services/surface-manager/cmd/surface-manager/main.go`
- 提供 `/api/surfaces*`、`/api/surfacefs/*`、`/api/admin/surfaces*`、`/surfacefs/static/*`。

## 4. 开发与运行方式（可验证）
1. 全量测试：`go test ./...`
2. 一键部署：`DEPLOY_TAIL=0 ./scripts/deploy.sh`
3. 全量重置：`./scripts/reset_db.sh`
4. 健康检查：
- `curl -sS http://127.0.0.1:18080/version`
- `curl -sS http://127.0.0.1:18083/healthz`
- `curl -sS http://127.0.0.1:18084/healthz`
- `curl -sS http://127.0.0.1:18085/healthz`
- `curl -sS http://127.0.0.1:18086/healthz`

## 5. 最近关键变更摘要
1. **分服务架构迁移完成**：彻底移除旧单体入口，实现 Hub 与 6 个独立服务（Chat, AI, Auth, File, DB, Surface）的解耦运行与路径级代理。
2. **多服务生命周期协同**：落地 Hub 与 Service 的级联退出与反向心跳监测机制，确保本地多进程运行环境的可控性。
3. **Hub 日志体系精细化**：在 Hub 层增加了前端日志来源识别（PAGE/SURF），并对 `/debug/log` 等高频接口实施了访问日志静默过滤，使开发者能更聚焦于核心服务逻辑。
4. **关键故障修复（ASR 鉴权）**：修复了架构重构后由于分服配置同步遗漏导致的 ASR 401 鉴权失效问题，保障语音交互链路可用性。
5. **环境纯净化与 PRD 落地**：产出全平台重构 PRD，清理旧单体残留，对齐 `data/kagent.db` 主库存储。

## 6. 项目术语表
| 术语               | 定义（本项目语境）                                 | 来源文件                                                       | 状态   |
| ------------------ | -------------------------------------------------- | -------------------------------------------------------------- | ------ |
| `Hub`              | 统一入口与控制面，负责路由代理、服务注册与静态托管 | `hub/cmd/hub/main.go`                                          | active |
| `Service Proxy`    | Hub 的路径级转发机制，将请求派发到独立服务         | `hub/cmd/hub/service_proxy.go`                                 | active |
| `chat-server`      | 聊天业务后端服务                                   | `services/chat-server/cmd/chat-server/main.go`                 | active |
| `ai-doubao`        | AI 能力服务（ASR/LLM/TTS）                         | `services/ai-doubao/cmd/ai-doubao/main.go`                     | active |
| `auth-service`     | 用户注册/登录/JWT 服务                             | `services/auth/cmd/auth/main.go`                               | active |
| `file-service`     | 文件与 Blob 存储服务                               | `services/file/cmd/file/main.go`                               | active |
| `database-service` | scoped 数据库服务                                  | `services/database/cmd/database/main.go`                       | active |
| `surface-manager`  | surface catalog/session/capability 管理服务        | `services/surface-manager/cmd/surface-manager/main.go`         | active |
| `service manifest` | 服务能力声明，用于注册与路由绑定                   | `services/*/manifest.json`                                     | active |
| `Capability Token` | 权限能力令牌，用于 Surface 访问受限资源            | `plan/260316-hub-service-platform-architecture-prd.md`       | active |
| `Scope Isolation`  | 作用域隔离，强制限制 User/Service/Surface 存储路径 | `plan/260316-hub-service-platform-architecture-prd.md`       | active |

## 7. 待确认事项
1. 各服务 `internal/app` 当前采用独立复制，后续可继续做职责最小化与重复代码收敛。
2. Hub 代码中仍保留历史本地 handler 逻辑（运行时已由代理前置接管），可在后续阶段做进一步瘦身清理。

## 8. 文档更新时间与信息来源
- 更新时间：2026-03-16 21:24 CST
- 信息来源：
  - 文件扫描：`find hub services webui/page scripts`
  - 代码核验：`hub/cmd/hub/main.go`、`hub/cmd/hub/service_proxy.go`、`services/*/cmd/*/main.go`
  - 运行验证：`go test ./...`、`scripts/deploy.sh`、`scripts/reset_db.sh`、Hub 代理链路 smoke（auth/chat/ws/file/database/surfaces）
