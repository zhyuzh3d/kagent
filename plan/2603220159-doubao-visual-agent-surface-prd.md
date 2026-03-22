# Doubao 视觉闭环 Agent Surface 框架需求文档

## 文档信息

- 撰写时间：2026-03-22 01:59:29 CST
- 文档类型：框架性需求文档
- 文档目标：定义一个基于 `page-surface` 与 `hub-service` 机制运行的视觉闭环 GUI Agent 系统，用于在浏览器内通过截图理解、鼠标键盘控制与大模型反思完成 `doubao.com` 文生图任务
- 适用范围：
  - `webui/surface/*`
  - `webui/page/surface/*`
  - `services/ai_doubao`
  - `services/autogui`
  - Hub 工具平面
- 依据文件：
  - `doc/_instruction.md`
  - `doc/_instruction/core.md`
  - `doc/_page-surface.md`
  - `webui/page/surface/components/runtime.js`
  - `webui/page/surface/lib/bridge.js`
  - `services/autogui/cmd/autogui/main.go`
  - `services/ai_doubao/cmd/ai_doubao/bootstrap_runtime.go`
  - `services/ai_doubao/internal/app/llm.go`

---

## 1. 背景

当前仓库已经具备以下真实基础：

1. `page-surface` 运行机制已存在，`surface` 可以作为自治工作区装载到宿主页并通过 `MessageChannel` 与宿主通信。
2. Hub 已具备统一工具平面，`surface` 的正式能力访问路径应是直接调用 Hub tools，而不是经页面私有 helper 代行。
3. `autogui` 已提供鼠标移动、点击、滚轮、键盘输入、按键组合、全屏截图、区域截图等原子 GUI 工具。
4. `ai_doubao` 已提供文本生成与流式文本能力，但尚未提供图像识别型 ISR 工具。

本需求的目标不是实现一个硬编码网页脚本，而是实现一个可自观察、可反思、可在视觉反馈中逐步决策的浏览器内 GUI Agent。

## 2. 总目标

构建一个自治 `surface`，它能够：

1. 在当前已打开的浏览器环境中，以视觉闭环方式打开新标签页并进入 `doubao.com`。
2. 在页面演化过程中持续截图并调用图像理解模型，判断当前界面状态与下一步动作。
3. 通过 Hub 正式工具调用 `autogui` 原子能力执行鼠标与键盘操作。
4. 在需要生成图像提示词时，调用 `ai_doubao` 文本能力生成适合文生图的提示词。
5. 最终把提示词录入到 Doubao 画图输入框并触发生成。

## 3. 核心原则

### 3.1 AI 优先，不做流程硬编码

系统不应把“打开标签页 -> 输入地址 -> 切画图 -> 输入提示词 -> 点击生成”写成固定脚本链，而应由模型基于当前截图和当前子任务动态决策下一步。

### 3.2 本地代码只负责约束与循环

本地实现负责：

1. 保存任务状态。
2. 调用 Hub tools。
3. 维护循环节奏。
4. 验证 AI 返回的命令是否合法。
5. 记录执行结果并回传给 AI。

本地实现不负责“猜页面现在长什么样”。

### 3.3 永远以截图闭环校正

每一轮动作执行后都必须重新截图并重新理解当前界面，禁止让模型一次性生成长串 GUI 操作脚本后盲执行。

### 3.4 Surface 是自治执行面，Page 是治理承载面

符合 `doc/_page-surface.md` 的边界：

1. `Page` 负责装载、观察、暂停、恢复与展示状态。
2. `Surface` 负责任务内的规划、观察、执行、反思与状态回报。

## 4. 产品形态

建议新增一个专用 `surface`，暂定名：

1. `webui/surface/buildin/visual_agent/`

该 `surface` 不是普通工具页，而是一个带执行驾驶舱的自治 Agent 工作区。其 UI 不以业务内容为主，而以“任务态、观察态、执行态、反思态”可视化为主。

## 5. 使用场景

首个目标场景为：

1. 用户在宿主页打开 `visual_agent` surface。
2. 用户输入目标：“进入 doubao.com 的画图模式，生成一个画图美女提示词，填入并触发生成。”
3. `surface` 自动运行视觉闭环任务。
4. 用户可观察当前子任务、最新截图、最近命令、当前推理结论，并可暂停/继续/终止。

## 6. 非目标

本轮不承诺：

1. 支持任意网站的通用稳定自动化。
2. 支持多浏览器、多显示器、多操作系统复杂差异自动适配。
3. 支持无监督长时间持续运行。
4. 支持网页 DOM 级自动化或浏览器扩展注入。
5. 支持自动判断生成图片质量并继续优化多轮提示词。

## 7. 系统总架构

系统由三部分组成。

### 7.1 Agent Surface

`surface` 内部包含：

1. `Runtime`
2. `Planner`
3. `Perception`
4. `Actuator`
5. `Reflector`
6. `UI`

### 7.2 Hub Tool Plane

由 Hub 转发至：

1. `autogui.*`
2. `ai.llm.generate`
3. 新增的 `ai.vision.isr`
4. 未来可选的 `autogui.text.insert`

### 7.3 宿主页

