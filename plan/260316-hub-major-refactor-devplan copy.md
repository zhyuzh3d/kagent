# 260316 Hub 大重构开发计划（改进版：含 Tool Protocol v1 / Service Lifecycle / Routing Schema / Security Contract）

* 文档类型：开发计划（devplan）+ 技术规格附录（tech spec）
* 更新时间：2026-03-16
* 范围：`hub/`、`services/*`、`webui/`（含 surface）
* 目的：将原开发计划补足到“可直接驱动 AI 编码与人工实现”的程度，尤其补齐以下四类不可猜测细节：

  * Tool Protocol v1
  * Service Lifecycle Contract
  * Routing Metadata Schema
  * Security Token Contract

---

## 0. 文档定位与使用方式

本文档不是 PRD，也不是仅供讨论的架构草案，而是面向实现的开发规格文档。阅读顺序建议如下：

1. 先读“不可变前提”，明确哪些设计不能被实现时自行改变。
2. 再读“目标架构概述”，理解最终形态与职责边界。
3. 然后直接按“Tool Protocol v1”“Service Lifecycle Contract”“Routing Schema”“Security Contract”落代码。
4. 最后按“Phase 1/2/3”实施与验收。

**实现原则**

* 可以补实现细节，但不得改变本文中已经冻结的字段名、接口语义、角色边界与安全前提。
* 若发现文档存在冲突，必须优先指出冲突并暂停该部分实现，不得自行发明兼容语义。
* 可以在开发期临时双栈，但最终形态必须删除旧接口、旧路由与旧协议结构。

---

## 1. 不可变前提（冻结）

### 1.1 动态路由闭环：采用方案 A，且最终不兼容旧接口

* 统一以 `tool_id` 为主键完成路由、鉴权、审计、治理。
* 所有现有旧 REST/WS 业务能力最终都必须迁移到统一工具协议。
* 开发期允许短暂双栈，但最终必须删除旧路径和旧分类代理逻辑。

### 1.2 安全：Hub 完全掌控授权

* 只有 Hub 可以解析/校验用户 JWT。
* 只有 Hub 可以签发用户 token、service token、surface token。
* service 不持有用户 JWT 签名密钥，不验证用户 JWT，不签发任何用户态 token。
* Surface Manager / Service Manager 只做元数据与状态管理，不参与权限授予。

### 1.3 数据：所有 DB 操作必须经由 database-service

* 除 `database-service` 外，任何模块不得直接打开或写入 DB 文件。
* DB 密钥与敏感配置仅允许存在于 `services/database/config/configx.*` 或等价敏感配置载体。
* 每个 service 原则上只操作自己的数据库。
* `_share` 库由 `database-service` 统一管控：

  * 任何 service 只能写入自己的条目；
  * 所有写入都必须带 caller service 身份；
  * 写入约束必须由应用层与 DB 约束双重保证。

### 1.4 传输：必须同时支持 HTTP/HTTPS/WebSocket/UDS

* 对外统一入口由 Hub 提供：`http/https/ws`。
* 对内 Hub -> service 默认优先使用 UDS。
* 在开发/调试场景允许回退到 loopback TCP。

### 1.5 Hub 最终职责边界

Hub 最终只保留四类内核职责：

* Gateway
* Security
* Supervisor
* Routing

Hub 最终不承载具体业务域实现，不直接承载本地 DB/Blob/File 业务逻辑。

---

## 2. 目标架构概述

### 2.1 角色与边界

#### Hub（平台内核）

负责以下能力：

* Gateway：统一入口、静态资源托管、WS 连接终止、必要的桥接。
* Security：身份认证、token 签发与验证、请求身份注入、capability 校验。
* Supervisor：service 注册、冲突检测、心跳、prepare-start、级联退出、实例状态管理。
* Routing：基于 `tool_id` 的绑定、选路、评分、熔断、降级、审计与统计。

#### Services（原子能力提供者）

* 只暴露统一工具执行入口。
* 不面向用户直接暴露旧式业务 REST。
* 不解析用户 JWT。
* 只信任来自 Hub 的调用，以及 Hub 颁发的 service/platform 级身份凭证。
* 自身若需 DB 能力，必须调用 database-service。

