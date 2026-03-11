let ws = null;
let running = false;
let utteranceActive = false;
let lastVoiceAt = 0;
let lastUtteranceEndAt = 0;
let vadTimer = null;

const workerConfig = {
  utteranceSilenceMs: 500,
  utteranceEndDebounceMs: 2000
};

function wsReady() {
  return ws && ws.readyState === 1;
}

function updateConfig(next) {
  if (!next || typeof next !== "object") return;
  if (Number.isFinite(next.utteranceSilenceMs) && next.utteranceSilenceMs > 0) {
    workerConfig.utteranceSilenceMs = next.utteranceSilenceMs;
  }
  if (Number.isFinite(next.utteranceEndDebounceMs) && next.utteranceEndDebounceMs >= 0) {
    workerConfig.utteranceEndDebounceMs = next.utteranceEndDebounceMs;
  }
}

function sendControl(type, reason, turnId, text, extra) {
  if (!wsReady()) return;
  const payload = { type };
  if (reason) payload.reason = reason;
  if (turnId) payload.turn_id = turnId;
  if (text) payload.text = text;
  if (extra && typeof extra === "object") {
    if (extra.action_id) payload.action_id = extra.action_id;
    if (extra.action_name) payload.action_name = extra.action_name;
    if (extra.action_status) payload.action_status = extra.action_status;
    if (extra.action_followup) payload.action_followup = extra.action_followup;
    if (extra.action_surface_id) payload.action_surface_id = extra.action_surface_id;
    if (extra.action_manual_confirm) payload.action_manual_confirm = extra.action_manual_confirm;
    if (extra.action_block_reason) payload.action_block_reason = extra.action_block_reason;
    if (extra.action_args && typeof extra.action_args === "object") payload.action_args = extra.action_args;
    if (extra.action_result && typeof extra.action_result === "object") payload.action_result = extra.action_result;
    if (extra.action_effect && typeof extra.action_effect === "object") payload.action_effect = extra.action_effect;
    if (extra.action_state && typeof extra.action_state === "object") payload.action_state = extra.action_state;
    if (extra.surface_id) payload.surface_id = extra.surface_id;
    if (extra.event_type) payload.event_type = extra.event_type;
    if (extra.business_state && typeof extra.business_state === "object") payload.business_state = extra.business_state;
    if (typeof extra.visible_text === "string") payload.visible_text = extra.visible_text;
    if (typeof extra.status === "string") payload.status = extra.status;
    if (Number.isFinite(extra.state_version)) payload.state_version = extra.state_version;
    if (Number.isFinite(extra.limit)) payload.limit = extra.limit;
    if (Number.isFinite(extra.cursor)) payload.cursor = extra.cursor;
  }
  ws.send(JSON.stringify(payload));
}

function startVAD() {
  if (vadTimer) clearInterval(vadTimer);
  vadTimer = setInterval(() => {
    if (!running || !wsReady()) return;
    if (!utteranceActive) return;
    if (Date.now() - lastVoiceAt >= workerConfig.utteranceSilenceMs) {
      utteranceActive = false;
      if (Date.now() - lastUtteranceEndAt < workerConfig.utteranceEndDebounceMs) return;
      lastUtteranceEndAt = Date.now();
      self.postMessage({ type: "vad_utterance_end" });
    }
  }, 100);
}

function stopVAD() {
  if (vadTimer) {
    clearInterval(vadTimer);
    vadTimer = null;
  }
  utteranceActive = false;
}

self.onmessage = function (e) {
  const msg = e.data;
  if (!msg || !msg.type) return;

  switch (msg.type) {
    case "config":
      updateConfig(msg.config);
      break;
    case "connect":
      if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) {
        self.postMessage({ type: "ws_open" });
        return;
      }
      if (ws) {
        try { ws.close(); } catch (_) { }
      }
      ws = new WebSocket(msg.url);
      ws.binaryType = "arraybuffer";
      ws.onopen = () => self.postMessage({ type: "ws_open" });
      ws.onclose = () => self.postMessage({ type: "ws_close" });
      ws.onerror = () => self.postMessage({ type: "ws_error" });
      ws.onmessage = (ev) => {
        if (typeof ev.data === "string") {
          self.postMessage({ type: "ws_text", data: ev.data });
        } else if (ev.data instanceof ArrayBuffer) {
          self.postMessage({ type: "ws_binary", data: ev.data }, [ev.data]);
        }
      };
      break;
    case "send_control":
      sendControl(msg.control, msg.reason, msg.turn_id, msg.text, msg.extra);
      break;
    case "send_audio":
      if (wsReady()) ws.send(msg.data);
      break;
    case "voice_detected":
      lastVoiceAt = Date.now();
      utteranceActive = true;
      lastUtteranceEndAt = 0;
      break;
    case "set_ai_loud":
      if (msg.value) lastVoiceAt = Date.now();
      break;
    case "start":
      running = true;
      startVAD();
      sendControl("start", null, msg.turn_id);
      break;
    case "stop":
      running = false;
      stopVAD();
      sendControl("stop", null, msg.turn_id);
      break;
    case "interrupt":
      sendControl("interrupt", "barge_in");
      break;
    case "disconnect":
      running = false;
      stopVAD();
      if (ws) {
        try { ws.close(); } catch (_) { }
        ws = null;
      }
      break;
  }
};
