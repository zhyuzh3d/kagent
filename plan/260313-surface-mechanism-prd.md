# Surface 机制改造 PRD（可发现 / 可管理 / 可隔离 / 可扩展）

更新时间：2026-03-13 CST  
依据与范围：基于本对话线程达成共识 + 当前仓库结构与现状实现（仅用于对齐迁移目标，不承诺现状已满足）。  
文档类型：需求文档（PRD）

---

## 1. 背景与问题

当前项目的 surface 能力仍处于 Demo 阶段（例如 `get_surfaces` 由前端硬编码返回单个 counter），缺少：

- 可发现：无法稳定列出“当前可用 surface 清单 + 基本信息”。
- 可管理：缺少统一启用/禁用、冲突处理、错误标记、未来安装/删除入口。
- 可隔离存储：surface 的数据/上传文件缺少明确的、可强约束的可写目录与鉴权模型。
- 可扩展宿主能力：surface 内若要使用 LLM/TTS/ASR 等能力，需通过 page 统一代理而非直连服务端接口。

本 PRD 目标是将 surface 从“单个 demo iframe + 硬编码 bridge”升级为“基于 manifest 的包机制 + 启用管理 + 文件隔离 + token 权限边界”。

---

## 2. 目标（Goals）

1. 启动即完成 surface 发现与入库：程序启动扫描预设目录，解析各 surface 的 `manifest.json`，完成基本校验与入库。
2. 前端一次性拉取“当前用户可用 surface 基本信息”：提供 `GET /api/surfaces` 返回已扫描并入库的 surfaces（包含 enabled/status 等）。
3. 支持前端 surface 管理：用户可在 UI 中对 surface 进行启用/禁用（写入数据库中的用户配置表），冲突/错误可视化并默认禁用。
4. 取消 `surface_states` 数据库表：surface 状态持久化交由 surface 自己写入用户目录下的文件（`surface_data/...`），系统只保留对话流中的 surface 事件与 action 记录。
5. 统一的 surface 文件读写隔离：服务端提供 surface 文件系统（surfacefs）接口/静态路由，强约束目录，不允许跨 surface 与目录穿越。
6. 身份与权限：page 层负责用户认证；surface 层靠 token（`surface_session_token` -> capability token / 签名 URL）获取其目录的受限访问能力。
7. 行为一致性：LLM action 的 `get_surfaces/open_surface/close_surface/get_state/surface.call.*` 在新机制下仍可工作，且 `get_surfaces` 返回只包含“已启用且唯一”的集合。

---

## 3. 非目标（Non-goals）

- 本期不实现 surface 的在线安装/删除/刷新机制，只预留接口与数据结构扩展点（未来再做）。
- 不在系统层维护“全局摘要快照”（例如每 surface 的 last_visible_text 等），列表状态展示由 surface 自身机制在激活后通过事件更新。
- 不做复杂权限系统（角色/多租户策略等），仅满足单机 + 登录用户隔离。
- 不强制实现每个 surface 的数据迁移框架；以“尽可能向前兼容”为规范要求，不兼容时采用弹窗选择清理/取消加载。

---

## 4. 术语与约定

- **Surface**：可被打开/关闭的页面模块（通常 iframe 加载），通过与父层通信注册 action、请求宿主能力、读写自己的数据目录。
- **buildin/ext/custom**：
  - buildin：内置系统 surface
  - ext：第三方扩展 surface（来源官方站点，经过审核）
  - custom：用户自制 surface
- **surface_id**：surface 的全局唯一标识，使用 UUID（字符串）。
- **version**：版本采用“可比较的 (major, minor)”两段语义（例如 `"1"`、`"1.2"`），用字符串存储与传输。
- **min_supported_version**：最小向前兼容版本（同 (major, minor) 语义）。若当前 surface 的数据版本低于该值，则判定不兼容。
- **surface_session_token**：父层在 surface 生命周期内下发的会话令牌，用于换取受限访问 capability。

---

## 5. 目录结构（发现与运行时约定）

### 5.1 Surface 扫描根目录

统一挂载在：

- `webui/surface/buildin/`
- `webui/surface/ext/`
- `webui/surface/custom/`

每个子目录代表一个 surface 包目录，至少包含 `manifest.json`。

示例（仅示意）：

```text
webui/surface/
  buildin/<surface_pkg>/{manifest.json, ...}
  ext/<surface_pkg>/{manifest.json, ...}
  custom/<surface_pkg>/{manifest.json, ...}
```

### 5.2 Surface 数据根目录（每用户）

所有 surface 的可写数据统一落在：

- `data/users/<user_id>/surface_data/<surface_id>/`

允许 surface 与用户在该目录下任意创建子目录与文件（由 surface 自己约束数据组织）。

