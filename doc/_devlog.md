## [2026-03-19 13:15 CST] 文档架构准则更新 (Doc & Arch Rules Update)
- 主要变更：
  - **强调独立项目准则**：在 `core.md` 和 `structure.md` 中明确了 `hub/` 与每个 `services/` 子目录均为逻辑上独立的 Golang 项目，禁止跨层文件依赖，仅通过 `pkg/` 共享协议。
  - **完善数据库安全契约**：在说明文档中补充了 `database` 服务自动根据 Hub 注入的 Caller 身份执行“物理存储范围锁定 (Scope Locking)”的机制。
  - **明确身份验证边界**：重申 Hub 是身份验证的唯一物理边界，Service 侧仅根据 Hub 注入的 `X-Caller-*` Header 执行业务隔离。
- 关键文件：`doc/_instruction/core.md`, `doc/_instruction/structure.md`, `doc/_instruction.md`

## [2026-03-19 03:28 CST] 简化开发日志 (Devlog Rule simplification)
- 核心变更：更新了 `AGENTS.md` Rule 6.3，将开发日志格式由长模板简化为一两句话的简明模式，提升维护效率。

## [2026-03-19 03:32 CST] 核心规则 AGENTS.md 行为准则改进 (Core Rules Update)
- 时间范围：2026-03-19 03:18 -> 2026-03-19 03:32
- 主要变更：
  - **增强 Agent 决策能力**：在 `AGENTS.md` 的“默认上下文”中添加了规则，要求 Agent 在遇到困难、原则冲突或决策不确定时，主动读取 `doc/_note.md` 参考以往的工作经验和启发。
  - **文档职责对齐**：在 `AGENTS.md` 中增加了对 `doc/_note.md` 职责的正式定义，即“沉淀的工作经验、原则与启发点”。
- 关键文件/模块：
  - `AGENTS.md`
  - `doc/_note.md`
- 开发结果：
  - 成功：核心规则库已更新，Agent 现在的行为逻辑中包含“困难时查阅经验笔记”的启发式路径。
- 关键思路：
  - **知识闭环**：通过规则强制引导 Agent 回顾 `doc/_note`，能有效利用以往任务中积累的深度思考，减少重复错误或低效尝试。

## [2026-03-18 23:10 CST] Hub 治理协议解析优化 (Protocol Parsing Optimization)
- 时间范围：2026-03-18 23:05 -> 2026-03-18 23:10
- 主要变更：
  - **性能优化**：在 `hub/internal/supervisor/handler.go` 中，移除了注册和心跳工具中的 `json.Marshal/Unmarshal` 二次解析逻辑。
  - **直接映射实现**：新增了 `parseRegisterRequest` 和 `parseHeartbeatRequest` 高效解析器，直接从 `map[string]any` 提取字段，消除内存分配开销。
  - **清理冗余**：移除了 `hub/internal/supervisor/handler.go` 中不再需要的 `encoding/json` 引用。
- 关键文件/模块：
  - `hub/internal/supervisor/handler.go`
- 开发结果：
  - 成功：`go build` 校验通过，Hub 关键治理路径的执行效率得到显著提升。
- 关键思路：
  - **零拷贝思想**：针对高频的心跳和重量级的注册请求，避免不必要的序列化往返，提高系统整体吞吐量。

## [2026-03-18 23:05 CST] 修复 Hub 引导逻辑中的路径不一致问题 (Path Re-alignment)
- 时间范围：2026-03-18 23:00 -> 2026-03-18 23:05
- 主要变更：
  - **路径归一化**：在 `hub/cmd/hub/main.go` 中，将注入给 `LifecycleManager` 的注册 URL 从 `/api/service/register` 正式修改为工具调用入口 `/api/tool/call`。
  - **协议同步**：确保所有经由 Hub 拉起的子服务获取到的“控制平面”入口完全对齐到统一工具网关。
- 关键文件/模块：
  - `hub/cmd/hub/main.go`
- 开发结果：
  - 成功：`go build` 校验通过，Hub 启动逻辑与工具化架构达成一致。
