# `chrome_control` Service 开发计划

- 文档类型：开发计划（DevPlan）
- 创建时间：2026-03-22 14:13:10 CST
- 范围：
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/services.json`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.mod`
  - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.sum`
  - 如需 WS 工具注册与治理视图校验，涉及 `pkg/toolproto/` 与 `hub/` 的最小必要适配
- 目标：
  - 新增一个独立的浏览器自动化 service：`chrome_control`
  - 该 service 归属新工具大类 `category=chrome`
  - 提供“第一阶段 + 第二阶段”能力，明确排除第三阶段能力
  - 保证工具可用、稳定、可观测、可调试，不依赖桌面坐标与焦点状态

---

## 0. 计划结论

本轮应当新增一个独立 Go service `chrome_control`，而不是扩展现有 `autogui`。

核心结论如下：

1. `chrome_control` 的主实现必须基于 Chrome DevTools Protocol（CDP），不能以 OS 鼠标键盘模拟作为主链路。
2. `headed` 与 `headless` 必须走同一套 CDP 执行路径，只在浏览器启动参数和窗口控制上区分，不能维护两套交互逻辑。
3. `autogui` 不进入本计划的验收闭环；`chrome_control` 的一二阶段能力必须在不借助 `autogui` 的前提下独立达标。
4. 工具设计必须围绕“浏览器实例 / 标签页 / 页面状态 / DOM / 下载 / 调试事件”这些浏览器语义建模，而不是继续暴露“移动鼠标、按下键盘”一类桌面语义。
5. 实现上优先使用 `chromedp + cdproto`，原因是当前仓库是纯 Go 多 service 架构，`chromedp` 可以直接承载 Go 侧 Chrome 进程启动、CDP 会话、Target 管理、事件订阅与高层动作封装，同时允许在必要处下沉到原生 CDP domain。
6. 本计划明确排除第三阶段内容：不接管已有用户 Chrome、不处理系统原生文件选择器、不承诺验证码或反爬验证绕过、不做桌面级弹窗接管。

---

## 1. 已核验事实

以下结论已由当前仓库和外部协议资料直接核验：

1. 项目当前采用 `Hub + 多独立 Service` 模式，新 service 需按 `tool + lifecycle + register/heartbeat` 接入，这与新增浏览器 service 完全兼容。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`
2. 当前 `services/autogui` 已被纳入受管 service，但它只提供桌面级鼠标、键盘、截图、文本插入和生命周期工具，没有浏览器会话、Tab、DOM、页面状态、下载管理等语义。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/cmd/autogui/main.go`
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/manifest.json`
3. 当前 Hub 已支持标准原子工具入口 `POST /api/tool/call` 与流式工具入口 `GET /api/tool/ws?tool_id=...`，service 侧也已支持 `streaming / ws_path` 元数据与 WS 代理能力。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/toolproto/supervisor.go`
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/tool_handler_ws.go`
4. 当前仓库未接入任何浏览器自动化库，也没有 Chrome/CDP 相关实现；现有依赖里只有 `robotgo` 这类桌面自动化库。依据：
   - `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.mod`
