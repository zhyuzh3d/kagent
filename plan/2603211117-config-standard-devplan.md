# Config 标准落地开发计划

## 目标

将以下约束落成项目统一实现，并消除当前 `hub` 与各 `service` 在配置目录、启动加载、空配置处理和用户覆盖路径上的不一致：

1. `hub` 或 `service` 必须包含项目目录下的 `config/` 文件夹。
2. `config/` 内必须包含 `config.json`、`configx.json`、`configx.json.example`。
3. `configx.json` 用于存储用户敏感信息；`configx.json.example` 用于示例其字段与格式。
4. `config.json` 与 `configx.json` 默认允许为空，推荐统一按 `{}` 处理以避免解析失败。
5. `service` 必须在程序启动第一时间加载 `config/` 中的配置并据此初始化运行时依赖。
6. 用户自定义配置统一收敛到 `data/user/<user_id>/service/<svc_id>/config/user_custom_config.json`。

## 当前差距

基于当前代码核对，主要缺口如下：

1. `hub` 与各 `service` 虽已基本具备 `config/` 目录，但自动补齐与脚手架逻辑仍混用 `configx.json.example`，没有把三文件结构作为统一硬约束。
2. 只有 `chat_server`、`ai_doubao` 在启动早期显式读取配置；`account`、`file_storage`、`sql_db`、`surface_manager`、`autogui` 尚未按统一时序加载。
3. 现有配置读取器通常直接按 JSON 解析；空文件、空白文件尚未被稳定视为合法输入。
4. `chat_server` 当前用户覆盖配置仍在 `services/chat_server/run/user_custom_config.json`，未迁移到统一的 `data/user/.../config/user_custom_config.json`。
5. `hub` 自身目前只实际消费 `hub/config/services.json`，未把 `hub/config/config.json`、`hub/config/configx.json` 纳入启动配置路径。

## 落地范围

本次开发按以下范围收敛：

1. 统一 `hub` 与受管 `service` 的配置目录结构与初始化补齐逻辑。
2. 统一所有受管 `service` 的启动配置加载顺序。
3. 为配置读取增加“空配置合法”能力。
4. 明确并落地 `config.json`、`configx.json`、`configx.json.example`、`user_custom_config.json` 的职责边界。
5. 调整文档、脚手架、manifest 默认参数与相关页面提示，确保与真实实现一致。

不纳入本次范围：

1. 重新设计 service/admin 页面文件编辑能力；现有“可编辑全部 service 文件”能力已满足“编辑 config 文件是子集”这一要求。
2. 重新设计 Hub 的通用文件治理模型。

## 统一设计结论

### 1. 配置目录标准

每个 `hub` / `service` 项目根目录固定包含：

1. `config/config.json`
2. `config/configx.json`
3. `config/configx.json.example`

约束：

1. `config.json`：非敏感默认配置。
2. `configx.json`：真实敏感配置，允许为空。
3. `configx.json.example`：示例字段、示例格式、示例结构，不存放真实敏感值。

### 2. 空配置语义

统一采用以下语义：

1. 空文件、只含空白字符、`{}` 都视为合法空配置。
2. 配置读取阶段将空输入归一化为 `{}`。
3. 启动时只校验“当前服务在当前模式下真正需要的字段”，不因配置文件为空而无条件失败。

### 3. 启动顺序

所有受管 `service` 统一遵循以下启动顺序：

1. 确定项目根目录与 `config/` 路径。
2. 确保 `config.json`、`configx.json`、`configx.json.example` 存在。
3. 读取并解析 `config.json` 与 `configx.json`。
4. 合并为运行时配置对象。
5. 初始化依赖与内部组件。
6. 再执行 Hub 注册、HTTP/WS 路由挂载、心跳与对外服务。

`hub` 自身也遵循同类顺序，但其运行细节按 Hub 进程特性处理。

### 4. 用户自定义配置边界

用户自定义配置统一收敛到：

`data/user/<user_id>/service/<svc_id>/config/user_custom_config.json`

边界：

1. 它属于用户覆盖层，不替代项目内基础 `config/`。
2. 默认合并顺序为：`config.json` -> `configx.json` -> `user_custom_config.json`。
3. 若某服务暂不支持用户级覆盖，可只实现前两层，但路径规范保持统一。

## 开发任务拆解

### A. 配置基础设施收敛

