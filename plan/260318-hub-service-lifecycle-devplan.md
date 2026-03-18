# Hub 统一生命周期管理（Kagent==Hub）开发计划

日期：2026-03-18（CST）

## 1. 背景与目标

本项目采用 Hub + 多独立 Service 的多进程架构。当前存在一部分“构建、启动、健康检查、拉起顺序”等生命周期编排逻辑集中在 `scripts/deploy.sh` 的现状。

本计划的目标是将 **Service 生命周期管理**（启动/关闭/重启/健康判定/可用性探测/超时与重试）收敛到 Hub（根目录可执行 `kagent`）中；而 `scripts/deploy.sh` 仅负责构建产物与启动 Hub 本身，不再管理 Service 的启动与运行期状态。

## 2. 一致结论（最终观点汇总）

### 2.1 产物与目录布局（跨平台、可复制、相对路径、无软链接）
- **Kagent==Hub**：Hub 直接构建到项目根目录，产物固定为 `./kagent`（不采用 wrapper“拉起后退出”的方式，也不依赖软链接）。
- 每个 Service 的可执行文件固定放在其自身目录下的运行目录中：
  - `services/<svc>/run/<svc>-latest`（固定文件名；`<svc>` 必须与 service_id 一致）。
  - 同目录下固定存在 `services/<svc>/run/manifest.json`（与可执行文件强绑定的运行期 manifest）。
- Hub 仅需要配置每个 Service 的目录（例如 `services/ai-doubao/`），即可推导：
  - 可执行：`services/<svc>/run/<svc>-latest`
  - manifest：`services/<svc>/run/manifest.json`
- 路径全部使用**相对项目根目录（appRoot）**的相对路径，确保项目拷贝到任何位置可用。
- 不使用软链接（避免 mac/win/linux 行为差异导致混乱）。

### 2.2 生命周期判定（register/heartbeat 为准；/healthz 仅手工排障）
- Hub 的生命周期决策 **基于 service 的 register/heartbeat**，而非 Hub 主动调用 `/healthz`。
- 健康位同时出现在 **register** 与 **heartbeat**（例如 `healthy=true/false`）。
- `/healthz` 接口作为“手工检查/排障备用”，不作为 Hub 默认启动流程的门槛。

### 2.3 Tool 可用性探测（Hub schema 测试请求；无需额外体系）
- Tool 可用性由 Hub 按 schema 发起“测试调用”验证，不引入额外探测协议。
- 约定：仅 Hub 主动发起的特定 tool 测试才启用 `ctx.hub_only==true`；所有代理转发的调用均为 `ctx.hub_only==false`。
- Service 端只有在 `ctx.hub_only==true` 时才识别`args.healthz`字段，否则忽略（避免被前端或非 Hub 调用伪造）。

### 2.4 Secret 规范
- 统一要求每个 service 的 secret 放在：`services/<svc>/run/.service_secret`（与可执行文件强绑定）。

### 2.5 状态落盘策略（极简）
- Service 的说明、健康度、可用工具列表等运行态信息只存在于 Hub 内存变量，异常记录到日志；不要求写入数据库。
- 允许（可选）在每次 Hub 启动完成后写入一条“启动快照”到数据库，作为事后分析依据（包含：Hub 版本、启动时间、各 service 启动结果与完整 register 信息）。

### 2.6 配置策略（Service 自由 + Hub 顶格限制）
- `register_timeout`、`restart_policy` 等策略由 service 的 `manifest.json` 提供，Hub 只做统一顶格限制：
  - `max_timeout_ms = 5000`
  - `max_restart_times = 10`
- Hub 对 manifest 中的策略做 clamp（并提供默认值/兜底），确保配置缺失或非法时仍可工作。

## 3. 范围与非目标

### 3.1 范围（In Scope）
- 将 `scripts/deploy.sh` 收敛为：构建 Hub + 构建各 Service 产物到其 run 目录 + 启动 `./kagent`。
- Hub 新增（或完善）Service 生命周期管理子系统：
  - 读取 Hub 配置（可拉起哪些 service）。
  - 读取每个 service 的 `run/manifest.json`（默认启动参数、策略、工具元信息等）。
  - spawn/stop/restart，等待 register/heartbeat 的健康位，超时处理。
  - 支持Tool 测试调用（仅 Hub 发起，`ctx.hub_only==true`），但不自动进行。
- 规范化：`services/<svc>/run/.service_secret`。
- 启动快照写 DB hub-snapshot 表。

### 3.2 非目标（Out of Scope）
- 不要求将 `/healthz` 引入默认启动门槛。
- 不设计额外“探测协议”或全新监控体系（仅复用 schema 测试调用 + register/heartbeat 健康位）。
- 不将运行态 registry/tool 列表等写入数据库作为权威状态。

## 4. 配置与数据结构设计

### 4.1 Hub 配置（建议：`config/services.json` 或扩展现有 `config/config.json`）
最小化配置原则：Hub 只描述“要管理哪些 service（目录）”，其余从 service manifest + register 取得。

建议结构（示例）：
```json
{
  "service":{
    "global":{
      "max_timeout_ms": 5000,
      "max_restart_times": 10,
      "grace_period_ms": 1000
    },
    "lifecycle_default": {
      "register_timeout_ms":1000,
      "restart_policy":"never",
      "restart_backoff_ms":300
    },
    "services": [
      { "service_id": "ai-doubao", "dir": "services/ai-doubao" },
      { "service_id": "chat-server", "dir": "services/chat-server" },
      { "service_id": "file", "dir": "services/file" },
      { "service_id": "database", "dir": "services/database" },
      { "service_id": "surface-manager", "dir": "services/surface-manager" }
    ]
  }
}
```

