# `autogui` RobotGo 能力扩展开发计划

- 文档类型：开发计划（DevPlan）
- 创建时间：2026-03-22 23:04:45 CST
- 范围：
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.mod`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.sum`
  - 如需同步项目说明，后续实施完成后最小必要更新 `doc/_instruction/structure.md` 与 `doc/_devlog.md`
- 目标：
  - 在保留 `autogui` 作为桌面自动化 service 的前提下，系统性补齐 `robotgo v1.0.1` 的高层能力覆盖面
  - 把零散的底层 API 能力整合成少量高价值、可组合、可治理的工具
  - 明确提供按窗口名称截图的正式工具能力
  - 保持与当前已上线工具 ID 的兼容，不做破坏式替换

---

## 0. 计划结论

本轮不应新建 service，也不应把 `robotgo` 的每个 Go 函数机械映射成一个工具；正确方向是继续扩展现有 `autogui`，把 `robotgo` 的高层能力收敛为一组稳定、可理解、覆盖面高的工具平面。

本计划的核心结论如下：

1. `autogui` 应继续作为唯一桌面自动化 service，避免再拆出新的桌面控制子 service。
2. 工具面应按“用户任务语义”组织，而不是按 `robotgo` 的单函数组织；重点工具面是显示器信息、颜色采样、鼠标、键盘、剪贴板、窗口、截图、进程、OCR。
3. 当前已存在的 `autogui.mouse.move`、`autogui.mouse.click`、`autogui.mouse.scroll`、`autogui.keyboard.type`、`autogui.keyboard.press`、`autogui.screen.capture`、`autogui.screen.capture_region`、`autogui.text.insert` 不应删除；应作为兼容入口保留，并逐步把实现下沉到统一执行层。
4. 新增能力必须优先以“整合型工具”进入工具平面，避免出现几十个碎片化工具。
5. 窗口截图必须提供正式的一等工具，且支持按窗口名称匹配、同名多窗口消歧、窗口区与客户区二选一。
6. `robotgo` 的底层句柄/bitmap/pointer/X11-特定接口不纳入工具面；这类接口仅保留在内部实现层，不对上层 caller 暴露。
7. 当前 `services/autogui/cmd/autogui/main.go` 已经承担 manifest、HTTP、解析、执行、截图编码、注册心跳等全部职责；若继续直接堆叠新工具，文件会迅速失控，因此本轮扩展必须伴随最小必要的内部模块化重构。

---

## 1. 已核验事实

以下结论可由当前仓库与本地依赖源码直接核验：

1. 当前 `autogui` 只提供鼠标移动、点击、滚动、键盘输入、按键、文本插入、全屏截图、区域截图，以及 `service.lifecycle.*`。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/cmd/autogui/main.go`
2. 当前 `autogui` 对 `robotgo` 的实际调用非常有限，核心只覆盖 `Move`、`Click`、`ScrollDir`、`TypeStr`、`KeyTap`、`WriteAll`、`CaptureScreen`、`FreeBitmap`、`ToImage`。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/cmd/autogui/main.go`
3. 项目当前锁定的桌面自动化依赖为 `github.com/go-vgo/robotgo v1.0.1`。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.mod`
4. `robotgo v1.0.1` 的高层能力不止输入与截图，还覆盖了显示器信息、像素取色、平滑移动、拖拽、鼠标按下抬起、多次点击、键盘按下抬起、剪贴板读写、窗口查询与控制、进程枚举与终止、OCR、提示框等。依据：
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/robotgo.go`
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/key.go`
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/ps.go`
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/screen.go`
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/img.go`
5. `FindIds(name)` 的语义是“大小写不敏感的子串匹配”，这意味着“按窗口名定位”天然会遇到多结果问题，工具层必须定义明确的消歧规则。依据：
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/ps.go`
6. `GetBounds(pid)` / `GetClient(pid)` 能提供窗口外框与客户区坐标，足以实现“按窗口名称截图”。依据：
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/robotgo_mac_win.go`
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/robotgo_x11.go`
7. `robotgo` 在 Linux 开源版依赖 X11；Wayland 支持不在当前开源版本承诺范围内。后续实现不得把 Wayland 当成已覆盖平台写入验收标准。依据：
   - `/Users/zhyuzh/go/pkg/mod/github.com/go-vgo/robotgo@v1.0.1/README.md`
8. 当前 service 已按标准生命周期接入 Hub，因此扩展工具面不需要改变平台治理模型；重点是补齐工具 manifest 与执行分发。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`