> 注：此约定替代“写回 surface 包目录内部”的设计，避免覆盖安装内容、便于清理与备份。

---

## 6. manifest.json 需求（最小字段）

### 6.1 必须字段

- `id`：UUID 字符串（surface_id）
- `name`：显示名称
- `version`：版本字符串（`"major"` 或 `"major.minor"`）
- `min_supported_version`：最小兼容版本字符串（同上语义）
- `entry`：入口文件（相对 manifest 所在目录），默认建议 `index.html`

### 6.2 可选字段

- `desc`：简要描述，可包含“命令/能力提示”（非强约束，仅展示与引导用）
- `icon`：图标路径（相对 manifest）
- `tags`：标签数组
- `permissions`：预留（未来扩展）

### 6.3 校验规则（启动扫描时）

1. JSON 可解析、字段类型正确。
2. `id` 为合法 UUID 格式（字符串校验）。
3. `entry` 必须存在且在包目录内（防 `../`、符号链接穿越等）。
4. `version` / `min_supported_version` 必须满足 `(major, minor)` 语法（字符串），并可比较。
5. `id` 冲突：同一次扫描结果中出现重复 `id`，全部标记为 conflict 并默认禁用（进入 UI 可见但不进入可用列表）。

---

## 7. 数据库设计（surface 清单与用户启用）

> 目标：支撑“启动扫描入库 + 前端一次性拉清单 + 用户启用/禁用”。

### 7.1 surfaces（全局清单）

字段建议：

- `surface_id` TEXT PRIMARY KEY
- `surface_type` TEXT NOT NULL  (`buildin|ext|custom`)
- `pkg_path` TEXT NOT NULL（相对 `webui/surface/<type>/...` 的包路径或可还原的路径信息）
- `manifest_json` TEXT NOT NULL（原始 manifest 或规范化后的 JSON）
- `manifest_hash` TEXT NOT NULL（用于判断是否变更，预留刷新）
- `status` TEXT NOT NULL（`ok|invalid|conflict|missing_entry|...`）
- `error` TEXT NOT NULL DEFAULT ''（错误信息，便于 UI 展示）
- `scanned_at_ms` INTEGER NOT NULL

### 7.2 user_surfaces（用户启用配置）

字段建议：

- `user_id` TEXT NOT NULL
- `surface_id` TEXT NOT NULL
- `enabled` INTEGER NOT NULL（0/1）
- `updated_at_ms` INTEGER NOT NULL
- PRIMARY KEY(`user_id`,`surface_id`)

默认策略（建议，可配置）：

- buildin：默认 enabled=1（除非 conflict/invalid）
- ext/custom：默认 enabled=0 或 1（待定，见“待确认事项”）

---

## 8. 启动扫描与入库行为

1. 程序启动后扫描 `webui/surface/{buildin,ext,custom}` 三个根目录的子目录。
2. 对每个包目录读取 `manifest.json`，进行最小校验。
3. Upsert 写入 `surfaces`：
   - `ok`：可用且字段齐全
   - `invalid`：manifest 解析失败或字段不合法
   - `conflict`：surface_id 重复
4. 生成/更新 `user_surfaces`：
   - 新发现的 surface：为每个已存在用户插入默认 enabled（策略见上）。
   - 已存在的 surface：不得重置用户的 enabled（仅更新 surfaces 的 manifest/status）。

> 本期不做“运行时刷新”，仅启动扫描一次。后续可加手动刷新与定时刷新。

---

## 9. API 需求（最小闭环）

### 9.1 拉取清单

- `GET /api/surfaces`
  - 返回当前登录用户视角的 surface 列表（join `surfaces` + `user_surfaces`）
  - 仅将 `enabled=true 且 status=ok 且 id 唯一` 的 surface 标记为“可用”（供 LLM get_surfaces 与 UI 使用）
  - 对 `invalid/conflict` 的 surface：返回但标红，并明确不可用原因

### 9.2 启用/禁用

- `POST /api/surfaces/{surface_id}/enable`（body: `{enabled: true|false}`）
  - 仅改 `user_surfaces.enabled`
  - 若 `surfaces.status != ok`，允许写 enabled 但实际“可用列表”仍应过滤（避免误用）

### 9.3 surfacefs（文件系统）

基于 `surface_session_token` / capability token：

- `POST /api/surfacefs/write`：写文件（强约束到 `data/users/<uid>/surface_data/<surface_id>/`）
- `POST /api/surfacefs/read`：读文件（同上）
- `POST /api/surfacefs/list`：列目录（同上）
- `POST /api/surfacefs/delete`：删除（同上，需额外确认策略：是否允许递归删除）

### 9.4 surfacefs 静态资源访问（为 `<img>` / `<a>`）

