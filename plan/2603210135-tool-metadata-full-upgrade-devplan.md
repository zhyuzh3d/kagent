# Tool 元数据全量升级开发计划

- 文档类型：开发计划（DevPlan）
- 创建时间：2026-03-21 01:35:52 CST
- 范围：`pkg/toolproto/`、`hub/internal/{app,supervisor,routing,gateway}/`、`services/{account,ai_doubao,chat_server,file_storage,sql_db,surface_manager}/`、`webui/page/service/`
- 目标文件：
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/toolproto/supervisor.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/hub_platform.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/handler.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/routing/schema.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/hub_manifest.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/admin_handler.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/admin_service_tools.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/account/internal/app/app.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/internal/app/{ai_service_protocol.go,service_manifest.go}`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/cmd/ai_doubao/main.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/internal/app/service_manifest.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/file_storage/internal/app/{hub_platform.go,ai_service_protocol.go}`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/file_storage/cmd/file_storage/main.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/internal/app/{hub_platform.go,ai_service_protocol.go,hub_builtins.go}`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/cmd/sql_db/main.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/surface_manager/internal/app/{hub_builtins.go,ai_service_protocol.go}`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/surface_manager/cmd/surface_manager/main.go`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/service/{admin.html,admin.js,components/render.js,components/logic.js}`
- 目标约束：
  - 全部 service 与 Hub 自身 tool 必须收敛到统一完整的 tool 声明模型。
  - Hub 对外必须提供完整且分层的 tool 视图，不再让页面直接拼接零散 `manifest.provides[]`。
  - `hub_auth_required` 不再保留为独立字段，其语义并入 `hub_only`。
  - `bound_service_id` 保留，明确表示 Hub 当前把该 `tool_id` 路由给哪个 `service_id`。

---

## 0. 计划结论

本轮工作不能再按“哪个 service 缺一点补一点”的方式推进，而要一次性完成四件事：

1. 统一 tool 声明协议，避免每个 service 各自维护一套缩水结构。
2. 修正 Hub 的接入链路，避免 service 已声明的字段在 register 过程中被 Hub 丢掉。
3. 新增 Hub 的完整 tool 视图，把 `spec / observed / governance` 三层清晰分开。
4. 改造 admin 页面，只消费 Hub 的完整 tool 视图，不再把 runtime manifest、service session manifest 和治理结果混在一起展示。

如果只改某个单点，例如只补 `service/account` 或只改 `admin.html`，问题会继续回流，因为当前真正的根因是“协议层、Hub 入库层、Hub 视图层、页面消费层”四层同时不统一。

## 1. 已核验的当前事实

以下事实已由当前仓库代码直接核验：

