# 开发计划：智能回避与 ASR 确权打断机制

## 1. 目标
替换现有的“检测到声音即刻打断”模式，实现：
- **瞬时回避 (Ducking)**：前端监测到声音超过阈值，立即将 TTS 音量降至 10%。
- **自动恢复 (Recovery)**：声音消失且未触发 ASR 识别时，音量恢复 100%。
- **确权打断 (Commit Stop)**：前端收到 `asr_partial` 确认有效字词且 AI 正在播放时，本地停止播放并向后端发送 `interrupt` 指令。

## 2. 核心改动

### 第一阶段：前端播放器音量控制
- 修改 [webui/page/chat/audio-playback.js](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/audio-playback.js)：
  - 新增 `setVolume(value, rampMs)` 方法。
  - 使用 Web Audio API 的 `gainNode.gain.setTargetAtTime` 实现平滑的音量过渡，防止爆音。
  - 导出 `isSpeaking()` 方法（判断队列是否为空）。

### 第二阶段：VAD 避让与恢复逻辑
- 修改 [webui/page/chat/audio-capture.js](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/audio-capture.js)：
  - **Ducking**：当 `rms > bargeInThreshold` 触发布防时，调用 `audioPlayback.setVolume(0.1, 50)`：瞬时调低 AI 音量。
  - **Recovery**：当监测到持续静音且未触发 `asr_partial` 时，调用 `audioPlayback.setVolume(1.0, 200)`：平滑恢复音量。

### 第三阶段：前端确权打断逻辑
- 修改 [webui/page/chat/event-router.js](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/webui/page/chat/event-router.js)：
  - 处理 `asr_partial` 事件：
    - 检查当前 AI 状态（是否正在 `speaking`）。
    - 检查文本是否包含有效字符（排除噪音误读）。
    - 命中则：1. `audioPlayback.stopPlayback()` 2. `sessionController.sendInterrupt()`。

### 第四阶段：后端逻辑清理
- 修改 [internal/session.go](file:///Users/zhyuzh/BaiduTongbu/2026.03.03kagent/kagent/internal/session.go)：
  - 移除 `maybeInterruptForRecognizedSpeech` 中的自动打断逻辑。
  - 确保后端仅响应前端发来的显式 `interrupt` 信号来终止 LLM 和 TTS 任务。

## 3. 详细参数建议
- `duckingVolume`: `0.1` (保留微弱背景，给用户反馈)
- `duckingRampDownMs`: `50` (极速下降)
- `duckingRampUpMs`: `300` (平滑恢复)
- `minCharCountForInterrupt`: `2` (避免极短噪音误触发)

## 4. 验证标准
- [ ] 测试环境噪音：AI 说话时咳嗽一声，音量应瞬间变小，随后立即恢复。
- [ ] 测试真实抢话：AI 说话时用户说“等一下”，音量小 10% 后 0.5 秒内 AI 彻底闭嘴。
- [ ] 边界测试：快速开关网页，确认音量节点不会导致残留声音。
