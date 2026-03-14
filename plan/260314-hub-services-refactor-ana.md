# Hub + 独立 Services 架构重构：对话结论汇总与方案草案（Analysis）

- 日期：2026-03-14
- 范围：后端架构重构（单机场景）；Browser/Surface → Hub → Services；服务发现与路由；安全与审计；性能与大对象传输
- 依据：本轮对话达成一致内容 + 当前仓库可核验实现现状（示例证据见 `internal/session.go:86`、`internal/protocol.go:20`、`webui/page/chat/event-router.js:22`、`internal/auth.go:33`、`internal/surfacefs.go:46`）

---

## 0. 背景与目标

### 0.1 背景（现状简述，可核验）
当前仓库后端代码主要集中在 `internal/` 单一 Go package 内，核心链路为单机会话：`ASR -> LLM -> TTS`，前端通过 WebSocket 与后端通信，协议由 `ControlMessage/EventMessage` 约定并被前端事件路由强绑定（例如 `internal/protocol.go:20`、`webui/page/chat/event-router.js:22`）。

核心耦合点之一是会话 `NewSession()` 内部直接构造 Doubao 的 ASR/LLM/TTS 客户端实现（`internal/session.go:86`），使得替换 Provider/多 Provider 并存的改动集中落在会话引擎。

### 0.2 总体目标（对话确认）
将后端重构为 **Hub + 无限多独立 service（Go 可执行程序）** 的单机分布式架构：
- Hub：尽可能薄的中间层（gateway/registry/policy/supervisor），对外暴露统一入口，集中鉴权与安全屏障、审计与可观测。
- Hub 负责加载/关闭/监听各 service（至少保留极简 supervisor 能力）；service 以独立可执行文件部署（对话目标约定：集中放在 `service/` 目录下，当前仓库未落地，待实现）。
- Services：各自独立进程，单一职责（`ai.*`、`sys.*`、`store.*`、`surface.*` 等），通过 API 提供能力；原则上禁止 service 之间直接互调（如需组合，由 Hub 做路由与策略约束）。
- 控制面：采用“工具清单（list_tools）+ 每工具 schema”的表达方式（类 MCP 思想），Hub 汇聚后提供给大模型/Surface 编排参考。
- 数据面：流式/二进制/大对象传输不强行走“全 MCP 化 JSON”，优先 WebSocket 二进制帧与 blob handle 引用机制。
 - 技术偏好/约束（对话确认）：service 以 Go 为主、尽量少依赖；surface 以原生 JS/静态资源为主，避免引入高复杂度依赖体系（例如 npm 依赖爆炸带来的可用性与维护风险）。

---

## 1. 已达成一致（采纳）清单（不可遗漏）

### 1.1 协议与传输
- Browser/Surface **只连接 Hub**；Hub 以“近乎透传”方式转发到 service，并在 service 不可用时做中间降级/错误处理与统一日志。
- Hub ↔ service 的 IPC：以 **HTTP + WebSocket** 为主；单机场景内部优先 **Unix Domain Socket（UDS）上的 HTTP/WS**，减少端口暴露与误连风险；外部（浏览器）仍走 `127.0.0.1` 的 HTTP/WS。
- WebSocket 可用于 **二进制流**（音频帧、TTS chunk、截图块等），避免全链路 base64。

### 1.2 控制面/数据面分离
- 控制面：采用“工具清单（list_tools）+ 每工具 schema”作为权威规范，便于喂给大模型、便于 Hub 聚合/隐藏/降级工具。
- 数据面：继续走 WS binary 与 blob handle；不强行把音频/token/大对象都塞进 JSON。

### 1.3 大对象传输（句柄/引用）
- 大对象传输使用 **handle/blob 引用**：先写入 blob，再在后续请求中传 `blob_id`/临时签名引用；不暴露真实路径。
- 可独立实现 `blob service`（上传/下载/签名 URL/GC/配额），并作为其它服务的统一附件通道。

