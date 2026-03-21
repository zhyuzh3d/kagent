# Kagent 主页面与自治工作区系统规范 (Page-Surface Guide)

## 0. 维护信息

- 文档定位：目标架构规范 / 开发指导文档
- 最后核验时间：2026-03-22 00:13:53 CST
- 适用范围：`webui/page/*` 宿主页、`webui/surface/*` 工作区、`services/surface_manager`、Hub 身份与工具平面
- 重要说明：本文定义的是 **理想目标规范** 与后续重构方向，不代表仓库当前所有 `page` / `surface` 实现都已完全符合；若现有实现与本文冲突，应优先调整实现，而不是降低规范
- 依据：
  - `hub/internal/app/identity.go`
  - `services/surface_manager/cmd/surface_manager/tool_http_handler.go`
  - `services/surface_manager/internal/app/surface_catalog.go`
  - `services/surface_manager/internal/app/surfacefs.go`
  - `services/sql_db/cmd/sql_db/bootstrap_runtime.go`
  - `services/file_storage/cmd/file_storage/tool_http_handler.go`
  - `webui/page/chat/surface-bridge.js`
  - `webui/page/chat/action-engine.js`
  - `webui/page/surface/components/runtime.js`
  - `webui/surface/buildin/counter/index.html`
  - 以及本轮用户对产品目标、边界与术语的明确说明

---

## 1. 文档目标

本文只回答一个核心问题：

> 如何定义一个统一的宿主-工作区契约，使任何主页面都能装载任何自治工作区，而工作区又能以正式的 `surface caller` 身份使用平台工具与持久化能力。

本文不定义以下内容：

- 外部指令来源必须是用户、助理 AI、自动化任务还是其他系统
- `surface` 内部是否存在多个 worker / agent
- 复杂任务如何拆解、如何协作、如何调度
- 工作区面向工作、娱乐、创作还是管理的具体业务差异

这些都属于 `surface` 内部自治范围，不是 `page-surface` 统一规范的职责。

---

## 2. 核心定义

### 2.1 `Page`

`Page` 是宿主页面（Host Page）。

它的职责是：

- 装载、连接、卸载 `surface runtime`
- 维护工作区窗口与工作区级状态
- 调用 `surface` 已注册的动作
- 提供受控的 `host actions`
- 作为 `surface` 接入 Hub 的授权与运行环境承载面
- 观察并记录 `surface` 的状态、日志、执行结果

`Page` 不是 `surface` 的业务实现者，也不是 `surface` 内部逻辑的代行者。

### 2.2 `Surface`

`Surface` 是自治工作区（Autonomous Workspace）。

它至少必须包含以下三层：

1. `Surface Runtime`
2. `Surface Executor`
3. `Surface UI`

其中：

- `Runtime` 负责接入宿主协议、接收动作、回报状态
- `Executor` 是最小执行单元，哪怕只是一个简单 JS 逻辑也算
- `UI` 是工作区对用户和宿主呈现的可视界面

`Surface` 内部是否还存在额外 worker、agent、operation graph、artifact pipeline，均为可选实现，不进入统一规范。

### 2.3 `Surface Action`

`Surface Action` 是 `surface` 向外注册的、允许 `page` 调用的标准动作集合。

原则：

- 动作列表只由 `surface` 自己定义
- `page` 只能调用已注册动作
- `surface` 必须允许 `page` 调用已注册动作
- `surface` 内部如何处理动作，完全自治

### 2.4 `Host Action`

`Host Action` 是 `page` 向 `surface` 暴露的标准宿主能力。

原则：

- 可用 `host actions` 列表只由 `page` 定义
- `surface` 只能调用已声明允许的 `host actions`
- `host action` 的执行主体是 `page`
- `host action` 的业务后果由 `page` 负责返回结果

### 2.5 `Surface Caller`

`Surface Caller` 是 `surface` 调用 Hub tools 时的正式调用身份。

目标语义：

- `caller.type = surface`
- `caller.user_id = <workspace owner>`
- `caller.surface_id = <surface runtime id>`

`surface` 访问文件、数据库、共享记录、其他 service tool 时，必须依赖这个正式身份，而不是依赖前端显示用的裸用户信息。

---

## 3. 三个平面

整个系统分为三个稳定平面。