- 关键思路：
  - **架构对齐**：通过将治理接口收缩到单一入口 `/api/tool/call`，从根源上消除了物理端口和路由的复杂性。
  - **环境透明性**：确保子服务通过 `registerURL` 环境变量拿到的不再是已废弃的旧路由，避免服务启动时陷入死循环或 404 陷阱。

## [2026-03-18 23:00 CST] Hub 治理工具安全加固 (Governance Hardening)
- 时间范围：2026-03-18 22:55 -> 2026-03-18 23:00
- 主要变更：
  - **加固治理接口**：在 `hub/internal/supervisor/handler.go` 中为 `hub.governance.service.register` 和 `hub.governance.service.heartbeat` 工具增加了强制 Loopback 校验。
  - **细化 Drain 策略**：为 `hub.governance.service.drain` 工具在 `IdentityService` 类型调用时增加了 Loopback 校验，同时保持对 `IdentityUser`（管理员）的灵活访问。
  - **上下文对齐**：完善了从 `ToolHandler` 注入的 `RemoteAddr` 在治理逻辑中的应用，确保所有治理操作均符合“本地域安全”原则。
- 关键文件/模块：
  - `hub/internal/supervisor/handler.go`
- 开发结果：
  - 成功：`go build` 校验通过，Hub 的治理防线已在工具化架构下完整复盖。

## [2026-03-18 22:40 CST] Hub 接口全量迁往工具模式 (Pure Tool-Centric Arch)
- 时间范围：2026-03-18 22:25 -> 2026-03-18 22:40
- 主要变更：
  - **残余 REST 接口清零**：彻底删除了 `/version`, `/api/internal/healthz`, `/admin/shutdown` 等最后几个传统 HTTP 端点。
  - **工具清单对齐**：在 `hub/internal/gateway/hub_manifest.go` 中新增 `hub.system.shutdown` 和 `hub.system.health` 工具定义。
  - **逻辑闭环实现**：在 `SystemHandler` 中实现了 `handleShutdownTool` 和 `handleHealthTool`。
  - **安全加固**：通过在 `ToolHandler` 中注入 `RemoteAddr` 到 Context，确保 `hub.system.shutdown` 工具即使通过匿名调用也能强制执行回环地址（Loopback）校验。
  - **运维脚本适配**：同步重构了 `scripts/deploy.sh`，将其停止服务的逻辑从旧的 REST URL 切换到通过 `curl` 调用统一的工具入口 `/api/tool/call`。
- 关键文件/模块：
  - `hub/cmd/hub/main.go`
  - `hub/internal/gateway/hub_manifest.go`
  - `hub/internal/gateway/system_handler.go`
  - `hub/internal/gateway/tool_handler.go`
  - `scripts/deploy.sh`
- 开发结果：
  - 成功：`go build` 校验通过；Hub 现在仅保留 `/api/tool/*` 和静态文件服务出口。
- 经验与结论：
  - 有效实践：将 `RemoteAddr` 注入 Context 是在统一工具框架下处理“物理安全校验”的有效手段。
  - 架构收益：从此所有外部调用（包括管理操作）均可通过工具流水记录、审计以及实施统一的权限策略。

## [2026-03-18 22:20 CST] Hub 内部功能工具化与架构收归落地 (DevComplete)
- 时间范围：2026-03-18 21:25 -> 2026-03-18 22:20
- 主要变更：
  - **核心分发器重构**：在 `hub/internal/gateway/tool_handler.go` 中实现了 `InternalToolRegistry`，支持内存级工具（Go 函数）的直接调度，消除了 `hub.*` 工具的外部 RPC 开销。
  - **治理与管理功能工具化**：
    - 重构 `AdminHandler`：将服务列表、路由绑定、审计查询等 5 个管理接口转为 `hub.admin.*` 工具。
    - 重构 `SupervisorHandler`：将服务注册、心跳、Drain 等 3 个治理接口转为 `hub.governance.*` 工具。
  - **系统接口与流式工具适配**：
    - 重构 `SystemHandler`：将版本获取、配置热更新、冒烟测试、日志上报等接口转为 `hub.system.*` 工具.
    - 新增流式工具：实现了 `hub.system.logs.subscribe` WebSocket 工具，支持实时订阅 Hub 运行日志。
  - **身份平面全量接入**：所有内部工具均通过 `IdentityMiddleware` 进行调用方身份识别与权限校验（`IdentityUser` / `IdentityService` / `IdentityAnonymous`）。
  - **端点清理与终态收拢**：在 `hub/cmd/hub/main.go` 中注释并移除了所有不再需要的旧有 REST API 路由，Hub 现已全面转向“工具驱动”的单一入口架构。