#### Surface（webui/surface 插件）

* 通过 Hub 统一入口加载。
* 默认不具备直接读写用户数据的能力。
* 只能通过 Hub 授予的 capability，在工具协议允许的范围内调用工具。

#### Surface Manager / Service Manager

* 仅维护清单、元数据、日志聚合、状态上报。
* 不负责 token 发放，不负责授权。

### 2.2 最终统一工具入口

#### 对外（Hub）

* `POST /api/tool/call`
* `GET /api/tool/ws`（WebSocket upgrade）

#### 对内（Service）

* `POST /service/tool/exec`
* `GET /service/tool/ws`（可选；若 service 不实现流式 WS，则由 Hub 负责上游 WS 与下游 HTTP streaming 或轮询桥接）

#### 对内（Hub Internal API，统一规范）

以下接口仅用于“service <-> hub”的平台内部协作，路径前缀必须统一为 `/api/internal/`，避免出现多套并行风格：

* `POST /api/internal/supervisor/register`
* `POST /api/internal/supervisor/heartbeat`
* `POST /api/internal/supervisor/prepare-start`
* `POST /api/internal/supervisor/drain`
* `POST /api/internal/supervisor/unregister`
* `GET /api/internal/healthz`

### 2.3 统一身份注入原则

Hub 在将请求转发给 service 时，必须注入且仅注入由自己信任的身份上下文；service 不得相信客户端自己携带的身份头。

受保护头示例：

* `X-Hub-Request-Id`
* `X-Hub-Trace-Id`
* `X-Caller-Type`
* `X-Caller-User-Id`
* `X-Caller-Service-Id`
* `X-Caller-Surface-Id`
* `X-Hub-Service-Token`
* `X-Hub-Platform-Token`

客户端若带同名 header，Hub 必须覆盖或剥离。

---

## 3. Tool Protocol v1（冻结）

本节用于消除协议层歧义。所有 service、surface、Hub 转发层必须遵守以下 envelope 结构。

### 3.1 非流式调用：`POST /api/tool/call`

#### Request Body

```json
{
  "tool_id": "app.chat.send_message",
  "args": {
    "project_id": "p_123",
    "thread_id": "t_456",
    "content": "hello"
  },
  "context": {
    "request_id": "req_01H...",
    "trace_id": "tr_01H...",
    "idempotency_key": "idem_xxx",
    "timeout_ms": 30000,
    "caller": {
      "type": "user",
      "user_id": "u_123",
      "service_id": "",
      "surface_id": "surface_chat"
    },
    "capabilities": [
      "app.chat.thread.write"
    ],
    "meta": {
      "client_version": "webui-0.1.0"
    }
  }
}
```

#### 字段规则

* `tool_id`：必填。全局唯一工具标识。必须遵循 `category.type.tool` 格式，例如 `storage.file.read`、`app.chat.send_message`。
* `args`：必填。工具输入参数对象。不得为数组根节点。
* `context`：对外调用可选（客户端可不提供）。由 Hub 负责生成、补全与清洗；外部 caller 提供的字段不能直接信任。
  - `request_id`：一次调用唯一标识。由 Hub 入口生成；客户端若传入，仅可作为调试信息，不得覆盖 Hub 生成值。
  - `trace_id`：调用链追踪 ID。由 Hub 生成或继承；客户端不可强制指定。
  - `idempotency_key`：可选。由客户端提供，Hub 透传并执行幂等语义（如工具支持）。
  - `timeout_ms`：可选。若未指定，Hub 使用工具默认超时；Hub 可按策略裁剪上限。
  - `caller`：由 Hub 根据 user JWT / surface token / service token 推导并覆盖，客户端提供的 caller 字段无效。
  - `capabilities`：由 Hub 校验与裁剪为最终生效范围；客户端提供仅作为“请求意图”，不得扩大权限。

#### Response Body