### 3.1 治理平面

治理平面通过 Hub / `surface_manager` 工作，负责：

- `surface package` 扫描与 catalog
- enable / disable
- session token 发放
- runtime 元数据查询
- package 文件读写
- 日志查询

当前可见治理工具包括：

- `ui.surface.catalog_list`
- `ui.surface.get`
- `ui.surface.enable_set`
- `ui.surface.session_issue`
- `ui.surface.runtime_status`
- `ui.surface.logs_query`
- `ui.surface.package_*`

治理平面回答的是：

> 这个工作区是什么、能不能被加载、属于谁、包文件在哪里、当前是否可用。

### 3.2 运行平面

运行平面通过 `iframe + postMessage + MessageChannel` 工作，负责：

- 宿主与工作区连接
- 动作注册
- 宿主动作调用
- 宿主能力回调
- 状态回报
- 流式消息
- 关闭与异常处理

运行平面回答的是：

> 这个工作区现在如何与宿主交互、如何接收指令、如何回报状态。

### 3.3 工具平面

工具平面通过 Hub 统一工具入口工作，负责：

- `surface` 以正式 `surface caller` 身份调用工具
- 持久化文件、数据库、共享记录
- 使用 `autogui`、LLM、图像、外部服务等平台能力

工具平面回答的是：

> 这个工作区如何使用平台能力完成真正的工作。

**硬约束**：

- `surface` 访问平台能力的正式方式应是调用 Hub tools
- 文件操作只是工具平面中的一种能力，不应单独成为系统的唯一标准路径
- `surfacefs_request` 如保留，只能视为历史兼容或过渡机制，不应作为长期主协议

---

## 4. 统一生命周期

统一规范只定义 `surface runtime lifecycle`，不定义内部 worker 生命周期。

### 4.1 Runtime 生命周期

建议的统一生命周期状态：

- `catalogued`
- `enabled`
- `opening`
- `connected`
- `registered`
- `ready`
- `idle`
- `busy`
- `error`
- `closing`
- `closed`

语义要求：

- `registered` 表示工作区已向宿主完成动作注册与基础信息声明
- `ready` 表示工作区已完成初始化，允许动作调用
- `idle` 表示工作区可服务且当前无进行中的主任务
- `busy` 表示工作区当前存在进行中的主任务或长耗时动作
- `closed` 表示 runtime 已结束，不等于 package 被删除，不等于数据被删除

### 4.2 Workspace 生命周期

`Page` 还需要维护工作区级状态，这些状态不属于 `surface` 业务状态：

- `open`
- `focused`
- `frozen`
- `minimized`
- `maximized`
- `geometry.x`
- `geometry.y`
- `geometry.width`
- `geometry.height`
- `z_index`

语义要求：

- `runtime state` 由 `surface` 主报
- `workspace state` 由 `page` 维护
- 两者必须分层，不得混用

---

## 5. 连接与注册协议

运行平面推荐使用四阶段协议：

1. `surface_connect`
2. `surface_register`
3. `surface_register_ack`
4. `surface_ready`

### 5.1 `surface_connect`

方向：

- `page -> surface`

作用：

- 建立 transport 上的会话语义
- 告知工作区自身身份与初始环境

建议字段：

```json
{
  "type": "surface_connect",
  "request_id": "conn-xxx",
  "surface_id": "surface_xxx",
  "surface_type": "custom",
  "surface_version": "1.0",
  "surface_session_token": "sst_...",
  "page_info": {
    "page_id": "chat",
    "page_type": "host",
    "page_version": "1.0"
  },
  "workspace_state": {
    "open": true,
    "focused": true,
    "frozen": false,
    "minimized": false,
    "maximized": false,
    "geometry": {
      "x": 120,
      "y": 60,
      "width": 960,
      "height": 640
    },
    "z_index": 10
  }
}
```

说明：

- `surface_session_token` 是 `surface caller` 身份的基础
- `page_info` 用于让工作区知晓宿主类型与宿主能力版本
- `workspace_state` 是宿主视角，不是 `surface` 自己的业务状态

### 5.2 `surface_register`

方向：

- `surface -> page`

作用：

- 明确声明工作区身份与对外能力
- 完成动作注册

建议字段：

