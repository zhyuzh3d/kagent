# Service Standard 修订计划

- 文档类型：开发计划（DevPlan）
- 创建时间：2026-03-19 19:49 CST
- 范围：仅制定 `doc/_service_standard.md` 的修订计划；本计划不直接修改标准文档，不涉及代码实现。
- 目标文件：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
- 依据（可追溯）：
  - 当前标准草案：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
  - 入口说明：`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction.md`
  - 本轮会话中围绕 Hub / Service 边界、tool 元数据、Hub 可信注入、治理职责与快照机制形成的共识

---

## 1. 计划目标

本次修订不是推翻 `Hub + Service + tool` 的总体方向，而是把标准文档改写得更贴近当前真实目标：

1. 明确 `Hub` 是标准治理入口和管理者，不是强制控制器。
2. 明确 `Service` 只要兼容 Hub 的 `tool + lifecycle` 协议，就可以被 Hub 接纳和治理。
3. 明确 `allowed_caller_types`、`capabilities_required` 仅是 Hub 侧分发门槛，不等于 service 内部授权逻辑。
4. 补齐 tool 元数据，使其能够表达协议形态、是否依赖 Hub 信任、副作用与风险等级。
5. 把 Hub 的职责收敛为接纳、标记、展示、默认路由、运行统计、快照治理。
6. 让标准文档承认 service 可自治、可用户定制、可 AI 撰写这一现实前提，避免写出无法执行的“强制”措辞。

## 2. 修订原则

本次修订应遵循以下原则：

1. 不把“被 Hub 管理”写成“系统唯一合法运行方式”。
2. 不把 Hub 的 caller/capability 筛选写成 service 内部业务授权。
3. 不把“推荐规范”误写成“Hub 可强制验证的事实”。
4. 不增加与当前目标不匹配的重型机制，例如请求级签名、复杂配额系统、追踪 AI 决策原因。
5. 所有新增字段和概念都必须服务于两件事：
   - Hub 更好地接纳和治理 service
   - AI 更好地理解和使用 tool

## 3. 已达成的关键共识

后续修订必须以以下共识为准：

1. `Hub` 是管理者，不是管制者。
2. 任何程序都可以自运行，但只有符合规范的程序才会被 Hub 接纳治理。
3. `Service` 被 Hub 接纳的最低前提，是兼容 `tool` 协议与生命周期治理接口。
4. 用户身份证鉴权属于 service 内部职责，不属于 Hub 的强制职责。
5. `Hub` 注入的 caller/capability 结果是可信上下文，但 service 是否消费，由 service 自己决定。
6. `allowed_caller_types` 和 `capabilities_required` 只解决 Hub 路由分发门槛。
7. `tool` 应补充协议、Hub 依赖性、副作用、风险等级等元数据。
8. `Hub` 应维护 tool 维度的 `reliability`、`success_rate`、`call_count`。
9. `Hub` 退出前应将自身与全部 service/tool 运行快照写入数据库，并绑定当前用户或 `anonymous`。
10. `Hub` 可信注入的关键前提是 `.service_secret` 机制，而不是每次请求签名。

## 4. 文档结构调整计划

当前 `doc/_service_standard.md` 的问题之一，是 Hub 的接纳规则、治理职责、tool 语义和安全边界混在一起。修订时建议重组为以下结构：

1. 文档目标与适用范围
   - 说明本标准是“Hub 可接纳 service 的协议标准”，不是系统对所有进程的强制管控标准。
2. 总体定位
   - 说明 `Hub` 是标准治理入口
   - 说明 `Service` 可以自治运行，但不一定被 Hub 纳管
3. 三层边界
   - 接纳规范：Hub 认不认这个 service
   - 治理规范：Hub 会对它做什么
   - 自治规范：service 内部自己决定什么
4. Tool 协议与元数据
   - 请求/响应结构
   - 新增字段
   - Hub 路由门槛字段与 service 自治字段的边界
5. Lifecycle 与 register/session
   - 保留生命周期与注册语义
   - 收紧 register 中“只上报事实，不申领治理权”的表达
6. Hub 治理输出
   - 路由
   - 标记
   - 展示
   - 统计
   - 快照
7. 推荐安全模式
   - `.service_secret`
   - Hub 来源信任
   - 非 Hub 请求拒绝的推荐实现
8. 非目标与明确不保证事项
   - 不保证 service 内部一定消费 Hub 注入结果
   - 不保证 Hub 强制控制所有 service

## 5. 重点修订项

### 5.1 重写 Hub 定位

将当前文档中容易引发误解的“唯一治理边界”“统一入口”等表述，修订为更准确的说法：

1. `Hub` 是标准治理入口、推荐入口、受治理入口。
2. `Hub` 不是系统对所有进程的唯一控制点。
3. `Hub` 接纳的是符合规范并愿意接受治理的 service。
4. 未被 Hub 接纳的程序，可以运行，但不属于 Hub 的 tool 平面。

### 5.2 重写“必须通过 Hub”的表述

统一调整为：

1. `Service` 必须兼容 Hub 的 `tool + lifecycle` 协议，Hub 才会接纳它。
2. 这不等于 service 内部必须强制执行 Hub 注入的 caller 或鉴权结果。
3. Hub 纳管的是对外统一接入、生命周期和基础治理，不是 service 内部业务授权。

### 5.3 明确平台安全与业务安全分工

增加明确表述：

