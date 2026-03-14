# Hub + Service 最小化架构开发计划

- 日期：2026-03-14
- 文档类型：开发计划（Dev Plan）
- 状态：拟执行
- 范围：仅基于当前仓库已存在的模块与链路，搭建“全面架构”的最小功能闭环
- 依据：
  - `plan/260314-hub-services-refactor-ana.md`
  - `doc/_instruction.md`
  - 当前代码实现：`main.go`、`internal/session.go`、`internal/protocol.go`、`internal/llm.go`、`internal/surfacefs.go`、`internal/surface_catalog.go`、`webui/page/chat/*`、`webui/page/surface/*`

---

## 1. 计划结论

本次重构不应直接追求“完整平台化”，而应以**不破坏现有实时对话、项目/线程管理、surface/action 机制**为前提，先把当前单体后端重构为：

- **Hub**：保留现有 HTTP/WS 入口、JWT/Auth、项目/线程/消息存储、surface 管理、surfacefs、会话编排与审计能力。
- **首个独立 service：`ai-doubao`**：把当前直接内嵌在 `Session` 中的 Doubao ASR/LLM/TTS 能力抽出为独立进程。
- **最小注册与监管闭环**：Hub 能读取 service 配置、拉起/检查/停止该 service、记录其健康状态与调用日志，并把该能力纳入统一路由。

这一定义有两个关键约束：

1. **Browser/Surface 对 Hub 的现有协议保持稳定**，不在第一阶段改动 `ControlMessage/EventMessage` 的对外语义。
2. **只拆“当前最硬耦合、最值得先拆”的 AI Provider 层**，不在本阶段拆分项目/线程、surface、surfacefs、action 编排、blob、sys service、通用依赖求解器。

---

## 2. 当前项目现状与可核验约束

### 2.1 已经具备“Hub 雏形”的部分

当前 `main.go` 实际上已经承担了大量 Hub 职责，且这些能力都与业务主链路强相关，不应在第一阶段继续拆散：

- 对外统一入口：HTTP API + `/ws`
- 身份与鉴权：JWT 登录、Cookie 认证
- 配置管理：`/api/config`
- 项目与线程：`/api/projects`、`/api/threads`
- surface 管理：`/api/surfaces`
- surface 文件能力：`/api/surfacefs/*`
- 管理控制：`/admin/shutdown`
- WebSocket 会话入口：按 `user/project/thread` 建立运行上下文

结论：**当前主程序天然适合作为第一阶段的 Hub 进程**，无需为“形式上的纯净架构”先做大规模目录迁移。

### 2.2 当前最需要解耦的位置

`internal/session.go` 的 `NewSession()` 直接构造：

- `NewDoubaoASRClient(...)`
- `NewDoubaoLLMClient(...)`
- `NewDoubaoTTSClient(...)`

这意味着：

- Provider 切换必须改会话核心代码
- Provider 生命周期与会话生命周期强耦合
- 未来多 Provider、多 service 并存时，路由与审计无法收束到 Hub

结论：**AI Provider 层是本次最优先、也是最合适的拆分点**。

### 2.3 当前不应优先拆分的部分

以下模块虽然未来可以 service 化，但当前阶段不应拆：

- `session.go` 中的 turn 状态机、历史装配、action/state 回流
- `main.go` 中的 Auth、项目/线程 CRUD、surface 列表、surfacefs
- `webui/page/chat/*` 当前的 Worker、事件路由、VAD、播放控制
- `webui/page/surface/*` 当前的 surface manager / dispatcher 体系

原因不是这些模块不重要，而是它们要么是当前业务主干，要么仍在演进中；此时继续拆分会把“架构升级”变成“全站迁移”，风险过大。

---

## 3. 本次开发目标与非目标

### 3.1 目标

本计划的最小交付目标定义为：