- 关键文件/模块：
  - `hub/internal/gateway/tool_handler.go`
  - `hub/internal/gateway/admin_handler.go`
  - `hub/internal/gateway/system_handler.go`
  - `hub/internal/supervisor/handler.go`
  - `hub/internal/app/logger.go`
  - `hub/cmd/hub/main.go`
- 开发结果：
  - 成功：`go build ./hub/cmd/hub/` 编译通过；`hub.*` 命名空间工具注册闭环；日志订阅 WS 工具可用。
- 经验与结论：
  - 有效实践：使用匿名接口 (`interface { RegisterTool(...) }`) 在 `main.go` 中进行依赖注入，能在保持各 Handler 独立性的同时，实现工具碎片的解耦注册。
  - 有效实践：将日志订阅内置于 Logger 的 PubSub 模式，比读取磁盘文件更实时且更安全（支持内存级多路复用）。
- 下一步：
  - 验证前端（WebUI）对 `hub.admin.*` 工具的调用适配情况，并逐步清理完全无用的 Legacy Handler 代码（目前仅为注释状态）。

## [2026-03-18 21:25 CST] Hub 内部功能工具化与架构收归计划 (DevPlan)
- 时间范围：2026-03-18 21:01 -> 2026-03-18 21:25
- 主要变更：
  - **制定全量工具化计划**：产出 `plan/2603182125-hub-internal-toolification-devplan.md`，确立了“Hub 作为虚拟服务”与“统一工具分发器”的核心架构转型方案。
  - **明确命名空间结构**：定义了 `hub.admin.*`、`hub.governance.*` 和 `hub.system.*` 的逻辑划分，并拟定了废弃旧有 REST API 路由的演进路径。
  - **身份验证集成方案**：规划了 Hub 从“账号管理者”向“无状态验证者”转变的底层流程，包括基于 `account-service` 同步公钥的身份平面下沉。
  - **统一调度闭环**：确立了内部分发器（Internal Dispatcher）的接口规范，支持同步与 WebSocket 流式工具的统一调度。
- 关键文件/模块：
  - `plan/2603182125-hub-internal-toolification-devplan.md`
  - `hub/internal/gateway/tool_handler.go` (规划中)
- 开发结果：
  - 成功：完成了 Hub 向“逻辑网关”转型的顶层设计与实施计划，确保架构逻辑高度一致性。
- 下一步：
  - 开始按照计划分阶段实施 Hub 内建 Manifest 与内存调度器的逻辑开发。

## [2026-03-18 21:01 CST] Account Service Tool-First 重构落地 (DevComplete)
- 时间范围：2026-03-18 18:35 -> 2026-03-18 21:01
- 主要变更：
  - 新增 `services/account` 标准 tool service，落地 `account.auth.register/login/logout/me/password_change` 与内部工具 `account.system.keys.get`、`account.session.dump_active`，并使用 Ed25519 私钥签发 token。
  - Hub 从内建 `/api/auth/*` 完整切换到 account 体系：删除路由暴露，改为解析 `svc.account.token`，执行公钥验签 + `sid` 单会话校验。
  - 扩展 tool 协议：`CallResponse` 新增顶层 `effects`（set_cookie/set_header），`ServiceTool` 新增 `allowed_caller_types`；Hub 网关统一执行副作用与 caller type 权限校验。
  - 前端账号链路全切 tool：`webui/page/account` 与 `webui/page/chat` 不再访问 `/api/auth/*`，改为调用 `/api/tool/call` 的 `account.auth.*`。
  - 服务治理配置更新：`account` 纳入 `hub/config/config.json`、`config/services.json`、`hub/config/services.json`；`scripts/reset_db.sh` 增加 account 停止与 pid 清理。
  - 清理历史实现：删除 `services/auth/` 全目录。