```json
{
  "ok": true,
  "result": {
    "message_id": "m_789"
  },
  "error": null,
  "meta": {
    "request_id": "req_01H...",
    "trace_id": "tr_01H...",
    "service_id": "chat-service",
    "instance_id": "chat-01",
    "duration_ms": 84
  }
}
```

#### Error Response Body

```json
{
  "ok": false,
  "result": null,
  "error": {
    "code": "TOOL_TIMEOUT",
    "message": "tool execution timed out",
    "details": {
      "timeout_ms": 30000
    },
    "retryable": true
  },
  "meta": {
    "request_id": "req_01H...",
    "trace_id": "tr_01H...",
    "service_id": "chat-service",
    "instance_id": "chat-01",
    "duration_ms": 30010
  }
}
```

#### 错误码（v1 保留集）

* `BAD_REQUEST`
* `UNAUTHORIZED`
* `FORBIDDEN`
* `TOOL_NOT_FOUND`
* `ROUTE_NOT_FOUND`
* `SERVICE_UNAVAILABLE`
* `TOOL_TIMEOUT`
* `TOOL_EXEC_ERROR`
* `RATE_LIMITED`
* `CONFLICT`
* `INTERNAL_ERROR`

### 3.2 流式调用：`GET /api/tool/ws`

#### WS 首帧（client -> hub）

```json
{
  "type": "tool.call",
  "tool_id": "app.chat.stream_reply",
  "args": {
    "thread_id": "t_456",
    "prompt": "hello"
  },
  "context": {
    "request_id": "req_01H...",
    "trace_id": "tr_01H...",
    "timeout_ms": 120000,
    "caller": {
      "type": "user",
      "user_id": "u_123",
      "surface_id": "surface_chat"
    },
    "capabilities": [
      "app.chat.thread.write",
      "app.chat.reply.stream"
    ]
  }
}
```

#### 流式事件格式（server -> client）

```json
{
  "type": "tool.event",
  "event": "delta",
  "request_id": "req_01H...",
  "trace_id": "tr_01H...",
  "seq": 3,
  "done": false,
  "payload": {
    "text": "hello"
  }
}
```

#### v1 事件类型

* `accepted`：Hub 已接受请求并完成初始路由。
* `started`：目标 service 已开始执行。
* `delta`：渐进输出。
* `progress`：阶段进度。
* `log`：可选调试日志；生产环境默认不对用户暴露。
* `result`：最终结果。
* `error`：错误终止。
* `completed`：流结束，后续不再有事件。

#### 结束规则

* 每个流式请求必须以 `result + completed` 或 `error + completed` 结束。
* `seq` 必须单调递增，从 1 开始。
* 同一个 `request_id` 的事件不得乱序发送。

### 3.3 Service 侧统一执行入口：`POST /service/tool/exec`

Hub 发往 service 的 body 结构与外部 `POST /api/tool/call` 保持同 shape，但有两点差异必须遵守：

* `context` 在 **Hub -> service** 转发中为必填，且必须由 Hub 生成并写入可信 caller 信息。
* service 不得相信未经 Hub 注入的 caller 字段，不得信任客户端自带的任何身份 header。

### 3.4 HTTP 状态码约定

* `200`：业务成功，`ok=true`
* `400`：请求结构错误，对应 `BAD_REQUEST`
* `401`：未认证，对应 `UNAUTHORIZED`
* `403`：无权限，对应 `FORBIDDEN`
* `404`：工具或路由不存在，对应 `TOOL_NOT_FOUND` / `ROUTE_NOT_FOUND`
* `409`：冲突，对应 `CONFLICT`
* `429`：限流，对应 `RATE_LIMITED`
* `500`：内部错误，对应 `INTERNAL_ERROR`
* `502/503/504`：下游不可用、超时、网关错误，对应 `SERVICE_UNAVAILABLE` / `TOOL_TIMEOUT`

### 3.5 幂等规则

以下类型的工具建议实现幂等：

* 创建类写操作
* 支付/扣费类操作
* 发送消息但允许网络重试的操作

Hub 若收到相同 `(caller_id, tool_id, idempotency_key)` 的重复请求，可命中幂等结果缓存。