---

## 2. 设计目标与非目标

### 2.1 设计目标

1. 用尽量少的工具数量，覆盖 `robotgo` 几乎全部高层能力。
2. 工具入参与出参统一、稳定、可扩展，不把调用方绑死在某个底层 OS API 细节上。
3. 新旧能力共存，避免对已接入 caller 造成破坏。
4. 所有“状态读取类工具”都应返回结构化结果，而不是仅返回单值。
5. 窗口、截图、颜色、进程、OCR 等跨功能链路都应支持组合使用。
6. 对不稳定或平台差异大的能力，在工具层显式暴露平台限制和错误分类。

### 2.2 非目标

1. 不暴露 `HWND`、`XID`、bitmap 指针、C 结构体、颜色转换辅助函数等底层接口。
2. 不把 `robotgo` 的废弃别名逐个暴露成独立工具，例如 `TypeStr`、`PasteStr`、`MoveMouse`、`MouseClick`、`ClickV1`。
3. 不把全局 `SetDelay` 这种进程级可变状态作为主要控制方式；调用级参数优先于全局 mutable 配置。
4. 不承诺 Wayland、系统级沙箱权限弹窗处理等超出 `robotgo` 当前开源稳定面的能力。
5. 不在本轮把 `autogui` 改造成浏览器语义控制 service；浏览器控制仍属于独立的 `chrome_control` 范畴。

---

## 3. 目标工具面

本轮建议的“正式工具面”如下。为避免重复，部分当前工具会升级语义或保留为兼容别名。

### 3.1 显示与颜色

#### 3.1.1 `autogui.display.info.get`

用途：一次性读取显示器与主屏信息。

建议入参：

- `display_ids?: number[]`
- `include_scale?: boolean`
- `include_rect?: boolean`

建议出参：

- `main_display_id`
- `display_count`
- `screen_size`
- `displays[]`
  - `display_id`
  - `is_main`
  - `bounds`
  - `rect`
  - `sys_scale`
  - `scaled_size`

覆盖能力：

- `GetScreenSize`
- `DisplaysNum`
- `GetMainId`
- `GetDisplayBounds`
- `GetDisplayRect`
- `GetScreenRect`
- `GetScaleSize`
- `SysScale`
- `IsMain`

#### 3.1.2 `autogui.screen.color.get`

用途：读取当前鼠标位置颜色或指定坐标颜色。

建议入参：

- `use_cursor?: boolean`
- `x?: number`
- `y?: number`
- `display_id?: number`
- `points?: {x:number,y:number,display_id?:number}[]`

建议出参：

- `cursor_position`
- `samples[]`
  - `x`
  - `y`
  - `display_id`
  - `hex`
  - `raw_hex`

覆盖能力：

- `GetPixelColor`
- `GetPxColor`
- `GetLocationColor`
- 与 `Location` 联动

### 3.2 鼠标

#### 3.2.1 `autogui.mouse.position.get`

用途：读取当前鼠标位置。

覆盖能力：

- `Location`

#### 3.2.2 `autogui.mouse.move`

用途：统一绝对移动、相对移动、平滑移动。

建议入参：

- `x`
- `y`
- `mode: "absolute" | "relative"`
- `smooth?: boolean`
- `display_id?: number`
- `smooth_low?: number`
- `smooth_high?: number`
- `mouse_delay_ms?: number`

覆盖能力：

- `Move`
- `MoveSmooth`
- `MoveRelative`
- `MoveSmoothRelative`

兼容策略：

- 保留现有 `autogui.mouse.move(x,y)` 语义不变
- 新参数全部为向后兼容扩展

#### 3.2.3 `autogui.mouse.button`

用途：统一点击、双击、多击、按下、抬起，以及可选的“先移动再点击”。

建议入参：

- `action: "click" | "double_click" | "multi_click" | "down" | "up" | "move_click"`
- `button?: "left" | "right" | "center" | "wheelUp" | "wheelDown" | "wheelLeft" | "wheelRight"`
- `count?: number`
- `x?: number`
- `y?: number`
- `smooth_before?: boolean`

覆盖能力：

- `Click`
- `MultiClick`
- `Toggle`
- `MouseDown`
- `MouseUp`
- `MoveClick`
- `MovesClick`

说明：