1. Hub 仍以当前主程序为核心，对 Browser 暴露的接口与行为保持兼容。
2. Doubao ASR/LLM/TTS 从 Hub 进程内实现，迁移为独立 `ai-doubao` service。
3. Hub 能通过统一 client/gateway 调用该 service，而非直接 new Provider。
4. Hub 能对该 service 做最小监管：启动、健康检查、失败上报、停止。
5. Hub 具备最小 service 注册信息与调试可观测接口，为后续多 service 扩展预留稳定骨架。
6. 出现新链路不稳定时，支持**配置级回退到本地直连 Provider 模式**，确保可回滚。

### 3.2 非目标

本阶段明确不做：

1. 不改 Browser/Hub 协议，不重写前端聊天页事件模型。
2. 不拆分 `chat orchestrator` 为独立 service。
3. 不拆分 `surface-manager`、`surfacefs`、项目/线程、认证、SQLite 存储。
4. 不落地 blob service、大对象 handle、sys services。
5. 不实现完整 `requires/provides` 自动求解与多 service 依赖图。
6. 不做“第三方不可信 service 沙箱体系”的完整版本。
7. 不做管理 UI；只保留最小 admin/debug API。

---

## 4. 目标架构设计

### 4.1 第一阶段目标形态

### A. Browser / Surface

- 保持现状
- 仍只连接 Hub
- 仍使用现有 `/ws` 控制与事件模型
- 当前 LLM 的 surface action 提示、action report、state change 机制保持不变

### B. Hub

Hub 保留以下职责：

- HTTP/WS 统一入口
- JWT/Auth
- Runtime Config
- 项目/线程/消息/operation log 存储
- Surface Catalog 与 Surface Enable/Disable
- Surface Session Token 与 Capability Token
- Surface 状态与 action report 的收口、持久化与事件广播
- `Session` / `TurnPipeline` 编排
- service registry、service health、service lifecycle 的最小能力
- Provider 调用日志与错误统一包装

### C. `ai-doubao` service

该 service 仅承载当前已存在的三类能力：

- ASR
- LLM
- TTS

不在第一阶段承载：

- chat orchestration
- action 解释与 followup
- project/thread/message 存储
- surface 或 surfacefs

### D. 调用关系

第一阶段稳定关系应为：

`Browser/Surface -> Hub -> ai-doubao service`

而不是：

- `Browser -> ai-doubao`
- `Surface -> ai-doubao`
- `ai-doubao -> SQLite`
- `ai-doubao -> surface manager`

---

### 4.2 为什么选择“只拆 ai-doubao”

这是当前项目里收益最高、风险最可控的一刀：

1. `session.go` 对 Doubao 的直接构造是当前最硬的实现耦合点。
2. ASR/LLM/TTS 本就是“外部能力适配层”，天然适合作为边界。
3. 前端和 storage/surface/action 体系仍在项目内部快速迭代，不宜同时拆。
4. 只要先把 Provider 层 service 化，后续再拆 `chat-orchestrator`、`blob`、`sys.*` 时，Hub 骨架已经成立。

---

### 4.3 Hub 与 service 的最小内部契约

### 4.3.1 契约原则

1. 对外协议冻结，对内协议新建。
2. 先定义“可运行的最小契约”，不做过度抽象。
3. 先满足当前 `Session` 真实调用方式，再考虑未来通用化。

### 4.3.2 最小 service 信息接口

建议定义一个只服务 Hub 的最小信息接口，例如：

- `GET /healthz`
- `GET /service/info`

`/service/info` 最少返回：

- `service_id`
- `service_name`
- `version`
- `provider`
- `capabilities`
- `transport`

建议首版 `capabilities` 仅声明：

- `asr.stream`
- `llm.stream`
- `tts.synthesize`

说明：

- 这里先不强行落完整 `list_tools` + `requires/provides` 生态。
- 但字段命名要向未来兼容，避免第二次推翻。

### 4.3.3 最小业务接口

建议内部协议按当前真实链路设计为三类：

1. `ASR`：流式输入音频，流式输出 partial/final/endpoint
2. `LLM`：文本输入 + 历史输入，流式输出 delta/final
3. `TTS`：文本输入，输出完整音频 bytes 与格式