1. `doc/_service_standard.md` 已定义 tool 元数据最低字段，但之前仍保留了 `hub_auth_required`，且尚未给出 Hub 对外完整 tool 对象的分层结构。
2. `pkg/toolproto/supervisor.go` 中的 `toolproto.ServiceTool` 已经比多数 service 和 Hub 内部结构更丰富，包含 `protocol`、`version`、`hub_only`、`has_effects`、`risk_lv`、`streaming_mode`、`ws_path`、`scope_support` 等字段。
3. `hub/internal/app/hub_platform.go` 仍使用一套更窄的 `ServiceToolDescriptor`，缺少 `protocol`、`hub_only`、`has_effects`、`risk_lv`、`streaming_mode` 等关键字段。
4. `hub/internal/supervisor/handler.go` 的 `decodeInternalRegister()` 在接收 `toolproto.ServiceTool` 后，只回填了少量字段，导致字段在进入 Hub 时丢失。
5. `hub/internal/gateway/hub_manifest.go` 中 Hub 自身注册的 `admin / governance / system` 工具，目前基本只有 `tool_id`、`description`、`protocol`、`allowed_caller_types`，远不完整。
6. `services/account/internal/app/app.go` 的 `supervisorTools()` 相对最完整，已有 `description`、`input_schema`、`output_schema`、`timeout`、`allowed_caller_types`，但仍缺少 `hub_only`、`has_effects`、`risk_lv`、`scope_support` 等统一字段。
7. `services/chat_server/internal/app/service_manifest.go` 当前直接用 `toolproto.ServiceTool`，但实际填写字段仍很薄，除 `app.chat.stream` 外多数工具缺 schema、timeout、effects、risk 等信息。
8. `services/ai_doubao/internal/app/service_manifest.go` 能表达 `streaming_mode` 与 `ws_path`，但其上游描述结构和最终注册链路仍不完整。
9. `services/file_storage`、`services/sql_db`、`services/surface_manager` 仍维护各自私有的 `ServiceToolDescriptor` / `AIServiceToolDescriptor`，字段集合彼此不一致。
10. `services/sql_db/internal/app/ai_service_protocol.go` 与 `services/surface_manager/internal/app/ai_service_protocol.go` 甚至连 `allowed_caller_types`、`ws_path` 都未完整表达。
11. `hub/internal/routing/schema.go` 的 `ToolRegistryItem` 只展示了部分路由元数据，还不是完整的 Hub tool 视图。
12. `webui/page/service/admin.js` 当前直接把 `state.managed[].manifest.provides[]` 拉平为工具全集，只展示 `description`、`input_schema`、`output_schema`、`streaming`，无法表达治理字段，也没有区分声明事实与 Hub 观察/治理结果。
13. `hub.admin.services.list` 当前返回的是 managed service 列表和 service session 粗视图，但没有正式的“完整 tool catalog”接口供 admin 页面消费。

## 2. 本轮目标

### 2.1 统一目标

本轮改造完成后，应形成两层统一模型：

1. `DeclaredToolSpec`
   - service 对外声明给 Hub 的稳定事实
   - 由 `manifest.provides[]` 与 `register.tools[]` 共用
2. `HubToolView`
   - Hub 对外提供的完整视图
   - 至少区分 `spec`、`observed`、`governance`

### 2.2 用户可见目标

1. service admin 页面能看到完整的 tool 信息，而不是只有零散描述。
2. 每个 tool 至少能稳定展示：
   - 描述
   - 输入结构
   - 输出结构
   - 协议形态
   - 是否流式
   - `ws_path`
   - 默认超时
   - 允许 caller 类型
   - required capabilities
   - scope 支持
   - 是否要求 Hub 可信入口
   - 是否有副作用
   - 风险等级
   - 当前绑定 service
   - 当前绑定原因
   - 是否手工绑定
   - 当前健康实例数
   - 最近可见时间
   - 成功率 / 调用次数 / 可靠性

### 2.3 非目标

1. 本轮不引入新的复杂权限系统。
2. 本轮不让 Hub 替 service 做业务授权。
3. 本轮不追求页面一次性展示所有内部调试细节。
4. 本轮不把 runtime `manifest.json` 生命周期配置和 tool 元数据继续混在一个对象里。

## 3. 目标对象设计

### 3.1 声明层 `DeclaredToolSpec`

所有 service 与 Hub 自身工具都必须能表达至少以下字段：

1. `category`
2. `type`
3. `tool`
4. `tool_id`
5. `version`
6. `description`
7. `input_schema`
8. `output_schema`
9. `protocol`
10. `streaming`
11. `streaming_mode`
12. `ws_path`
13. `allowed_caller_types`
14. `capabilities_required`
15. `scope_support`
16. `hub_only`
17. `has_effects`
18. `side_effect`
19. `risk_lv`
20. `timeout_ms_default`

说明：

1. `hub_only` 保留，并吸收原 `hub_auth_required` 的语义。
2. 不再引入 `replacement_tool_id`。
3. service 只声明自己有资格声明的事实，不声明最终治理结论。

### 3.2 Hub 视图层 `HubToolView`

Hub 对外统一提供的完整对象至少包含：

1. 顶层：
   - `tool_id`
   - `service_id`
2. `spec`
   - 声明事实
3. `observed`
   - `registered`
   - `healthy_instance_count`
   - `last_seen_at_ms`
   - `transport`
   - `endpoint`
   - `source`