- `GET /surfacefs/static/<surface_id>/<path>?st=<signed>`
  - `st` 为短期签名 token（绑定 user_id + surface_id + path 前缀 + exp）
  - 用于无自定义 header 的资源加载
  - 过期后需由父层重新签发 URL

---

## 10. 前端需求

### 10.1 mainsurface 模块（page 级统一能力）

负责：

- surface registry：从 `GET /api/surfaces` 拉取并维护本地缓存
- surface lifecycle：open/close、iframe 管理、消息通道管理
- token 管理：获取并下发 `surface_session_token`，签发/刷新 capability token 与签名静态 URL
- host 能力代理：提供 `flash/chat/asr/tts/isr` 等能力给 surface 使用（统一鉴权、频控、日志）
- action 路由：统一处理 LLM action 与 surface 注册 action 的映射与执行
- 审计与可观测：记录 surface 触发的能力调用（按需写入 message 流或 ops）

补充约束（host 能力下沉）：

- 下层 surface **不得**直接调用服务端的模型能力 API；必须通过 page/mainsurface 代理，避免鉴权散落、成本不可控与日志不可追溯。
- mainsurface 对外提供的能力应至少覆盖：
  - `chat`：文本对话/推理
  - `tts`：朗读/合成
  - `asr`：语音识别
  - `isr`：多模态（对齐“chat 多模态”的语义）
  - `flash`：轻量提示/通知类能力（例如 toast、卡片提示等）
- mainsurface 需对能力调用做统一策略：并发上限、频控、必要时的用户确认、失败重试策略、以及可观测记录（用于排障与审计）。

### 10.2 surface 管理页面/模块

需求：

- 列表展示：name/type/version/desc/status/enabled
- 冲突/错误可视化：`invalid/conflict` 必须高亮并给出 error 文案
- 启用开关：调用 enable API
- 不兼容弹窗：当加载 surface 时发现数据版本 < min_supported_version，弹窗提示并提供选项：
  - 取消加载
  - 清理并重新开始（删除 `data/users/<uid>/surface_data/<surface_id>/` 目录；是否同时清理相关 message 记录由后续另议）

### 10.3 chat page 改造范围（`webui/page/chat`）

目标：将现有 chat page 中“硬编码 surface demo”的部分替换为新 surface 机制的通用实现，并接入 surface 管理能力。

需求：

1. `get_surfaces` 行为改造：
   - 不再由前端硬编码返回固定 counter。
   - 改为读取 mainsurface registry（其数据源为 `GET /api/surfaces`），并严格过滤：`enabled=true 且 status=ok`，且 `surface_id` 唯一。
2. `open_surface/close_surface` 行为改造：
   - `open_surface(target)` 必须根据 registry 中的 `entry` 定位实际入口并加载，而不是固定加载某个 demo html。
   - `close_surface(target)` 必须支持关闭任意已打开 surface（按 `surface_id` 定位）。
3. surface 事件写入对话流：
   - surface 激活后通过父层转发 `state_change/surface_open` 等事件写入 message 流，形成可回放的对话历史。
4. surface 能力调用链路：
   - 下层 surface 通过 mainsurface 提供的 host 能力接口调用 `chat/tts/asr/isr/flash`，并可在 chat page 中观察到必要的日志/错误提示（不要求暴露敏感内容）。

---

## 11. surface 运行时协议（父层 <-> surface）

### 11.1 surface 注册 action（运行时唯一事实源）

原则：

- action 列表不写入 manifest（避免重复维护）。
- surface 在 `entry` 加载后，通过 JS 向父层注册其支持的 action（含名称、参数 schema/示例、说明等）。

父层需要：

- 维护“当前已打开 surface 的 action registry”
- 仅允许对“已注册且已启用”的 action 执行

### 11.2 状态更新

- surface 激活后：
  - 可通过 `get_state` / 或主动 emit `state_change` 事件将状态同步到对话流
  - 系统不维护额外“摘要快照表”，只把事件写入 message 流（用于回放与上下文）

---

## 12. LLM action 行为（对齐目标）

LLM 侧保持动作集合（后续可扩展）：

- `get_surfaces`：返回“当前用户可用、启用且唯一”的 surface 列表
- `open_surface(target)` / `close_surface(target)`：按 `surface_id` 打开/关闭
- `surface.get_state(surface_id)`：读取当前激活 surface 状态（由 surface 自己提供/由父层转发）
- `surface.call.<surface>.<action>`：执行 surface 注册的 action

约束：

- `get_surfaces` 的结果必须稳定、无歧义；冲突/禁用/invalid 的 surface 不应进入该列表。

---

## 13. 认证与安全