---

## 4. Service Lifecycle Contract（冻结）

### 4.1 Service 实例模型

每个 service 可包含多个实例。实例是 Supervisor 监管的基本对象。

#### Service 定义字段

* `service_id`：稳定服务标识，全局唯一，例如 `chat-service`
* `service_name`：显示名
* `version`：服务版本，例如 `0.1.0`
* `tools[]`：该 service 提供的工具清单
* `transport`：`uds | tcp`
* `endpoint`：对应 transport 的 endpoint 描述
* `health_path`：可选健康检查路径
* `weight`：默认选路权重
* `tags[]`：能力标签，如 `chat`, `storage`, `internal`

#### Instance 定义字段

* `instance_id`：实例唯一 ID，例如 `chat-service@macmini-01#1`
* `service_id`
* `pid`：可选，供本机监管时使用
* `status`：`starting | ready | draining | unhealthy | dead`
* `registered_at`
* `last_heartbeat_at`
* `score`
* `consecutive_failures`
* `endpoint_resolved`

### 4.2 注册接口

#### `POST /api/internal/supervisor/register`

仅允许 service -> Hub 调用。该接口不是用户态公开接口。

##### Request Body

```json
{
  "service_id": "chat-service",
  "instance_id": "chat-service@macmini-01#1",
  "version": "0.1.0",
  "transport": "uds",
  "endpoint": {
    "uds_path": "/tmp/kagent/chat-service.sock",
    "tcp_url": ""
  },
  "tools": [
    {
      "tool_id": "app.chat.send_message",
      "version": "v1",
      "streaming": false,
      "timeout_ms": 30000,
      "capabilities_required": [
        "app.chat.thread.write"
      ]
    },
    {
      "tool_id": "app.chat.stream_reply",
      "version": "v1",
      "streaming": true,
      "timeout_ms": 120000,
      "capabilities_required": [
        "app.chat.reply.stream"
      ]
    }
  ],
  "weight": 100,
  "tags": ["chat"],
  "health_path": "/healthz"
}
```

##### Response Body

```json
{
  "ok": true,
  "result": {
    "service_session_token": "<opaque-or-jwt>",
    "expires_in_sec": 3600,
    "heartbeat_interval_sec": 3,
    "inverse_heartbeat_interval_sec": 3,
    "inverse_heartbeat_failures_to_exit": 2,
    "drain_grace_period_sec": 30
  },
  "error": null,
  "meta": {
    "request_id": "req_xxx",
    "trace_id": "tr_xxx"
  }
}
```

### 4.3 注册约束

* `service_id + instance_id` 必须唯一。
* 同一 `instance_id` 重复注册时，Hub 应视为实例刷新；若 endpoint 或版本不一致，应记录审计并按配置允许覆盖或拒绝。
* `tools[]` 是 service 对外承诺的能力来源，Hub 以此建立工具注册表与路由表候选。
* service 侧必须使用 Hub 在 register response 中返回的 `heartbeat_interval_sec` / `inverse_heartbeat_interval_sec` 等参数，不得在代码中写死固定周期。

### 4.4 心跳接口

#### `POST /api/internal/supervisor/heartbeat`

##### Request Body

```json
{
  "service_id": "chat-service",
  "instance_id": "chat-service@macmini-01#1",
  "status": "ready",
  "metrics": {
    "cpu": 0.21,
    "mem_mb": 128,
    "active_requests": 3,
    "error_rate_1m": 0.01
  }
}
```

### 4.5 心跳与失联规则

* 心跳周期必须可配置，默认建议为 **3 秒**（满足 PRD 的“反向心跳监测”节奏；在低功耗环境可调大）。
* 失联阈值必须可配置：
  - `unhealthy`：默认错过 3 个心跳周期。
  - `dead`：默认错过 6 个心跳周期。
* 状态切换必须可审计。

### 4.6 反向心跳（Inverse Heartbeat，冻结）

该机制是强制要求：**service 必须主动监测 Hub 存活，并在 Hub 不可用时自杀退出**。