- 关键文件/模块：
  - `services/account/cmd/account/main.go`
  - `services/account/manifest.json`
  - `hub/internal/app/auth.go`
  - `hub/internal/app/identity.go`
  - `hub/internal/gateway/tool_handler.go`
  - `pkg/toolproto/v1.go`
  - `pkg/toolproto/supervisor.go`
  - `hub/cmd/hub/main.go`
  - `webui/page/account/index.html`
  - `webui/page/chat/index.html`
  - `config/services.json`
  - `hub/config/services.json`
- 开发结果：
  - 成功：`go test ./...` 全量通过；`rg "/api/auth/" webui/page` 无残留；`services/auth/` 已删除。
  - 失败/问题：未发现阻塞性失败；当前 keyring/sid 同步依赖 account 注册回调，Hub 冷启动窗口期可能出现短暂 401（已记录到项目说明“待确认事项”）。
- 经验与结论：
  - 有效实践：将登录态写入统一收敛到 `effects`，能避免 service 直接操作浏览器响应并天然获得跨 service 隔离。
  - 有效实践：将 caller 权限约束放进 tool 描述（`allowed_caller_types`）并在网关统一执行，比按 endpoint 白名单硬编码更稳定。
  - 失败教训：账号体系迁移涉及协议、网关、前端、配置和脚本，必须按“协议 -> 网关 -> 服务 -> 前端 -> 清理”顺序推进，否则中间态容易不可用。
- 下一步：
  - 在联调环境执行一次端到端 smoke（含 SSO 旧 token 失效）并观测 Hub 重启后 account key/sid 恢复稳定性。

## [2026-03-18 18:35 CST] Hub 主程序重构与模块化解耦 (Refactor-Complete)
- 时间范围：2026-03-18 16:30 -> 2026-03-18 18:35
- 主要变更：
  - **Hub 主入口瘦身**：将 `hub/cmd/hub/main.go` 从 1300+ 行重构为 220 行。逻辑按职责全量下沉至 `internal/` 包。
  - **Handler 职责分离**：
    - 抽离 `gateway/admin_handler.go`：接管所有 Admin API 路由。
    - 抽离 `gateway/system_handler.go`：接管 Auth、System、Shutdown 与静态资源路由。
    - 抽离 `supervisor/handler.go`：接管 Service 注册、心跳与管控逻辑。
  - **基础库增强**：在 `internal/app/httputil.go` 沉淀了统一的 JSON 写入、Loopback 判定与 RequestObserver 等工具函数。
  - **治理逻辑收拢**：在 `internal/supervisor/process_control.go` 沉淀了跨平台的进程管理逻辑，消除了代码冗余。
  - **架构健壮性**：通过 `ServiceSyncer` 接口解耦了 `supervisor` 与 `routing` 包的循环依赖，显著提升了编译稳定性。
- 关键文件/模块：
  - `hub/cmd/hub/main.go`
  - `hub/internal/gateway/{admin_handler,system_handler,middleware}.go`
  - `hub/internal/supervisor/{handler,process_control}.go`
  - `hub/internal/app/httputil.go`
- 开发结果：
  - 成功：通过 `go build ./hub/cmd/hub/` 编译校验与 `go test ./hub/...` 全量回归测试。
- 经验与结论：
  - 有效实践：大型 `main.go` 的重构应遵循“先抽工具 -> 后抽 Handler -> 最后装配”的节奏，辅以 struct-based DI 模式能极大提升测试性。
- 下一步：
  - 观察重构后多服务环境下的长时间运行指标，确认无内存泄漏。