约束：
- `dir` 必须是相对 appRoot 的路径。
- Hub 通过 `dir` 推导：
  - `exec = <dir>/run/<service_id>-latest`（Windows 上需处理 `.exe`）
  - `manifest = <dir>/run/manifest.json`
  - `secret = <dir>/run/.service_secret`

### 4.2 Service manifest（运行期绑定版：`services/<svc>/run/manifest.json`）
manifest 用于提供 service 的“默认启动参数”和“生命周期策略”。建议字段：
```json
{
  "service_id": "ai-doubao",
  "version": "…",
  "entry": {
    "args": ["-addr", "127.0.0.1:18081", "-config", "config/configx.json"],
    "env": { }
  },
  "lifecycle": {
    "register_timeout_ms": 1000,
    "restart_policy": "on-failure",
    "restart_backoff_ms": 300
  },
  "tools": { "schema": "…" }
}
```

约束：
- `entry.args` 中的路径建议用相对路径；Hub 启动子进程时统一设置工作目录为 appRoot，以稳定解析。
- Hub 对 `register_timeout_ms`、重启次数与 backoff 进行 clamp：
  - `register_timeout_ms <= max_timeout_ms`
  - `restart_times <= max_restart_times`

## 5. 生命周期与状态机（Hub 侧）

### 5.1 启动顺序（推荐）
1. Hub 启动自身 HTTP server（`./kagent` 已经在运行，才能接收 register/heartbeat）。
2. Hub 读取自身配置（可管理的 services 列表）。
3. 对每个 service：
   - 读取 `<dir>/run/manifest.json`。
   - 校验 `<dir>/run/.service_secret` 存在（缺失则记录错误并按策略处理）。
   - spawn 子进程（工作目录设为 appRoot）。
   - 等待该 service 在 `register_timeout_ms` 内完成 register，且 `healthy==true`：
     - 成功：进入 Ready，可参与路由与工具调用。
     - 失败/超时：进入 Failed，按 restart_policy 重启（受 Hub 顶格限制）。

### 5.2 关闭与退出
- Hub 收到退出信号时：
  - 向所有已注册 service 发送 shutdown（仍以 register/heartbeat 为事实源）。
  - 等待 grace period（Hub config），超时后强制终止进程。

### 5.3 健康位语义
- register：首次上报 `healthy`（true/false）与基础信息（version、endpoint、tools 摘要等）。
- heartbeat：周期性刷新 `healthy` 与可选 metrics。
- Hub 路由层仅选择 `healthy==true` 且状态 Ready 的实例。

## 6. Tool 健康探测（Hub-only）

### 6.1 约定
- 仅 Hub 主动发起的“特定 tool 测试”才设置 `ctx.hub_only==true`。
- 所有代理转发（用户/页面/surface 引发的调用）均保持 `ctx.hub_only==false`。

### 6.2 `args.healthz` 的处理建议
- 若保留 `args.healthz=true`：
  - Service 端只在 `ctx.hub_only==true` 时识别该字段并走探测逻辑。
  - `ctx.hub_only==false` 时忽略 `args.healthz`（避免被伪造影响业务调用）。

## 7. 构建与部署改造（deploy.sh 收敛）

### 7.1 构建产物
- Hub：构建到项目根目录 `./kagent`。
- Service：构建到各自目录：
  - `services/<svc>/run/<svc>-latest`（固定名）。
  - 同步/生成 `services/<svc>/run/manifest.json`（与二进制版本一致,来自`services/<svc>/manifest.json`）。
  - 同步/生成 `services/<svc>/run/.service_secret`（与二进制版本一致,来自`services/<svc>/.service_secret`）。

### 7.2 deploy.sh 行为
- 只做：
  - build（Hub + services）。
  - 启动 `./kagent`（可选跟随日志）。
- 不再做：
  - service 的启动/停止/健康检查（迁移到 Hub）。

## 8. 启动快照（可选）
- 在 Hub 启动流程完成（所有 service 达到“Ready/Failed”稳定态）后：
  - 写入一条启动快照到 DB（现有 sqlite 可复用）。
  - 快照包含：启动时间、Hub 版本、配置摘要、每个 service 的 spawn 结果、register 完整信息（可 JSON 存储）。
- 运行时 registry/tool 列表不落盘，仅日志记录异常。

## 9. 验收标准（DoD）
- `scripts/deploy.sh` 执行后：
  - 项目根目录存在 `./kagent` 并成功启动 Hub。
  - 各 service 二进制存在于 `services/<svc>/run/<svc>-latest`，且路径均为相对路径可解析。
  - Hub 能按配置拉起全部 service，并基于 register/heartbeat 的 `healthy` 完成 Ready 判定。
  - Hub 不在初次拉起时主动调用 `/healthz`（仅保留手工排障接口）。
  - Hub-only tool 测试调用能将 `ctx.hub_only==true` 传入 service，service 仅在该条件下识别 `args.healthz`。
- hub启动service完成后，启动快照成功写入 DB 一条记录。

## 10. 里程碑拆解（建议）
1. **产物布局改造**：Hub root `kagent`；service run 目录固定产物与 manifest（不改生命周期代码）。
2. **Hub 生命周期 MVP**：读取配置 + 读取 manifest + spawn + 等 register/healthy + 超时重启（不做 tool 测试）。
3. **Hub-only tool 探测**：完善 `ctx.hub_only` 注入与 service 端识别规则。
4. **退出/重启策略完善**：优雅 shutdown、强杀兜底、日志与状态可观测。
5. **启动快照（可选）**：写入 DB 一条汇总记录。