1. 平台安全由 Hub 主导：
   - 接纳
   - 路由
   - 前置 caller/capability 筛选
   - 基础日志
   - 状态快照
2. 业务安全由 service 主导：
   - 用户身份鉴权
   - 对象级权限
   - 内部限额
   - 是否信任或忽略 Hub 注入结果

### 5.4 Tool 元数据修订

将现有 tool 声明补齐为更符合当前目标的字段体系。计划新增或明确以下字段：

1. `protocol`
   - 值示例：`http`、`uds`
   - 作用：表达 tool 的通信协议形态
2. `hub_only`
   - 表达该 tool 是否只接受 Hub 作为可信入口
3. `allowed_caller_types`
   - 增补 `all`
   - `all` 表示 tool 不需要 caller 信息
4. `has_effects`
   - 表达是否存在副作用
5. `risk_lv`
   - 默认值为 `0`
   - 由 service 内部设定
6. “是否需要 Hub 鉴权”字段
   - 用于让 Hub 对无需鉴权的 tool 减少额外计算

修订时要明确：

1. `allowed_caller_types` 与“是否需要 Hub 鉴权”不是同一件事。
2. `allowed_caller_types` 只影响 Hub 是否转发。
3. “是否需要 Hub 鉴权”表达 tool 是否依赖 Hub 可信注入。

### 5.5 收紧 register/session 的文档表达

保留 `manifest / register / heartbeat / routing overlay / service session` 的总体思路，但修订为：

1. register 主要上报实例事实。
2. Hub 最终维护治理视角的字段。
3. service 不申领最终治理权。
4. `reliability`、`success_rate`、`call_count` 这类治理统计由 Hub 维护。
5. session/overlay 章节应明确区分：
   - service 自报事实
   - Hub 观察与治理结果

### 5.6 新增 Hub 快照治理要求

增加专门小节，明确：

1. Hub 应维护自身与全部 service/tool 的运行态汇总。
2. Hub 在退出前应把快照写入数据库。
3. 快照应至少包含：
   - Hub 自身状态
   - 全部 service 基本状态
   - 全部 tool 的 `reliability`
   - 全部 tool 的 `success_rate`
   - 全部 tool 的 `call_count`
4. 快照应与当前用户或 `anonymous` 绑定。

### 5.7 安全部分改为“推荐可信模式”，避免伪强制

当前安全章节应从“强制所有 service 内部实现某种验证”改成：

1. 推荐可信模式：
   - service 通过 `.service_secret` 机制信任 Hub
   - service 可拒绝非 Hub 来源
2. Hub 的责任是：
   - 只接纳符合接口规范的 service
   - 记录 tool 的自声明和运行治理状态
3. Hub 不负责证明 service 内部一定实现了预期鉴权逻辑。
4. 对于声明 `hub_only` 或“需要 Hub 鉴权”的 tool，文档应说明这是 service 的接入承诺或推荐安全姿态，而不是 Hub 对全世界的物理强制。

## 6. 修订步骤

建议按以下顺序修订 `doc/_service_standard.md`：

1. 先重写文档开头的定位和适用范围
   - 先把“Hub 是什么、不是什 么”写准
2. 再重写 Hub / Service 边界章节
   - 去除容易导致强制误解的表述
3. 再重写 tool 协议与元数据章节
   - 引入 `protocol`、`hub_only`、`all`、`has_effects`、`risk_lv`、Hub 鉴权需求字段
4. 再补 Hub 治理输出章节
   - `reliability`、`success_rate`、`call_count`
   - 快照写库要求
5. 最后修订安全章节
   - 收敛为推荐可信模式
   - 明确 Hub 不替 service 内部执法

## 7. 计划产出物

本次计划完成后，下一轮修订应产出：

1. 一版重写后的 `doc/_service_standard.md`
2. 文档中新增或调整的字段定义表
3. 明确的“Hub 接纳规范 / 治理规范 / service 自治规范”三层边界
4. 明确的“推荐可信模式”描述
5. 明确的 Hub 运行统计与退出快照要求

## 8. 风险与注意事项

修订时需避免以下风险：

1. 把推荐安全模式写成 Hub 可强制验证的事实。
2. 把 `allowed_caller_types` 错写成业务权限系统。
3. 把 Hub 的 caller/capability 筛选和 service 内部鉴权混为一谈。
4. 新增字段过多但语义重叠，导致 AI 和开发者更难理解。
5. 把快照、统计、标记写成过重审计系统，偏离“本地项目、基础治理”的目标。

## 9. 验收标准

当后续真正修订 `doc/_service_standard.md` 时，应满足以下验收条件：

1. 文档准确表达 Hub 是管理者而非管制者。
2. 文档准确表达 Hub 接纳的是“兼容协议的 service”，而不是“全系统唯一合法服务”。
3. 文档明确区分平台安全与业务安全。
4. 文档明确区分 Hub 分发门槛与 service 内部授权。
5. tool 元数据包含本轮共识中的新增字段或等价表达。
6. 文档新增 Hub 治理统计与退出快照要求。
7. 文档安全章节不再假装 Hub 可以强制保证 service 内部逻辑。

---

**计划更新时间**：2026-03-19 19:49 CST

**计划结论**：下一步不应直接补丁式微调当前标准草案，而应按“定位澄清 -> 边界重写 -> tool 元数据扩充 -> Hub 统计/快照补充 -> 安全章节收敛”的顺序整体修订 `doc/_service_standard.md`。