宿主页只负责：

1. 装载 `surface`
2. 观察 `state_change`
3. 调用 `surface actions`
4. 展示执行轨迹

## 8. Agent 运行模型

### 8.1 双层智能体模型

建议采用双层智能体，而不是单一 prompt。

第一层：高层规划器

1. 输入总目标。
2. 输出一组子任务。
3. 每个子任务必须包含完成判据。

第二层：视觉控制器

1. 输入当前截图、当前子任务、最近执行结果、允许命令 grammar。
2. 输出当前观察、子任务状态、下一步命令、是否推进到下一子任务。

### 8.2 标准循环

每一轮循环如下：

1. 获取当前截图。
2. 调用视觉理解工具。
3. 得到结构化观察和下一步命令。
4. 本地校验命令。
5. 顺序执行命令。
6. 等待短暂稳定时间。
7. 再次截图。
8. 判断是否完成当前子任务。
9. 必要时进入反思或重规划。

### 8.3 反思机制

当出现以下情况时，必须触发反思而不是盲重试：

1. 连续多轮截图语义无明显变化。
2. 连续多轮命令重复。
3. 命令执行成功但界面没有进入预期状态。
4. 视觉模型对当前界面置信度过低。

## 9. 子任务模型

高层规划器输出的子任务应至少包含：

1. `id`
2. `goal`
3. `done_when`
4. `hints`
5. `max_turns`

对于当前首个场景，子任务示意如下：

1. 打开新的浏览器标签页。
2. 导航到 `doubao.com`。
3. 等待进入可交互首页或聊天态。
4. 切换到画图模式。
5. 生成适合“美女”主题的文生图提示词。
6. 聚焦画图输入框。
7. 录入提示词。
8. 提交生成。

说明：

1. 这些只是首场景示例，不应在代码中硬编码为唯一任务链。
2. 真正执行时应由规划器输出等价子任务结构。

## 10. Tool 需求

### 10.1 现有可用工具

当前已确认可用：

1. `autogui.mouse.move`
2. `autogui.mouse.click`
3. `autogui.mouse.scroll`
4. `autogui.keyboard.type`
5. `autogui.keyboard.press`
6. `autogui.screen.capture`
7. `autogui.screen.capture_region`
8. `ai.llm.generate`

### 10.2 必须新增的工具

#### A. `ai.vision.isr`

这是本项目的关键新能力，必须设计为通用工具，不能只为 `doubao.com` 定制。

目标：

1. 输入一张或多张图片。
2. 输入任务说明。
3. 输入期望 JSON schema。
4. 输出结构化结果。

建议输入字段：

1. `instruction`
2. `images`
3. `response_schema`
4. `system_prompt`
5. `temperature`

建议输出字段：

1. `text`
2. `json`
3. `model`
4. `finish_reason`

设计要求：

1. 支持通用图像理解，不绑定任何具体网站。
2. 支持按调用方提供的 schema 返回 JSON。
3. 支持单图与多图输入。
4. 支持大图片输入的基础压缩或尺寸约束。

#### B. `autogui.text.insert`

虽然当前已有 `autogui.keyboard.type`，但对长文本、中文提示词、复杂输入框场景来说，它不应成为正式主路径。

建议新增通用高阶工具：

1. `autogui.text.insert`

目标：

1. 接收一段文本。
2. 优先通过剪贴板 + 粘贴完成录入。
3. 必要时 fallback 到逐字输入。

建议输入字段：

1. `text`
2. `mode`
3. `clear_before`
4. `submit`

说明：

1. `mode` 可选 `paste_preferred` / `type_only`。
2. 该工具必须是通用录入工具，不绑定 Doubao 场景。

可选拆分为：

1. `autogui.clipboard.write_text`
2. `autogui.text.insert`

但对上层 agent 来说，推荐只暴露 `text.insert` 作为主入口。

## 11. 命令 grammar 要求

视觉控制器不应直接返回 Hub 原始 tool call payload，而应返回一个受限 DSL，由本地执行器编译成 Hub tool 调用。

建议命令种类：

1. `mouse_move`
2. `mouse_click`
3. `mouse_scroll`
4. `key_press`
5. `text_insert`
6. `wait`

建议响应结构：

```json
{
  "ui_summary": "当前界面概述",
  "subtask_status": "continue",
  "advance_to_next_subtask": false,
  "commands": [
    { "kind": "key_press", "key": "t", "modifiers": ["command"] },
    { "kind": "text_insert", "text": "https://www.doubao.com", "submit": true }
  ],
  "next_capture_region": null,
  "confidence": 0.92
}
```

要求：

1. 每轮允许返回的命令数必须受限，建议不超过 3 条。
2. 命令必须是原子、短链、可验证的。
3. 不允许模型一次性输出大段脚本。

## 12. Surface 内部模块职责

### 12.1 Runtime

负责：

1. 接入 `surface_connect`
2. 注册 `surface actions`
3. 接收宿主动作
4. 回报执行状态

### 12.2 Planner

负责：

1. 把用户目标转换成子任务数组。
2. 在阻塞时触发重规划。

### 12.3 Perception

负责：

