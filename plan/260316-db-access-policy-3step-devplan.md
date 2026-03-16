# 260316 DB 访问约束落地开发计划（3 步实施 + 踩坑点改进）

- 文档类型：开发计划（devplan）
- 更新时间：2026-03-16 CST
- 范围：Hub（`hub/`）、database-service（`services/database/`）、其余 services（`services/*`）、部署与重置脚本（`scripts/`）、WebUI 调用链路（`webui/page/*`）
- 约束（已确认）：**除 Hub 与 database-service 外，其余 service 禁止直接访问数据库（不得直接打开/写入 DB 文件）**
- 编写依据（可核验）：
  - DB 单入口/禁止直连的规格来源：`plan/260316-hub-major-refactor-devplan copy.md:47`、`:49`、`:1001`
  - Hub 默认使用 `data/kagent.db` 作为 auth user store：`hub/cmd/hub/main.go:38`
  - Hub user store 逻辑依赖 `updated_at_ms` 列：`hub/internal/app/user_store.go:82`
  - 当前 `data/kagent.db` 的 `users` 表缺少 `updated_at_ms`（本地可复现：`sqlite3 data/kagent.db ".schema users"`）
  - surface-manager 默认也使用 `data/kagent.db`：`services/surface-manager/cmd/surface-manager/main.go:29`
  - `reset_db.sh` 当前会清空 `data/*`：`scripts/reset_db.sh:34`
  - supervisor/register 已返回 service session token：`hub/cmd/hub/main.go:286`（返回体字段 `ServiceSessionToken`：`:362`）

---

## 1. 背景与目标

### 1.1 当前观测到的“可启动但不可用/不稳定”问题

1) **Hub 注册/新用户创建不可靠**
- 现象：`POST /api/auth/register` 返回 `{"ok":false,"error":"用户名已存在"}`，但这并不一定是用户名冲突；当前实现对 `CreateUser()` 的任意错误都映射成“用户名已存在”（需要修正错误分流与日志）。
- 可能根因：Hub user store 期望的 users schema（含 `updated_at_ms`）与当前 `data/kagent.db` 实际 users schema 不一致，导致 insert 失败但被误报。

2) **`reset_db.sh` 后冷启动不稳定**
- 现象：`scripts/reset_db.sh` 会 `rm -rf data/*`，会导致 DB/secret/state 全部消失；在“干净 data/”条件下，部分服务可能因 DB 初始化/外键约束/默认数据插入顺序等问题启动失败或进入不可用状态。

3) **DB 访问责任边界未兑现**
- 现状：多服务仍包含并使用 `internal/app/sqlite_store.go`（chat-server、surface-manager、auth、file 等），这与“除 Hub 与 database-service 外禁止直连 DB”的约束冲突，也制造了“共用/竞争同一 DB 文件 + schema 漂移”的风险面。

### 1.2 本计划的最终目标（Definition of Done）

在不讨论 `git status` 的前提下，本计划以“运行闭环可验收”为准：

- Hub：
  - `POST /api/auth/register`、`POST /api/auth/login` 可用（错误码/错误文案不误导，且有日志可追溯）。
  - `POST /api/tool/call` 在用户态可完成至少 2 个 tool_id 的闭环（例如：`app.chat.project_list`、`storage.database.schema`）。
- database-service：
  - 仍为唯一 DB 工具执行者（`storage.database.*`、`storage.share.*` 等工具按 caller scope 生效）。
- 其余 service：
  - **不再直接打开/写入 DB 文件**（无 `sql.Open("sqlite", ...)`/无 sqlite 文件路径参数依赖，或仅在迁移期保留但默认关闭并有清晰的迁移门禁）。
- 脚本与可操作性：
  - `./scripts/reset_db.sh` + `DEPLOY_TAIL=0 ./scripts/deploy.sh` 后，能跑通“注册/登录 -> tool call”的 smoke（脚本级或命令级可执行）。

---

## 2. 设计原则（与现有代码对齐）