推荐实现方式：service 使用定时器按 `inverse_heartbeat_interval_sec`（默认建议 3 秒，可配置）调用 `POST /api/internal/supervisor/heartbeat`。

* 若调用成功：继续运行。
* 若连续失败达到 `inverse_heartbeat_failures_to_exit`（默认建议 2 次，可配置）：
  - service 进入 `draining`（若支持），停止接收新请求；
  - 在 `drain_grace_period_sec` 内尝试完成已建立的流式请求；
  - 最终退出进程（平滑自杀）。

注意：由于该调用依赖 Hub 处理能力，**请求失败本身即可作为 Hub 不可用信号**；不要求再额外实现第二套“Hub ping”接口，但若实现 `GET /api/internal/healthz` 可用于更友好的诊断。

### 4.7 Prepare-Start 协议

用于在真正切流量前完成资源探测、socket 预创建、依赖就绪检查。

#### `POST /api/internal/supervisor/prepare-start`

##### Request Body

```json
{
  "service_id": "chat-service",
  "instance_id": "chat-service@macmini-01#1",
  "expected_transport": "uds"
}
```

##### Response Body

```json
{
  "ok": true,
  "result": {
    "prepared": true,
    "endpoint": {
      "uds_path": "/tmp/kagent/chat-service.sock"
    }
  },
  "error": null,
  "meta": {
    "request_id": "req_xxx",
    "trace_id": "tr_xxx"
  }
}
```

### 4.8 Drain 接口（可选但建议）

用于在实例退出前进入 `draining` 状态，让 Hub 停止给该实例分配新请求（尤其是流式请求）。

#### `POST /api/internal/supervisor/drain`

##### Request Body

```json
{
  "service_id": "chat-service",
  "instance_id": "chat-service@macmini-01#1",
  "reason": "shutdown",
  "grace_period_sec": 30
}
```

##### Response Body

```json
{
  "ok": true,
  "result": {
    "draining": true
  },
  "error": null,
  "meta": {
    "request_id": "req_xxx",
    "trace_id": "tr_xxx"
  }
}
```

### 4.9 Draining 与级联退出

* 当实例进入 `draining` 时，不再接收新请求，但应允许已建立的流式请求在 grace period 内完成。
* Supervisor 可在上游依赖退出时触发级联退出，例如某些临时服务依赖父服务生命周期。
* 级联退出必须先 `drain`，再停止路由，最后回收实例记录。

### 4.10 注销接口（可选）

#### `POST /api/internal/supervisor/unregister`

实例主动优雅退出时使用。若未调用但心跳失联，Hub 仍需自动清理。

### 4.11 Hub Internal Healthz（冻结）

该接口用于诊断与部署自检，不承载鉴权语义。

#### `GET /api/internal/healthz`

##### Response Body

```json
{
  "ok": true,
  "timestamp_ms": 1700000000000
}
```

---

## 5. Routing Metadata Schema（冻结）

### 5.1 路由核心原则

* 路由主键是 `tool_id`，不是 URL path。
* 一个 `tool_id` 可绑定多个 service 实例。
* 路由选择优先使用“手工绑定 + 健康状态 + 熔断状态 + 得分 + 权重”的决策顺序。

### 5.2 核心表结构（逻辑模型）

#### Tool Registry

记录工具定义。

* `tool_id`
* `version`
* `streaming`
* `default_timeout_ms`
* `capabilities_required[]`
* `input_schema_ref`
* `output_schema_ref`
* `owner_service_id`
* `enabled`

#### Service Registry

记录 service 定义。

* `service_id`
* `version`
* `enabled`
* `default_weight`
* `transport_preference`
* `tags[]`

#### Instance Registry

记录实例状态。

* `instance_id`
* `service_id`
* `status`
* `transport`
* `endpoint`
* `score`
* `consecutive_failures`
* `last_success_at`
* `last_failure_at`
* `last_heartbeat_at`

#### Tool Route Binding

记录 `tool_id -> service_id` 候选关系。

* `tool_id`
* `service_id`
* `binding_mode`：`manual | discovered`
* `priority`
* `weight`
* `enabled`
* `circuit_state`：`closed | open | half_open`
* `failure_window`
* `failure_threshold`
* `recover_after_sec`