- 当前 `autogui.mouse.click` 保留，内部可转调 `autogui.mouse.button`

#### 3.2.4 `autogui.mouse.drag`

用途：拖拽鼠标到指定位置。

建议入参：

- `to_x`
- `to_y`
- `from_x?: number`
- `from_y?: number`
- `mode?: "absolute" | "relative"`
- `button?: "left" | "right" | "center"`
- `smooth?: boolean`

覆盖能力：

- 实用上覆盖 `DragSmooth`
- 兼容吸收旧 `Drag`

#### 3.2.5 `autogui.mouse.scroll`

用途：统一方向滚动、XY 滚动、平滑滚动。

建议入参：

- `mode: "direction" | "xy" | "smooth" | "relative"`
- `direction?: "up" | "down" | "left" | "right"`
- `amount?: number`
- `x?: number`
- `y?: number`
- `steps?: number`
- `step_delay_ms?: number`

覆盖能力：

- `Scroll`
- `ScrollDir`
- `ScrollSmooth`
- `ScrollRelative`

兼容策略：

- 现有 `autogui.mouse.scroll(amount,direction)` 保持可用

### 3.3 键盘与剪贴板

#### 3.3.1 `autogui.keyboard.key`

用途：统一按键 tap、press、down、up、toggle。

建议入参：

- `action: "tap" | "press" | "down" | "up" | "toggle"`
- `key`
- `modifiers?: string[]`
- `target_pid?: number`
- `toggle_state?: "down" | "up"`

覆盖能力：

- `KeyTap`
- `KeyPress`
- `KeyDown`
- `KeyUp`
- `KeyToggle`

兼容策略：

- 现有 `autogui.keyboard.press` 保留，内部路由到该统一实现

#### 3.3.2 `autogui.keyboard.text`

用途：统一文本输入、延时输入、优先粘贴、Unicode 输入、可选清空与提交。

建议入参：

- `text`
- `mode: "type" | "type_delay" | "paste" | "paste_preferred" | "unicode"`
- `delay_ms?: number`
- `clear_before?: boolean`
- `submit?: boolean`
- `target_pid?: number`

覆盖能力：

- `Type`
- `TypeDelay`
- `TypeStr`
- `TypeStrDelay`
- `UnicodeType`
- `Paste`
- `CmdV`
- 现有 `text.insert`

兼容策略：

- 现有 `autogui.keyboard.type` 与 `autogui.text.insert` 均保留
- 新实现统一下沉到同一文本执行层

#### 3.3.3 `autogui.clipboard`

用途：读写剪贴板文本。

建议入参：

- `action: "get" | "set" | "clear"`
- `text?: string`

覆盖能力：

- `ReadAll`
- `WriteAll`

### 3.4 截图与 OCR

#### 3.4.1 `autogui.screen.capture`

用途：统一全屏、指定显示器、区域截图。

建议入参：

- `target: "screen" | "display" | "region"`
- `display_id?: number`
- `x?: number`
- `y?: number`
- `width?: number`
- `height?: number`
- `format?: "png" | "jpeg"`
- `quality?: number`
- `save_path?: string`
- `return_base64?: boolean`

覆盖能力：

- `CaptureScreen`
- `CaptureImg`
- `Capture`
- `SaveCapture`
- 当前 `autogui.screen.capture`
- 当前 `autogui.screen.capture_region`

说明：

- 现有两个截图工具建议继续保留，但文档中明确 `autogui.screen.capture` 为统一主工具

#### 3.4.2 `autogui.window.capture`

用途：按窗口名称或 PID 截取指定窗口图像。

建议入参：

- `window_name?: string`
- `pid?: number`
- `match_index?: number`
- `require_single_match?: boolean`
- `area?: "window" | "client"`
- `activate_first?: boolean`
- `format?: "png" | "jpeg"`
- `quality?: number`
- `save_path?: string`
- `return_base64?: boolean`

建议实现链路：

1. 按 `pid` 或 `window_name` 查找目标窗口
2. `window_name` 走 `FindIds(name)`，若多结果：
   - `require_single_match=true` 时直接报错并返回候选列表
   - 否则按 `match_index` 选中
3. `area=window` 时用 `GetBounds(pid)`，`area=client` 时用 `GetClient(pid)`
4. 如 `activate_first=true`，先 `ActivePid(pid)` 再短暂等待
5. 通过 `CaptureScreen(x,y,w,h)` 截图并按目标格式编码