### 1.4 Hub 角色边界
- Hub 保留 **极简 supervisor + admin API（无 UI）**：进程生命周期（启动/停止/重启/健康检查）、基础路由表固化、registry 与最小策略执行。
- 管理 UI / 管理 surface 可外置为独立 service + surface（通过 Hub 的 admin API 操作），但 supervisor 不能完全外置以避免“管理模块坏了无法自救”。
  - 对话补充：模块管理功能（模块列表、启停、升级、依赖路由表查看）可作为“管理模块 + 管理 surface”独立实现；Hub 只提供 admin API 与最小自救能力。

### 1.5 安全与治理策略
- 采用 service 分级：`trusted`（官方/签名/白名单）与 `untrusted`（用户/AI 生成），默认对 untrusted 更严格（见 4.2）。
- Hub 作为集中安全屏障：统一鉴权、权限（capability）校验、配额与限流、审计与可观测；必要时对危险操作提供人工确认机制（策略层面）。
- 引入“官方 sys services”集中承载系统操作能力（文件、截图、设备控制等），并在接口层实现目录白名单、每 user/surface 隔离目录等约束。
- “代码审查/AI 审查 + 编译准入”可作为门槛与警告机制，但不作为强安全保证（见 4.1 风险说明）。
  - 对话补充（最终修正版本）：你希望尽量不做“绝对保障/绝对拦截”，而是“检查/检验为主”；用户明确要放开时允许放开，但必须强提示与审计。

### 1.6 日志与限额（Hub 极简但必要）
- Hub 需要能记录“全局日志/审计”以便 Debug：特别是 Surface 对工具的调用历史（时间线、路由到哪个 service、成功/失败、耗时、摘要、blob 元信息）。
- Hub 可做极简的频率/文件大小/数据大小限制（按用户、按 surface、按 tool、按 service 维度），避免滥用与自爆。

### 1.7 API 映射/路由与循环依赖
- 每个 service 在注册时声明：
  - `provides`：提供哪些 API 类型/工具及其 schema、版本、可见性等
  - `requires`：依赖哪些 API 类型（可选/必选）
  - 可见性：`public`（前端可用）、`internal`（仅 Hub 或 allowlist）、`hub_only`
- Hub 在加载时自动为每个 requires 选择 provider 并固化路由表；允许用户手工 pin 覆盖。
- 解析需避免循环依赖；后续可引入“基于历史成功率/频率/评价”的策略优化，但第一阶段先做可解释的确定性算法。

---

## 2. 关键设计：工具清单（控制面）规范（权威）

### 2.1 为什么选择工具清单而非仅 OpenAPI（对话结论）
工具清单更贴合“LLM/编排器”视角：工具名 + 参数/返回 schema + 风险/权限/幂等/超时等元信息是一等字段，Hub 易于聚合、隐藏、降级与审计；OpenAPI 可作为派生产物提供给人类/调试/SDK，但不是权威来源。

### 2.2 建议的 service 注册模型（草案）
service 启动后向 Hub 注册（可走 UDS HTTP）：
- `service_id` / `service_name` / `version` / `build_hash`
- `trust_level`: `trusted|untrusted`
- `transport`: `http|ws`，支持的 streaming 能力、binary 支持
- `provides`: 工具列表（每个工具包含 schema 与元数据）
- `requires`: 依赖列表（按“API 类型”而不是具体实现绑定）
- `visibility`: 默认可见性策略 + allowlist（可选）

每个工具的最小字段建议：
- `name`: 全局唯一（建议命名空间：`sys.fs.read`、`ai.llm.stream`、`blob.put`）
- `description`
- `input_schema` / `output_schema`（JSON Schema）
- `side_effect`: `none|read|write|device|network|unknown`
- `capabilities_required`: capability scopes（例如 `sys.fs.read:user/surface`）
- `idempotency`: `idempotent|non_idempotent|unknown`
- `timeout_ms_default`
- `streaming`: `none|sse|ws_text|ws_binary`（控制面声明；数据面另行约定）

> 注：schema 与元信息是给 Hub 路由/策略和给 LLM 参考的“契约”。Hub 运行时只需在入口做校验与审计，尽量不解析业务 payload。

---

