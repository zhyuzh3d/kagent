# 根目录 config 分拆与删除开发计划

## 1. 任务目标

按以下归类原则完成根目录 `config/` 的拆分，并在兼容迁移完成后删除根目录 `config/`：

1. `hub` 或各 `service` 的静态配置，放到各自的 `config/`。
2. 可执行文件、运行期清单、Hub 会生成或读写的运行态文件，放到各自的 `run/`。
3. 根目录不再保留共享但无明确归属的 `config/` 单一入口。

## 2. 当前事实梳理

### 2.1 根目录 `config/` 文件现状

#### `config/config.json`

- 内容：`app.debug`、`app.ui`、`chat.frontend`、`chat.session`、`chat.asr`、`chat.llm`、`chat.tts`、`chat.pipeline` 公共运行参数。
- 当前读取方：
  - [`hub/cmd/hub/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go) 默认 `-public-config config/config.json`
  - [`services/chat_server/cmd/chat-server/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/cmd/chat-server/main.go) 默认 `-public-config config/config.json`
- 当前消费链路：
  - Hub 通过 [`hub/internal/app/runtime_config.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/app/runtime_config.go) 装载并在 [`hub/internal/gateway/system_handler.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/system_handler.go) 的 `hub.system.config.get` 中对外暴露。
  - Chat Server 通过自己的 [`services/chat_server/internal/app/runtime_config.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/internal/app/runtime_config.go) 装载相同 schema。
- 问题：
  - 同一个文件同时承载 `hub/page` 配置和 `chat-server` 行为配置，归属混杂。
  - `services/chat_server/config/config.json` 当前仅为 `{ "service": {} }`，并未承载真实 chat 公共配置。

#### `config/services.json`

- 内容：`service.global`、`service.lifecycle_default`、`service.services[]`，本质是 Hub 受管服务生命周期配置。
- 当前读取方：
  - [`hub/cmd/hub/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go) 默认 `-services-config config/services.json`
  - [`hub/internal/supervisor/lifecycle.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/lifecycle.go) 负责解码
- 当前相关脚本：
  - [`scripts/deploy.sh`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh) 没读 `config/services.json`，而是读 `hub/config/config.json` 里的同构内容构建 service。
- 问题：
  - 这是 Hub 自身配置，却放在仓库根目录。
  - 读取入口分裂：Hub 启动默认读根目录，部署脚本默认读 `hub/config/config.json`。

#### `config/configx.json`

- 内容结构：`models[].config.chat`、`asr_s`、`tts_s` 等私有模型/供应商凭据配置；包含敏感字段，不应在说明中展开明文。
- 代码检索结果：
  - 当前代码没有直接引用根目录 `config/configx.json` 的固定路径。
  - `ai-doubao` 默认 flag 是相对路径 `config/configx.json`，但实际运行清单 [`services/ai_doubao/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/manifest.json) 已显式传入 `services/ai_doubao/config/configx.json`。
  - `chat-server` 也有自己的 [`services/chat_server/config/configx.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/config/configx.json)，只承载 `ai_service` 指向 Hub 的私有接入参数。
- 问题：
  - 根目录保留了一份私有 AI 配置副本，但运行期真正使用的是 service 自己的配置文件。
  - 归属上应属于 `services/ai_doubao/config/`，不应继续悬挂在根目录。

#### `config/configx.json.example`

- 内容：`config/configx.json` 的样例版本。
- 现状：
  - 与 [`hub/config/configx.json.example`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/configx.json.example) 内容一致。
  - 当前实际更贴近 [`services/ai_doubao/config/configx.json.example`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/config/configx.json.example) 的职责。
- 问题：
  - 示例文件归属不清，且与 Hub 副本重复。

### 2.2 目录与运行态现状

- 已存在的 service 本地配置目录：
  - `services/ai_doubao/config`
  - `services/chat_server/config`
  - `services/file_storage/config`
  - `services/sql_db/config`
  - `services/surface-manager/config`
- 已存在的 service 运行目录：
  - `services/*/run` 已承载 `*-latest`、`manifest.json`、`service.log`
- Hub 当前会读写的 service 运行态文件：
  - [`hub/internal/supervisor/lifecycle.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/lifecycle.go) 约定 `services/<svc>/run/manifest.json`
  - 同文件约定 `services/<svc>/run/.service_secret`
  - [`scripts/deploy.sh`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh) 会构建 `services/<svc>/run/<service_id>-latest` 并复制 `manifest.json` 到 `run/manifest.json`
- 结论：
  - “Hub 可读写的执行工件放 `run/`” 这条规则在 `services/*/run` 上已经基本成立，剩余问题主要集中在根目录 `config/` 的历史残留。

### 2.3 重复与不一致

- `config/services.json` 与 `hub/config/services.json` 完全一致。
- `config/configx.json` 与 `hub/config/configx.json` 完全一致。
- `config/configx.json.example` 与 `hub/config/configx.json.example` 完全一致。
- `config/config.json` 与 `hub/config/config.json` 不一致：
  - 根目录文件是 Page/Chat 公共配置。
  - `hub/config/config.json` 实际却装的是 service 生命周期配置，命名具有误导性。
- `services/chat_server/cmd/chat-server/main.go` 默认 `-user-config run/user_custom_config.json`，但 [`services/chat_server/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/manifest.json) 传的是 `data/users/default/user_custom_config.json`，当前存在默认值与运行清单不一致的问题。