1. 获取截图。
2. 组装给 `ai.vision.isr` 的请求。
3. 解析视觉返回结果。

### 12.4 Actuator

负责：

1. 校验 DSL 命令。
2. 编译到 Hub tool 调用。
3. 执行并记录结果。

### 12.5 Reflector

负责：

1. 判断进展是否停滞。
2. 触发重试、回退或重规划。

### 12.6 UI

负责：

1. 展示任务概述。
2. 展示当前子任务。
3. 展示最新截图。
4. 展示最近观察与最近命令。
5. 提供启动、暂停、继续、单步、终止入口。

## 13. Surface Action 需求

至少应暴露以下动作：

1. `mission.start`
   - 输入：任务描述。
2. `mission.pause`
3. `mission.resume`
4. `mission.abort`
5. `mission.step`
6. `mission.get_state`

可选扩展：

1. `mission.set_goal`
2. `mission.retry_from_current`
3. `mission.override_subtask`

## 14. State Model 需求

`business_state` 至少包含：

1. `phase`
2. `goal`
3. `subtasks`
4. `current_subtask_index`
5. `last_observation`
6. `last_commands`
7. `last_tool_results`
8. `retry_count`
9. `latest_capture`
10. `blocked_reason`

建议 `phase` 枚举：

1. `idle`
2. `planning`
3. `observing`
4. `executing`
5. `reflecting`
6. `paused`
7. `blocked`
8. `done`
9. `error`

## 15. UI 需求

该 `surface` 的 UI 不是普通消费页，而是执行驾驶舱。

至少包含四块：

1. 任务面板：目标、当前子任务、当前阶段。
2. 视觉面板：最新截图、可选区域截图。
3. 推理面板：AI 当前观察、推进理由、反思结论。
4. 执行面板：最近命令、最近工具结果、暂停/继续/终止。

要求：

1. 首屏应可看清当前任务态与最新截图。
2. 记录信息应按时间线展示。
3. 必须明确区分“AI 观察”“AI 决策”“工具执行结果”。

## 16. 安全与约束

### 16.1 命令护栏

必须限制：

1. 单轮命令数量。
2. 鼠标坐标范围。
3. 文本长度。
4. 连续重试次数。

### 16.2 执行护栏

必须支持：

1. 用户手动暂停。
2. 用户手动终止。
3. 每个子任务超时。
4. 视觉低置信度时停止自动推进。

### 16.3 Scope 约束

本轮系统默认只在浏览器当前前台会话中使用，不承诺对其他桌面应用安全自动化。

## 17. 里程碑建议

### 第一阶段：闭环骨架

完成：

1. `surface` 驾驶舱 UI
2. 任务状态机
3. 单轮截图 -> 识别 -> 执行 -> 再截图循环
4. 基础暂停/恢复/终止

### 第二阶段：通用视觉与文本工具

完成：

1. `ai.vision.isr`
2. `autogui.text.insert`
3. 命令 DSL 与编译器

### 第三阶段：首个场景打通

完成：

1. 新标签页打开
2. 导航到 `doubao.com`
3. 切到画图模式
4. 自动生成提示词
5. 自动录入并提交

### 第四阶段：反思与稳态

完成：

1. 卡住检测
2. 反思再规划
3. 执行时间线完善
4. 更清晰的状态回报

## 18. 验收标准

### 18.1 系统验收

1. `surface` 可被宿主页装载并运行。
2. `surface` 能直接通过 Hub tools 调用 `autogui` 与 `ai_doubao`。
3. `surface` 运行时状态可通过 `state_change` 稳定观察。

### 18.2 工具验收

1. `ai.vision.isr` 可接受图片和 schema 并返回结构化 JSON。
2. `autogui.text.insert` 可稳定录入长文本。

### 18.3 场景验收

1. 从当前浏览器环境出发，agent 能新开标签页并导航到 `doubao.com`。
2. agent 能基于截图理解界面并切换到画图模式。
3. agent 能生成一段“美女”主题的文生图提示词。
4. agent 能把提示词录入输入框并提交。

### 18.4 交互验收

1. 用户能看见当前子任务和最近截图。
2. 用户能暂停、继续、终止。
3. 系统在卡住时不会无限盲试。

## 19. 风险

主要风险：

1. 视觉模型对网页细节的稳定识别能力不足。
2. 不同浏览器/窗口尺寸导致坐标泛化能力弱。
3. 长文本输入在不同输入框中表现不稳定。
4. `doubao.com` 页面结构变化会影响任务成功率。

应对策略：

1. 始终使用小步闭环。
2. 优先支持区域截图。
3. 为视觉返回结果引入置信度与阻塞态。
4. 把录入能力从 `keyboard.type` 升级为通用 `text.insert`。

## 20. 结论

本项目应被定义为一个“运行在 `surface` 内、通过 Hub tools 感知和操作浏览器的视觉闭环自治 Agent”，而不是一个网页脚本器。

它的技术核心不是页面 UI，而是三个通用能力的组合：

1. 任务分解
2. 视觉理解
3. 小步执行与反思

后续开发应严格围绕这三个能力展开，而不是围绕单一网站流程写死逻辑。