1) **避免共用同一个 sqlite 文件承载不同 schema**
- 在迁移完成前，Hub（auth user store）与 surface-manager/chat 等任何“残留 sqlite_store”都不应共享同一 DB 文件，否则会产生不可控 schema 漂移。

2) **先恢复用户态闭环，再推进 service 去 DB**
- 因为 `/api/tool/call` 当前依赖用户 JWT（`hub/internal/gateway/tool_handler.go` 的 `extractJWTClaims`），用户态闭环是所有进一步治理（包括 service->database-service 间接访问）的前置门禁。

3) **逐服务迁移，保留可回退路径**
- 每个 service 的 DB 迁移应可逐步落地：先引入 database-service 的“受控调用通路”，再替换具体读写点，最后移除 sqlite_store 与冗余代码。

---

## 3. 三步实施计划（可直接执行）

### Step 1：止血——修复 Hub 用户库与错误映射，保证“注册/登录 -> tool call”闭环

#### 3.1.1 目标
- 解决“注册误报用户名已存在”的问题，确保 Hub auth 的可用性与可诊断性。
- 消除 Hub auth 与其他模块在 `data/kagent.db` 上的 schema 冲突风险。

#### 3.1.2 推荐方案（优先级从高到低）

**方案 A（推荐）：Hub 用户库独立 DB 文件（与业务 DB 解耦）**
- 修改 Hub `-sqlite-path` 的默认值：从 `data/kagent.db` 调整到例如 `data/hub/users.db`（或 `data/hub/auth.db`）。
  - 变更点：`hub/cmd/hub/main.go:38`
- `reset_db.sh` 的默认清理策略需明确：是否保留 `data/hub/users.db`（“保留账号”）或一起清空（“全量重置”）。建议提供参数化模式（见 Step 1.4）。

**方案 B：在现有 `data/kagent.db` 上做 schema 迁移（短期兼容）**
- `hub/internal/app/user_store.go` 在 init 或启动时检测缺失列并迁移：
  - 若 `users` 表缺少 `updated_at_ms`：执行 `ALTER TABLE users ADD COLUMN updated_at_ms INTEGER NOT NULL DEFAULT 0;`
  - 若 `users.username` 需要唯一：补齐唯一索引/约束（SQLite 需要用唯一索引实现：`CREATE UNIQUE INDEX ...`，同时处理重复数据）。
- 风险：与其他 service 仍共享 DB 文件时，迁移可能被覆盖或引发新冲突；因此仅建议作为过渡，并尽快切到方案 A。

#### 3.1.3 错误映射与日志改造（必须做）
- `POST /api/auth/register`：
  - 仅在“用户名唯一冲突”时返回“用户名已存在”（HTTP 409 更合理；如前端依赖固定 200，也至少区分错误码字段）。
  - 其他 DB 错误返回“内部错误/注册失败”，并在 Hub log 记录真实 error（避免误导排查）。
- `POST /api/auth/login`：
  - 若 schema 不匹配导致查询错误，需要返回明确 internal error（而不是“用户名或密码错误”）。

#### 3.1.4 `reset_db.sh` 的改进（踩坑点）
当前脚本会 `rm -rf data/*`（`scripts/reset_db.sh:34`），容易造成：
- secret 轮换导致旧 cookie/token 全失效；
- 依赖默认数据插入的 service 冷启动失败；
- 难以在“仅重置对话数据、保留账号/配置”与“全量重置”之间切换。

建议改为参数化（示例约定，不限定实现语言/方式）：
- `./scripts/reset_db.sh users`：仅重置 user-scoped 数据（如 `data/user/*`），保留 `data/hub/*`（账号/secret）
- `./scripts/reset_db.sh all`：全量清空（现有行为）
- 脚本输出需明确提示“将会删除哪些路径”，并提供 3 秒倒计时或二次确认（避免误操作）。

#### 3.1.5 Step 1 验收清单
- `DEPLOY_TAIL=0 ./scripts/deploy.sh` 后：
  - `curl -sS -X POST /api/auth/register` 成功返回 `ok=true` 且能拿到 cookie；
  - `curl -sS /api/auth/me` 返回 `ok=true`；
  - `curl -sS -X POST /api/tool/call tool_id=app.chat.project_list` 返回 `ok=true`（若依赖 chat-server 注册，则需确保 chat-server 已 ready）。