1. **page 层**：提供用户注册/登录与落库（实现方案另文细化）。登录态可采用 cookie 或 JWT，但建议避免在 surface 中直接暴露用户 token。
2. **surface 层**：仅使用父层下发的 `surface_session_token` / capability token：
   - 不能凭 user token 直接访问 `surfacefs`
   - capability 必须绑定 `user_id + surface_id + scope + exp`
3. **目录安全**：服务端必须强约束：
   - 路径规范化、防 `../`、禁止符号链接逃逸
   - 仅允许访问 `data/users/<uid>/surface_data/<surface_id>/` 前缀

---

## 14. 运行方式与“绿色软件”体验要求（根目录定位）

需求：面向无技术基础用户，下载后即可运行，不要求用户手动 `cd` 或配置 cwd。

约定（需求级）：

- 程序需内置“根目录定位规则”：以可执行文件所在目录为 root 优先，寻找 `webui/`、`config/`、`data/`、`webui/surface/` 等关键目录；找不到则给出可读错误提示。
- 构建产物仍可放在根目录（并在构建前备份旧可执行文件到 `bin/`），但运行逻辑不得依赖“用户从根目录启动”这一前提。

---

## 15. 风险与对策

- **manifest 变更导致入库过期**：本期不做刷新；需预留 hash 与刷新入口。
- **surface 状态完全下沉导致列表无摘要**：接受；必要时未来可加“可选摘要”机制（仍不回到 `surface_states`）。
- **静态资源鉴权难**：采用签名 URL（`st=`）与短期有效期，避免依赖 Referer/Origin。
- **token 泄露**：签名 URL 短期 + 绑定 path；capability token 仅通过 postMessage 下发且定期刷新。
- **迁移期双实现并存导致行为不一致**：迁移需要明确“旧 demo surface 入口/硬编码 bridge”退役策略，避免同名 action/事件重复写入对话流。

---

## 16. 验收标准（DoD）

1. 启动扫描三类目录并入库，`GET /api/surfaces` 一次性返回清单，且包含 `type/status/enabled`。
2. UI 可启用/禁用 surface，且启用状态持久化到 `user_surfaces`。
3. 冲突 surface 默认禁用并标红展示，且不进入 `get_surfaces` 列表。
4. `surface_states` 表不再作为依赖（删除或不再读取），surface 状态由文件与事件机制承载。
5. surfacefs 读写强制落在 `data/users/<uid>/surface_data/<surface_id>/`，并通过 token 校验；静态资源路由支持签名 URL。
6. buildin 的 counter surface 完成迁移为“标准 surface 包”（含 manifest/entry），chat page 不再依赖旧 demo 硬编码入口，`open_surface(counter)` 可用。
7. 下层 surface 通过 mainsurface 代理调用 `chat/tts/asr/isr/flash` 的链路跑通（至少具备可验证的最小示例），且服务端能记录调用的可追溯信息。

---

## 17. 待确认事项

1. ext/custom 的默认 enabled 策略：默认启用还是默认禁用？
2. “清理并重新开始”时清理范围：仅 surface_data 目录？是否清理历史 messages/ops？
3. surface action 注册的 schema 规范：是否需要统一参数/返回值 schema（便于 LLM 提示与校验）？
4. `surface_id` 冲突后的 UI 交互：当前共识为“全部禁用 + 官方远程核实预留”，是否需要提供本地手动选择覆盖？

---

## 18. 迁移：buildin counter（现有唯一 surface 的标准化改造）

目标：将当前唯一的 counter demo surface 迁移为符合本 PRD 的“标准 surface 包”，用于验证完整机制闭环（扫描入库 -> 列表 -> open -> 注册 action -> 状态/数据 -> 关闭）。

迁移要求：

1. 目录与 manifest：
   - counter 必须位于 `webui/surface/buildin/<counter_pkg>/`（包名可与 `name` 不同）
   - 包内必须提供 `manifest.json`，满足第 6 节字段要求
   - `entry` 必须明确（默认 `index.html`），并可包含 css/js/子目录等多文件结构
2. action 注册：
   - counter 的可用 action 列表由运行时注册产生（不写入 manifest）
   - 至少覆盖现有 demo 支持的核心动作（例如 get_state / set_count / increment / reset 等），并保证与 action 路由机制兼容
3. 状态与数据：
   - counter 在激活后应能从 `data/users/<uid>/surface_data/<counter_surface_id>/` 恢复状态（例如 count 值），并在变化时 emit `state_change`
   - 不依赖系统级 `surface_states` 表
4. chat page 集成：
   - `get_surfaces` 能列出 counter（当 enabled 且 status=ok）
   - `open_surface(counter_id)` 能正确加载 counter 的 `entry`，并完成握手/注册/事件上报
5. 兼容与退役：
   - 迁移完成后，应明确旧 demo 文件/旧硬编码逻辑的退役策略（是否删除、是否保留但默认不走），以避免双入口造成混乱

