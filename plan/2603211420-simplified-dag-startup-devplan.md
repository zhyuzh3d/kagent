# Hub DAG 启动重构开发计划

> 时间：2026-03-21 14:20 CST  
> 主题：将 Hub 当前“启动进程 + register + 依赖后置 init”的复杂启动机制，重构为“manifest 显式依赖 + DAG 分批启动 + registered 即 ready”的精简模型。  
> 范围：`hub/internal/{supervisor,app,gateway}/`、`hub/config/services.json`、`pkg/toolproto/`、`services/*/manifest.json`、各 service 启动入口与旧进程自清理机制、相关说明文档。  
> 依据：`hub/internal/supervisor/lifecycle.go`、`hub/internal/supervisor/handler.go`、`hub/internal/app/hub_platform.go`、`hub/config/services.json`、`services/*/cmd/*/main.go`、本轮启动日志与仓库实时核验。

## 1. 结论

本轮不再修补现有两阶段启动机制，而是直接切到新的单阶段启动模型：

1. service 依赖只认 `manifest` 显式声明。
2. Hub 只负责：读依赖、检测环、分批启动、等待 register。
3. service 自己负责：读取配置、关闭旧实例、完成内部初始化、开启监听、确认工具可用后再 register。
4. Hub 侧 `registered` 直接等于 `ready`，删除 `service.lifecycle.init`、`initializing` 等旧机制。
5. 不兼容旧启动模型，不保留双轨逻辑。

## 2. 目标模型

### 2.1 单一启动语义

启动阶段只保留以下状态：

1. `starting`
2. `registered`
3. `failed`
4. `skipped`

其中：

1. `registered` 直接视为正式可路由实例。
2. Hub 不再维护“registered 但未 ready”的中间态。
3. service 若尚未可服务，不得发起 register。

### 2.2 依赖唯一事实源

依赖关系统一下沉到各 service 自己的 `manifest.json`，例如：

1. `depends_on`

Hub 启动器只读取：

1. `service_id`
2. `entry`
3. `lifecycle.register_timeout_ms`
4. `depends_on`

`hub/config/services.json` 不再承担依赖拓扑事实源，只保留受管服务清单与必要全局启动参数。

### 2.3 旧实例关停边界

旧实例关停属于 service 自治能力，不属于 Hub 启动编排职责。

硬约束：

1. 每个 service 必须在自己启动早期完成同 service 旧实例探测与稳妥关停。
2. Hub 不再负责 `kill_old`、旧 PID 清理、旧实例预停。
3. service 必须在旧实例处理完成后，再进入监听与 register。

## 3. Hub 端新算法

### 3.1 构图

Hub 在启动前执行：

1. 读取所有受管 service manifest。
2. 以 `service_id -> depends_on[]` 构建有向图。
3. 校验依赖目标必须存在于受管 service 集合。

### 3.2 环检测

Hub 对依赖图执行环检测：

1. 若存在环，输出明确的环状路径。
2. 处于环内的 service 标记为 `skipped`。
3. 环外且无环的 service 继续参与后续拓扑排序。

### 3.3 分批启动

Hub 对无环子图做拓扑分层：

1. 第 1 批：无依赖 service
2. 后续批次：其依赖均已 `registered` 的 service

启动策略：

1. 批次之间串行推进。
2. 同批 service 可并行启动。
3. 每个 service 只等待 register 成功，不再反调 init。

### 3.4 下游失败传播

若某 service 启动失败：

1. 其直接和间接依赖方全部标记 `skipped`。
2. Hub 不再继续启动这些下游 service。
3. 输出清晰的因果链，例如 `surface_manager skipped because sql_db failed`。

## 4. 必须删除的旧机制

### 4.1 Hub 侧删除项

必须删除：

1. `service.lifecycle.init` 调用链
2. `initializing` 阶段与相关状态字段
3. `depends_on` 驱动的后置 init 编排
4. Hub 侧旧实例 PID 记录清理与 `kill_old` 语义
5. “register 成功但仍需 init 才 ready”的判定分支

重点文件：

1. `hub/internal/supervisor/lifecycle.go`
2. `hub/internal/supervisor/handler.go`
3. `hub/internal/app/hub_platform.go`
4. `hub/internal/gateway/admin_*`
5. `hub/config/services.json`

### 4.2 Service 侧删除项

必须删除：