```json
{
  "type": "surface_register",
  "request_id": "reg-xxx",
  "surface": {
    "surface_id": "surface_xxx",
    "surface_type": "custom",
    "surface_version": "1.0",
    "title": "Example Surface",
    "description": "workspace description"
  },
  "actions": [
    {
      "name": "get_state",
      "description": "读取当前状态",
      "args_schema": {},
      "result_schema": {},
      "timeout_ms_default": 3000,
      "side_effect": "none",
      "streaming": "none"
    }
  ],
  "initial_state": {
    "lifecycle_status": "starting",
    "business_state": {},
    "visible_text": ""
  }
}
```

硬约束：

- `actions` 必须位于顶层
- 不得把运行期动作注册依赖到静态 package manifest 中
- `surface` 至少应注册 `get_state`

### 5.3 `surface_register_ack`

方向：

- `page -> surface`

作用：

- 确认注册成功
- 向工作区返回宿主可提供的 `host actions`
- 向工作区返回宿主的关键运行信息

建议字段：

```json
{
  "type": "surface_register_ack",
  "request_id": "reg-ack-xxx",
  "ok": true,
  "page_info": {
    "page_id": "chat",
    "page_type": "host",
    "page_version": "1.0",
    "runtime_protocol_version": "1"
  },
  "host_actions": [
    {
      "name": "host.toast",
      "description": "显示页面提示",
      "args_schema": {
        "message": "string",
        "level": "string"
      },
      "result_schema": {
        "shown": "boolean"
      }
    }
  ],
  "workspace_state": {
    "open": true,
    "focused": true,
    "frozen": false,
    "minimized": false,
    "maximized": false
  }
}
```

### 5.4 `surface_ready`

方向：

- `surface -> page`

作用：

- 表示执行器与界面已完成初始化
- 从此允许 `page` 调用已注册动作

建议字段：

```json
{
  "type": "surface_ready",
  "request_id": "ready-xxx",
  "ready": true,
  "state": {
    "lifecycle_status": "idle",
    "business_state": {},
    "visible_text": "",
    "state_version": 1,
    "updated_at_ms": 0
  }
}
```

---

## 6. 双向动作体系

### 6.1 `surface actions`

`Surface Actions` 是工作区向宿主注册的动作。

建议描述符字段：

- `name`
- `description`
- `args_schema`
- `result_schema`
- `timeout_ms_default`
- `side_effect`
- `streaming`

`streaming` 建议值：

- `none`
- `surface_to_page`
- `page_to_surface`
- `duplex`

### 6.2 `host actions`

`Host Actions` 是宿主向工作区暴露的动作。

建议分两类。

#### 宿主交互类

- `host.toast`
- `host.open_link`
- `host.focus_surface`
- `host.open_surface`
- `host.close_surface`
- `host.request_user_attention`

#### 宿主管理类

- `host.workspace.set_title`
- `host.workspace.set_badge`
- `host.workspace.set_geometry_hint`
- `host.workspace.request_refresh`
- `host.session.refresh`

说明：

- `host actions` 的白名单由 `page` 定义
- `surface` 只能调用白名单内动作
- `host action` 的返回结果由 `page` 明确回传

### 6.3 `host_action_call`

方向：

- `surface -> page`

作用：

- 请求宿主执行一个 `host action`

建议字段：

```json
{
  "type": "host_action_call",
  "call_id": "host-xxx",
  "action": {
    "name": "host.toast",
    "args": {
      "message": "任务已完成",
      "level": "info"
    }
  }
}
```

### 6.4 `host_action_result`

方向：

- `page -> surface`

作用：

- 返回 `host action` 执行结果

建议字段：

```json
{
  "type": "host_action_result",
  "call_id": "host-xxx",
  "ok": true,
  "result": {
    "shown": true
  },
  "error": ""
}
```

---

## 7. 标准状态协议

### 7.1 `state_change`

方向：

- `surface -> page`

作用：

- 上报当前工作区状态事实

建议字段：

```json
{
  "type": "state_change",
  "surface_id": "surface_xxx",
  "event_type": "state_change",
  "state": {
    "lifecycle_status": "busy",
    "business_state": {
      "count": 3
    },
    "visible_text": "3",
    "state_version": 7,
    "updated_at_ms": 0
  }
}
```

硬约束：

