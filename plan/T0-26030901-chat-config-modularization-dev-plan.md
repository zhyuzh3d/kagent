# 开发计划文档：T0-26030901-chat-config-modularization-dev-plan

## 1. 文档元信息
- 生成时间：2026-03-09 12:38 CST
- 计划类型：配置体系重构 + 后端配置接口 + 前端对话页模块化
- 事实依据：
  - 当前项目说明 `doc/_instruction.md`
  - 当前后端实现 `main.go`、`internal/config.go`、`internal/session.go`、`internal/asr.go`、`internal/llm.go`、`internal/pipeline.go`、`internal/tts.go`
  - 当前前端实现 `webui/page/chat/index.html`
  - 既有计划文档 `plan/T0-26030301-dev-plan.md`、`plan/T0-26030401-fix-dev-plan.md`

## 2. 背景与问题定义
当前项目的实时对话链路已经具备可运行闭环，但“影响体验的参数”仍分散在多个位置，且缺少统一配置来源：

1. 前端存在硬编码参数：
  - Worker 停顿检测 `UTTERANCE_SILENCE_MS=500`
  - 主线程输入阈值 `voiceThreshold=0.018`
  - 抢话阈值 `dynamicBargeThresh=0.08`
  - `replyOnsetGuardMs=1200`
  - `preRollMaxFrames=5`
  - `silentTailFrames=50`
2. 后端存在硬编码参数：
  - `trigger_llm` 等待 ASR final 超时 `320ms`
  - `maxHistoryMessages=20`
  - ASR `end_window_size=500`、`force_to_speech_time=1000`、`accelerate_score=10`
  - LLM `systemPrompt`
  - TTS backlog 分组阈值 `3000/5000/10000/20000 ms`
3. 配置结构尚未分层：
  - `config/configx.json` 当前承载私密模型接入信息
  - `config/config.json` 当前未形成公开默认配置事实源
  - 不存在用户覆盖配置与前端字段元数据机制
4. 前端 `webui/page/chat/index.html` 目前承担 UI、Worker、WS、音频采集、播放、VAD、调试、状态管理等所有职责，后续加入配置抽屉后维护成本会明显升高。

本轮计划目标不是直接完成所有实现，而是将“统一配置来源、配置读写接口、尽早生效机制、前端模块化拆分”的实施路径一次性定义清楚，并约束真实落地顺序。

## 3. 术语与边界约定
### 3.1 运行层级
本轮统一采用以下作用域层级：

`app > chat > thread > turn > message`

说明：
1. `app`：单机版软件实例级别的公共配置与行为边界。
2. `chat`：一次“开始对话”到“停止”的完整会话边界，对应当前页面中点击开始后的整段实时对话。
3. `thread`：话题边界；当前实现中只有单一 thread 概念，暂不支持多 thread 并存。
4. `turn`：一轮用户输入 + AI 回复，对应当前前后端代码中的 `turn_id` 语义。
5. `message`：turn 内更细的文本/音频处理边界。

### 3.2 生效标签
前端展示层采用 `mtrca` 标记：

1. `m`：message 级别
2. `t`：turn 级别
3. `r`：thread 级别
4. `c`：chat 级别
5. `a`：app 级别

本轮约束：
1. `mtrca` 仅作为前端元数据和交互说明，不进入配置字段名。
2. 实际代码中由“配置读取时机”决定生效边界，而不是仅凭标签文本。
3. 当前项目只有单 thread 运行模型，`r` 先作为术语与未来扩展边界保留。

## 4. 目标与范围
### 4.1 In Scope
1. 建立公开默认配置 `config/config.json` 的正式结构，重点收拢对话相关参数到 `chat` 域。
2. 保留私密配置 `config/configx.json`，继续只承载敏感接入参数。
3. 新增用户覆盖配置 `data/users/default/user_custom_config.json`，仅保存覆盖项。
4. 新增前端配置元数据 `webui/json/config_info.json`，控制显示字段、说明、控件形式和 `mtrca` 标签。
5. 提供后端公开配置读取接口与更新接口。
6. 梳理现有后端代码中的配置读取时机，尽可能把参数前移到更细粒度生效边界。
7. 拆分 `webui/page/chat/index.html` 的脚本逻辑，建立模块化前端结构与统一配置注入机制。