### 2.3 Hub 如何把工具清单“交给大模型写 JS surface 调度”（对话目标补齐）
对话目标：Hub 汇聚 `sys.*`、`ai.*` 等工具后，提供给大模型参考，用于生成/驱动 JS surface 的调度逻辑。

建议落地为两条通道（可并存）：
1) **运行时通道（给编排/对话用）**：Hub 在 LLM 的 system prompt 或 tool context 中注入“当前可用工具清单 + schema + 风险/权限提示”；当工具集合变化（service 上下线/升级）时，Hub 可触发重新注入或通过专用事件通知编排器/前端。
2) **开发/生成通道（给代码生成用）**：Hub 暴露 `GET /api/tools`（或等价接口）返回聚合后的工具清单，供“代码生成/审查模块”或管理 surface 拉取，用于生成新的 surface JS/资源包。

---

## 3. 数据面：WS binary + blob handle（可执行约定）

### 3.1 WS binary 适用场景
- 上行音频帧（ASR）
- 下行音频 chunk（TTS）
- 可能的截图分块/视频帧（若未来需要）

关键要求：
- 禁止在热路径大规模 base64（CPU/体积/GC 负担）
- Hub 做 backpressure：每条流设置队列上限、超时、可中断；避免某 service 卡住拖垮 Hub
- 日志只记录元数据（长度、hash、时间、方向、工具名），必要时抽样存档

### 3.2 blob handle 模式（建议）
提供统一的 `blob` 服务（可由 Hub 内置或独立 service）：
- `blob.put`：上传 bytes（HTTP body 或 WS binary），返回 `{blob_id, size, sha256, mime, ttl}`
- `blob.get`：下载（需 capability）
- `blob.sign_url`：签名临时 URL（供浏览器直接拉取/播放）
- `blob.gc`：按 TTL/引用计数清理

所有引用只暴露 `blob_id`，不暴露真实路径；与 `surfacefs` 类似的 capability token 可复用思路（参考 `internal/surfacefs.go:46`）。

### 3.3 “禁止模块之间互相调用”的工程化落地建议（对话目标补齐）
你希望原则上禁止 service 之间互相调用；但允许通过 Hub 间接调用并由 Hub 做路由/策略。

建议的实现策略（从弱到强，可逐步增强）：
- **地址隔离（推荐起步）**：Hub 是唯一 registry，只有 Hub 知道其它 service 的 UDS 路径/端口；service 侧默认不持有其它 service 的地址信息。
- **鉴权隔离**：service 调 Hub 的“内部工具”也必须带 capability/token，Hub 按 allowlist 与 scope 校验，避免任意 service 调用 admin 或高危 sys 工具。
- **（可选）进程级隔离**：对 `untrusted` service 采用最小权限运行（受限用户/受限目录），从根上减少其“私自调用 OS”能力（见 4.2）。

---

## 4. 安全模型：能力集中 + 分级 +（可选）强隔离

### 4.1 “官方 sys services + 源码审查”可行性边界
可行用途：
- 将危险操作集中到可控实现（目录白名单、每 user/surface 隔离目录、审计），降低误用概率
- 对第三方/AI 生成 service 进行准入门槛（检测明显的系统调用/危险依赖）

不可当作强安全边界的原因：
- 源码审查难以覆盖所有绕过路径与运行时行为
- service 仍可能直接调用 OS（读文件、发网络、执行命令等），Hub 入口策略无法阻止其“私自行为”

结论：审查是“治理与提示”，强安全需要 OS 级隔离或最小权限运行作为兜底。

### 4.2 你期望的平衡（对话确认的方向）
你希望“尽量不限制，主要做检查检验，不做绝对保障；用户要乱搞不拦”。建议落地为：
- 默认：`untrusted` service 以受限方式运行（最小权限/受限目录/可观测/可一键停用）
- 用户可显式切换为“完全放开”（需要明确确认 + 强提示 + 仍记录审计）
- `trusted` 官方 service 可获得更高权限与更宽松的限制

最低成本的强约束建议（不等同于重型容器）：
- 以不同 OS 用户运行 untrusted service + 文件权限隔离（真正限制读写目录）
- 再配合 Hub capability（入口权限）形成“两道门”