1. 抽出统一的配置目录确保与空 JSON 读取能力，供 `hub` 与各 `service` 复用。
2. 将现有所有 `configx.json.example` 生成逻辑改为“三文件同时存在”的标准输出。
3. 清理当前仅补齐 `config.json` 或仅生成 example 的不一致逻辑。

### B. Hub 配置启动收敛

1. 为 `hub` 增加对 `hub/config/config.json`、`hub/config/configx.json` 的标准加载入口。
2. 保持 `hub/config/services.json` 继续作为生命周期配置事实源，但纳入统一配置初始化流程。
3. 明确 Hub 自身哪些字段来自普通配置、哪些来自敏感配置。

### C. Service 启动入口收敛

1. 为 `account`、`file_storage`、`sql_db`、`surface_manager`、`autogui` 补齐启动第一时间加载配置的逻辑。
2. 将 `chat_server`、`ai_doubao` 调整到统一配置读取接口，避免继续走各自分裂实现。
3. 把“先注册 Hub 再补读配置”之类的潜在逆序问题全部消除。

### D. 空配置合法化

1. 所有配置读取器支持空文件归一化为 `{}`。
2. 所有服务的字段校验逻辑从“文件非空校验”改为“按运行模式做最小必需字段校验”。
3. 为 `configx.json` 缺省场景提供清晰错误信息，只在真正使用到敏感字段时失败。

### E. 用户覆盖配置迁移

1. 将现有 `chat_server` 的用户覆盖配置路径迁移到统一 `data/user/<user_id>/service/<svc_id>/config/user_custom_config.json`。
2. 抽出通用的用户配置路径解析逻辑，避免未来服务再次把覆盖文件写回各自 `run/`。
3. 明确迁移兼容策略：读取旧路径、写入新路径，或一次性迁移后只认新路径。

### F. 默认资源与脚手架同步

1. 更新新 service 生成器默认输出。
2. 更新受管 service 初始化补齐逻辑。
3. 更新 manifest 默认参数、README 模板和相关说明，避免继续引用旧路径或 `.example` 替代真实文件。

### G. 文档与验收同步

1. 更新 `doc/_service_standard.md`、`doc/_instruction/structure.md`、必要时更新 `core.md`。
2. 追加 `doc/_devlog.md` 记录本轮真实改动。
3. 为后续 review 准备统一验收清单。

## 验收标准

完成后必须同时满足：

1. `hub/` 与每个受管 `service` 根目录都存在 `config/`、`config.json`、`configx.json`、`configx.json.example`。
2. 自动补齐、脚手架、新 service 生成逻辑都输出同一目录结构。
3. 所有受管 `service` 在启动最早阶段完成配置读取，再执行注册与对外监听。
4. 空 `config.json` / `configx.json` 不会导致解析失败。
5. 用户自定义配置路径统一到 `data/user/<user_id>/service/<svc_id>/config/user_custom_config.json`。
6. 文档、代码、默认样例、运行路径表述一致，不再出现旧的 `run/user_custom_config.json` 作为正式路径，也不再把 `configx.json.example` 误当成真实敏感配置文件。

## 实施顺序建议

1. 先做配置基础设施收敛与三文件标准化。
2. 再做 Hub 与各 service 的统一启动加载。
3. 然后处理空配置合法化与最小字段校验。
4. 最后迁移用户覆盖路径、同步脚手架与文档。

## 风险与注意事项

1. `chat_server`、`ai_doubao` 当前对配置字段依赖较深，空配置合法化后要避免把“启动成功但功能运行时崩溃”引入主链路。
2. 用户覆盖路径迁移会影响现有读写逻辑与历史文件兼容，需要明确迁移策略。
3. Hub 与 service 若共用一套配置读取辅助，必须保持“只读自己项目目录”的边界，不能重新引入跨项目读取。

---

**文档更新时间**：2026-03-21 11:17:00 CST

**本轮修改范围**：新增 `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/plan/2603211117-config-standard-devplan.md`，沉淀本轮关于 `config/`、`config.json`、`configx.json`、`configx.json.example`、启动顺序、空配置合法化与用户自定义配置路径的统一开发计划。

**信息来源**：本轮用户确认结论、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/AGENTS.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction.md`、`/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`，以及前序对 `hub`、`services/*`、`webui/page/service` 现状的代码核对结果。