### 4.2 Out of Scope
1. 多用户登录、权限系统、云端同步。
2. 复杂后端配置校验框架或数据库持久化。
3. 多 thread 会话管理能力的正式落地。
4. 非对话页面的完整配置 UI 实现。

## 5. 总体设计决策
### 5.1 配置文件职责
1. `config/config.json`
  - 公开默认配置事实源。
  - 不包含敏感字段。
  - 面向整个项目，其中对话能力集中到 `chat` 域。
2. `config/configx.json`
  - 私密接入配置。
  - 保留模型、Token、AppID、私有 URL、资源 ID 等敏感字段。
3. `data/users/default/user_custom_config.json`
  - 用户级覆盖配置，仅保存 diff。
  - 面向单机版默认用户；后续多用户可扩展到 `data/users/<user>/...`。
4. `webui/json/config_info.json`
  - 纯前端展示元数据。
  - 决定哪些字段可展示、可编辑、如何提示和如何分组。

### 5.2 合并优先级
运行时有效公开配置按以下优先级合并：

`user_custom_config overrides > config/config.json defaults`

私密接入配置单独从 `config/configx.json` 读取，不进入前端公开配置接口。

### 5.3 校验策略
采用“前端主校验、后端宽松合并”的轻量策略：

1. 前端负责：
  - 字段是否允许编辑
  - 基本类型、范围、输入格式校验
2. 后端负责：
  - JSON 解析成功
  - 文件写入成功
  - 运行态读取失败时提供明确错误
3. 未知字段策略：
  - 不阻断保存
  - 运行态 typed config 消费不到的字段自然失效
4. 极端损坏恢复：
  - 前后端提示用户手工删除 `data/users/default/user_custom_config.json`

## 6. 公开配置结构建议
### 6.1 顶层结构
建议 `config/config.json` 采用以下结构：

```json
{
  "app": {
    "debug": {},
    "ui": {}
  },
  "chat": {
    "frontend": {},
    "session": {},
    "asr": {},
    "llm": {},
    "tts": {},
    "pipeline": {}
  }
}
```

说明：
1. 顶层使用 `app` 与 `chat` 两个主域，避免未来顶层字段无序扩张。
2. `chat` 域对应当前实时对话页面和链路，不再使用 `dialog` 命名。
3. `config_info.json` 可直接按 `app` / `chat` 与二级分组生成 UI Tab。

### 6.2 首批建议纳入 `chat` 的参数
1. `chat.frontend`
  - `voiceThreshold`
  - `utteranceSilenceMs`
  - `bargeInThreshold`
  - `bargeInMinFrames`
  - `bargeInCooldownMs`
  - `replyOnsetGuardMs`
  - `preRollMaxFrames`
  - `silentTailFrames`
  - `frameSamples16k`
2. `chat.session`
  - `triggerLLMWaitFinalMs`
  - `maxHistoryMessages`
  - `controlQueueSize`
  - `upstreamAudioQueueSize`
  - `downstreamTTSQueueSize`
3. `chat.asr`
  - `endWindowSize`
  - `forceToSpeechTime`
  - `accelerateScore`
  - `enableITN`
  - `enablePunc`
  - `enableAccelerateText`
  - `asrContextMaxMessages`
4. `chat.llm`
  - `systemPrompt`
  - `responseStylePreset`
  - `streamTimeoutMs`
5. `chat.tts`
  - `voiceType`
  - `readTimeoutMs`
  - `writeTimeoutMs`
6. `chat.pipeline`
  - backlog 分组阈值
  - 拼句上限
  - 句子切分标点集
  - 估算时长参数

### 6.3 暂不建议公开编辑但可保留在配置层的字段
1. 编解码细节
2. 协议常量
3. WebSocket 协议帧结构相关常量
4. 私密模型接入信息

策略：
1. 允许后端在 `config.json` 中保留这些字段。
2. 前端仅展示 `config_info.json` 中声明的字段。
3. 未声明字段可读但不提供页面编辑入口。