---

### Step 2：打通“service 受控访问 database-service”的通路（通过 Hub，不直连 DB）

#### 3.2.1 目标
- 允许 service 以“service caller”的身份，通过 Hub 调用 database-service 的工具接口，从而替换掉各 service 内部的 sqlite_store。
- 该通路必须可鉴权、可审计、可限权。

#### 3.2.2 现有基础（可复用的代码事实）
- Hub supervisor register 会返回 `ServiceSessionToken`（`hub/cmd/hub/main.go:362`），这是 service 级身份的候选凭据。
- Hub 转发到 service 时已经会注入 `X-Hub-Service-Token` 与 caller headers（`hub/internal/security/headers.go` + `hub/internal/gateway/tool_handler.go`）。
- database-service 已具备 `storage.database.query/execute/schema` 与 `storage.share.read/write`（`services/database/cmd/database/main.go`）。

#### 3.2.3 需要补齐的能力（实现要点）

**A. Hub 需要支持“service caller 入站”**
- 现状：`/api/tool/call` 依赖用户 JWT（`hub/internal/gateway/tool_handler.go` 的 `extractJWTClaims`），service 无法用 session token 作为 caller 调用。
- 改造策略（两种择一）：
  1) 新增内部入口：`POST /api/internal/tool/call`（仅 loopback + `X-Hub-Service-Token`），用于 service->hub 调用；
  2) 扩展现有 `/api/tool/call`：若带有效 `X-Hub-Service-Token` 则走 service caller 分支，否则走 user JWT 分支。

**B. service token 校验与 caller 注入**
- Hub 校验 service token：
  - token 来源：`SupervisorRegisterResult.ServiceSessionToken`
  - 校验逻辑应与 `hub/internal/app/hub_platform.go` 的 `VerifyServiceSessionToken` 对齐（并确保 instance/status 校验一致）。
- Hub 构造 toolproto.Context：
  - `caller.type = "service"`
  - `caller.service_id` 填 service 自身 ID
  - capabilities 需要按白名单裁剪（至少先“只允许调用 database-service 的 storage.database.* / storage.share.*”）。

**C. database-service 的 scope 隔离与 db_name 策略**
- database-service 当前根据 caller type 解析 scope（user/surface/service），并允许 share.write 仅 service caller。
- 需要明确：
  - service scope 的默认 DBName 是否继续使用 `kagent.db`（当前默认），还是按 service_id 映射到独立 DB 文件（建议：`<service_id>.db` 或 `<service_id>/kagent.db`，减少互相污染）。
  - 若需要传 `db_name`，需要在 database-service 的 `storage.database.*` 工具中支持从 args 读取并写入 `StorageScopeTarget.DBName`（当前未做）。

#### 3.2.4 Step 2 验收清单
- 任意一个 service（建议从 surface-manager 开始）能在不打开 sqlite 文件的情况下，完成一次：
  - `service -> hub (service caller) -> database-service` 的 `storage.database.execute` 写入；
  - 再通过 `storage.database.query` 读回；
  - 全程可在 Hub 审计/路由信息中看到 tool_id、service_id、request_id。

---

### Step 3：逐服务迁移去 sqlite_store + 收敛重复代码（最终达成约束）

#### 3.3.1 目标
- surface-manager/chat-server/auth/file 等服务不再直接访问 DB。
- 删除或冻结遗留 sqlite_store，收敛 `services/*/internal/app` 中与服务无关的通用代码。

#### 3.3.2 迁移顺序（推荐）

1) **surface-manager**
- 原因：它当前默认直连 `data/kagent.db`，且 reset 后最易暴露外键/默认数据插入问题，是“冷启动不稳”的主要风险源之一。
- 策略：
  - 将 surface catalog / user_surfaces / surfacefs 元数据迁移到 database-service 的 service scope 或 user/surface scope；
  - surfacefs 的实际文件内容仍存文件系统（由 file-service 或 scoped file 规则承载），DB 仅存索引/manifest/权限等元信息。

