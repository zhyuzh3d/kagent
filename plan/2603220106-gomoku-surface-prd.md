# 五子棋 Surface 技术开发文档

## 文档信息

- 初稿时间：2026-03-22 01:06:41 CST
- 升级时间：2026-03-22 01:14:45 CST
- 文档类型：技术开发文档
- 文档目标：把五子棋 `surface` 从产品需求描述升级为可直接进入开发的技术说明
- 目标产物：`webui/surface/buildin/gomoku/` 目录下的五子棋页面与配套资源
- 依据文件：
  - `doc/_instruction.md`
  - `doc/_instruction/core.md`
  - `doc/_page-surface.md`
  - `webui/page/surface/components/runtime.js`
  - `webui/page/surface/lib/bridge.js`
  - `webui/page/surface/lib/action-dispatcher.js`
  - `webui/page/surface/lib/surface-manager.js`
  - `webui/page/surface/lib/manifest.js`
  - `webui/surface/buildin/counter/manifest.json`
  - `webui/surface/buildin/counter/index.html`
  - `services/surface_manager/internal/app/surface_catalog.go`
  - `services/surface_manager/internal/app/surface_package.go`

---

## 1. 开发目标

本需求的直接开发目标不是抽象游戏设计，而是交付一个可被当前 `page-surface` 宿主装载的内建 `surface` 包。

必须满足：

1. 页面主入口位于 `webui/surface/buildin/gomoku/`。
2. 该 `surface` 能在现有宿主页中被扫描、装载、建立消息通道并完成一局本地双人五子棋。
3. 该 `surface` 提供足够明确的 `surface actions`，使宿主页或其他调用方无需解析 DOM 即可完成落子与棋盘状态感知。
4. 在视觉上保持极简，但达到可作为 `surface` 示例的高完成度 UI 水平。

本轮不做：

1. 联网对战。
2. AI 对手。
3. 历史战绩持久化。
4. Hub tool 调用、文件读写或数据库接入。

## 2. 当前实现事实与可行性边界

以下事实已由代码确认：

1. `services/surface_manager/internal/app/surface_catalog.go` 当前扫描 `surface` 包时，正式读取的是包目录下 `manifest.json`，字段体系是 `id`、`name`、`version`、`min_supported_version`、`entry`、`desc`、`tags`、`permissions`。
2. `webui/page/surface/components/runtime.js` 与 `webui/page/surface/lib/surface-manager.js` 当前会向 `iframe` 发送 `surface_connect`，并附带 `surface_id`、`surface_type`、`surface_version`、`session_token`。
3. `webui/page/surface/lib/bridge.js` 当前真正支持的宿主回调消息名是 `host_call` / `host_call_result`，不是 `_page-surface.md` 目标规范中的 `host_action_call` / `host_action_result`。
4. `webui/surface/buildin/counter/index.html` 当前真实跑通的 `surface` 侧消息模式是 `surface_actions`、`surface_ready`、`state_change`、`action_result`，而不是完整的 `surface_register -> surface_register_ack -> surface_ready` 四阶段实现。
5. `webui/page/surface/lib/action-dispatcher.js` 当前已经支持 `surface.call.<surface_id>.<action_name>` 形式的宿主动作分发，因此五子棋只要正确注册动作，即可被宿主调用。

结论：

1. 五子棋 `surface` 的 `v1` 不需要新增后端能力，技术上可直接落地。
2. 但开发文档必须区分“目标协议”与“当前兼容实现”，否则会出现文档正确、运行不通的情况。

## 3. 开发策略

### 3.1 总体策略

`v1` 采用“当前宿主兼容优先，结构上向目标协议对齐”的策略：

1. 包结构和 `manifest.json` 先严格兼容当前 `surface_manager` 扫描逻辑。
2. 运行时消息协议先严格兼容当前宿主页已消费的消息名和字段。
3. 内部代码组织按 `_page-surface.md` 的 `Runtime / Executor / UI` 三层写，方便后续升级到 `surface_register` 规范。

### 3.2 兼容性原则

`v1` 必须直接兼容当前宿主页：

1. 接收 `surface_connect`。
2. 输出 `surface_actions`。
3. 输出 `surface_ready`。
4. 输出 `state_change`。
5. 输出 `action_result`。
6. 如需宿主辅助提示，仅调用当前桥接层已支持的 `host_call`。

同时保留未来迁移空间：

1. 内部实现应封装注册阶段，不把 `surface_actions` 写死在页面各处。
2. 内部实现应封装宿主调用层，后续可从 `host_call` 平滑迁移到 `host_action_call`。
3. 状态快照结构应尽量贴近 `_page-surface.md` 的 `state` 语义，即使当前宿主未完全消费。