5. Chrome 112 之后 headless/headful 已统一到同一 Chrome 实现链路，这意味着可以用一套浏览器自动化主逻辑同时支持可见与不可见模式。依据：
   - [Chrome Headless mode](https://developer.chrome.com/docs/chromium/headless)
6. Chrome DevTools Protocol 原生支持本计划所需的大部分能力，包括：
   - 浏览器与窗口：`Browser.*`
   - 下载控制与下载事件：`Browser.setDownloadBehavior`、`Browser.downloadWillBegin`、`Browser.downloadProgress`
   - BrowserContext / Target / Tab：`Target.createBrowserContext`、`Target.createTarget`、`Target.activateTarget`、`Target.getTargets`、`Target.closeTarget`
   - 网络、Cookie、Header、UA：`Network.*`
   - DOM、DOMSnapshot、Runtime、Page、Input：用于 DOM 查询、页面信息、脚本执行、截图、鼠标与键盘事件
   依据：
   - [Chrome DevTools Protocol](https://chromedevtools.github.io/devtools-protocol/)
   - [Browser domain](https://chromedevtools.github.io/devtools-protocol/tot/Browser/)
   - [Target domain](https://chromedevtools.github.io/devtools-protocol/tot/Target/)
   - [Network domain](https://chromedevtools.github.io/devtools-protocol/tot/Network/)
7. `chromedp` 当前提供的 Go API 已直接覆盖或承接本计划的关键执行面，包括 `NewExecAllocator`、`NewRemoteAllocator`、`NewContext`、`ListenBrowser`、`ListenTarget`、`Navigate`、`WaitVisible`、`Click`、`SendKeys`、`Evaluate`、`Dump`、`Screenshot`、`FullScreenshot`。依据：
   - [chromedp package docs](https://pkg.go.dev/github.com/chromedp/chromedp)
8. 当前 `go list -m -versions github.com/chromedp/chromedp` 可见最新 tag 到 `v0.15.0`；`cdproto` 由 `chromedp` 依赖树引入并由 `go mod tidy` 固化具体伪版本。

---

## 2. 选型审查

### 2.1 方案 A：继续扩展 `autogui`

结论：否决。

原因：

1. `autogui` 的抽象层是桌面，而不是浏览器。
2. 桌面坐标、窗口焦点、分辨率、缩放比、遮挡、系统动画都会显著降低可靠性。
3. 无法自然表达 Tab、DOM、Cookie、Network、Download、Console 等浏览器语义。
4. 无头模式下桌面自动化根本不成立。

### 2.2 方案 B：新建 `chrome_control`，但主逻辑仍靠 OS 鼠标键盘

结论：否决。

原因：

1. 与方案 A 一样，稳定性基础错误。
2. 不能满足“页面信息、HTML、DOM 对象、下载事件、Cookie/Storage、网络日志”的刚性需求。
3. 无法保证 headed/headless 共用同一执行路径。

### 2.3 方案 C：新建 `chrome_control`，以 Playwright/Node sidecar 为主

结论：当前阶段不采用。

原因：

1. 当前仓库和所有 service 都是 Go 原生项目，新增 Node runtime、browser driver 管理和跨进程桥接，会把接入、部署、调试复杂度整体抬高。
2. 当前系统的标准 service 入口、生命周期、运行态清理、日志和治理都已是 Go 路径；在现阶段引入另一套语言 runtime，不符合“最小必要变化”原则。
3. 这类方案不是不能做，而是当前并非最低成本、最高确定性的落地路线。

### 2.4 方案 D：新建 `chrome_control`，以 `chromedp + cdproto` 为主

结论：采用。

原因：

1. 与当前纯 Go service 架构一致。
2. 可以直接使用 Chrome DevTools Protocol，不经过额外 driver 翻译层。
3. 高层动作可由 `chromedp` 提供，低层能力可在需要时直接下沉到 `cdproto` 的 Browser / Target / DOM / Input / Network / Runtime / Page domain。
4. 适合做“浏览器实例 + 标签页 + 事件缓存 + 下载状态 + WS 订阅”的持久化 service。
5. 更容易与现有 `toolproto`、Hub 路由、生命周期治理和受管 service 目录结构对齐。

---

## 3. 范围界定

### 3.1 本计划必须实现的范围

必须完整覆盖你要求的第一阶段与第二阶段能力：

1. 启动新的 Chrome 浏览器实例，支持 `headed` 与 `headless`。
2. 打开指定页面，并能继续管理该浏览器实例与其标签页。
3. 获取当前页面信息，包括：
   - URL
   - 标题
   - readyState
   - HTML 源码
   - DOM 结构对象
   - 节点查询结果
4. 页面交互工具，包括：
   - 点击
   - 输入
   - 按键
   - 悬停
   - 滚动
   - 下拉选择
   - 右键
   - 拖拽
   - 等待类动作
5. 下载控制，包括：
   - 设置下载目录
   - 等待下载
   - 获取下载结果
6. 常用运行配置，包括：
   - viewport
   - user agent
   - extra headers
   - timezone
   - permission override
   - timeout
7. 页面与浏览器可观测能力，包括：
   - 页面截图
   - 元素截图
   - console 日志
   - network 请求摘要
   - 对应的列表查询与第二阶段 WS 订阅

### 3.2 本计划明确排除的范围

以下内容不做：

1. 接管已打开的用户 Chrome。
2. 连接任意外部 remote-debugging 端口的未知浏览器实例。
3. 操作系统原生文件选择器、系统级弹窗、权限系统窗口。
4. 验证码、人机验证、反爬对抗。
5. 用 `autogui` 替 `chrome_control` 补足一二阶段验收。
6. 在本轮引入 Playwright、Selenium、ChromeDriver 或 Node sidecar。

---

## 4. 可靠性原则

本 service 的可用性与稳定性，必须建立在以下原则上：

### 4.1 浏览器内控制优先，OS 桌面控制不进入主链路

1. 所有页面动作默认通过 CDP 的 DOM / Runtime / Input / Page / Target 能力完成。
2. 不依赖屏幕坐标、窗口是否在前台、用户鼠标当前位置、键盘焦点或系统 DPI。
3. 不把 `autogui` 作为一二阶段能力的 fallback。

### 4.2 选择器和页面语义优先，坐标只是派生结果

1. `click / hover / drag / context_click` 首先根据 locator 解析目标节点。
2. 坐标仅作为浏览器视口内的派生数据，不允许将“桌面绝对坐标”作为标准主输入。
3. 对元素类动作，先执行：
   - 节点存在检查
   - 可见性检查
   - 滚动到视口
   - box model / bounding box 计算
   - 再发出点击或拖拽事件

### 4.3 输入工具不等价于物理键盘输入

1. `chrome.action.input` 的默认语义应是“把文本可靠写入目标元素并触发合理事件”，而不是“模仿用户一个字符一个字符敲键盘”。
2. 默认模式用 DOM 级 fill，必要时派发 `input` / `change` 事件。
3. 仅在用户显式要求快捷键、组合键或富文本编辑器兼容时，才走 `chrome.action.press` 或 `chrome.action.input` 的 `keys` 模式。

### 4.4 headed/headless 同路径

1. `headed` 和 `headless` 共用同一套浏览器会话、Target 管理、DOM 查询、动作执行和事件采集逻辑。
2. 二者只允许在以下维度有差异：
   - Chrome 启动参数
   - 是否显示平台窗口
   - 是否允许窗口尺寸/位置调整

### 4.5 每个标签页操作串行化

1. 同一 `tab_id` 上的动作必须串行执行，避免多个 tool 并发写同一页面状态。
2. 不同 `browser_id`、不同 `tab_id` 可以并发。
3. 所有 wait / navigate / action 工具都必须绑定明确超时。

### 4.6 错误可诊断

每个失败动作默认尽量返回：

1. 错误类别
2. 目标 `browser_id` / `tab_id`
3. 当前 URL
4. 当前标题
5. locator 摘要
6. 超时/等待阶段
7. 最近 console 错误摘要
8. 可选截图或 HTML 摘要引用

---

## 5. 总体架构

### 5.1 Service 形态

新增独立 service：

```text
services/chrome_control/
├── cmd/chrome_control/main.go
├── config/
│   ├── config.json
│   ├── configx.json
│   └── configx.json.example
├── internal/app/
│   ├── config.go
│   ├── runtime_root.go
│   ├── registry.go
│   ├── browser_session.go
│   ├── tab_session.go
│   ├── launch.go
│   ├── cleanup.go
│   ├── cdp_allocator.go
│   ├── cdp_target.go
│   ├── cdp_locate.go
│   ├── cdp_actions.go
│   ├── cdp_wait.go
│   ├── cdp_download.go
│   ├── cdp_storage.go
│   ├── cdp_debug.go
│   ├── tool_browser.go
│   ├── tool_tab.go
│   ├── tool_page.go
│   ├── tool_action.go
│   ├── tool_wait.go
│   ├── tool_download.go
│   ├── tool_storage.go
│   ├── tool_debug.go
│   ├── tool_http_handler.go
│   ├── tool_ws_handler.go
│   ├── supervisor_registration.go
│   └── lifecycle.go
├── manifest.json
└── run/
```

### 5.2 运行时对象模型

#### `BrowserSession`

对应一个 service 自己启动和拥有的 Chrome 进程。

最小字段：

1. `browser_id`
2. `mode`：`headless` / `headed`
3. `chrome_pid`
4. `allocator_ctx` / `browser_ctx`
5. `cancel`
6. `browser_context_id`
7. `debug_ws_url`
8. `launch_config`
9. `download_root`
10. `created_at`
11. `last_seen_at`
12. `tabs`
13. `console_buffer`
14. `network_buffer`
15. `download_buffer`
16. `window_state`

#### `TabSession`

对应一个 page target / tab。

最小字段：

1. `tab_id`（映射 CDP TargetID）
2. `browser_id`
3. `target_ctx`
4. `cancel`
5. `url`
6. `title`
7. `ready_state`
8. `attached_at`
9. `last_activity_at`
10. `lock`
11. `last_html_snapshot_meta`
12. `last_dom_snapshot_meta`

### 5.3 状态持久化原则

1. 浏览器和标签页的主状态放内存，不做跨服务重启恢复。
2. service 启动时清理自己遗留的临时 profile 目录、下载临时目录和上次记录的 Chrome 子进程。
3. `run/` 仅保存：
   - `.service_pid`
   - `manifest_runtime.json`
   - `owned_browsers.json` 或等价轻量状态文件，用于异常退出后的清理
4. 下载文件本体和浏览器 profile 放在 service 自己的 runtime data root，不放仓库源码目录。

---

## 6. 工具面设计

所有工具统一归属 `category=chrome`。遵循当前 `tool_id` 规范，命名为 `chrome.<type>.<tool>` 或 `chrome.<type>.<tool>.<subtool>`。

### 6.1 浏览器工具

#### `chrome.browser.launch`

作用：

1. 启动一个新的 Chrome 实例
2. 选择 `headed` / `headless`
3. 可选立即打开 `start_url`

核心入参：

1. `mode`: `headed` | `headless`
2. `start_url`
3. `executable_path`（可选）
4. `window`
   - `width`
   - `height`
   - `left`
   - `top`
   - `state`
5. `profile_mode`
   - `ephemeral`
   - `persistent`
6. `user_data_dir`（可选）
7. `proxy`
8. `lang`
9. `timezone`
10. `user_agent`
11. `extra_headers`
12. `download_dir`
13. `default_timeout_ms`
14. `allow_insecure_certs`

核心出参：

1. `browser_id`
2. `chrome_pid`
3. `mode`
4. `browser_context_id`
5. `initial_tab_id`
6. `debug_ws_url`
7. `effective_config`

#### `chrome.browser.list`

返回当前 service 拥有的全部 `BrowserSession` 摘要。

#### `chrome.browser.state.get`

返回单个 `browser_id` 的详细状态，包括：

1. 浏览器状态
2. 当前标签页列表
3. 下载目录
4. 最近下载摘要
5. 最近 console / network 摘要计数

#### `chrome.browser.close`

关闭指定 `browser_id`，并清理对应上下文、标签页与子进程。

### 6.2 标签页工具

#### `chrome.tab.open`

作用：

1. 在指定 `browser_id` 下打开新 tab
2. 可选 `url`
3. `headed` 模式下可选 `new_window=true`

#### `chrome.tab.list`

返回指定浏览器下的全部 tab 摘要。

#### `chrome.tab.activate`

将指定 `tab_id` 切为活动 tab。

#### `chrome.tab.close`

关闭指定 `tab_id`。

#### `chrome.tab.navigate`

导航到新 URL，并返回导航前后摘要。

#### `chrome.tab.reload`

重新加载页面。

#### `chrome.tab.stop`

停止当前页面导航和资源加载。

### 6.3 页面信息工具

#### `chrome.page.info.get`

返回：

1. `url`
2. `title`
3. `ready_state`
4. `frame_tree_summary`
5. `viewport`
6. `document_lang`
7. `last_navigation_at`

#### `chrome.page.html.get`

返回当前页面 HTML。

要求：

1. 默认返回执行脚本后的序列化 DOM HTML，而不是单纯原始响应体。
2. 可选返回：
   - `outer_html`
   - `inner_html`
   - 目标节点 HTML

#### `chrome.page.dom.snapshot`

返回 DOM 结构对象。

要求：

1. 默认返回 service 归一化后的树状对象，而不是直接把 CDP 原始扁平结构原样透出。
2. 可选参数：
   - `depth`
   - `include_text`
   - `include_attributes`
   - `locator`
   - `raw`
3. `raw=true` 时允许返回更接近 CDP `DOMSnapshot` 的结构。

#### `chrome.page.node.query`

按 locator 查询节点并返回：

1. 节点数量
2. 文本摘要
3. 属性
4. 可见性
5. bounding box
6. 交互可用性

#### `chrome.page.screenshot`

支持：

1. 当前视口截图
2. 整页截图
3. 元素截图

#### `chrome.page.eval`

执行 JS 并返回结果。

约束：

1. 默认只接受显式表达式或函数体，不做任意脚本文件注入。
2. 默认结果需 JSON 可编码。
3. 风险等级高于普通 DOM 查询工具。

### 6.4 页面配置工具

#### `chrome.page.viewport.set`

作用：

1. 设置页面 viewport
2. 用于截图、响应式调试、移动端模拟前置配置

#### `chrome.page.user_agent.set`

作用：

1. 设置当前 tab 的 UA
2. 与 viewport 可组合实现移动端页面仿真

#### `chrome.page.headers.set`

作用：

1. 设置当前 tab 或 browser context 的额外请求头

#### `chrome.page.timezone.set`

作用：

1. 设置当前 tab 的时区仿真

#### `chrome.page.permission.set`

作用：

1. 设置权限 override，如通知、剪贴板、定位等

### 6.5 页面动作工具

所有动作工具共享 `locator` 结构。

建议统一 locator 结构：

```json
{
  "strategy": "css",
  "value": "#submit",
  "state": "visible",
  "timeout_ms": 8000,
  "nth": 0
}
```

首版支持：

1. `css`
2. `xpath`

#### `chrome.action.click`

语义：

1. 定位元素
2. 确认可见和可交互
3. 自动滚动到视口
4. 计算元素点击点
5. 通过 CDP Input 域点击

#### `chrome.action.input`

语义：

1. 默认用 DOM 级 fill 写入值
2. 自动可选 `clear_before`
3. 自动触发必要事件
4. 可选 `mode=keys` 走键盘事件模式

#### `chrome.action.press`

语义：

1. 发送按键或组合键
2. 主要用于快捷键、回车、Escape、Tab 等

#### `chrome.action.hover`

通过元素定位后移动浏览器内指针到目标节点。

#### `chrome.action.scroll`

支持：

1. 页面滚动
2. 元素滚动到可见
3. 方向与距离控制

#### `chrome.action.select`

为 `select` 元素选择 `value` 或 `label`。

#### `chrome.action.context.click`

对目标节点执行右键点击。

#### `chrome.action.drag`

支持：

1. 源节点 -> 目标节点
2. 源节点 -> 偏移量

要求：

1. 通过浏览器内坐标计算与 CDP Input 事件完成
2. 不依赖系统鼠标

### 6.6 等待工具

#### `chrome.wait.selector`

等待节点进入目标状态：

1. `attached`
2. `visible`
3. `hidden`
4. `detached`

#### `chrome.wait.text`

等待页面或目标节点出现目标文本。

#### `chrome.wait.navigation`

等待页面导航完成。

支持：

1. URL 匹配
2. title 匹配
3. load 事件
4. DOMContentLoaded

#### `chrome.wait.network.idle`

等待网络空闲，用于页面稳定和下载前置等待。

### 6.7 下载工具

#### `chrome.download.dir.set`

设置指定 `browser_id` 的下载目录。

约束：

1. 默认只允许落到 service 配置允许的根目录下。
2. 工具层要返回最终实际目录。

#### `chrome.download.wait`

等待一个下载任务完成。

支持：

1. 按最新下载等待
2. 按 `guid`
3. 按文件名模式

#### `chrome.download.list`

返回最近下载记录：

1. `guid`
2. `url`
3. `suggested_filename`
4. `state`
5. `received_bytes`
6. `total_bytes`
7. `file_path`

### 6.8 Storage 工具

#### `chrome.storage.cookies.get`

获取当前 tab / context 的 cookie。

#### `chrome.storage.cookies.set`

写入 cookie。

#### `chrome.storage.local.get`

读取 localStorage。

#### `chrome.storage.local.set`

写入 localStorage。

#### `chrome.storage.session.get`

读取 sessionStorage。

#### `chrome.storage.session.set`

写入 sessionStorage。

### 6.9 Debug 工具

#### 第一阶段

提供查询型工具：

1. `chrome.debug.console.list`
2. `chrome.debug.network.list`

#### 第二阶段

提供流式订阅工具：

1. `chrome.debug.console.subscribe`
2. `chrome.debug.network.subscribe`

要求：

1. 工具在 manifest 中声明 `streaming=true`
2. 通过 `GET /service/tool/ws` 承载
3. 只向订阅者推送其指定 `browser_id` / `tab_id` 的事件

---

## 7. 生命周期与配置设计

### 7.1 service 生命周期

必须补齐并注册：

1. `service.lifecycle.health`
2. `service.lifecycle.state.get`
3. `service.lifecycle.shutdown`

### 7.2 `config.json` 设计

建议至少包括：

1. `chrome_executable_candidates`
2. `default_mode`
3. `default_window`
4. `default_viewport`
5. `default_lang`
6. `default_timezone`
7. `default_user_agent`
8. `default_timeout_ms`
9. `default_navigation_timeout_ms`
10. `default_download_root`
11. `allowed_download_roots`
12. `max_browsers`
13. `max_tabs_per_browser`
14. `event_buffer_size`
15. `cleanup_stale_browsers_on_start`
16. `allow_insecure_certs`

### 7.3 `configx.json` 设计

本轮可以为空结构，但保留标准文件。

未来可放：

1. 代理认证
2. 内部测试账号
3. 特殊受限站点的敏感配置

### 7.4 Chrome 可执行文件发现策略

实现顺序：

1. 若请求显式传入 `executable_path`，优先使用
2. 其次读取 `config.json`
3. 否则按 OS 约定路径自动探测
4. 都失败时返回明确错误，不做隐式降级

---

## 8. 关键实现策略

### 8.1 启动与连接

1. 使用 `chromedp.NewExecAllocator` 启动 service 自己拥有的 Chrome 进程。
2. 显式传入 `--remote-debugging-port=0` 或等价 allocator 配置，让 Chrome 自动分配调试端口。
3. 通过 `DevToolsActivePort` / `webSocketDebuggerUrl` 建立 Browser 级连接。
4. 每个 `BrowserSession` 默认建立独立 BrowserContext。

### 8.2 标签页管理

1. `launch` 后自动创建首个 tab。
2. `tab.open` 使用 `Target.createTarget`。
3. `tab.activate` 使用 `Target.activateTarget`。
4. `tab.close` 使用 `Target.closeTarget`。
5. 对新 target 的发现使用 `chromedp.ListenBrowser` 或 `WaitNewTarget` 模式配合自有 registry。

### 8.3 页面信息

1. URL、标题、导航状态优先走 `chromedp.Location`、`chromedp.Title`、`Page` / `Runtime` 状态查询。
2. HTML 获取优先通过 DOM 序列化。
3. DOM 结构对象默认返回 service 归一化树，避免把 CDP 原始对象直接暴露给上层。

### 8.4 动作执行

1. `click / hover / context click / drag`：
   - 通过 locator 找到节点
   - 获取布局盒
   - 发出浏览器内 Input 事件
2. `input`：
   - 默认 DOM fill
   - 可选 key event 模式
3. `press`：
   - 用于快捷键和组合键
4. `select`：
   - 直接操作 `select` 元素并派发必要事件

### 8.5 下载

1. 浏览器级下载控制统一使用 `Browser.setDownloadBehavior`，而不是旧的 `Page.setDownloadBehavior`。
2. 开启下载事件，用 `Browser.downloadWillBegin` 和 `Browser.downloadProgress` 维护本地下载状态机。
3. 每个浏览器实例默认拥有独立下载目录。

### 8.6 调试与日志

1. console、network 事件写入 ring buffer。
2. 查询工具从 ring buffer 读取最近 N 条。
3. 第二阶段的 WS 订阅从同一缓存与事件分发器派生。

### 8.7 版本兼容策略

1. 不直接跟着 CDP tip-of-tree 生字段写代码。
2. 以 `chromedp` 当前稳定 release 为上层依赖源，必要时引入与其兼容的 `cdproto`。
3. 对 `Browser.setDownloadBehavior`、窗口操作、权限 override 这类存在实验属性的方法，要在代码里补上能力探测与明确错误信息。

---

## 9. 风险与治理策略

### 9.1 高风险工具

高风险工具至少包括：

1. `chrome.page.eval`
2. `chrome.page.headers.set`
3. `chrome.page.permission.set`
4. `chrome.storage.cookies.set`
5. `chrome.action.press`
6. `chrome.download.dir.set`

建议：

1. 在 manifest 中设置更高 `risk_lv`
2. 明确 `allowed_caller_types`
3. 输出足够详细的审计信息

### 9.2 资源泄漏风险

主要风险：

1. Chrome 子进程残留
2. 临时 profile 残留
3. 下载目录膨胀
4. WS 订阅断开后监听器泄漏

控制措施：

1. 启动时清理上次 service 遗留资源
2. BrowserSession cancel 时统一清理
3. ring buffer 限长
4. 订阅连接断开时自动注销 listener

### 9.3 并发风险

主要风险：

1. 同一 tab 并发动作互相打断
2. 导航中动作与下载等待交叉
3. `ListenTarget` 回调里阻塞导致死锁

控制措施：

1. `tab_id` 级串行执行锁
2. listener 回调不做阻塞动作，只投递事件
3. 长动作一律挂明确 context timeout

---

## 10. 开发拆解

### 10.1 第 0 组：脚手架与接入

任务：

1. 新建 `services/chrome_control/` 目录
2. 新建 `manifest.json`
3. 新建 `config/` 三件套
4. 新建 `cmd/chrome_control/main.go`
5. 接入标准注册、心跳、运行态 manifest、`.service_pid`
6. 在 `hub/config/services.json` 中纳入受管列表

验收：

1. service 能被 Hub 启动与注册
2. `service.lifecycle.*` 工具可用

### 10.2 第 1 组：浏览器实例与 Registry

任务：

1. 实现 `BrowserSession` / `TabSession` registry
2. 实现启动、关闭、列出、状态查询
3. 实现 Chrome 可执行文件发现和启动参数拼装
4. 实现 service-owned Chrome 清理机制

验收：

1. `chrome.browser.launch`
2. `chrome.browser.list`
3. `chrome.browser.state.get`
4. `chrome.browser.close`

### 10.3 第 2 组：Tab 与导航

任务：

1. 实现 tab open/list/activate/close
2. 实现 navigate/reload/stop
3. 实现 `page.info.get`

验收：

1. `chrome.tab.*` 基础可用
2. 多 tab 场景下 registry 正确

### 10.4 第 3 组：HTML / DOM / 节点查询 / 截图

任务：

1. 实现 `page.html.get`
2. 实现 `page.dom.snapshot`
3. 实现 `page.node.query`
4. 实现 `page.screenshot`

验收：

1. 可拿到页面 HTML
2. 可拿到结构化 DOM 对象
3. 可返回元素级截图

### 10.5 第 4 组：动作与等待

任务：

1. 统一 locator 模型
2. 实现 `click / input / press / hover / scroll / select / context.click / drag`
3. 实现 `wait.selector / wait.text / wait.navigation / wait.network.idle`

验收：

1. 本地表单页、拖拽页、右键页、异步加载页可稳定通过

### 10.6 第 5 组：下载控制

任务：

1. 实现 `download.dir.set`
2. 实现下载事件监听与状态机
3. 实现 `download.wait / download.list`

验收：

1. 本地下载测试页在 headless/headed 下都可通过

### 10.7 第 6 组：Storage 与页面配置

任务：

1. cookies get/set
2. local/session storage get/set
3. viewport / user agent / headers / timezone / permission set

验收：

1. 配置变更对后续页面请求和渲染生效

### 10.8 第 7 组：Debug 查询与 WS 订阅

任务：

1. 实现 console / network ring buffer
2. 实现列表查询工具
3. 实现两个 WS 订阅工具

验收：

1. 查询工具可读最近事件
2. WS 工具可稳定订阅并退出

### 10.9 第 8 组：治理、元数据、文档同步

任务：

1. 完整补齐 manifest tool 元数据
2. 标注 `streaming / ws_path / timeout / risk_lv / allowed_caller_types`
3. 必要时补充项目说明文档

验收：

1. Hub service admin 页面可完整看到 `chrome_control` 的工具清单

---

## 11. 测试与验收方案

### 11.1 测试原则

必须以本地可控测试页为主，不依赖外网站点做主验收。

推荐使用 Go `httptest` 或本地小型静态测试页面构造：

1. 基础页面
2. 表单页面
3. 异步加载页面
4. 右键菜单页面
5. 拖拽页面
6. 下载页面
7. Cookie / Storage 页面
8. Console / Network 页面

### 11.2 必过验收用例

#### 浏览器与导航

1. headless 启动，打开指定 URL，返回正确标题和 URL
2. headed 启动，打开指定 URL，返回正确标题和 URL
3. 同浏览器打开多个 tab，切换和关闭行为正确

#### 页面信息

1. 获取 HTML 成功
2. 获取 DOM 结构对象成功
3. 节点查询返回文本、属性和 bounding box

#### 页面动作

1. 点击按钮触发 DOM 更新
2. 输入文本并提交表单
3. 快捷键回车触发提交
4. 右键工具触发 contextmenu
5. 拖拽工具完成 source -> target

#### 等待

1. 等待可见元素成功
2. 等待文本出现成功
3. 等待导航完成成功
4. 等待 network idle 成功

#### 下载

1. 自定义下载目录生效
2. 下载等待成功返回实际文件路径
3. 下载列表能看到完成状态

#### Storage / 配置

1. Cookie set/get 正确
2. localStorage / sessionStorage set/get 正确
3. viewport / UA / headers / timezone 配置生效

#### Debug

1. console.list 能读到 `console.log` 与 `console.error`
2. network.list 能读到本地请求摘要
3. console/network 的 WS 订阅能收到新增事件

### 11.3 失败场景验收

必须验证：

1. 非法 `browser_id`
2. 非法 `tab_id`
3. 定位器找不到节点
4. 等待超时
5. 下载超时
6. 浏览器进程异常退出
7. headless 和 headed 模式下的错误表现一致性

---

## 12. 预计改动文件清单

### 12.1 必改

1. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/manifest.json`
2. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/cmd/chrome_control/main.go`
3. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/config/config.json`
4. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/config/configx.json`
5. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/config/configx.json.example`
6. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/chrome_control/internal/app/*.go`
7. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/config/services.json`
8. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.mod`
9. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.sum`

### 12.2 可能需要最小适配

1. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/toolproto/supervisor.go`
2. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/tool_handler_ws.go`
3. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`
4. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_devlog.md`

说明：

1. 只有在新增 WS tool 元数据表达、治理视图或项目说明同步确有必要时，才改这些文件。
2. 本计划本身不要求预先修改文档事实源。

---

## 13. 开发时的关键约束

1. 不把 `chrome_control` 做成 `autogui` 的子功能。
2. 不为了“看起来像人工操作”而使用桌面鼠标键盘模拟。
3. 不先连外网真实网站来碰运气验证；先用本地受控页面跑通。
4. 不做第三阶段内容的任何隐式预留承诺。
5. 不把 service 设计成一次性脚本执行器；它必须是有状态的浏览器 runtime service。
6. 不让 `page.eval` 成为实现其他工具的唯一路径；标准动作工具必须有自己的结构化逻辑。

---

## 14. 本计划完成后的开发顺序建议

建议后续直接按以下顺序进入 `dev` 实施：

1. 先搭 service 脚手架和 registry。
2. 再做 `browser/tab/page info/html/screenshot` 这条最小闭环。
3. 再做 locator + action + wait。
4. 再做 download。
5. 再做 storage/config/debug。
6. 最后做 WS subscribe、文档同步和完整 smoke test。

这样可以保证：

1. 先让 service 真正跑起来
2. 再逐层叠加能力
3. 每一层都能被本地测试页独立验证

---

## 15. 参考依据

### 15.1 项目内依据

1. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction.md`
2. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/core.md`
3. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_instruction/structure.md`
4. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/doc/_service_standard.md`
5. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/cmd/autogui/main.go`
6. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/services/autogui/manifest.json`
7. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/pkg/toolproto/supervisor.go`
8. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/hub/internal/gateway/tool_handler_ws.go`
9. `/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/go.mod`

### 15.2 外部依据

1. [Chrome Headless mode](https://developer.chrome.com/docs/chromium/headless)
2. [Chrome DevTools Protocol](https://chromedevtools.github.io/devtools-protocol/)
3. [CDP Browser domain](https://chromedevtools.github.io/devtools-protocol/tot/Browser/)
4. [CDP Target domain](https://chromedevtools.github.io/devtools-protocol/tot/Target/)
5. [CDP Network domain](https://chromedevtools.github.io/devtools-protocol/tot/Network/)
6. [chromedp package docs](https://pkg.go.dev/github.com/chromedp/chromedp)
7. [chromedp/examples](https://github.com/chromedp/examples)（只作辅助手册，不作为稳定契约依据）

