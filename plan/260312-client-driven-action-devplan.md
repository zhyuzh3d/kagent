# 前端 Action 引擎接管与 Client-Driven 架构开发计划

## 1. 目标与背景
在之前的实现中，前端的 Surface 组件存在“隐式唤起”和“死数据缓存”问题。当面板被用户关闭时，前端并没有通知外部，且在接收到大语言模型下发的 `action_call`（如 `surface.get_state`）时，组件库擅自将不可见的面板重新拉起，并利用之前的旧缓存向 LLM 提供了错误报告。

本计划将彻底贯彻**“前端胖客户端 (Rich Client) + 后端瘦持久层 (Thin Server)”**架构。剥夺底层自动唤起的权利，让所有的 Action 执行、状态呈现严格与用户的肉眼可见性保持绝对一致。

## 2. 核心代码改造点

### 2.1 前端：`webui/page/chat/surface-bridge.js`
负责彻底阻断不可见面板的复活行为与缓存欺骗。

- **【改造点 A】禁用隐式强行唤起**：
  在 `dispatchAction(action)` 函数内，移除 `if (!visible) { setVisible(true); }` 的流氓行径。
  替换为：`if (!visible) { return Promise.resolve({ ok: false, reason: "surface_closed" }); }`
- **【改造点 B】严格管理生命周期事件**：
  在用户点击面板右上角 `关闭(close)` 按钮的监听器中：
  不仅仅是调用隐藏样式，而是要：
  1. 调用 `onSurfaceEvent({ type: "surface_closed", ... })`。
  2. 主动清理本地的 `stateCache` 字典。
- **【改造点 C】断绝缓存依赖**：
  在 `dispatchAction` 中针对 `get_state` 读取缓存的代码里，前置判定 `if (!visible)`，彻底杜绝死面板返回旧 96 数据。

### 2.2 前端：`webui/page/chat/event-router.js` & `chat-store.js`
负责处理上面发出的生命周期事件并向后同步。

- **【改造点 D】路由识别 `surface_closed`**：
  确保事件总线能够把 `surface_closed` 当作一种特殊的 `state_change` 或直接封包为独立 event 传回后端，确保本地日志或后端 SQLite 能记录面板的死亡时间。

### 2.3 后端：`internal/session.go` & `internal/sqlite_store.go`
后端不再需要改动任何关于 `surface.get_state` 的业务拦截，只需要保持纯粹的 WS 中转以及提供相应的落库支持。

- **【改造点 E】支持闭合状态的持久化**：
  若前端发送了 `surface_closed` 事件，后端 `handleStateChange` 中应该识别并将 SQLite `surface_states` 中的该记录标记为 `status = "closed"`，或者增加相应的动作落库，使得全局状态保持真实一致。

## 3. `hostops` 规范的未来延展 (仅作架构设计保障)
随着后续系统开发可能接入 OS 文件读取、后台自动化爬取等任务，这些任务会构成 `hostops` (宿主操作)。
按照全客户端驱动 (Client-Driven) 架构，我们在此约定未来的代码链路保障：
1. LLM 输出带有 `action_name: "hostops.file.read"`。
2. JS 前端的 `action-engine.js` 拦截到此命令，发起 fetch/XHR 调用本地 Go 服务的 `/api/hostops/file/read`。
3. Go 服务返回具体文件文本给 前端 JS。
4. 前端 JS 构建 `action_report` (装配上 file content 作为 result) 并向 WS 总线投递。
在整个过程中，Go 所扮演的角色永远只是 API 资源提供者和最后的消息流刻录机，绝不绕过前端自行组装 `action_report` 推入对话队列。

## 4. 验证与验收标准
1. 打开聊天页并呼出/触发 Counter 面板，修改几个数字。
2. **手动点击关闭** Counter 面板。
3. 文本/语音提问：“你现在能看到 Counter 的数字吗？”
4. **预期结果**：
   - Counter 浮窗**不会**重新弹起。
   - Frontend 控制台生成了 `surface_closed` 事件，并且拦截了 `action_call`，往外输出包含 `"reason": "surface_closed"` 的 report。
   - LLM 获取失败报告，回复类似“面板已被关闭，我无法读取数字”。