4. `governance`
   - `enabled`
   - `bound_service_id`
   - `binding_reason`
   - `manual_override`
   - `reliability`
   - `success_rate`
   - `call_count`
   - `conflict_reason`

## 4. 代码改造总任务

### 4.1 共享协议层

目标：让 `pkg/toolproto/` 成为唯一权威协议来源。

任务：

1. 审核并收敛 `pkg/toolproto.ServiceTool` 字段集，使其与修订后的 `_service_standard` 一致。
2. 明确哪些字段属于声明事实，哪些字段不允许出现在 service register 负载里。
3. 如有必要，为 Hub 对外视图新增单独结构，例如 `ToolSpec`、`ToolObserved`、`ToolGovernance`、`ToolView`，避免继续复用 register 结构直接给前端。
4. 统一 `streaming` / `streaming_mode` / `protocol` 的语义规范，避免某些 service 用 `"stream"` 字符串、某些用 `bool`。

验收：

1. 仓库内不再出现多套不一致的 tool 基础协议定义。
2. 新字段不再只存在于文档或某个 service 局部实现里。

### 4.2 Hub 接入层

目标：service 已声明的字段，进入 Hub 后不能丢。

任务：

1. 改造 `hub/internal/supervisor/handler.go` 的 `decodeInternalRegister()`，完整接收并透传共享协议字段。
2. 改造 `hub/internal/app/hub_platform.go` 的 `ServiceToolDescriptor` / `ServiceManifest`，与共享协议对齐，避免再次缩水。
3. 收敛 `normalizeToolDescriptor()`，完整处理新字段归一化。
4. 明确 `manifest` 静态基线与 `register` 运行事实的合并规则。

验收：

1. 任一 service 通过 register 上报的声明字段，在 Hub session 中都可见。
2. Hub 不再因为中间转换丢掉 `protocol`、`hub_only`、`risk_lv`、`has_effects` 等字段。

### 4.3 Hub 治理视图层

目标：Hub 提供正式完整的 tool catalog，而不是多个半成品接口。

任务：

1. 扩展 `hub/internal/routing/schema.go` 或新增独立 builder，生成正式 `HubToolView[]`。
2. 将 `observed` 与 `governance` 字段纳入计算：
   - `healthy_instance_count`
   - `last_seen_at_ms`
   - `bound_service_id`
   - `binding_reason`
   - `manual_override`
   - `reliability`
   - `success_rate`
   - `call_count`
   - `conflict_reason`
3. 让 `bound_service_id` 明确等于当前 `tool_id` 的生效承接 service。
4. 避免把 Hub 内部调试结构直接暴露给页面。

验收：

1. Hub 能返回完整 tool 目录视图。
2. 同一个 tool 的声明事实、观察事实、治理事实不会再混淆。

### 4.4 Hub 自身工具

目标：Hub 自己注册的工具不能再是“最不完整的一批”。

任务：

1. 全量补齐 `hub.internal.governance/admin/system` 工具元数据。
2. 覆盖以下工具组：
   - `hub.admin.*`
   - `hub.governance.service.*`
   - `hub.system.*`
3. 为每个 Hub 工具补充：
   - 输入 schema
   - 输出 schema
   - `hub_only`
   - `has_effects`
   - `risk_lv`
   - 默认超时
   - caller 限制
4. 对 `hub.system.shutdown`、`hub.admin.service.files.write`、`hub.admin.routes.bind` 等高风险工具标注正确风险等级和副作用。

验收：

1. Hub 自身工具在 admin 页面上的元数据完整度不低于任何一个 service。
2. 不再出现“Hub 工具自己也信息不全”的情况。

## 5. 各 service 改造任务

### 5.1 account

当前特点：相对最完整，但仍是单点手写，不是统一标准样板。

任务：

1. 把 `supervisorTools()` 输出补齐为统一字段集合。
2. 为登录态相关 tool 明确 `has_effects` 与 `side_effect`。
3. 为生命周期工具补上统一字段，如 `protocol`、`hub_only`、`risk_lv`、schema。
4. 作为“声明最完整的原子工具 service”样板，验证共享协议落地效果。