## 4. 交付目录与文件职责

建议交付目录：

1. `webui/surface/buildin/gomoku/manifest.json`
2. `webui/surface/buildin/gomoku/index.html`
3. `webui/surface/buildin/gomoku/style.css`
4. `webui/surface/buildin/gomoku/app.js`

说明：

1. `manifest.json` 是当前正式包入口描述，供 `surface_manager` catalog 扫描。
2. `index.html` 是页面入口，只负责挂载容器与引入资源。
3. `style.css` 负责视觉系统、布局、棋盘、棋子、反馈态。
4. `app.js` 负责 runtime 协议、状态机、动作分发、渲染编排。

若实现时倾向单文件，也允许把 `style.css` 和 `app.js` 内联回 `index.html`；但逻辑职责仍应按上述分层设计。

## 5. 包描述规范

### 5.1 `manifest.json`

由于当前 `surface_manager` 的正式扫描结构仍是旧字段体系，`manifest.json` 必须按现有结构编写。

建议格式：

```json
{
  "id": "gomoku",
  "name": "Gomoku",
  "version": "1.0",
  "min_supported_version": "1.0",
  "entry": "index.html",
  "desc": "极简且优雅的本地双人五子棋 surface",
  "tags": ["buildin", "game", "gomoku"],
  "permissions": {
    "sandbox": ["allow-scripts", "allow-downloads"],
    "allow": []
  }
}
```

约束：

1. `entry` 必须指向包内可访问页面。
2. `permissions` 保持最小权限，不申请额外能力。
3. `v1` 不声明任何工具平面权限。

### 5.2 页面内运行态元信息

虽然包级扫描依赖 `manifest.json`，但为兼容 `webui/page/surface/lib/manifest.js` 的泛 URL 装载逻辑，建议在 `index.html` 内额外内嵌一份运行态 manifest 片段。

建议字段：

1. `surface_id`
2. `surface_type`
3. `surface_version`
4. `title`
5. `description`
6. `permissions`
7. `actions`

这不是当前 buildin 包扫描的正式来源，但能提升页面级调试与通用装载兼容性。

## 6. 技术架构

五子棋 `surface` 内部按三层实现：

### 6.1 Runtime 层

职责：

1. 接收 `surface_connect`。
2. 管理 `MessagePort`。
3. 统一发送 `surface_actions`、`surface_ready`、`state_change`、`action_result`。
4. 统一处理宿主发来的 `action_call`。
5. 提供可选 `host_call` 封装。

### 6.2 Executor 层

职责：

1. 维护棋局状态单一事实源。
2. 处理 `new_game`、`place_stone`、`get_state`、`get_cell_state`。
3. 完成落子合法性校验。
4. 完成连五判定与和局判定。

### 6.3 UI 层

职责：

1. 渲染棋盘、棋子、当前状态、最后一步、结果与按钮。
2. 把本地点击转换为统一动作调用，不直接绕过 Executor 修改状态。
3. 根据状态快照进行完整重渲染或局部更新。

## 7. 状态模型

### 7.1 单一事实源

内部必须维护一个统一状态对象，禁止把 UI DOM 作为真实状态来源。

建议状态结构：

```json
{
  "board_size": 15,
  "phase": "playing",
  "current_player": "black",
  "move_count": 0,
  "last_move": null,
  "winner": "",
  "winning_line": [],
  "board": [[0]],
  "state_version": 1,
  "updated_at_ms": 0
}
```

字段定义：

1. `board_size`：固定为 `15`。
2. `phase`：`playing` / `won` / `draw`。
3. `current_player`：`black` / `white`。
4. `move_count`：当前总手数。
5. `last_move`：最近一步，包含 `row`、`col`、`player`。
6. `winner`：`black` / `white` / `""`。
7. `winning_line`：胜利五子路径坐标数组。
8. `board`：`15 x 15` 二维数组，空位为 `0`，黑子为 `1`，白子为 `2`。
9. `state_version`：每次状态变化递增。
10. `updated_at_ms`：最近一次变更时间戳。

### 7.2 规则约束

1. `board` 初始化时必须是完整 `15 x 15` 矩阵。
2. 只有 `phase=playing` 时允许 `place_stone`。
3. 非空坐标禁止重复落子。
4. 每次成功落子后必须先判胜，再判和，再切换回合。
5. 任一失败动作不得污染状态对象。

## 8. Surface Actions 设计

该 `surface` 必须提供足够丰富的动作集合，覆盖“棋子操作”和“棋盘状态感知”两类能力。