- `state_change` 默认应发送完整状态快照，而不是不稳定 patch
- `lifecycle_status` 推荐至少覆盖 `idle`、`busy`、`error`
- `business_state` 是业务层自治状态
- `visible_text` 是给宿主快速概览的摘要

### 7.2 `action_call`

方向：

- `page -> surface`

建议字段：

```json
{
  "type": "action_call",
  "action": {
    "id": "act-xxx",
    "name": "increment",
    "args": {
      "step": 1
    },
    "timeout_ms": 5000
  }
}
```

### 7.3 `action_result`

方向：

- `surface -> page`

建议字段：

```json
{
  "type": "action_result",
  "action_id": "act-xxx",
  "action_name": "increment",
  "ok": true,
  "status": "ok",
  "result": {
    "changed": true
  },
  "error": "",
  "state": {
    "lifecycle_status": "idle",
    "business_state": {
      "count": 4
    },
    "visible_text": "4",
    "state_version": 8,
    "updated_at_ms": 0
  }
}
```

硬约束：

- `action_result` 回答的是一次动作执行事实
- `state_change` 回答的是当前工作区状态事实
- 两者不能混同

---

## 8. 流式协议设计

### 8.1 基本判断

`iframe` 与宿主页之间完全可以流式传递数据。

原因：

- `MessageChannel` 本质就是双向异步消息通道
- 只要把“流”拆成一系列有序消息帧，协议上天然支持增量传输

因此：

- 流式不是浏览器层面的阻碍
- 真正要设计的是帧结构、序号、结束语义与错误语义

### 8.2 建议的统一流式帧

推荐引入统一流协议：

- `stream_open`
- `stream_chunk`
- `stream_end`
- `stream_error`

建议字段：

```json
{
  "type": "stream_chunk",
  "stream_id": "str-xxx",
  "channel": "action_result",
  "seq": 3,
  "payload": {
    "text": "partial data"
  }
}
```

### 8.3 适用场景

流式协议适用于：

- 长文本生成
- 长日志输出
- 任务进度推送
- 图片处理阶段回报
- 长时操作观察
- 页面向工作区或工作区向页面的连续事件流

### 8.4 设计要求

- 单个流必须有稳定 `stream_id`
- 每个 chunk 必须有 `seq`
- 必须允许显式结束与显式错误
- 必须允许宿主超时关闭
- 流式动作是否支持，必须由 action 描述符 `streaming` 声明

---

## 9. `Surface Caller` 与用户信息

### 9.1 基本原则

`Surface` 不应把“裸用户信息”当作权限依据。

正确方式是：

- `page` 以用户身份向 Hub 申请 `surface_session_token`
- `page` 在连接阶段把 token 传给 `surface`
- `surface` 使用该 token 作为自己的正式调用身份

### 9.2 推荐链路

1. 用户已在 `page` 中登录
2. `page` 调 `ui.surface.session_issue(surface_id)`
3. Hub 发放 `surface_session_token`
4. `page` 把 token 作为 `surface_connect` 的一部分交给 `surface`
5. `surface` 调用 Hub tools 时携带该 token
6. Hub 恢复：
   - `caller.type = surface`
   - `caller.user_id = owner`
   - `caller.surface_id = runtime`
7. 下游 service 按 `surface scope` 处理数据库和文件

### 9.3 展示信息与权限信息分离

如果 `surface` 需要显示用户昵称、头像、偏好等信息：

- 应由宿主显式提供 `viewer_profile`
- 仅供界面展示
- 不作为工具调用权限依据

权限判断必须只认正式 `surface caller`。

### 9.4 本规范的前提假设

本文假定 Hub 已补齐以下能力：

- `X-Surface-Token` 或等价机制的正式校验
- 恢复 `caller.type=surface`
- 恢复 `caller.user_id`
- 恢复 `caller.surface_id`

现有 `sql_db` 与 `file_storage` 已能消费 `surface` scope，这证明目标方向正确；规范层面应继续沿这条线推进，而不是退回到 page 代做文件请求。

---

## 10. 工具平面访问原则

### 10.1 正式原则

`Surface` 使用平台能力的标准方式，应是调用 Hub tools。

包括但不限于：

- `storage.file.*`
- `storage.database.*`
- `storage.share.*`
- `autogui.*`
- `ai.*`
- 未来的其他业务 service tools