## 7. 后端改造计划
### 7.1 阶段 1：建立配置加载与公开读取能力
目标：先把运行时有效配置建立起来，不立即大规模改业务逻辑。

任务：
1. 扩展现有 [internal/config.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/config.go)
  - 保留 `configx.json` 的私密模型加载能力
  - 新增公开配置结构定义与读取逻辑
2. 新增运行时配置管理模块
  - 建议新文件：`internal/runtime_config.go`
  - 职责：
    - 读取 `config/config.json`
    - 读取 `data/users/default/user_custom_config.json`
    - 合并得到有效公开配置
    - 提供线程安全读取
3. 在 [main.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/main.go) 增加接口
  - `GET /api/config`
  - 返回有效公开配置
4. 保持现有 `/version`、`/ws`、静态资源行为不变

验收：
1. 后端启动时即使用户覆盖文件不存在，也能正常回退到默认 `config.json`
2. `GET /api/config` 返回完整公开配置 JSON
3. 私密字段不会被读入该接口响应

### 7.2 阶段 2：建立公开配置更新与覆盖写盘能力
目标：让前端能一次性提交整份公开配置，由后端落成 diff。

任务：
1. 在配置管理模块增加：
  - 接收新的完整公开配置
  - 与默认 `config.json` 计算差异
  - 生成 `user_custom_config.json` 的 `overrides`
2. 增加接口：
  - `PUT /api/config`
  - 请求体为完整公开配置
3. 写盘策略：
  - 写临时文件
  - 原子 rename 替换
4. 错误策略：
  - JSON 不合法时返回错误
  - 写盘失败时返回错误
  - 不额外拒绝未知字段

验收：
1. 覆盖文件能自动创建
2. 相同配置重复保存时 diff 稳定
3. 删除用户个性化配置后可自动回退默认配置

### 7.3 阶段 3：重构后端配置消费点，推进更早生效
目标：不是“所有字段都立即生效”，而是让每类参数在合理边界上尽早生效。

建议改造方向：
1. `m` 级别
  - 主要落在前端，不强求后端支持
2. `t` 级别
  - `startTurn()`、`startASRTurn()`、`pipeline.RunTurn()` 创建时读取一次快照
3. `r` 级别
  - 预留 thread reset 入口时读取
4. `c` 级别
  - `NewSession()` 或 `start` 触发 chat 时固定
5. `a` 级别
  - 作为 app 默认配置基线

需要重点梳理的后端参数：
1. [internal/session.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/session.go)
  - `upstreamAudioQueueSize`
  - `downstreamTTSQueueSize`
  - `trigger_llm` final 等待时间
  - `maxHistoryMessages`
2. [internal/asr.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/asr.go)
  - `end_window_size`
  - `force_to_speech_time`
  - `accelerate_score`
  - `maxCtx`
  - 读写超时
3. [internal/llm.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/llm.go)
  - `systemPrompt`
  - HTTP timeout
4. [internal/pipeline.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/pipeline.go)
  - 拼句策略阈值
  - 估算时长参数
  - backlog 上限
5. [internal/tts.go](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/tts.go)
  - `voiceType`
  - 读写超时

关键约束：
1. 不为“立即生效”而破坏现有 Session/Turn 状态机稳定性。
2. 新参数必须绑定明确读取边界，否则前端标记 `mtrca` 将失真。

## 8. 前端改造计划
### 8.1 阶段 4：从单文件脚本迁移到模块化结构
目标：在不改变现有视觉与交互结果的前提下，先拆逻辑，再接配置抽屉。

建议拆分：
1. `webui/page/chat/chat-app.js`
  - 页面入口与模块装配
2. `webui/page/chat/config-store.js`
  - 加载 `/api/config`
  - 管理当前有效公开配置
  - 向其他模块提供读/订阅接口
3. `webui/page/chat/io-worker.js`
  - 从 inline worker 独立出去
4. `webui/page/chat/ws-client.js`
  - WS 控制/事件收发
5. `webui/page/chat/audio-capture.js`
  - 麦克风采集、降采样、VAD 输入