### 4.3 “审查 → 编译 → 发布 → 启动”的模块准入流水线（对话目标补齐）
你提出：Hub 可调用“代码审查模块”审查新模块源码，确认无明显问题后再编译为 `service/` 下的可执行文件并启动，以从源头减少代码问题；模块化之后升级可通过“拉取新代码→编译→替换产物→重启模块”完成。

建议将其定义为可插拔流水线（允许用户关闭/跳过，但强提示）：
1) 代码获取：拉取/接收源码（来源与校验待定）
2) 静态检查/审查：规则扫描 +（可选）AI 审查，重点关注“直接系统调用/危险依赖/网络外联/自启动行为”等
3) 编译与产物固化：记录 `version/build_hash`，输出二进制到 `service/`（目标约定），生成 manifest 与产物 hash
4) 启动与健康检查：Hub supervisor 接管，失败可回滚到上一版本

同时补齐可追溯性（建议必做）：
- 记录源码版本、构建参数、产物 hash；Hub 启动前校验 hash/签名（至少 hash），便于审计与复现。

---

## 5. API 映射与路由：避免环的确定性绑定（建议算法方向）

### 5.1 不建议仅用“贪心最短路”的原因（对话深入）
局部最短不代表全局可满足；多个 requires 同时存在时，贪心选择可能导致后续依赖无 provider 或形成环，且难以解释与修复。

### 5.2 建议的第一版求解器（可解释、可回溯）
目标：在加载阶段生成“路由表（binding table）”，使所有 service 的 requires 被满足且无环，满足可见性/allowlist/版本约束。

建议流程：
1) 收集约束：所有 service 的 `provides/requires/visibility/trust/version`。
2) 为每个 requires 生成候选 provider 列表并排序（优先 trusted、版本更匹配、依赖更少、链路更短等）。
3) 全局绑定（带回溯）：
   - 先处理“候选最少”的 requires（最难满足）
   - 每绑定一步做增量环检测（SCC）与可满足性检查
   - 失败则回溯换候选
4) 固化路由表：输出可审计、可复现的绑定结果；支持用户 pin 覆盖；运行时仅按路由表转发。

### 5.3 后续可选优化（对话提议）
记录每个工具的使用频率、成功率、用户评价、用户手工选择；用于候选排序打分或给 AI 提供建议，但不改变“约束满足优先”的原则。

---

## 6. 与当前项目的对接：迁移优先级建议（结合现有耦合点）

### 6.1 第一阶段：先把 AI Provider service 化（建议从 Doubao 开始）
依据：当前 `NewSession()` 内部直接依赖 Doubao 的 ASR/LLM/TTS 构造（`internal/session.go:86`），是最明确的替换点与解耦收益点。

建议步骤（不破坏前端协议）：
- 冻结 Browser/Hub 协议：先不改 `ControlMessage/EventMessage` 的对外字段与 `type` 语义（`internal/protocol.go:20` + `webui/page/chat/event-router.js:22`）。
- 将 Doubao 能力移动为 `ai-doubao` service：Hub/会话引擎通过 IPC 调用，保持原有流式语义（LLM delta、TTS chunk、ASR events）。
- 保持 pipeline/turn 编排在一个地方（先留在 Hub 或未来的 chat-orchestrator service），避免在初期引入多跳延迟与复杂性。

### 6.2 第二阶段：抽 sys/blob/surface 管理
- `sys.*`：文件/截图/设备控制集中到官方 sys services，并通过 capability 做细粒度限制与审计。
- `blob`：落地大对象句柄服务，替换现有 base64/直接路径暴露风险点（逐步迁移）。
- `surface-manager`：surface catalog、会话、surfacefs 可独立 service；Hub 仅转发与鉴权。

### 6.3 第三阶段：chat orchestrator service（可选）
将对话编排（ASR→LLM→TTS、interrupt、followup/continuation）拆为 `chat-orchestrator` service：
- 前向：对接现有 chat 页面（仍通过 Hub）
- 后向：对接 `ai.*` 工具（Doubao 或其他 Provider）
- 优点：Provider 可替换；编排逻辑可独立升级
- 约束：需要严格的 streaming 契约与 backpressure 策略，否则链路延迟与队列风险上升