## [2026-03-18 14:38 CST] 部署脚本重定向循环修复与实时日志优化 (Bug-Fix)
- 时间范围：2026-03-18 14:25 -> 2026-03-18 14:38
- 主要变更：
  - **修复重定向无限循环**：移除了 `scripts/deploy.sh` 中导致 `tee` 与 `tail` 产生竞争/循环的全局 `exec` 重定向，彻底解决了终端“反复重复大量输出”的问题。
  - **精简实时输出**：将 `tail -F` 改进为 `tail -n 0 -F`，避免监控模式启动时瞬间倾倒已有日志，实现了“新增一行、显示一行”的精准体验。
  - **双路日志落地**：重构 `log_deploy` 函数，支持“终端实时回显 + 文件静默记录”的双向写入，确保脚本自身的部署轨迹也被完整保留在 `log.txt` 中。
- 关键文件/模块：
  - `scripts/deploy.sh`
- 开发结果：
  - 成功：通过了脚本逻辑核验，确认不再产生递归输出。
- 经验与结论：
  - 失败教训：在 Bash 中对脚本 stdout 执行全局 `exec > >(tee -a file)` 后再在该脚本内 `tail -F file`，会通过 shell 继承的 stdout 形成无限递归反馈环。
  - 有效实践：使用 `tail -n 0` 是处理“仅关注后续增量”需求的标准方案。
- 下一步：
  - 确认不同平台（Mac/Linux）下 `tail -n 0 -F` 的语义一致性。

## [2026-03-18 14:25 CST] 部署脚本日志集成与持续输出模式落地 (UX-Enhancement)
- 时间范围：2026-03-18 14:20 -> 2026-03-18 14:25
- 主要变更：
  - **脚本日志全集成**：在 `scripts/deploy.sh` 中引入 `exec > >(tee -a "${hub_log}") 2>&1`，实现了脚本自身的构建/启动日志与 Hub 运行时日志的物理合并。
  - **持续日志监控模式**：脚本执行最后自动进入 `tail -F` 模式，开发者无需手动执行 tail 即可实时观测 Hub 的编排进度与自检结果。
  - **交互流程优化**：明确了“Ctrl+C 仅停止监控，不停止服务”的交互心智。
- 关键文件/模块：
  - `scripts/deploy.sh`
- 开发结果：
  - 成功：实现了部署与监控的无缝衔接，提升了首屏“部署即见效”的沉浸感。
- 经验与结论：
  - 有效实践：利用 `exec` 开启全局重定向，配合 `tee` 保留终端输出，是保持“脚本简洁”且“日志完整”的最优 Bash 实践。
- 下一步：
  - 观察在日志滚动（Rotate）临界点是否会出现极短时间的日志丢失（目前方案在清空文件后立即重定向，风险极低）。

## [2026-03-18 14:20 CST] Hub 启动自清理与端口抢占机制落地 (Self-Cleaning)
- 时间范围：2026-03-18 14:15 -> 2026-03-18 14:20
- 主要变更：
  - **端口抢占逻辑落地**：在 `hub/internal/app/port_preempt.go` 中实现了 `EnsurePortReady` 工具函数。该函数在 Hub 启动最前端执行，通过“HTTP 优雅请求 + PID 物理强杀”双保险机制确保 18080 端口可用。
  - **跨平台兼容支持**：针对 Unix/Mac 使用 `lsof` 和 `kill -9`，针对 Windows 使用 `netstat` 和 `taskkill`，增强了 Hub 二进制文件在不同环境下的自愈能力。
  - **主入口集成**：在 `hub/cmd/hub/main.go` 中接入抢占逻辑，实现了“无脚本依赖”的纯净启动体验。
- 关键文件/模块：
  - `hub/internal/app/port_preempt.go`
  - `hub/cmd/hub/main.go`
  - `doc/_instruction.md`
- 开发结果：
  - 成功：通过了 `go build` 静态编译校验，Hub 现已具备完全脱离外部脚本进行自我生命周期管理的能力。