6. `webui/page/chat/audio-playback.js`
  - TTS 播放队列、`playbackEpoch`
7. `webui/page/chat/conversation-store.js`
  - `chat/thread/turn/message` 运行态数据
8. `webui/page/chat/chat-view.js`
  - 聊天气泡渲染
9. `webui/page/chat/debug-panel.js`
  - 调试日志面板
10. `webui/page/chat/config-drawer.js`
  - 左侧配置抽屉

实施顺序建议：
1. 先拆 `config-store.js`
2. 再拆 `io-worker.js`
3. 再拆音频与会话逻辑
4. 最后拆 UI 面板与抽屉

理由：
1. Worker 目前嵌在字符串中，后续参数注入困难，是最影响配置化落地的阻塞点。
2. 配置 store 是后续所有“尽快生效”能力的公共依赖。

### 8.2 阶段 5：引入 `config_info.json` 驱动前端配置面板
目标：前端决定哪些字段可见、可编、如何提示，不把展示元信息塞回后端接口。

任务：
1. 新增 `webui/json/config_info.json`
2. 约定每个字段包含：
  - `label`
  - `description`
  - `group`
  - `tab`
  - `show`
  - `advanced`
  - `control`
  - `unit`
  - `min/max/step`
  - `recommended`
  - `scope`
  - `applyHint`
3. 配置面板只渲染其中声明的字段
4. 页面提交时仍然发送完整公开配置对象

验收：
1. `config_info.json` 不需要后端参与解析
2. 前端只允许编辑声明字段
3. 未声明字段虽存在于 `/api/config` 响应中，但不显示、不编辑

### 8.3 阶段 6：统一前端参数生效机制
目标：把现有直接读常量的逻辑改为通过统一配置读取点消费。

优先处理：
1. Worker 停顿检测参数
2. 主线程 VAD 阈值与抢话参数
3. `replyOnsetGuard`
4. pre-roll / tail audio
5. 仅前端消费的 debug/UI 参数

实现原则：
1. `m` 参数直接从 store 读最新值
2. `t/c/a` 参数在开始边界读取快照
3. 不追求所有参数热更新；追求语义一致