## 3. 拆分原则落地

### 3.1 配置归属原则

1. Hub 治理、受管 service 生命周期、构建清单来源，属于 `hub/config/`。
2. Chat 页面与会话行为参数，属于 `services/chat_server/config/`。
3. AI 供应商私有凭据与模型配置，属于 `services/ai_doubao/config/`。
4. 运行时可执行物、Hub 复制/生成的 manifest、Hub 注入的 bootstrap secret、进程日志，属于 `services/*/run/`。

### 3.2 单一事实源建议

1. `hub/config/services.json` 作为 Hub 生命周期配置唯一事实源。
2. `services/chat_server/config/config.json` 作为 Chat 公共参数唯一事实源。
3. `services/ai_doubao/config/configx.json` 与 `services/ai_doubao/config/configx.json.example` 作为 AI 私有配置唯一事实源。
4. 删除 `hub/config/config.json` 这一误导命名文件，或改名为更准确的 `hub/config/services.json` 单入口后只保留后者。

### 3.3 对 `config/config.json` 的拆分决策

该文件不应整体平移到单一目录，而应按语义拆分：

1. `app.debug`、`app.ui`
   - 若继续由 Hub 对前端统一下发，则迁入 `hub/config/` 的公共配置文件。
2. `chat.frontend`、`chat.session`、`chat.asr`、`chat.llm`、`chat.tts`、`chat.pipeline`
   - 迁入 `services/chat_server/config/config.json`。

原因：

- `app.*` 更接近 Hub/Page 壳层配置。
- `chat.*` 明确属于 Chat Server 运行参数。
- 若不拆，仍会把 Hub 与 Chat Server 的配置混在同一文件里，只是把问题从根目录搬到另一个目录。

## 4. 实施计划

### 阶段 1：收敛单一事实源

1. 将 Hub 启动参数默认值从 `config/services.json` 切到 `hub/config/services.json`。
2. 将部署脚本统一改为读取 `hub/config/services.json`，不再读取命名误导的 `hub/config/config.json`。
3. 删除或重命名 `hub/config/config.json`，避免它继续与 `hub/config/services.json` 并存。
4. 明确 `ai-doubao` 运行时只认 `services/ai_doubao/config/configx.json`。

### 阶段 2：拆分公共配置

1. 新增 Hub 公共配置文件，承载 `app.*`。
2. 将根目录 `config/config.json` 中的 `chat.*` 全量迁入 `services/chat_server/config/config.json`。
3. 调整 Hub 的 `RuntimeConfigManager`，使其支持：
   - 只管理 Hub 所属的 `app.*`
   - 或以聚合模式对外返回 `app.* + chat.*`