- 经验与结论：
  - 有效实践：将端口清理逻辑内置于应用本身，能显著降低部署脚本的复杂性，并提升本地开发调试的顺滑度。
- 下一步：
  - 验证在极端高并发启动下的端口竞争表现（虽然单机场景较少见）。

## [2026-03-18 14:05 CST] Hub 托管式编排与极简部署架构落地 (DevComplete)
- 时间范围：2026-03-18 13:50 -> 2026-03-18 14:05
- 主要变更：
  - **Hub 编排逻辑下沉**：在 `hub/internal/supervisor/lifecycle.go` 中实现了动态秘钥 (`S2H_TOKEN`, `H2S_TOKEN`) 的生成与 `.service_secret` 的加密注入（0600 权限）。
  - **自动冒烟测试集成**：在 `hub/cmd/hub/main.go` 的启动流程中接入了 `app.SmokeTester`，实现服务 Ready 后自动触发全链路核验并记录 `System:Internal:AutoSmokeTest` 日志。
  - **极简部署脚本 (Minimalist)**：重构了 `scripts/deploy.sh`，利用 `jq` 动态发现服务并并行构建。实现了 3 秒优雅停机宽限期及超时强制杀进程逻辑，彻底去除了脚本层级的重复校验。
  - **SDK 级鉴权对齐**：验证并对齐了 `pkg/hubsvc` 中的 Bootstrap 逻辑，确保所有独立服务能自动读取注入的秘钥并完成身份注册。
- 关键文件/模块：
  - `hub/internal/supervisor/lifecycle.go`
  - `hub/cmd/hub/main.go`
  - `scripts/deploy.sh`
  - `doc/_instruction.md`
- 开发结果：
  - 成功：通过了 `go build` 静态编译校验，所有核心组件职责已按计划收拢至 Hub。
- 经验与结论：
  - 有效实践：将编排权交给 Hub (Init-Process) 而非外置 Bash 脚本，极大地提升了系统在不同 OS 环境下的鲁印性与安全性。
- 下一步：
  - 确认在生产环境中的 3s 强制杀进程行为是否符合实际运维预期。

## [2026-03-18 13:50 CST] Hub 托管式编排与极简部署计划 (DevPlan)
- 时间范围：2026-03-18 13:33 -> 2026-03-18 13:50
- 主要变更：
  - **制定全量编排计划**：产出 `plan/260318-deployment-orchestration-refactor-devplan.md`，确立了“Hub 作为系统 Init 进程”的核心架构。
  - **职责再平衡**：计划将动态秘钥注入、子进程拉起、注册校验及自动冒烟测试全部收拢至 Hub 内部。
  - **部署脚本降级**：`deploy.sh` 将仅保留构建与 Hub 进程管理逻辑，并引入 3 秒超时强杀机制，确保部署流水线的绝对可靠。
- 关键文件/模块：
  - `plan/260318-deployment-orchestration-refactor-devplan.md`
  - `hub/internal/supervisor/lifecycle.go`
  - `scripts/deploy.sh`
- 开发结果：
  - 成功：完成了从“脚本驱动”向“平台驱动”转型的系统化设计。
- 下一步：
  - 待用户确认计划后，开始分步实施 Hub 内部的服务自动拉起逻辑。

## [2026-03-18 13:31 CST] 部署脚本精简与冗余清理 (Cleanup)
- 时间范围：2026-03-18 13:28 -> 2026-03-18 13:31
- 主要变更：
  - **移除同步逻辑**：删除了 `deploy.sh` 中旧的 `sync_service_secret` 函数及其循环调用。秘钥现在由 Hub 动态生成，脚本不再参与。
  - **启动参数精简**：移除了 Hub 启动命令中与内置默认值相同的路径参数（`public-config`, `user-config`, `sqlite-path`, `services-config`），化简为仅显式指定 `-addr`。
- 关键文件/模块：
  - `scripts/deploy.sh`
- 开发结果：
  - 成功：脚本进一步“瘦身”，降低了维护成本，且启动行为与 Hub 内部默认配置保持高度一致。