#### Tool Call Audit

记录真实调用统计。

* `request_id`
* `trace_id`
* `tool_id`
* `service_id`
* `instance_id`
* `caller_type`
* `caller_id`
* `status`
* `duration_ms`
* `error_code`
* `created_at`

### 5.3 选路顺序（固定）

对于一次 `tool_id` 调用，Hub 的选路顺序如下：

1. 找到该 `tool_id` 的所有 `enabled=true` 候选绑定。
2. 过滤掉 `service.enabled=false` 的 service。
3. 过滤掉 `instance.status != ready` 的实例。
4. 过滤掉 `circuit_state=open` 的绑定。
5. 若存在 `binding_mode=manual` 的候选，则仅在 manual 集合内继续选。
6. 按 `priority DESC` 排序。
7. 在同一优先级内按 `score DESC` 再按 `weight DESC` 选择。
8. 若仍并列，则按最近成功时间更近者优先。

### 5.4 评分规则（v1 简化版）

初版可采用简单线性评分：

`score = base_weight + success_bonus - failure_penalty - latency_penalty`

建议参数：

* `base_weight = binding.weight`
* 最近 100 次内成功率高于阈值时增加 `success_bonus`
* 连续失败次数增加 `failure_penalty`
* 平均延迟高于工具目标延迟时增加 `latency_penalty`

初版不要求复杂机器学习评分，但必须保证评分字段真实来自调用链路，而不是静态填充值。

### 5.5 熔断规则（v1）

* 当某绑定在滑动窗口内失败次数达到 `failure_threshold` 时，进入 `open`。
* `open` 状态持续 `recover_after_sec`。
* 到期后进入 `half_open`，允许少量探测请求。
* 若探测成功恢复为 `closed`；若失败则重新 `open`。

### 5.6 缺省行为

* 若 `tool_id` 存在但无可用实例，返回 `SERVICE_UNAVAILABLE`。
* 若 `tool_id` 根本不存在，返回 `TOOL_NOT_FOUND`。
* 不允许静默 fallback 到旧路径或模糊 URL 分类器。

---

## 6. Security Token Contract（冻结）

### 6.1 Token 分类

系统存在四类 token：

1. **User JWT**

   * 用于用户登录态。
   * 仅 Hub 解析与验证。

2. **Service Session Token**

   * Hub 在 service 注册成功后签发。
   * 用于 service 向 Hub 发起 heartbeat、unregister、受控内部调用。

3. **Surface Capability Token**

   * Hub 为 surface 会话签发。
   * 表达某个 surface 在某个用户上下文下可调用的工具能力边界。

4. **Platform Token**

   * 预留能力：用于 Hub 作为平台身份访问关键内核服务（如 database-service）时使用。
   * 默认实现应以 **UDS + 文件权限 + 回环限制** 作为主要信任边界；Platform Token 在 v1 中只需保留接口与结构，不强制启用。
   * 不对用户和普通 service 暴露。

### 6.2 User JWT Claims（建议）

```json
{
  "iss": "hub",
  "sub": "u_123",
  "aud": "hub-api",
  "typ": "user",
  "roles": ["user"],
  "sid": "session_abc",
  "exp": 9999999999,
  "iat": 9999990000
}
```

### 6.3 Service Session Token Claims（建议）

```json
{
  "iss": "hub",
  "sub": "chat-service@macmini-01#1",
  "aud": "hub-internal",
  "typ": "service",
  "service_id": "chat-service",
  "instance_id": "chat-service@macmini-01#1",
  "exp": 9999999999,
  "iat": 9999990000
}
```

### 6.4 Surface Capability Token Claims（建议）

```json
{
  "iss": "hub",
  "sub": "surface_session_xyz",
  "aud": "hub-api",
  "typ": "surface",
  "user_id": "u_123",
  "surface_id": "surface_chat",
  "capabilities": [
    "app.chat.thread.read",
    "app.chat.thread.write",
    "app.chat.reply.stream"
  ],
  "exp": 9999999999,
  "iat": 9999990000
}
```

