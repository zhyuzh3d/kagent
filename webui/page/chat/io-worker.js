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

function sendControl(type, reason, turnId, text) {
  if (!wsReady()) return;
  const payload = { type };
  if (reason) payload.reason = reason;
  if (turnId) payload.turn_id = turnId;
  if (text) payload.text = text;
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

self.onmessage = function(e) {
  const msg = e.data;
  if (!msg || !msg.type) return;

  switch (msg.type) {
    case "config":
      updateConfig(msg.config);
      break;
    case "connect":
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
      sendControl(msg.control, msg.reason, msg.turn_id, msg.text);
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
      if (ws) {
        ws.close();
        ws = null;
      }
      break;
    case "interrupt":
      sendControl("interrupt", "barge_in");
      break;
    case "disconnect":
      running = false;
      stopVAD();
      if (ws) {
        try { ws.close(); } catch (_) {}
        ws = null;
      }
      break;
  }
};