覆盖能力：

- `FindIds`
- `GetBounds`
- `GetClient`
- `ActivePid`
- `CaptureScreen`

这是一项正式验收能力，不可只做临时脚本能力。

#### 3.4.3 `autogui.ocr.extract`

用途：对图片、区域截图、窗口截图做 OCR。

建议入参：

- `source_type: "image_path" | "base64" | "region" | "window"`
- `image_path?: string`
- `base64_data?: string`
- `region?: {x:number,y:number,width:number,height:number,display_id?:number}`
- `window_name?: string`
- `pid?: number`
- `match_index?: number`
- `lang?: string`

建议出参：

- `text`
- `lang`
- `source_meta`

覆盖能力：

- `GetText`

说明：

- OCR 依赖本机 Tesseract 或对应 build/tag 方案，必须在工具错误里明确提示依赖缺失

### 3.5 窗口

#### 3.5.1 `autogui.window.info.get`

用途：统一查询活动窗口、指定 PID 窗口、按名称匹配窗口列表。

建议入参：

- `target: "active" | "pid" | "name"`
- `pid?: number`
- `window_name?: string`
- `include_path?: boolean`
- `include_bounds?: boolean`
- `include_client_bounds?: boolean`

建议出参：

- `windows[]`
  - `pid`
  - `title`
  - `process_name`
  - `process_path`
  - `bounds`
  - `client_bounds`
  - `is_active`

覆盖能力：

- `GetTitle`
- `GetPid`
- `FindIds`
- `FindName`
- `FindPath`
- `GetBounds`
- `GetClient`

#### 3.5.2 `autogui.window.control`

用途：统一窗口激活、最小化、最大化、关闭。

建议入参：

- `action: "activate" | "minimize" | "maximize" | "close"`
- `pid?: number`
- `window_name?: string`
- `match_index?: number`

覆盖能力：

- `ActivePid`
- `ActiveName`
- `MinWindow`
- `MaxWindow`
- `CloseWindow`

### 3.6 进程

#### 3.6.1 `autogui.process.list`

用途：读取进程列表或按名称筛选。

建议入参：

- `name_filter?: string`
- `include_path?: boolean`
- `limit?: number`

建议出参：

- `processes[]`
  - `pid`
  - `name`
  - `path?`
  - `exists`

覆盖能力：

- `Pids`
- `Process`
- `FindNames`
- `FindName`
- `FindPath`
- `PidExists`

#### 3.6.2 `autogui.process.control`

用途：统一进程存在性检查、终止、启动。

建议入参：

- `action: "exists" | "kill" | "run"`
- `pid?: number`
- `command?: string`

覆盖能力：

- `PidExists`
- `Kill`
- `Run`

限制建议：

- `run` 是高风险能力，实施时必须做显式的权限与参数边界审查
- 若平台内已有更合适的进程执行 service，可考虑先只实现 `exists` 与 `kill`

### 3.7 交互提示

#### 3.7.1 `autogui.dialog.alert`

用途：展示原生提示框。

建议入参：

- `title`
- `message`
- `default_button?: string`
- `cancel_button?: string`

覆盖能力：

- `Alert`

说明：

- 这不是桌面自动化主链路，但属于 `robotgo` 高层能力的一部分，可低优先级补入

---

## 4. 能力覆盖结论

若完成上述工具面，则 `robotgo` 当前开源版本的高层业务能力已基本覆盖：

1. 鼠标：覆盖
2. 键盘：覆盖
3. 剪贴板：覆盖
4. 截图：覆盖
5. 显示器信息：覆盖
6. 颜色采样：覆盖
7. 窗口查询与窗口控制：覆盖
8. 进程查询与进程控制：基本覆盖
9. OCR：覆盖
10. 提示框：覆盖

剩余不覆盖项主要是：

1. 底层句柄与平台特定对象
2. bitmap / image 的内部转换辅助函数
3. 若干兼容别名与废弃 API

这符合“几乎全部覆盖 `robotgo` 功能，但不暴露底层接口”的目标。

---

## 5. 实现策略

### 5.1 保留 service 身份与生命周期契约

不改变以下事实：

1. `service_id` 仍为 `autogui`
2. 仍通过现有 `service.lifecycle.health`
3. 仍通过现有 `service.lifecycle.state.get`
4. 仍通过现有 `service.lifecycle.shutdown`