### 6.4 （可选）“通用接口适配层”service（对话早期诉求与后续演进的统一）
对话早期你提出：希望有模块向前端提供通用的 `flash/chat/asr/tts` 接口格式；后续你又提出可将编排拆成 `chat-orchestrator`，使其可对接不同 Provider。

可将两者统一为“适配层”思路：
- 对外（给 Hub/前端/Surface/编排器）：提供稳定的 `ai.*` 工具类型与 schema（例如 `ai.llm.stream`、`ai.asr.stream`、`ai.tts.synthesize`）。
- 对内（对接具体 Provider service）：由适配层把不同 Provider 的差异抹平（模型参数、返回格式、事件语义差异）。

这样能在“换 Doubao 之外的大模型”时，把改动控制在 Provider service 与适配层之间，不牵动上层 Surface/编排逻辑。

---

## 7. Hub “近乎透传”仍需具备的最小能力（精简但不可缺）

必须具备：
- 统一身份鉴权（用户、surface 会话合法性）
- capability 校验（工具级权限/目录白名单/allowlist）
- 统一超时、限流、配额（按工具/按 service/按用户）
- 审计日志（最少记录：who/when/what tool/args 摘要/result/latency/blob 元信息）
- supervisor（进程生命周期、健康检查、崩溃重启节流）
- registry（service 注册、工具清单聚合、路由表固化）

明确不做（保持 Hub 薄）：
- 不在 Hub 内做复杂业务编排（除非尚未拆出 orchestrator）
- 不在 Hub 内解析大 payload（热路径仅做转发与元数据审计）
- 不提供 UI（UI 由外置管理模块提供）

---

## 8. 未决问题（后续需要明确）
- Windows 是否需要支持（决定 Hub↔service 是否必须采用 localhost 端口而非 UDS）
- service 运行隔离的最低标准（仅审查 vs 受限用户运行 vs 更强沙箱）
- tool/类型的版本语义与兼容策略（例如 `ai.tts.v1`、`ai.tts.v2`）
- 统一的错误码/重试语义/幂等语义规范（避免 LLM/Surface 调度不可控）
- streaming 的取消/中断语义（ASR finish、LLM cancel、TTS stop）如何跨 Hub↔service 传播
- `service/` 目录结构、版本命名与升级/回滚策略（当前为目标约定，待落地）
- “允许 Surface 直接调用 service”是否需要作为高级模式（对话结论倾向：默认不开放；如开放需重新评估安全与观测分裂成本）

---

## 9. 下一步建议（可执行）
1) 定义并固化 service manifest（注册、list_tools、requires/provides、visibility、capability、streaming、blob）。
2) 实现 Hub 的 registry + 路由表生成（带回溯）+ supervisor 最小闭环。
3) 将 Doubao ASR/LLM/TTS 抽成首个 `ai-doubao` service，保持现有前端协议不变，跑通语音对话主链路。
4) 引入 blob service（或 blob 子系统）替换大对象 base64/直传，落地 handle 引用。
5) 引入官方 sys services，并为 untrusted service 制定默认受限运行策略（可开关放开）。

---

## 10. 建议的仓库目录与构建产物布局（与“Hub + 多独立 service”一致）

### 10.1 目标形态（对话确认）
你希望项目结构是：
- 1 个主程序：`kagent`（Hub）
- 多个可被命令启动的子程序：例如 `service-manager`、`ai-doubao`、`chat-orchestrator`、`surface-manager`、`sys-*`、`blob` 等
- 每个 service 在源码上都是独立的小 Go 项目（独立 `go.mod`），编译后统一落到 `bin/service/` 下
- Hub 运行时按路由/策略启动并管理这些 service，通过 HTTP/WS（优先 UDS）聚合工具清单与转发调用

### 10.2 推荐目录（源码 / 产物 / 运行态分离）
说明：以下为“建议目标布局”，用于指导重构，不要求一次性迁移到位。