### 8.1 必需动作

1. `get_state`
   - 入参：`{}`
   - 返回：完整棋局状态。
2. `new_game`
   - 入参：`{}`
   - 返回：是否已重置，以及重置后的完整状态。
3. `place_stone`
   - 入参：`{ "row": number, "col": number }`
   - 返回：是否成功落子、失败原因、落子后完整状态。
4. `get_cell_state`
   - 入参：`{ "row": number, "col": number }`
   - 返回：该格的占用情况、棋子颜色、是否为最后一步。

### 8.2 动作执行原则

1. 本地点击落子必须复用 `place_stone` 的核心执行逻辑。
2. `get_state` 返回的数据必须足够让宿主理解棋盘，不依赖截图或文案推测。
3. `get_cell_state` 不是视觉特效接口，而是宿主精确感知某个坐标状态的辅助接口。
4. 未知动作必须返回失败的 `action_result`。

### 8.3 建议返回格式

`place_stone` 建议返回：

```json
{
  "accepted": true,
  "reason": "",
  "placed": { "row": 7, "col": 7, "player": "black" },
  "phase": "playing",
  "winner": "",
  "state": {}
}
```

非法落子时：

```json
{
  "accepted": false,
  "reason": "cell_occupied",
  "state": {}
}
```

## 9. 运行时消息协议

### 9.1 当前必须兼容的握手流程

基于当前宿主页实现，五子棋 `surface` 必须按以下流程工作：

1. 页面监听 `window.message` 中的 `surface_connect`。
2. 从 `event.ports[0]` 取得 `MessagePort`。
3. 保存 `surface_id`、`surface_type`、`surface_version`、`session_token`。
4. 通道建立后先发送 `surface_actions`。
5. 初始化完成后发送 `surface_ready`。
6. 此后任何状态变化都发送 `state_change`。
7. 宿主发来 `action_call` 时，处理后发送 `action_result`。

### 9.2 与 `_page-surface.md` 的对齐策略

`_page-surface.md` 的目标规范是：

1. `surface_connect`
2. `surface_register`
3. `surface_register_ack`
4. `surface_ready`

但当前宿主页尚未正式消费 `surface_register` / `surface_register_ack`。因此 `v1` 采用以下折中方案：

1. 对外行为以当前兼容消息为准。
2. 代码中保留 `registerSurface()` 之类的封装函数，由它统一产出 `surface_actions` 和 `surface_ready`。
3. 后续当宿主页补齐 `surface_register_ack` 后，只修改 Runtime 层，不重写棋局逻辑和 UI。

### 9.3 建议消息结构

`surface_actions`：

```json
{
  "type": "surface_actions",
  "surface_id": "gomoku",
  "surface_type": "buildin",
  "surface_version": "1.0",
  "actions": [
    { "name": "get_state", "description": "读取完整棋局状态", "args_schema": {} },
    { "name": "new_game", "description": "开始新对局", "args_schema": {} },
    { "name": "place_stone", "description": "在指定坐标落子", "args_schema": { "row": "number", "col": "number" } },
    { "name": "get_cell_state", "description": "读取单格状态", "args_schema": { "row": "number", "col": "number" } }
  ]
}
```

`surface_ready`：

```json
{
  "type": "surface_ready",
  "surface_id": "gomoku",
  "actions": [],
  "state": {
    "lifecycle_status": "idle",
    "business_state": {},
    "visible_text": "黑方回合，第 0 手",
    "state_version": 1,
    "updated_at_ms": 0
  }
}
```

`state_change`：

```json
{
  "type": "state_change",
  "surface_id": "gomoku",
  "event_type": "move.placed",
  "state": {
    "lifecycle_status": "idle",
    "business_state": {},
    "visible_text": "白方回合，第 1 手",
    "state_version": 2,
    "updated_at_ms": 0
  },
  "business_state": {},
  "visible_text": "白方回合，第 1 手",
  "state_version": 2,
  "updated_at_ms": 0
}
```

说明：

1. 保留 `state` 是为了向 `_page-surface.md` 对齐。
2. 同时保留顶层 `business_state`、`visible_text`、`state_version` 是为了与当前 `counter` 示例风格兼容。

`action_result`：

```json
{
  "type": "action_result",
  "action_id": "act-1",
  "action_name": "place_stone",
  "ok": true,
  "status": "ok",
  "result": {
    "accepted": true
  },
  "error": "",
  "state": {
    "lifecycle_status": "idle",
    "business_state": {},
    "visible_text": "白方回合，第 1 手",
    "state_version": 2,
    "updated_at_ms": 0
  }
}
```