建议保持与当前 Hub 侧语义对齐，而不是先发明一套新平台协议：

- ASR 输出直接映射当前 `ASREventPartial/Final/Endpoint`
- LLM 输出直接映射当前 `llm_delta/llm_final` 需求
- TTS 输出直接映射当前 pipeline 的分段合成需求

### 4.3.4 传输建议

为降低第一阶段重构复杂度，建议：

- **Phase 1 默认使用 loopback HTTP/WS**
- 传输层抽象为 endpoint 配置，不把调用代码写死到 TCP
- 若后续明确不考虑 Windows，再把 Hub <-> service 平滑切到 UDS

理由：

- 当前项目先需要验证“进程边界 + 路由 + 监管”是否稳定
- 若同时引入 UDS、supervisor、动态注册、全流式协议改造，故障面会过大
- loopback HTTP/WS 更易调试、抓日志、做灰度回退

---

### 4.4 Hub 内部模块重组建议

本次开发不建议先做大规模目录搬迁，而建议按“先抽象、再挪代码”的方式推进。

### 第一阶段建议保留不动的入口

- `main.go` 继续作为主 Hub 入口

### 建议新增的最小内部边界

- `internal/servicehub/`
  - service registry
  - service config
  - service lifecycle / health
  - service client factory
- `internal/ai/`
  - Hub 侧统一 provider gateway 接口
  - ASR/LLM/TTS 的 service client 实现
- `service/ai-doubao/` 或 `cmd/ai-doubao/`
  - 独立可执行服务入口

### 当前可保留在 Hub 的模块

- `internal/session.go`
- `internal/pipeline.go`
- `internal/sqlite_store.go`
- `internal/auth.go`
- `internal/surfacefs.go`
- `internal/surface_catalog.go`
- `internal/operation_log.go`

原则是：**只把 Provider 实现与其接线点拆出去，不把整个应用重新切碎**。

---

### 4.5 当前阶段不落地的“平台能力”，但要预留接口

以下能力暂不实现，但应在命名与结构上预留扩展点：

1. 多 service registry
2. 通用工具聚合接口
3. `requires/provides` 绑定
4. blob handle / 大对象引用
5. untrusted service 分级运行
6. 管理模块和升级模块

建议方式：

- Hub 内部结构预留 `ServiceInfo`、`CapabilityDescriptor`、`ServiceStatus`
- API 先做最小 debug/admin 输出，不对前端暴露复杂控制面

---

## 5. 分阶段实施计划

### 5.1 Phase 0：边界冻结与回退设计

### 目标

在改代码前先锁定哪些内容绝对不能动，确保本次重构是“可回退的架构升级”，不是“无保护的大迁移”。

### 任务

1. 冻结 Browser/Hub 协议：
   - `ControlMessage`
   - `EventMessage`
   - `/ws` 握手方式
2. 冻结前端运行流程：
   - Worker 建连
   - 历史拉取
   - VAD 触发
   - ASR/LLM/TTS 事件消费
3. 定义 Provider 运行模式配置：
   - `local`：继续使用当前进程内 Provider
   - `service`：改走 Hub -> service
4. 定义 service 基础配置项：
   - endpoint
   - timeout
   - health check interval
   - restart policy

### 交付物

- 内部架构说明
- 配置字段草案
- 回退策略说明

### 验收标准

- 不修改任何前端协议语义
- 任何后续阶段失败，都可以回到 `local` 模式继续运行

---

### 5.2 Phase 1：Hub 内部解耦 Provider 构造

### 目标

把 `Session` 从“直接 new Doubao 客户端”改为“依赖一个可切换的 Provider Gateway”，先完成进程内抽象，再引入进程外 service。

### 任务

1. 抽出统一接口：
   - `ASRClient`
   - `LLMClient`
   - `TTSClient`
   - `ProviderGateway` / `ProviderFactory`