### 6.5 Platform Token Claims（建议）

```json
{
  "iss": "hub",
  "sub": "hub-core",
  "aud": "platform-internal",
  "typ": "platform",
  "scope": ["database.admin", "database.query"],
  "exp": 9999999999,
  "iat": 9999990000
}
```

### 6.6 Token 生命周期建议

* User JWT：按用户登录态策略控制，可较长。
* Service Session Token：默认 1 小时，可续签。
* Surface Capability Token：默认 15 分钟到 1 小时，偏短。
* Platform Token：预留能力；若启用则建议偏短周期轮换（10~30 分钟）或使用进程内轮换缓存。

### 6.7 注入与校验规则

* 用户请求到达 Hub 时，只校验 user JWT / surface capability token。
* Hub 转发到 service 时，必须剥离原始用户 JWT，不向下游暴露 JWT 签名链路。
* Hub 向下游注入 caller 身份 header 与 `X-Hub-Service-Token` / `X-Hub-Platform-Token`。
* service 只校验“该请求是否由 Hub 转发且 token 有效”，不再独立验证用户 JWT。

### 6.8 Header 信任边界

以下 header 为 Hub 专属头，客户端传入无效：

* `X-Hub-Service-Token`
* `X-Hub-Platform-Token`
* `X-Caller-Type`
* `X-Caller-User-Id`
* `X-Caller-Service-Id`
* `X-Caller-Surface-Id`

### 6.9 database-service 特别规则

* database-service 不接受用户 JWT 直接访问。
* database-service 只接受来自 Hub 的平台级或受控 service 级身份。
* 当普通 service 通过 Hub 请求 database-service 时，Hub 需要同时传达：

  * 平台受信调用身份；
  * 原始 caller 的 service_id / user_id 上下文；
  * 目标库与操作范围。

---

## 7. `_share` 库写入约束（补充冻结）

### 7.1 数据模型建议

共享库中的共享记录至少包含：

* `id`
* `namespace`
* `category`
* `service_id`
* `key`
* `value_json`
* `visibility`
* `created_at`
* `updated_at`

### 7.2 强约束

* 写入时 `service_id` 必须等于 caller service_id。
* caller 为用户或 surface 时，不得直接写 `_share`；必须经由某个 service 代理，并由 Hub 注入其 service 身份。
* database-service 必须在应用层检查，同时在 DB 层使用触发器或等效约束兜底，防止绕过应用层写入他人条目。

### 7.3 读取规则

* `_share` 默认公共可读，但建议至少支持按 `namespace/category` 过滤。
* 后续若需要细粒度可见性控制，可在 `visibility` 基础上扩展，而不改变 `service_id` 强约束。

---

## 8. 传输适配规格（UDS 优先）

### 8.1 Endpoint 描述规范

统一 endpoint 描述对象：

```json
{
  "transport": "uds",
  "uds_path": "/tmp/kagent/chat-service.sock",
  "tcp_url": ""
}
```

或：

```json
{
  "transport": "tcp",
  "uds_path": "",
  "tcp_url": "http://127.0.0.1:18081"
}
```

### 8.2 Transport 抽象要求

Hub 内部的 transport 层必须抽象成统一 client 接口，上层 routing 不关心 UDS/TCP 差异。

统一能力：

* call
* stream
* timeout
* retry
* connection reuse

### 8.3 回退策略

* 默认优先 UDS。
* 若实例声明 `transport=uds` 但 endpoint 不可用，可按配置选择：

  * 直接判定实例 unhealthy；
  * 或在开发模式下尝试回退到其 `tcp_url`。
* 生产默认不做隐式跨传输回退，避免掩盖部署错误。

---

## 9. 三阶段实施计划（加强版）

### Phase 1：职责迁移与 Hub 纯粹化

#### 目标

* Hub 从业务承载体收缩为平台内核。
* 所有核心能力开始迁移到统一工具协议。
* database-service 成为唯一 DB 入口。

#### 必做事项