### 5.2 ai_doubao

当前特点：已有 `streaming_mode` / `ws_path` 基础，但上游与下游还没收拢完整。

任务：

1. 统一 `AIServiceToolDescriptor` 字段集。
2. 补齐 `hub_only`、`has_effects`、`risk_lv`、timeout、schema。
3. 对 `ai.llm.stream`、`ai.speech.asr`、`ai.speech.tts` 明确协议、流式模式、副作用与风险。
4. 确保 lifecycle tools 与业务 tools 一起走统一对象。

### 5.3 chat_server

当前特点：直接复用 `toolproto.ServiceTool`，但大部分工具信息仍非常薄。

任务：

1. 全量补齐 `app.chat.*` 工具的输入/输出 schema、默认超时、risk、effects。
2. 对 `app.chat.stream` 明确 `protocol`、`streaming_mode`、`ws_path`、副作用、风险。
3. 为 lifecycle tools 对齐统一字段。
4. 避免 chat_server 成为“字段定义正确，但实际填充仍为空”的半完成状态。

### 5.4 file_storage

当前特点：已开始处理 `AllowedCallerTypes` 与 `ScopeSupport`，但协议结构仍私有化。

任务：

1. 去掉本地私有缩水结构，或让其完全对齐共享协议。
2. 补齐 `hub_only`、`has_effects`、`risk_lv`、`protocol`、`streaming_mode`。
3. 为写入/删除类工具明确副作用与风险级别。
4. 为 `storage.blob.*` 与 `storage.file.*` 补全 schema 和 scope 语义。

### 5.5 sql_db

当前特点：部分 schema 已有，但协议描述结构仍更窄，字段透传也不完整。

任务：

1. 扩展 `AIServiceToolDescriptor`，纳入 caller、ws_path、hub_only、has_effects、risk 等字段。
2. 为 `storage.database.*`、`storage.share.*` 补齐副作用、风险和 scope。
3. 明确查询类、写入类、schema 类工具的差异化风险等级。
4. 保证其 register 输出与 Hub 展示一致。

### 5.6 surface_manager

当前特点：本地自定义结构仍旧，工具数多但说明薄。

任务：

1. 统一 descriptor 结构。
2. 为 `ui.surface.*` 工具补 schema、scope、effects、risk。
3. 对 session/capability 颁发类工具标注更高风险等级。
4. 对文件和包写入类工具标注副作用。

## 6. Admin API 与页面改造

### 6.1 Hub Admin API

目标：提供正式 tool catalog 接口。

任务：

1. 为 admin 增加正式工具目录查询能力，建议新增单独接口或 tool，例如：
   - `hub.admin.tools.list`
   - 或扩展 `hub.admin.service.get`
2. 为 admin 增加正式 service catalog 能力，返回结果必须包含 `hub` 自身，而不只是受管子服务。
3. service catalog 中的 `hub` 必须带显式标记，例如 `builtin=true`、`service_kind=hub` 或等价字段，供页面特殊处理。
4. 服务详情接口返回的 tool 数据应来自 Hub 完整视图，而不是简单返回 `reg.Manifest.Provides`。
5. 保留 service 级原始声明查看能力，但与治理视图分开。

### 6.2 Admin 页面

目标：页面直接消费 `HubToolView[]`。

任务：

1. `webui/page/service/admin.js` 停止直接拉平 `state.managed[].manifest.provides[]`。
2. service 列表必须包含 `hub`，且 `hub` 要按内建虚拟 service 特殊渲染。
3. 对 `hub` 的界面特殊处理至少包括：
   - 不复用普通 service 的启动、停止、重启、构建按钮
   - 对 `hub.system.shutdown` 单独标红并单独确认
   - 不默认复用普通 service 的 runtime manifest 编辑与工作区文件编辑入口
   - 以“内建/当前进程”语义展示 endpoint、instance、transport 和状态