## 9. 当前代码到配置的首批映射建议
### 9.1 前端
1. [webui/page/chat/index.html#L432](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L432)
  - `chat.frontend.voiceThreshold`
  - 建议作用域：`m`
2. [webui/page/chat/index.html#L312](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L312)
  - `chat.frontend.utteranceSilenceMs`
  - 建议作用域：`m`
3. [webui/page/chat/index.html#L1041](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L1041)
  - `chat.frontend.bargeInThreshold`
  - 建议作用域：`m`
4. [webui/page/chat/index.html#L1055](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L1055)
  - `chat.frontend.bargeInMinFrames`
  - 建议作用域：`m`
5. [webui/page/chat/index.html#L1058](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L1058)
  - `chat.frontend.bargeInCooldownMs`
  - 建议作用域：`m`
6. [webui/page/chat/index.html#L458](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L458)
  - `chat.frontend.replyOnsetGuardMs`
  - 建议作用域：`t`
7. [webui/page/chat/index.html#L451](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L451)
  - `chat.frontend.preRollMaxFrames`
  - 建议作用域：`m`
8. [webui/page/chat/index.html#L456](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/index.html#L456)
  - `chat.frontend.silentTailFrames`
  - 建议作用域：`m`

### 9.2 后端
1. [internal/session.go#L16](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/session.go#L16)
  - `chat.session.upstreamAudioQueueSize`
  - `chat.session.downstreamTTSQueueSize`
  - 建议作用域：`c`
2. [internal/session.go#L231](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/session.go#L231)
  - `chat.session.triggerLLMWaitFinalMs`
  - 建议作用域：`t`
3. [internal/session.go#L660](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/session.go#L660)
  - `chat.session.maxHistoryMessages`
  - 建议作用域：`r`
4. [internal/asr.go#L355](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/asr.go#L355)
  - `chat.asr.*`
  - 建议作用域：`t`
5. [internal/llm.go#L21](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/llm.go#L21)
  - `chat.llm.systemPrompt`
  - 建议作用域：`t` 或 `r`
6. [internal/pipeline.go#L284](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/pipeline.go#L284)
  - `chat.pipeline.groupingPolicy`
  - 建议作用域：`t`
7. [internal/tts.go#L145](/Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/tts.go#L145)
  - `chat.tts.voiceType`
  - 建议作用域：`t`

## 10. 实施步骤与里程碑
### M1：配置结构与后端只读接口
1. 设计 `config.json` 结构
2. 新增运行时配置管理模块
3. 接入 `GET /api/config`
4. 补基础测试

### M2：后端写接口与 diff 覆盖文件
1. 实现 `PUT /api/config`
2. 支持原子写入 `user_custom_config.json`
3. 确认覆盖回退机制

### M3：后端配置消费点前移
1. 收拢 session/asr/llm/pipeline/tts 参数读取点
2. 明确每类参数的 `mtrca` 生效边界
3. 不稳定或高风险参数先保守为 `c/a`

### M4：前端配置 store 与 Worker 拆分
1. 建立 `config-store.js`
2. 抽出 `io-worker.js`
3. 让前端运行参数通过 store 注入

### M5：前端对话页脚本模块化
1. 拆音频采集
2. 拆播放
3. 拆 WS 事件路由
4. 拆会话状态与视图

### M6：配置抽屉与 `config_info.json`
1. 设计左侧抽屉
2. 按 Tab 与分组展示
3. 仅开放声明字段编辑
4. 提供保存、重置、恢复默认行为

### M7：前端统一生效与体验调优
1. 按 `mtrca` 落实生效时机
2. 优化“修改后何时生效”的用户提示
3. 校准 VAD / barge-in / reply guard 参数

## 11. 风险与应对
1. 风险：配置迁移过快导致现有可运行链路回归
  - 应对：先做只读接口与默认值回退，再逐步接管消费点
2. 风险：配置读取点不统一，`mtrca` 标签与真实行为不一致
  - 应对：先标注后重构，逐模块绑定读取边界
3. 风险：前端模块化过程中 Worker 与音频链路回归
  - 应对：优先保持接口和事件协议不变，只拆内部职责
4. 风险：用户覆盖文件写坏导致启动异常
  - 应对：原子写入 + 启动错误提示“删除用户自定义配置文件”
5. 风险：配置项过多导致 UI 混乱
  - 应对：`config_info.json` 只声明高价值字段，其他字段不展示

## 12. 验收标准
1. 后端存在统一的公开配置读取与更新接口。
2. `config/config.json` 成为公开默认配置事实源，`configx.json` 继续只承载敏感信息。
3. `data/users/default/user_custom_config.json` 只保存覆盖项，删除后可回退默认配置。
4. 前端只展示 `config_info.json` 声明字段，且能正确加载默认值与覆盖值。
5. 现有对话链路在配置化改造后仍保持可用：
  - 启动会话
  - 识别文本
  - LLM 增量
  - TTS 播放
  - barge-in
  - stop/cleanup
6. `webui/page/chat/index.html` 不再承载全部核心脚本逻辑，完成首轮模块化拆分。
7. 至少一批高价值参数完成更早生效改造，并能在 UI 上准确提示所属 `mtrca` 边界。

## 13. 建议的开发顺序结论
本计划最终建议的真实实施顺序为：

1. 先建立公开配置结构与后端配置管理器。
2. 再建立后端 `GET/PUT /api/config`。
3. 然后梳理并前移后端配置消费点，明确 `mtrca` 生效边界。
4. 再拆前端 `config-store.js` 与 `io-worker.js`。
5. 再完成 `chat` 页脚本模块化。
6. 最后接入 `config_info.json` 与配置抽屉，并统一优化前端生效机制。

原因：
1. 若先做 UI 而没有统一配置事实源，后续返工成本会更高。
2. 若先拆前端而没有配置 store，模块边界仍会重复耦合。
3. 若不先梳理后端读取边界，`mtrca` 标签只能停留在文案层，无法成为可信行为说明。
