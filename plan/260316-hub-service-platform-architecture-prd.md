# Hub Service Platform 架构改进需求描述文档 (PRD)

- 日期：2026-03-16
- 文档类型：需求说明（PRD）
- 状态：初稿
- 目标：将 kagent 从单体/半解耦架构彻底重构为“Hub + 多独立 Service”的平台化架构

---

## 1. 项目背景与目标
随着业务逻辑的复杂化，原有的单体架构在职责边界、扩展性及多端协同（多 Tab 场景）上遇到了瓶颈。本次架构改进旨在建立一个以 **Hub** 为核心，**Service** 为原子能力提供者，**Page/Surface** 为表现形式的现代化本地服务平台。

### 最终期望目标：
1. **职责分离**：Hub 负责路由与安全，Service 负责业务与工具，Page/Surface 负责展示。
2. **三层安全隔离**：实现 `User`、`Service`、`Surface` 三个层级的存储与权限隔离。
3. **多实例协同**：稳定支持多页面、多 Surface 并发运行，统一管理 Session 与 Capability。
4. **灵活扩展**：支持第三方 Service 快速接入，通过逻辑路径（如 `storage.file.read`）动态发现能力。
5. **生命周期协同**：实现 Hub 与 Service 的级联退出与启动冲突清理机制。

---

## 2. Hub 平台核心功能
Hub 是整个系统的“交通枢纽”与“安保中心”，其职责定义如下：

- **统一入口 (Entrypoint)**：
    - 作为 HTTP/WS 的唯一网关，托管所有静态资源（webui）。
    - 代理所有 `/api/*` 请求至对应的底层服务。
- **服务治理 (Supervisor)**：
    - 管理服务的注册、注销、健康检查。
    - 维护服务元数据（Manifest），处理同名 `service_id` 的实例冲突。
    - **启动预清理**：启动任一 Service 前，必须确保旧的同 `service_id` 实例已被终止。
    - **级联退出**：Hub 进程退出前，必须主动尝试杀死所有当前已注册且处于活跃态的 Service。
- **工具聚合与静态路由 (Routing)**：
    - 汇总所有 Service 提供的工具集（Tool Set）。
    - 建立确定的 `tool_id -> service_id` 路由绑定表，并根据可靠性、延迟、成功率进行评分优选。
- **安全与鉴权中心 (Security)**：
    - 用户认证（JWT）、服务认证（Service Token）、Surface 认证（Session/Capability Token）。
    - 强制执行 Scope 隔离（文件夹/数据库路径级动态锁定）。
- **审计与观测 (Observability)**：
    - 记录所有路由调用流、耗时及状态，输出结构化 Ops 日志。

---

## 3. Service 设计理念
Service 应遵循以下原子化原则：

1. **逻辑路径唯一性**：工具标识统一采用 `category.type.tool`（如 `ai.speech.asr`）。
2. **无感知透明化**：Service 不应知道谁在调用它（Page 或其它 Service），所有调用均通过 Hub 路由透明转发。
3. **独立配置边界**：每个 Service 拥有独立的 `config/` 目录，区分公开配置与敏感配置（`configx.json`）。
4. **Manifest 驱动**：通过 `manifest.json` 声明自身身份、提供的工具、依赖的类型、Reliability 分级。
5. **Session 绑定**：Service 启动后必须向 Hub 注册以获取身份令牌，所有站内调用均需携带该令牌。
6. **反向心跳监测 (Inverse Heartbeat)**：Service 必须每 3 秒监测一次 Hub 的存活状态（如通过健康检查接口或长连接状态）；若探测到 Hub 已关闭，Service 必须执行平滑自杀退出。

---

## 4. 各 Service 具体需求设计

### 4.1 Chat Server (业务逻辑核心)
- **需求目标**：承载聊天应用的全部状态与编排逻辑，脱离 Hub 独立运行。
- **核心功能**：
    - 项目 (Project) 与线程 (Thread) 的 CRUD 逻辑。
    - 流式对话编排（Trigger LLM、Turn 状态流转、历史消息同步）。
    - 中断 (Interrupt) 与后续跟进 (Continuation) 业务规则。
    - Surface 事件汇聚与 Action Report 处理。

### 4.2 AI Service (ai-doubao)
- **需求目标**：将具体的 AI Provider 能力封装为标准工具。
- **核心功能**：
    - `ai.speech.asr`：语音流转文本。
    - `ai.llm.stream`：大语言模型流式输出。
    - `ai.speech.tts`：文本转语音合成。
- **扩展性**：支持多套 AI Service 平行存在，Hub 根据评分或用户配置进行路由切换。

### 4.3 Auth Service (认证中心)
- **需求目标**：统一全平台的身份与能力背书。
- **核心功能**：
    - 用户层：注册、登录、登出、JWT 签发。
    - 服务层：Service ID 注册审核、Service Session Token 签发。
    - 表现层：Surface 加载时的 Session Token 签发与 Capability 转授。
- **安全性**：加密存储敏感信息，支持持久化根密钥以保证多 Tab 稳定性。

### 4.4 File Service (存储中心)
- **需求目标**：提供三层 Scope 隔离的文件与内容寻址能力。
- **核心功能**：
    - **文件系统**：支持 `user` (用户私有)、`surface` (Surface 专属)、`service` (服务内部) 的目录隔离。
    - **Blob 存储**：提供不可变内容的 Put/Get 接口，支持 TTL。
    - **安全性**：禁止调用方直接传递真实绝对路径，由 Hub/Service 根据逻辑逻辑映射。

### 4.5 Database Service (逻辑库管理)
- **需求目标**：提供 Scoped 的轻量数据库访问能力。
- **核心功能**：
    - 逻辑库挂载：根据调用上下文锁定到特定的 `.db` 文件。
    - 统一查询接口：提供 Query/Execute 动作。
    - 隔离性：确保 Surface 只能操作其专属数据库，无法越权访问主库或其他 Surface 库。

### 4.6 Surface Manager (插件网关)
- **需求目标**：管理 Surface 生态的生命周期。
- **核心功能**：
    - 目录扫描：自动发现 `webui/surface/` 下的插件。
    - 运行时状态监控：记录各 Surface 的开启/关闭、错误日志、UI 状态。
    - 权限辅助：配合 Auth Service 生成 Surface 运行所需的权限清单。

---

## 5. 最终期望的功能实现标准 (DoD)
- [ ] **Hub 清度**：Hub 源码中不再包含任何硬编码的聊天业务或 AI 适配逻辑。
- [ ] **服务自治**：关闭任一非核心 Service（如 Surface Manager），不影响 Chat 主链路的运行（Graceful Degradation）。
- [ ] **Scope 闭环**：Surface 在无 Page 转授的情况下，无法通过 API 访问任何用户级文件或数据。
- [ ] **多端一致**：多 Tab 打开不同对话线程时，AI 响应、Surface 联动、音频输出保持各自 Session 独立，互不干扰。
- [ ] **级联退出校验**：Hub 正常退出后，系统中不应残留任何关联的独立 Service 进程。
- [ ] **管理透明**：通过 `admin.html` 可以清晰看到所有服务的健康度、工具绑定关系及实时调用量。