### 10.2 不推荐的长期模式

以下模式不应成为长期主协议：

- 为文件读写单独长期维护一套只属于 surface 的专用请求协议
- 让 `page` 持续代替 `surface` 执行业务工具调用
- 把“工作区调用平台工具”降级成宿主页私有 helper

### 10.3 推荐的统一封装

建议新增统一前端库：

- `webui/lib/surfaceTool.js`
- `webui/lib/pageSurfaceTool.js`

#### `surfaceTool.js`

供每个 `surface` 复用，至少统一提供：

- `connectRuntime()`
- `registerSurface()`
- `ready()`
- `emitStateChange()`
- `respondActionResult()`
- `callHostAction()`
- `callTool()`
- `openStream()`
- `sendStreamChunk()`
- `closeStream()`

#### `pageSurfaceTool.js`

供每个 `page` 复用，至少统一提供：

- `loadSurface()`
- `unloadSurface()`
- `connectSurface()`
- `waitSurfaceRegister()`
- `ackSurfaceRegister()`
- `waitSurfaceReady()`
- `callSurfaceAction()`
- `waitActionResult()`
- `handleHostActionCall()`
- `setWorkspaceState()`
- `bindSurfaceWindow()`
- `closeSurface()`

硬约束：

- 不允许每个 `surface` 自己手写一套连接、注册、工具调用逻辑
- 不允许每个 `page` 自己手写一套加载、等待、动作调用逻辑
- 统一库应放在 `webui/lib/` 下，作为跨页面与跨工作区的共享运行时基础设施

---

## 11. Package Manifest 与 Runtime Register 的边界

必须把这两者严格分开。

### 11.1 Package Manifest

静态 `manifest.json` 应只回答：

- 我是什么包
- 我的入口文件是什么
- 我的静态元数据是什么

它不应承担运行期注册职责。

### 11.2 Runtime Register

`surface_register` 应回答：

- 我当前 runtime 的身份是什么
- 我允许 page 调哪些 actions
- 我当前基础状态是什么

因此：

- `actions` 归 `surface_register`
- 不能把运行期 actions 的权威来源放在静态 `manifest.json`

---

## 12. 宿主实现要求

任何 `page`，无论是否带助理、是否只是试验场，都应遵守以下要求：

1. 不得在调用 `surface` 未注册动作时静默失败
2. 必须区分 `workspace state` 与 `runtime state`
3. 必须支持 `surface` 注册后的动作等待与超时处理
4. 必须对 `host actions` 实施白名单
5. 必须能够关闭、重载、冻结工作区
6. 必须记录工作区关键事件、错误与状态变化
7. 必须以统一库而非页面私有散装逻辑承载协议实现

`chat` 页面、`surface` 试验场页面、未来其他宿主页，都只是这个统一规范下的不同宿主实现。

---

## 13. `Surface` 实现要求

任何 `surface`，无论简单还是复杂，都应遵守以下要求：

1. 必须支持 `surface_connect`
2. 必须完成 `surface_register`
3. 必须在初始化完成后回 `surface_ready`
4. 必须至少提供 `get_state`
5. 必须把 `state_change` 作为统一状态回报机制
6. 必须把 `action_result` 作为统一动作完成回报机制
7. 必须把内部复杂度封装在自己内部，不把内部 worker 生命周期泄露成宿主协议负担
8. 若需要平台能力，应优先通过正式 Hub tool 平面访问

---

## 14. 重构优先级建议

若按本文推进，建议按以下优先级落地：

1. 统一 `surface caller` 身份链路
2. 新增 `webui/lib/surfaceTool.js`
3. 新增 `webui/lib/pageSurfaceTool.js`
4. 把连接协议收敛到 `connect/register/register_ack/ready`
5. 把 `actions` 注册收敛到顶层 runtime register
6. 把 `host_call` 全量改名为 `host_action_call`
7. 将 `host actions` 白名单化并在注册确认阶段下发
8. 将 `surfacefs_request` 退为兼容层或移除
9. 为流式动作补齐统一帧协议

---

## 15. 本规范的一句话总结

`Page` 是宿主，`Surface` 是自治工作区；二者通过统一运行协议协作，通过统一工具平面访问平台能力，并以正式 `surface caller` 身份完成持久化与执行。