```text
kagent/                              # 仓库根目录（monorepo）
├── cmd/                             # 约定：所有可执行程序的入口都在此
│   ├── kagent/                      # Hub 主程序入口
│   │   └── main.go
│   └── ...                          # （可选）如果把某些“官方 service”也放在根模块构建，可在此追加
├── api/                             # 共享“控制面契约”（工具清单/manifest/错误码/capability/路由表结构）
├── internal/                        # Hub 内部实现（supervisor/gateway/registry/policy/audit 等）
├── services/                        # 所有独立 service 的源码根
│   ├── ai-doubao/                   # 示例：Doubao 能力 service（独立 go.mod）
│   │   ├── go.mod
│   │   ├── cmd/ai-doubao/main.go    # 该 service 自己的入口（沿用 cmd 约定）
│   │   ├── internal/                # 该 service 自己的实现
│   │   └── manifest.json            # 静态声明（provides/requires/visibility/默认超时等）
│   ├── chat-orchestrator/           # 示例：对话编排 service
│   ├── surface-manager/             # 示例：surface 管理 service
│   ├── sys-fs/                      # 示例：官方 sys 文件服务
│   ├── sys-screenshot/              # 示例：官方 sys 截图服务
│   └── blob/                        # 示例：大对象句柄服务
├── bin/                             # 构建产物输出目录（不建议提交到 Git）
│   ├── kagent                        # Hub 二进制
│   └── service/                     # 所有 service 二进制（可进一步按版本分层）
│       ├── ai-doubao
│       ├── chat-orchestrator
│       └── ...
├── run/                             # 运行态文件（socket/pid/临时状态等；不建议提交到 Git）
│   ├── uds/                         # Hub↔service UDS sock（例如 ai-doubao.sock）
│   └── logs/                        # （可选）Hub 统一收集的 service 日志
├── webui/                           # 前端静态资源（保持现状）
├── data/                            # 数据目录（保持现状）
├── scripts/                         # 构建/打包脚本（例如 build all）
├── go.mod                           # Hub 根模块（建议保留）
└── go.work                          # 工作区（建议引入：把根模块与 services/* 一起纳入本地开发）
```

#### 为什么会出现多层 `cmd/`
这是 Go 生态的常见约定：用 `cmd/<program>/main.go` 明确“哪些目录会编译成可执行程序入口”，把入口（wiring）与可复用实现（packages）隔离。

在该布局中会出现两类 `cmd/`：
- 根 `cmd/kagent/`：Hub 的入口
- `services/<svc>/cmd/<svc>/`：该 service 自己的入口（因为 service 本身是独立小项目，也需要同样的入口隔离）

可以不使用 `cmd/` 约定，但对多可执行程序的仓库，长期维护成本通常更高（入口散落、构建路径不稳定、实现/入口边界变模糊）。

### 10.3 与“模块化分布式”的关系（关键点）
- 这里的“分布式”不是多机，而是 **进程边界**：每个 `services/*` 构建为独立可执行文件，由 Hub 负责启动/监控，并通过 IPC（UDS 上的 HTTP/WS）通信。
- “控制面”通过 `manifest.json + list_tools + schema` 实现可插拔与可发现；“数据面”通过 WS binary/blob handle 支持低延迟与大对象传输。
- 该目录布局把“可插拔能力”自然映射为可执行程序与可独立发布单元：升级/回滚可以按 service 维度进行，不必整体替换 Hub。

### 10.4 构建与升级（建议落地方式）
- 建议使用 `go.work` 管理根模块与多个 service 模块的本地开发与引用（避免大量 `replace`）。
- 构建脚本（示意）：
  - Hub：`go build -o bin/kagent ./cmd/kagent`
  - 单个 service：在 `services/<svc>/` 下 `go build -o ../../bin/service/<svc> ./cmd/<svc>`
  - 批量构建：`scripts/build-all.sh` 遍历 `services/*` 执行 build
- 建议的升级/回滚布局（可选但强烈推荐）：
  - `bin/service/<svc>/<build_hash>/<svc>` 存放具体版本产物
  - `bin/service/<svc>/current` 指向当前版本（软链接或小文本指针文件，取决于平台策略）
  - Hub supervisor 只启动 `current`；回滚只需切换指针并重启该 service