4. 工具全集必须包含 `hub` 自身工具，且展示方式与其他 service 的工具保持同一 catalog 视图。
5. 表格增加对完整字段的展示：
   - 描述
   - 协议
   - caller
   - capability
   - scope
   - `hub_only`
   - `has_effects`
   - `risk_lv`
   - `bound_service_id`
   - `binding_reason`
   - `manual_override`
   - `healthy_instance_count`
   - `last_seen_at_ms`
6. Drawer 中区分：
   - 原始声明
   - 观察态
   - 治理态
7. 明确 runtime manifest 是生命周期配置文件，不再把它当 tool 清单来源。

验收：

1. 页面不再依赖“managed service + 内嵌 manifest.provides”的拼装逻辑。
2. 页面中的 service 列表能稳定显示 `hub` 和全部普通 service。
3. 页面能完整显示 Hub 自身工具和所有 service 工具。

## 7. 迁移策略

### 7.1 分阶段迁移

建议顺序：

1. 先统一共享协议与 Hub 入库结构。
2. 再补 Hub tool view。
3. 再补 Hub 自身工具元数据。
4. 再逐个 service 收口。
5. 最后改 admin API 与页面。

### 7.2 兼容策略

1. 旧 service 若暂时未补全字段，Hub 应提供缺省值，但必须可识别为“不完整声明”。
2. 迁移窗口内允许旧结构转新结构，但不允许新增新的私有缩水结构。
3. 页面在过渡期可兼容旧返回，但最终必须只认正式 `HubToolView`。

## 8. 验证计划

### 8.1 协议验证

1. 每个 service register 后，Hub session 中的 tool 字段不丢失。
2. Hub 自身工具进入同一视图。
3. 同一个 `tool_id` 的多 provider 情况能正确生成 `bound_service_id` 与 `binding_reason`。

### 8.2 页面验证

1. service admin 工具全集能看到全部 service 和 Hub 自身工具。
2. 每个工具至少能展开看到 schema、caller、protocol、effects、risk、governance 状态。
3. 流式工具能正确展示 `streaming` 与 `ws_path`。

### 8.3 回归验证

1. tool 路由不受展示层改造破坏。
2. lifecycle tools 仍可被 Hub 调用。
3. register / heartbeat 链路仍稳定。

## 9. 风险与注意事项

1. 不要把 Hub 视图结构直接塞回 service register 协议，否则会混淆声明事实和治理结果。
2. 不要继续在 Hub 内部保留第二套缩水 `ServiceToolDescriptor`。
3. 不要遗漏 Hub 自身工具，否则 admin 页面仍会出现“内部工具最不完整”的反差。
4. 不要把 runtime lifecycle `manifest.json` 误当成 tool 元数据事实源。
5. 不要把 `bound_service_id` 误解成“替代工具名”；它表示当前路由选中的 service。

## 10. 验收标准

当本计划后续落地完成时，必须同时满足：

1. `doc/_service_standard.md` 与真实代码结构一致。
2. Hub 和全部 service 只使用一套统一的 tool 声明标准。
3. Hub 注册链路不再丢字段。
4. Hub 能对外提供正式完整的 `HubToolView`。
5. Hub 自身工具与各 service 工具都具备完整元数据。
6. `webui/page/service/admin.html` 对应页面能完整展示并消费这些信息。
7. `bound_service_id`、`binding_reason`、`manual_override`、`healthy_instance_count`、`last_seen_at_ms` 等治理/观察字段都能正确展示。

---

**计划依据**：

- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/toolproto/supervisor.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/hub_platform.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/handler.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/routing/schema.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/hub_manifest.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/admin_handler.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/account/internal/app/app.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/internal/app/service_manifest.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/internal/app/service_manifest.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/file_storage/cmd/file_storage/main.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/sql_db/cmd/sql_db/main.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/surface_manager/cmd/surface_manager/main.go`
- `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/service/admin.js`

**计划更新时间**：2026-03-21 01:45:55 CST

**本轮补充范围**：在既有全量升级计划基础上，补充 service 列表必须纳入 `hub`、工具全集必须纳入 `hub` 自身工具，以及管理界面对 `hub` 的特殊处理约束。