1. 仅为 Hub 后置 init 服务的 `service.lifecycle.init`
2. 依赖 Hub 反向调用 init 才能完成的内部启动逻辑
3. 对“Hub 会先帮我停旧实例”的隐含假设

## 5. Service 侧改造要求

### 5.1 新启动顺序

每个 service 的标准启动顺序统一为：

1. 定位项目根目录
2. 加载 `config/`
3. 关闭旧实例
4. 完成内部初始化
5. 启动监听
6. 验证对外工具已可用
7. 发起 register
8. 启动 heartbeat

### 5.2 register 前提

新增硬规范：

1. service 发起 register 时，`manifest.provides` 中声明的工具必须已达到可调用状态。
2. 若某工具依赖外部 service，则该依赖必须已经体现在 `manifest.depends_on` 中。
3. 禁止在 register 之后再做会影响工具可用性的关键初始化。

### 5.3 旧实例自清理

每个 service 需要统一自带：

1. 旧实例发现机制
2. 旧实例身份校验机制
3. 温和关闭优先、必要时强制退出的兜底机制
4. 启动幂等保证

首批重点 service：

1. `sql_db`
2. `account`
3. `surface_manager`
4. `chat_server`
5. `file_storage`
6. `ai_doubao`
7. `autogui`

## 6. 分阶段实施

### Phase 1：协议与配置收敛

目标：

1. 在各 service `manifest.json` 中补入 `depends_on`
2. 收敛 `hub/config/services.json` 为受管列表
3. 更新说明文档与 service 标准

产出：

1. 新 manifest 依赖规范
2. Hub 读取 manifest 依赖的代码路径设计

### Phase 2：Hub DAG 启动器重写

目标：

1. 用 DAG 启动器替换当前 `StartAll/finalizeStartup` 逻辑
2. 增加环检测、拓扑分层、失败传播
3. 删除旧 init 编排与旧实例统一清理逻辑

产出：

1. 新启动快照结构
2. 新日志语义
3. 新失败输出格式

### Phase 3：Service 自治启动重构

目标：

1. 每个 service 把内部初始化前置到 register 之前
2. 每个 service 落地旧实例自清理
3. 移除 `service.lifecycle.init`

产出：

1. 统一的 service 启动模板
2. 可复用的旧实例自清理辅助逻辑

### Phase 4：治理与管理界面对齐

目标：

1. 管理页与 Hub 状态接口去掉旧 `initializing` / `ready-after-init` 语义
2. 展示 `starting / registered / failed / skipped`
3. 清理与旧启动模型绑定的冗余字段和文案

## 7. 验收标准

### 7.1 算法验收

1. Hub 能从 manifest 依赖图中准确检测环。
2. 环中 service 会被明确标记并跳过，不影响无环子图启动。
3. 无环图会被稳定分批启动，且批次顺序正确。

### 7.2 启动语义验收

1. Hub 端 `registered` 直接等于可路由。
2. Hub 不再调用 `service.lifecycle.init`。
3. Hub 不再负责 service 启动期旧实例关停。

### 7.3 稳健性验收

1. 任一 service 启动失败时，下游依赖方会被清晰跳过。
2. service 重启不会因为旧实例残留而导致端口冲突或双实例竞争。
3. `go test ./...` 通过。
4. `./scripts/deploy.sh` 全量启动时，日志中不再出现旧两阶段语义的误导信息。

## 8. 风险与约束

1. 该方案不兼容旧 `service.lifecycle.init` 模型，必须整体切换，不能半新半旧。
2. 若某 service 仍存在未声明的隐式依赖，DAG 算法无法替它兜底，必须在重构中补全 manifest。
3. “旧实例自清理”如果各 service 落地质量不一致，会成为新的稳定性短板，因此需要统一模板与专项验证。

## 9. 本轮建议实施顺序

1. 先改 manifest 依赖模型与文档规则。
2. 再重写 Hub DAG 启动器。
3. 然后逐个 service 前移内部初始化并加旧实例自清理。
4. 最后删除旧两阶段启动残留与管理页冗余逻辑。

---

**计划结论**：采用“manifest 显式依赖 + DAG 分批启动 + register 即 ready + service 自治旧实例关停”的新模型，直接替换当前两阶段启动机制，不保留兼容层。这是当前最精简、最可解释、也最稳健的重构方向。  
**信息来源**：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/lifecycle.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/handler.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/hub_platform.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/services.json`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/*/manifest.json`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/*/cmd/*/main.go`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`、本轮关于启动流程与循环依赖的对话结论。