### 5.2 从单文件分发改为内部模块化

当前 `main.go` 过于集中，本轮建议重构为：

```text
services/autogui/
├── cmd/autogui/main.go
├── internal/app/
│   ├── manifest.go
│   ├── server.go
│   ├── execute.go
│   ├── args.go
│   ├── response.go
│   ├── robotgo_adapter.go
│   ├── tools_display.go
│   ├── tools_mouse.go
│   ├── tools_keyboard.go
│   ├── tools_clipboard.go
│   ├── tools_screen.go
│   ├── tools_window.go
│   ├── tools_process.go
│   ├── tools_ocr.go
│   └── tools_dialog.go
└── manifest.json
```

原则：

1. `main.go` 只保留启动、注册、心跳、HTTP 装配
2. 工具 schema 与工具执行分开
3. 截图编码与窗口定位做成共享 helper
4. 所有输入转换与错误包装统一收敛

### 5.3 先做统一执行层，再补工具入口

顺序必须是：

1. 先抽公共解析与执行 helper
2. 再补新的工具 schema
3. 再让旧工具入口复用新实现

不能反过来先堆更多 `switch case`，否则会把兼容层和新逻辑混成一团。

### 5.4 兼容策略

本轮建议采用“新增主工具 + 保留旧工具”的双轨策略：

1. 已存在工具 ID 继续保留
2. 其内部实现尽量复用新主工具逻辑
3. 对于语义更完整的新工具，在文档中标记为推荐调用路径
4. 在没有明确用户迁移需求前，不做删除旧工具或重命名旧工具

---

## 6. 分阶段实施计划

### 6.1 第一阶段：工具面骨架与基础读取能力

目标：

1. 完成内部模块化重构
2. 新增显示器信息、颜色采样、鼠标位置、窗口信息、窗口截图基础能力
3. 保证旧工具继续可用

交付项：

1. `autogui.display.info.get`
2. `autogui.screen.color.get`
3. `autogui.mouse.position.get`
4. `autogui.window.info.get`
5. `autogui.window.capture`
6. 旧工具兼容回归

验收重点：

1. 按窗口名称截图可稳定输出图片
2. 同名多窗口返回可读候选信息
3. 显示器/颜色类工具返回结构化 JSON

### 6.2 第二阶段：鼠标高级动作与键盘统一层

目标：

1. 补齐平滑移动、拖拽、鼠标按下抬起、多击、平滑滚动
2. 合并文本输入与键盘输入能力
3. 引入剪贴板读写工具

交付项：

1. `autogui.mouse.button`
2. `autogui.mouse.drag`
3. 升级 `autogui.mouse.move`
4. 升级 `autogui.mouse.scroll`
5. `autogui.keyboard.key`
6. `autogui.keyboard.text`
7. `autogui.clipboard`

验收重点：

1. 鼠标四类路径都可用：move / button / drag / scroll
2. 文本输入模式清晰，不再由多个工具散落实现
3. 旧 `keyboard.type` / `keyboard.press` / `text.insert` 行为无回归

### 6.3 第三阶段：进程、OCR 与可选补完能力

目标：

1. 补齐进程查询与控制
2. 补齐 OCR
3. 低优先级补齐 dialog alert

交付项：

1. `autogui.process.list`
2. `autogui.process.control`
3. `autogui.ocr.extract`
4. `autogui.dialog.alert`

验收重点：

1. OCR 依赖缺失时错误可诊断
2. 进程工具边界清楚，不产生明显越权入口
3. service manifest 与治理视图完整反映新增工具

---

## 7. 关键实现细节

### 7.1 窗口名称匹配规则

必须明确，否则调用方会频繁踩坑。

建议规则：

1. `window_name` 按 `FindIds(name)` 的默认能力做大小写不敏感子串匹配
2. 若命中 0 个结果，返回 `window_not_found`
3. 若命中多个结果且未提供 `match_index`：
   - 默认报 `multiple_windows_matched`
   - 返回候选列表 `[{pid,title,name,path,bounds}]`
4. 若给了 `match_index`，则按候选顺序选中
5. 如需更严格匹配，后续可再扩展 `match_mode=contains|exact|prefix`，但不是本轮必需项

### 7.2 截图输出规范

建议统一截图响应结构：