2. 改造 `NewSession()`：
   - 不再直接构造 `NewDoubao*Client`
   - 改为从 gateway/factory 获取
3. 把 Doubao 的本地实现整理为可复用 provider 模块，供：
   - Hub 本地模式复用
   - `ai-doubao` service 复用
4. 为调用链增加统一 request id / turn id / latency log

### 交付物

- Hub 内部 provider 抽象层
- 本地模式仍可运行的兼容实现

### 验收标准

- `local` 模式下行为与当前项目保持等价
- 不改变现有对话、surface、项目/线程行为

---

### 5.3 Phase 2：实现首个独立 `ai-doubao` service

### 目标

让 Doubao 能力脱离 Hub 进程运行，并通过稳定接口被 Hub 调用。

### 任务

1. 新建 `ai-doubao` 可执行程序
2. 实现最小 service 接口：
   - `GET /healthz`
   - `GET /service/info`
   - ASR 流式接口
   - LLM 流式接口
   - TTS 合成接口
3. 将现有 `internal/asr.go`、`internal/llm.go`、`internal/tts.go` 中可复用逻辑迁入 service 可调用层
4. 保证 service 自身不依赖：
   - SQLite
   - Auth
   - surface
   - WebSocket 浏览器上下文

### 交付物

- `ai-doubao` 可执行程序
- service 对外契约文档
- Hub 侧 client stub

### 验收标准

- Hub 可在 `service` 模式下完成完整链路：
  - ASR partial/final
  - trigger_llm
  - llm delta/final
  - TTS 回传与播放
- service 独立崩溃时，Hub 能返回明确错误而不是整体失控

---

### 5.4 Phase 3：补齐最小 registry / supervisor / health 闭环

### 目标

不是做“完整模块平台”，而是让首个 service 真正处于可治理状态，而不是仅仅“能跑通一次”。

### 任务

1. 引入 Hub 内 service 配置加载
2. 启动期注册 service 信息
3. 健康检查与状态缓存
4. 最小 lifecycle：
   - start
   - stop
   - restart（可选，先支持手动/配置驱动）
5. Hub 记录统一 service 调用日志：
   - service id
   - capability
   - turn id
   - latency
   - result
   - error
6. 提供最小 debug/admin 接口查看：
   - service 状态
   - endpoint
   - health
   - 最近错误

### 交付物

- service registry
- service health 状态
- debug/admin 查询接口

### 验收标准

- 启动时能明确识别 service 是否可用
- service 不可用时，Hub 能稳定降级并给出清晰日志
- service 调用有统一可观测记录

---

### 5.5 Phase 4：集成验证与灰度切换

### 目标

确保新架构不是“能编译”，而是对当前产品主链路真实可用。

### 任务

1. 建立 `local` 与 `service` 双模式回归清单
2. 验证实时语音主链路：
   - start / stop
   - interrupt
   - trigger_llm
   - history sync
3. 验证项目/线程切换不受影响
4. 验证 surface action / state_change / action_report 不受影响
5. 验证服务异常场景：
   - service 启动失败
   - service 超时
   - service 中途断连
6. 形成切换建议：
   - 默认是否继续 `local`
   - 是否允许通过配置切换 `service`

### 交付物

- 回归结果
- 切换策略
- 问题清单

### 验收标准

- 新模式下核心功能无显著回归
- 至少具备“按配置切换、可快速回退”的上线条件

---

## 6. 详细任务拆解

### 6.1 Hub 侧开发任务

1. 新增 service 配置结构与读取逻辑
2. 新增 provider gateway 抽象
3. 改造 `NewSession()` 与 pipeline 依赖注入
4. 新增 service client 实现
5. 新增 health / status / debug 输出
6. 新增调用审计字段与统一错误封装

### 6.2 `ai-doubao` service 开发任务

1. 进程入口
2. 配置加载与 provider 初始化
3. ASR 流式接口
4. LLM 流式接口
5. TTS 接口
6. health/info 接口
7. service 日志与 request id