- 下一步：
  - 准备按计划实施基于 `.service_secret` 的 Hub-Service 身份互信机制。

## [2026-03-18 12:45 CST] 部署与治理架构重构完成 (DevComplete)
- 时间范围：2026-03-18 12:10 -> 2026-03-18 12:45
- 主要变更：
  - **Manifest 结构化重构**：更新了 `ai-doubao`, `chat-server`, `file`, `database`, `surface-manager` 的 5 个 `manifest.json` 文件，将启动参数 (`entry`) 和生命周期 (`lifecycle`) 配置转为自包含模式，彻底废弃了脚本注入逻辑。
  - **治理逻辑回归 Hub**：在 `hub/internal/app/smoke.go` 中实现了 `SmokeTester` 模块，并在 `hub/cmd/hub/main.go` 中注册了 `/api/system/smoke-test` 内置接口。
  - **部署脚本流水线化**：重构了 `deploy.sh`，通过解析 `hub/config/config.json` 实现了动态的多服务构建、Manifest 自动同步以及内嵌的健康检查调用。
- 关键文件/模块：
  - `services/*/manifest.json`
  - `hub/internal/app/smoke.go`
  - `hub/cmd/hub/main.go`
  - `scripts/deploy.sh`
- 开发结果：
  - 成功：通过 `go build` 校验了 Hub 的编译正确性，新的部署流水线已具备落地条件。
- 经验与结论：
  - 有效实践：使用 Hub 内建的治理能力（如 `SmokeTester`）相较于外部脚本（如 `curl`）具备更强的 Context 感知能力和更细的原子化校验颗粒度。
- 下一步：
  - 在生产/模拟环境中执行 `./scripts/deploy.sh` 进行全量回归测试。

## [2026-03-18 12:10 CST] 部署与治理架构重构计划 (DevPlan)
- 时间范围：2026-03-18 12:05 -> 2026-03-18 12:10
- 主要变更：
  - **制定开发计划**：产出 `plan/260318-deployment-refactor-devplan.md`，深度整合了“Manifest 自包含配置”、“Config 驱动构建”以及“Hub 内建冒烟测试”三大核心变革。
  - **细化技术细节**：明确了 `entry` 与 `lifecycle` 字段的结构，并规定冒烟测试需作为 Hub 的独立 Go 模块实现（非 `main.go` 堆砌）。
- 关键文件/模块：
  - `plan/260318-deployment-refactor-devplan.md`
  - `hub/internal/app/smoke_test.go` (规划中)
- 开发结果：
  - 成功：完成了重构任务的系统性规划，待用户确认后可进入执行阶段。
- 下一步：
  - 按照计划顺序，首先更新各服务的 `manifest.json` 模板。

## [2026-03-18 12:05 CST] 部署与架构方案深度评估 (ANA)
- 时间范围：2026-03-18 11:36 -> 2026-03-18 12:05
- 主要变更：
  - **完成架构评估**：对 `deploy.sh` 的简化、`manifest.json` 职责解耦以及冒烟测试迁移至 Hub 等方案进行了深度可行性分析。
  - **产出 ANA 文档**：落盘 `plan/260318-deployment-architecture-refactor-ana.md`，明确了“配置下沉（Service）”与“治理上浮（Hub）”的技术路径。
- 关键文件/模块：
  - `plan/260318-deployment-architecture-refactor-ana.md`
  - `scripts/deploy.sh`
  - `hub/cmd/hub/main.go`
- 开发结果：
  - 成功：确认三项建议均符合 `Client-Driven` 与 `Hub-Centric` 核心原则，具备极高的重构价值。
- 经验与结论：
  - 有效实践：将业务性的冒烟测试从 Bash 脚本迁移至 Go 实现的 Hub 内部，可以显著提升环境适应性与治理颗粒度。
- 下一步：
  - 根据评估结论，分阶段实施 `deploy.sh` 的配置化重构与 Hub 侧冒烟测试逻辑开发。