4. 调整 Chat Server 的 `RuntimeConfigManager`，使其默认读取 `services/chat_server/config/config.json`。
5. 梳理前端配置读取链路：
   - 当前 WebUI 仍直接请求 `/api/config`
   - 需要决定保留兼容 REST，还是统一改为 `hub.system.config.get`

### 阶段 3：迁移私有配置样例与文档

1. 根目录 `config/configx.json.example` 下线，保留 `services/ai_doubao/config/configx.json.example`。
2. 若 `hub/config/configx.json(.example)` 仅为历史副本，则一并删除。
3. 同步更新说明文档与部署说明，明确每类配置的归属目录。

### 阶段 4：删除根目录 `config/`

1. 全仓 `rg` 确认不再存在对 `config/...` 根目录路径的代码或脚本引用。
2. 迁移或清理根目录残留的实际私有配置文件。
3. 删除根目录 `config/`。
4. 以部署、启动、配置读取、service 拉起流程做回归验证。

## 5. 风险与注意事项

1. `hub.system.config.get` 当前只挂 Hub 自己的 `RuntimeConfigManager`；如果把 `chat.*` 全移走，需要显式设计聚合逻辑，否则前端配置面板会丢字段。
2. WebUI 当前仍请求 `/api/config`，但仓库内未检出显式路由定义；实施前必须先确认该接口的真实承载位置，否则迁移后容易出现“工具接口改了但页面仍走旧路由”的隐性回归。
3. `chat-server` 的 `user-config` 默认值与 manifest 实参不一致，迁移公共配置时应一并统一，避免后续调试时出现“命令行直启”和“Hub 拉起”读取不同文件。
4. 根目录 `config/configx.json` 含敏感配置；迁移时只能做结构比对、文件搬迁和引用切换，不能把明文复制进文档或日志。

## 6. 建议的最小实施顺序

1. 先统一 Hub 生命周期配置入口到 `hub/config/services.json`。
2. 再拆 `config/config.json` 为 `hub app.*` 与 `chat-server chat.*`。
3. 然后清理 `config/configx.json(.example)` 与 `hub/config/configx.json(.example)` 历史副本。
4. 最后做全仓引用清理并删除根目录 `config/`。

## 7. 验收标准

1. 根目录 `config/` 已删除。
2. Hub 启动、部署脚本、Supervisor 生命周期装载只依赖 `hub/config/`。
3. Chat Server 公共配置只依赖 `services/chat_server/config/`。
4. AI 私有配置只依赖 `services/ai_doubao/config/`。
5. Hub 生成或同步的 service 文件仅落在 `services/*/run/`。
6. `rg -n 'config/'` 不再出现对根目录 `config/` 的有效运行时引用。
7. 部署后能完成：
   - `./scripts/deploy.sh`
   - Hub 正常启动
   - 受管 services 正常拉起
   - 前端配置读取不报错

## 8. 本文依据

- 目录与文件扫描：`find . -type d \( -name config -o -name run \)`、`rg --files config services hub`
- 配置内容核对：`config/config.json`、`config/services.json`、`config/configx.json`、`hub/config/*`、`services/chat_server/config/*`、`services/ai_doubao/config/*`
- 代码引用核对：
  - [`hub/cmd/hub/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/cmd/hub/main.go)
  - [`hub/internal/supervisor/lifecycle.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/supervisor/lifecycle.go)
  - [`scripts/deploy.sh`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/scripts/deploy.sh)
  - [`services/chat_server/cmd/chat-server/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/cmd/chat-server/main.go)
  - [`services/ai_doubao/cmd/ai-doubao/main.go`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/cmd/ai-doubao/main.go)
  - [`services/ai_doubao/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/ai_doubao/manifest.json)
  - [`services/chat_server/manifest.json`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chat_server/manifest.json)
  - [`webui/page/chat/config-store.js`](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/config-store.js)
- Git 历史参考：`git log --oneline -- config hub/config services/*/config`

**计划创建时间**：2026-03-20 09:39:17 CST

**计划范围**：分析根目录 `config/` 现状，提出分拆、迁移与删除计划；本轮未做代码实现。