### 6.3 测试任务

1. provider gateway 单测
2. Hub client/service mock 单测
3. service 接口单测
4. Hub -> service 集成测试
5. 语音主链路手工回归
6. 项目/线程与 surface 行为回归

---

## 7. 验收标准

本计划的完成标准不是“完成所有平台想象”，而是以下条件同时成立：

1. 当前前端聊天页无需重写即可继续使用。
2. `Session` 不再直接依赖 Doubao 具体实现构造。
3. `ai-doubao` 可作为独立进程提供 ASR/LLM/TTS。
4. Hub 能在 `service` 模式下跑通一条真实语音对话主链路。
5. 项目/线程、surface、surfacefs、auth、history 机制不被破坏。
6. Hub 能识别 service 健康状态，并在故障时输出清晰错误。
7. 存在 `local` 回退路径，可在 service 模式失稳时快速恢复。

---

## 8. 风险与应对

### 8.1 最大风险：流式语义回归

风险：

- ASR partial/final 顺序变化
- LLM delta 流式时序变化
- TTS 分段返回时机变化

应对：

- 内部协议优先贴合当前 `Session` 真实消费方式
- 先保证语义等价，再考虑协议美化
- 建立对比日志：本地模式 vs service 模式

### 8.2 风险：把架构重构做成全局重写

风险：

- scope 膨胀
- 原本稳定的 surface/action 体系被无谓扰动

应对：

- 本阶段只拆 Provider
- 不拆 storage / auth / surface / surfacefs
- 不引入第二个 service，直到第一个 service 稳定

### 8.3 风险：Hub 仍然过厚

风险：

- 重构后 Hub 依然承担大量业务

判断：

- 这是当前阶段可接受的设计，不属于失败
- 现阶段目标不是“做薄到极致”，而是先建立稳定 service 边界

应对：

- 明确把“chat orchestrator service 化”放到后续阶段
- 当前只要求 Hub 去掉对具体 Provider 的硬编码依赖

### 8.4 风险：service 监管过早复杂化

风险：

- 一上来做动态发现、签名校验、依赖求解、权限分级

应对：

- 第一阶段只做单 service 的静态配置注册
- 只实现 health、status、日志、回退
- 其余能力只预留接口不落地

---

## 9. 推荐实施顺序

建议严格按以下顺序执行，不要跳步：

1. 先完成 Phase 0，锁定边界与回退策略。
2. 先在 Hub 内完成 provider 抽象，再做独立 service。
3. 先只跑通 `ai-doubao` 一个 service，再补 supervisor/health。
4. 先让 `service` 模式可稳定回归，再讨论第二个 service。

不建议的顺序：

- 先拆 chat orchestrator
- 先拆 surface 体系
- 先做通用工具求解器
- 先做 blob/system service
- 先大规模迁移目录结构

---

## 10. 后续演进建议

当本计划完成且运行稳定后，后续才具备进入下一层平台化的条件。下一阶段优先级建议为：

1. `chat orchestrator` 独立化
2. blob handle 与大对象传输
3. `sys.*` 官方系统能力服务
4. 多 service registry 与基础 `provides/requires`
5. 管理模块 / 升级流水线

前提是：**本计划必须先证明“Hub + 首个独立 AI service”在当前项目里确实降低耦合、且没有破坏主链路稳定性。**

---

## 11. 最终建议

这次重构的成功标准，不是把代码拆得多漂亮，而是做到三件事：

1. 让当前项目从“会话引擎直接绑定 Doubao 实现”升级为“Hub 调用独立 AI service”。
2. 让现有 Browser、surface、项目/线程、消息模型无需重写即可继续工作。
3. 让后续真正的平台化工作，建立在一个已经被验证过的最小骨架上，而不是建立在一次性过度设计上。

因此，建议把本计划作为**当前阶段唯一正确的重构范围**：先完成最小闭环，再决定要不要继续推进更大的 Hub + 多 Service 体系。