2) **chat-server**
- 策略：
  - 将项目/线程/消息持久化迁移为 database-service 的 user scope（或单独 chat DBName）；
  - chat-server 作为 tool 执行者不再持有 sqlitePath 参数（或仅保留兼容开关并默认关闭）。

3) **file-service / auth-service**
- file-service：若存在 DB 元数据（如 blob 索引/GC 记录），迁移到 database-service 的 service scope。
- auth-service：当前 Hub 已内置 auth API，可评估将 auth-service 彻底下线（从 deploy 移除），避免双栈。

#### 3.3.3 重复代码收敛（踩坑点）
- `verifyServiceSessionTokenLoose`、心跳 guard、通用 JSON/ID/log 等应抽到 `pkg/`（或 `hub/internal` 的共享包），避免每个 service 一份导致“安全修复/协议变更无法一致落地”。
- 将 `services/*/internal/app` 的“与服务无关模块”拆走后，再做目录清理，避免误删引发回归。

#### 3.3.4 Step 3 验收清单
- 在 `services/*` 范围内全局检索不再出现 sqlite 打开/写入（例如 `sql.Open(\"sqlite\", ...)`、`modernc.org/sqlite` 的直连使用），或仅 database-service 保留。
- `reset_db.sh` + `deploy.sh` 后稳定跑通 smoke：
  - 注册/登录；
  - `app.chat.project_list`；
  - `storage.database.schema`；
  - surface-manager 的至少一个核心工具（例如 surfaces list / surfacefs list）可用。

---

## 4. 部署与验收改进（强烈建议作为门禁）

### 4.1 deploy 增加“验收级 smoke”
在 `scripts/deploy.sh` 的 healthcheck 之后追加（或新增 `scripts/smoke.sh`）：
- `POST /api/auth/register`（随机用户名）
- `GET /api/auth/me`
- `POST /api/tool/call`：
  - `tool_id=app.chat.project_list`
  - `tool_id=storage.database.schema`

### 4.2 常见踩坑清单（带规避策略）
- **共用 `data/kagent.db`**：任何两个模块对同名表做 `CREATE TABLE IF NOT EXISTS` 都可能造成 schema 漂移；规避：DB 文件隔离 + 明确 schema owner + migration。
- **错误误报**：将所有 DB 错误映射为“用户名已存在”会极大拉长排查链路；规避：错误分流 + 日志。
- **reset 语义不清**：全量删除会造成 secret/token 全失效与冷启动失败；规避：参数化 reset + 明确输出/确认。
- **service->hub 调用鉴权缺失**：迁移到 database-service 前，必须先打通 service caller 身份；否则会卡在“服务不能自我持久化”。

---

## 5. 任务拆解（可直接分配）

### Step 1（Hub 止血）
- [ ] 调整 Hub user store DB 文件策略（推荐独立 DB 文件）
- [ ] 修正 `/api/auth/register` 错误映射与日志
- [ ] 补充或调整 reset 语义（至少明确 all vs keep-users）
- [ ] deploy 增加 smoke：register/login + 2 个 tool call

### Step 2（service caller -> database-service 通路）
- [ ] Hub 支持 service caller 入站鉴权（复用 supervisor register token）
- [ ] 定义 service caller 可调用 tool_id 白名单（最小集先仅 database-service）
- [ ] database-service 支持按需要选择 DBName（若要独立 db）
- [ ] 选 1 个服务（surface-manager）完成“无 sqlite 的写/读回”样板迁移

### Step 3（逐服务迁移 + 收敛冗余）
- [ ] surface-manager 去 sqlite_store（元信息 -> database-service；文件内容 -> file/scoped FS）
- [ ] chat-server 去 sqlite_store（project/thread/message -> database-service）
- [ ] file-service/auth-service 去 sqlite（或下线无用 service）
- [ ] 抽公共逻辑到 `pkg/` 并删除重复实现