### 9.4 宿主能力调用

当前桥接层只正式支持 `host_call`。因此 `v1` 若需要宿主辅助提示，必须按当前协议调用：

```json
{
  "type": "host_call",
  "call_id": "host-1",
  "capability": "flash",
  "args": {
    "message": "黑方胜"
  }
}
```

约束：

1. `host_call` 只能做增强提示，不能成为主流程依赖。
2. 宿主不响应时，游戏仍必须完整可玩。

## 10. UI 技术要求

### 10.1 视觉原则

界面风格采用“现代东方感的极简棋具界面”，但要求技术上可稳定实现，不依赖复杂图像资源。

建议做法：

1. 背景使用轻微渐变或柔和纹理感纯 CSS 背景。
2. 棋盘使用 CSS Grid 绘制，避免引入 Canvas 以增加协议调试复杂度。
3. 棋子使用圆形 DOM 元素渲染，便于最后一步高亮和胜利线标记。
4. 全局颜色、间距、圆角、阴影统一收敛为 CSS variables。

### 10.2 布局结构

页面至少包含：

1. 顶部信息栏：标题、当前状态、重新开始按钮。
2. 棋盘容器：15 x 15 格点。
3. 底部信息栏：当前手数、最后一步、短提示。

约束：

1. 首屏必须完整显示棋盘主体。
2. 移动端允许压缩边距和字体，但不允许把棋盘挤出可视区域。
3. 不引入多余面板、日志区或调试控件到正式 UI。

### 10.3 反馈细节

必须实现：

1. 最近一步高亮。
2. 胜利五子路径高亮。
3. 落子出现的短促入场动画。
4. 非法点击的轻量反馈。

## 11. 开发阶段建议

### 11.1 第一阶段：棋局核心

完成项：

1. 初始化状态对象。
2. 落子逻辑。
3. 连五判定。
4. 和局判定。
5. `new_game`、`place_stone`、`get_state`、`get_cell_state`。

完成标准：

1. 在不接宿主协议的情况下，页面本地可完整走完一局。

### 11.2 第二阶段：Runtime 协议接入

完成项：

1. 接入 `surface_connect`。
2. 输出 `surface_actions`。
3. 输出 `surface_ready`。
4. 处理 `action_call`。
5. 输出 `state_change` 与 `action_result`。

完成标准：

1. 宿主页可通过标准动作控制五子棋 `surface`。

### 11.3 第三阶段：UI 打磨

完成项：

1. 统一视觉系统。
2. 调整响应式布局。
3. 增加最后一步与胜利路径反馈。
4. 处理非法点击与结束态提示。

完成标准：

1. 页面简洁、稳定、质感统一，达到正式示例级别。

## 12. 验收与验证

### 12.1 功能验收

必须通过：

1. `surface_manager` 能扫描到 `webui/surface/buildin/gomoku/manifest.json`。
2. 宿主页能成功打开该 `surface` 并完成 `surface_connect`。
3. 宿主页能看到声明的动作集合。
4. 宿主页能通过 `place_stone`、`new_game`、`get_state` 驱动该 `surface`。
5. 非法坐标、重复落子、终局后继续落子都不会破坏状态。

### 12.2 协议验收

必须通过：

1. 首次连通后能收到 `surface_ready`。
2. 每次成功落子后能收到新的 `state_change`。
3. 每次动作调用都能收到对应 `action_result`。
4. `get_state` 返回内容足够让宿主判断整个棋盘状态。

### 12.3 体验验收

必须通过：

1. 首屏视觉焦点明确，棋盘是唯一核心主体。
2. 最近一步、当前回合、胜利结果都易于理解。
3. 无需阅读说明，用户也能直接开始对局。

## 13. 风险与约束

主要风险：

1. `_page-surface.md` 的目标协议与当前宿主页实现尚未完全一致。
2. 当前桥接层正式支持的是 `host_call`，不是目标规范中的 `host_action_call`。
3. 当前 buildin 包扫描使用的仍是包级 `manifest.json` 旧字段体系，不应误写成页面级 manifest 结构。

对应策略：

1. `v1` 先保证当前可运行，再为目标协议保留封装边界。
2. 不在本需求中引入后端改造。
3. 不在本需求中引入工具平面、持久化或权限复杂度。

---

本文件的结论是：五子棋 `surface` 完全可以在现有仓库中以低风险方式实现，前提是开发遵循“包结构兼容当前扫描逻辑，消息协议兼容当前宿主页，内部架构向 `_page-surface.md` 目标协议对齐”的原则。