- `target_type`
- `x`
- `y`
- `width`
- `height`
- `format`
- `size_bytes`
- `captured_at`
- `png_base64` 或 `jpeg_base64`
- `save_path?`
- `window_meta?`

注意：

1. 不应再把全屏截图的 `width/height` 简单回填成输入参数 `0`
2. 应返回真实截图尺寸

### 7.3 错误分类

建议最少区分：

1. `bad_request`
2. `tool_not_found`
3. `platform_not_supported`
4. `window_not_found`
5. `multiple_windows_matched`
6. `capture_failed`
7. `ocr_dependency_missing`
8. `process_action_not_allowed`
9. `robotgo_exec_failed`

### 7.4 平台限制表达

文档与错误提示必须明确：

1. macOS 需要 Accessibility 与 Screen Recording 权限
2. Linux 开源版按 X11 为前提，不把 Wayland 算作当前验收成功
3. 某些窗口与进程能力在不同平台上行为可能略有差异，工具结果里应回传 `platform`

### 7.5 配置与安全

如引入配置项，建议仅限：

1. 默认截图格式
2. 最大返回 Base64 大小
3. OCR 是否启用
4. 是否允许 `process.control.run`
5. 某些高风险工具的显式开关

不要把调用级参数下沉成大量静态配置。

---

## 8. 风险与缓解

### 8.1 风险：单文件继续膨胀，导致后续不可维护

缓解：

1. 第一阶段先做内部模块化
2. 所有新增工具按领域分文件

### 8.2 风险：旧工具语义被新实现破坏

缓解：

1. 旧工具保留原有入参
2. 新增参数必须可选
3. 先做兼容回归，再补文档

### 8.3 风险：窗口名称匹配不稳定

缓解：

1. 返回候选列表
2. 引入 `match_index`
3. 对多匹配默认报错，不静默选第一个

### 8.4 风险：OCR 在目标环境不可用

缓解：

1. 启动时自检或首次调用时依赖检查
2. 错误中明确缺少 `tesseract` 或 build/tag 条件

### 8.5 风险：进程执行工具扩展出过大安全面

缓解：

1. `process.control.run` 可延后到第三阶段后半段
2. 必要时默认关闭，仅保留 `exists` 与 `kill`

### 8.6 风险：跨平台差异导致验收不一致

缓解：

1. 先以当前开发平台做主验证
2. 在文档与错误里显式标平台
3. 不把未验证平台写成已完成事实

---

## 9. 验收标准

完成本计划后，至少应满足：

1. `autogui` 工具面已覆盖显示、颜色、鼠标、键盘、剪贴板、窗口、截图、OCR、进程的大部分高层能力。
2. 已存在工具 ID 无破坏式回归。
3. “按窗口名称截图”已成为正式工具能力，并支持多匹配消歧。
4. 工具返回结构化、可诊断，不再大量依赖隐式参数和单值输出。
5. `services/autogui/manifest.json` 的工具清单与真实实现一致。
6. 关键工具已有最小验证，包括：
   - 显示器信息读取
   - 指定坐标取色
   - 窗口信息查询
   - 按窗口名称截图
   - 鼠标高级动作
   - 键盘统一动作
   - OCR 基础调用
7. 若实施中同步更新项目说明，则 `doc/_instruction/structure.md` 与 `doc/_devlog.md` 中的描述必须与最终代码事实一致。

---

## 10. 建议的实施顺序

建议严格按以下顺序推进：

1. 模块化重构 `autogui` 内部结构
2. 统一截图与窗口定位 helper
3. 完成 `display.info.get`、`screen.color.get`、`mouse.position.get`
4. 完成 `window.info.get` 与 `window.capture`
5. 完成鼠标高级工具
6. 完成键盘与剪贴板统一层
7. 完成 OCR
8. 视安全边界决定是否开放 `process.control.run`
9. 最后同步说明文档与开发日志

这个顺序能最大限度降低“先铺工具、后补结构”导致的返工。

---

## 11. 本计划的最终判断

如果目标是“尽可能多补充 `robotgo` 的功能，但避免工具面过碎”，那么最合理的终局不是几十个小工具，而是以上这组按任务语义整合后的正式工具面。

在这套设计下，`autogui` 会从“基础输入输出 service”升级为“完整桌面自动化 service”，同时仍保持：

1. 单一 service 边界清晰
2. 工具数量可控
3. 对 `robotgo` 的覆盖面足够高
4. 对平台治理与现有 caller 的冲击最小