1. 冻结并落地 Tool Protocol v1。
2. 所有新代码只允许走 `tool_id` 路由，不再新增旧 REST 业务接口。
3. chat/file/database/surface-manager/service-manager 定义工具清单。
4. Hub 内置 Auth，删除 auth-service 最终形态。
5. 清理 Hub 中直接操作 DB/Blob/File 的实现。

#### Phase 1 DoD

* `POST /api/tool/call` 跑通至少 2 个 service。
* database-service 可通过统一工具入口执行至少：query、execute、share.read、share.write。
* Hub 内部不再新增任何本地 DB 直接访问代码。

### Phase 2：Hub 完全重构

#### 目标

* 拆分 Hub 包结构。
* 打通 supervisor + routing + transport + security 闭环。
* 实现真实可用的评分、熔断、审计。

#### 建议目录

```text
hub/
  cmd/hub/
  internal/
    gateway/
    security/
    supervisor/
    routing/
    transport/
    observability/
    protocol/
```

#### Phase 2 DoD

* 路由主路径完全由 `tool_id` 驱动。
* 评分字段来自真实调用链路。
* UDS/TCP endpoint 可互换且不影响鉴权逻辑。
* prepare-start / heartbeat / drain / unregister 具备自动化测试或脚本验证。

### Phase 3：目录清理与最终精简

#### 目标

* 删除遗留文件、旧 proxy 分类器、旧协议结构、不可达 handler。
* Hub 最终只保留平台内核代码。

#### Phase 3 DoD

* `hub/` 中不包含 chat/file/db/surface-manager 等业务域实现。
* 旧 REST/WS 兼容层已删除。
* 新人可按目录直接理解系统分层。

---

## 10. 里程碑与验收清单

### 10.1 里程碑

* M1：Tool Protocol v1 冻结并跑通 1~2 个 service
* M2：database-service 成为唯一 DB 入口
* M3：supervisor + routing + scoring + circuit breaker 闭环可用
* M4：Hub 目录清理完成，旧接口删除

### 10.2 硬验收项

* 所有功能调用都经过 `tool_id` 路由。
* service 不持有 JWT 密钥与 DB 密钥。
* 除 database-service 外无任何 DB 文件直接访问。
* UDS/TCP 切换不影响功能与权限判断。
* prepare-start / 心跳 / 级联退出具备验证脚本或测试。

---

## 11. 建议的 AI 编码方式（供 Codex 使用）

若使用 AI 编码，不要一次要求“完成整个系统”，而应按以下顺序推进：

1. 根据本文档生成目标 repo 目录结构。
2. 先实现 `protocol/`：Tool Protocol v1 的数据结构与校验。
3. 再实现 `supervisor/`：register / heartbeat / unregister / drain。
4. 再实现 `routing/`：registry、binding、scoring、circuit breaker。
5. 再实现 `transport/`：UDS/TCP 抽象 client。
6. 最后逐个 service 迁移为工具协议。

AI 在实现时必须遵守：

* 不得修改已冻结字段名。
* 不得自行增加旧接口兼容层作为最终方案。
* 若发现文档冲突，先指出冲突再暂停实现相关部分。

---

## 12. 仍可后续单独补充但不阻塞开工的内容

以下内容会提升长期可维护性，但不是当前开工阻塞项：

* Tool input/output JSON Schema 的正式版本化规则
* 审计数据的持久化表结构细化
* 流式事件的 backpressure 策略
* 速率限制与配额模型
* Surface 级 capability 模板系统
* Platform token 的轮换与密钥托管细节

---

## 13. 结论

到本文档这一版本为止，规格已经从“架构计划”升级为“可以直接驱动实现的开发规格”。

它已经足够支持以下工作直接开工：

* Hub 内核目录重构
* Tool Protocol v1 编码
* Supervisor 生命周期管理实现
* Routing registry / binding / scoring / circuit breaker 实现
* Security token 基础链路实现
* database-service 作为唯一 DB 入口的迁移工作

若实现中遇到未定义细节，应优先在不违背本文冻结前提的情况下补最小实现，不得擅自改动本文核心协议与边界。
