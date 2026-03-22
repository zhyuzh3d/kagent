function buildControlOutboxKey(msg) {
  if (!msg || msg.type !== "send_control") return "";
  const extra = msg.extra && typeof msg.extra === "object" ? msg.extra : {};
  switch (String(msg.control || "").trim()) {
    case "surface_registry_sync":
      return "surface_registry_sync";
    case "surface_active_change":
      return "surface_active_change";
    case "surface_runtime_context":
      return `surface_runtime_context:${String(extra.surface_id || "").trim()}`;
    case "state_change":
      return `state_change:${String(extra.surface_id || "").trim()}`;
    case "config_change":
      return "config_change";
    case "fetch_history":
      return "fetch_history";
    default:
      return "";
  }
}

function cloneMessage(msg) {
  return msg ? JSON.parse(JSON.stringify(msg)) : msg;
}

export function createSessionController(options) {
  const {
    app,
    workerURL,
    audioPlayback,
    chatStore,
    eventRouter,
    setStatus,
    setButtons,
    appendDebug,
    flashIndicator,
    getWorkerConfig,
    getReplayControlMessages,
  } = options;

  let ioWorker = null;
  let audioCapture = null;
  let stopping = false;
  let pageCloseBound = false;
  let wsConnected = false;
  let controlOutbox = [];

  function bindAudioCapture(nextAudioCapture) {
    audioCapture = nextAudioCapture;
  }

  function workerSend(msg, transferables) {
    if (ioWorker) {
      if (msg.type === "send_audio" || msg.type === "send_control") {
        flashIndicator("send");
      }
      ioWorker.postMessage(msg, transferables || []);
    }
  }

  function queueControlMessage(msg) {
    const nextMsg = cloneMessage(msg);
    const key = buildControlOutboxKey(nextMsg);
    if (key) {
      const index = controlOutbox.findIndex((item) => item.key === key);
      if (index >= 0) {
        controlOutbox[index] = { key, msg: nextMsg };
        return;
      }
    }
    controlOutbox.push({ key, msg: nextMsg });
  }

  function flushControlOutbox() {
    if (!ioWorker || !wsConnected || controlOutbox.length === 0) return;
    const pending = controlOutbox;
    controlOutbox = [];
    pending.forEach((entry) => workerSend(entry.msg));
  }

  function replayControlState(reason = "ws_open") {
    if (typeof getReplayControlMessages !== "function") return;
    const messages = getReplayControlMessages(reason);
    if (!Array.isArray(messages) || messages.length === 0) return;
    messages.forEach((msg) => queueControlMessage(msg));
  }

  function sendControlMessage(msg, options = {}) {
    const queueWhenDisconnected = options.queueWhenDisconnected !== false;
    const nextMsg = cloneMessage(msg);
    if (!nextMsg || nextMsg.type !== "send_control") return;
    if (ioWorker && wsConnected) {
      workerSend(nextMsg);
      return;
    }
    if (queueWhenDisconnected) {
      queueControlMessage(nextMsg);
    } else {
      appendDebug("WARN", "SessionControl", null, null, `drop control without ws: ${nextMsg.control || "unknown"}`);
    }
  }

  function syncWorkerConfig() {
    if (!ioWorker || !app.publicConfig) return;
    const config = typeof getWorkerConfig === "function" ? getWorkerConfig() : null;
    if (!config) return;
    workerSend({ type: "config", config });
  }

  function finalizeUtterance(turnId) {
    if (!app.running) return;

    let finalText = "";
    const msgObj = chatStore.findUserMsg(turnId);
    if (msgObj) {
      finalText = msgObj.text;
      msgObj.isPartial = false;
      msgObj.element.classList.remove("partial");
    }
    sendControlMessage({ type: "send_control", control: "trigger_llm", turn_id: turnId, text: finalText });
    appendDebug("INFO", "SessionControl", turnId, finalText, "Trigger LLM explicitly (trigger_llm)");
  }

  function requestAIReply(reason = "surface_host_action") {
    if (!ioWorker) {
      appendDebug("WARN", "SessionControl", null, null, "call_ai_reply skipped: worker not ready");
      return { requested: false, reason: "worker_not_ready" };
    }
    if (!app.running) {
      appendDebug("WARN", "SessionControl", null, null, "call_ai_reply skipped: session not running");
      return { requested: false, reason: "session_not_running" };
    }
    const turnId = app.activeTurnId || app.currentTurn || 0;
    sendControlMessage({ type: "send_control", control: "call_ai_reply", turn_id: turnId, reason });
    appendDebug("INFO", "SessionControl", turnId, null, `Trigger AI reply explicitly (${reason})`);
    return { requested: true, turn_id: turnId };
  }

  function handleVadUtteranceEnd() {
    if (!app.running) return;
    app.utteranceActive = false;
    app.sustainedHighRmsCount = 0;
    app.replyOnsetGuardUntil = Date.now() + app.replyOnsetGuardMs;
    app.replyOnsetGuardTurn = app.currentTurn;
    app.replyOnsetGuardLoggedTurn = 0;

    finalizeUtterance(app.currentTurn);
  }

  function setupWorker() {
    if (ioWorker) {
      ioWorker.terminate();
    }
    stopping = false;
    wsConnected = false;
    ioWorker = new Worker(workerURL);
    syncWorkerConfig();
    ioWorker.onmessage = (event) => {
      const msg = event.data;
      if (!msg) return;
      switch (msg.type) {
        case "ws_open":
          wsConnected = true;
          replayControlState("ws_open");
          flushControlOutbox();
          const limit = (app.publicConfig && app.publicConfig.chat && app.publicConfig.chat.session && app.publicConfig.chat.session.maxHistoryMessages) || app.initialHistorySize || 20;
          if (limit > 0 && (!app.messages || app.messages.length === 0)) {
            appendDebug("INFO", "Network", null, null, `ws connected (via worker), fetching history sliding window limit=${limit}`);
            sendControlMessage({
              type: "send_control",
              control: "fetch_history",
              extra: { limit, before_id: 0, show_more: !!app.showMore },
            });
          } else {
            appendDebug("INFO", "Network", null, null, `ws connected (via worker), skip history fetch (messages.len=${app.messages ? app.messages.length : 0})`);
          }
          break;
        case "ws_close":
          wsConnected = false;
          appendDebug("WARN", "Network", null, null, "ws closed (via worker)");
          if (app.running && !stopping) stopAll("ws closed");
          break;
        case "ws_error":
          wsConnected = false;
          appendDebug("ERROR", "Network", null, null, "ws error (via worker)");
          break;
        case "ws_text":
          try {
            eventRouter.handleEvent(JSON.parse(msg.data));
          } catch (_) {
          }
          break;
        case "ws_binary":
          audioPlayback.handleAudioBinary(msg.data);
          break;
        case "vad_utterance_end":
          handleVadUtteranceEnd();
          break;
      }
    };
  }

  function bindPageCloseSignal() {
    if (pageCloseBound) return;
    pageCloseBound = true;
    window.addEventListener("pagehide", () => {
      sendControlMessage({ type: "send_control", control: "page_close", turn_id: app.currentTurn || 0 });
    });
  }

  async function connectWorkerWS(projectId, threadId) {
    const wsProto = location.protocol === "https:" ? "wss" : "ws";
    const params = new URLSearchParams({ tool_id: "app.chat.stream" });
    if (projectId) params.set("project_id", String(projectId));
    if (threadId) params.set("thread_id", String(threadId));
    const wsUrl = `${wsProto}://${location.host}/api/tool/ws?${params.toString()}`;
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        cleanup();
        reject(new Error("ws connect timeout"));
      }, 8000);

      const cleanup = () => {
        clearTimeout(timeout);
        if (ioWorker) ioWorker.removeEventListener("message", handler);
      };

      const handler = (event) => {
        if (event.data.type === "ws_open") {
          cleanup();
          resolve();
        }
        if (event.data.type === "ws_error") {
          cleanup();
          reject(new Error("ws error"));
        }
        if (event.data.type === "ws_close") {
          cleanup();
          reject(new Error("ws closed before open"));
        }
      };
      ioWorker.addEventListener("message", handler);
      workerSend({ type: "connect", url: wsUrl });
    });
  }

  function teardownWorker() {
    if (!ioWorker) return;
    wsConnected = false;
    try {
      ioWorker.postMessage({ type: "disconnect" });
    } catch (_) {
    }
    try {
      ioWorker.terminate();
    } catch (_) {
    }
    ioWorker = null;
  }

  function resetRunState() {
    app.running = true;
    app.sessionEpoch++;
    setButtons(true);
    app.currentAIMsg = null;
    app.currentPartialMsg = null;
    app.pendingChunkMeta = [];
    app.playbackQueue = [];
    app.playbackEpoch = 0;
    app.preRollBuffer = [];
    app.audioSending = false;
    app.utteranceActive = false;
    app.silentFramesSinceVoice = 0;
    app.sustainedHighRmsCount = 0;
    app.replyOnsetGuardUntil = 0;
    app.replyOnsetGuardTurn = 0;
    app.replyOnsetGuardLoggedTurn = 0;
    app.activeTurnId = 0;
    app.currentTurn = 0;
  }

  async function initWorkerConnection(projectId, threadId) {
    if (ioWorker) return;
    bindPageCloseSignal();
    setupWorker();
    try {
      await connectWorkerWS(projectId, threadId);
    } catch (err) {
      appendDebug("ERROR", "System", null, null, `ws initial connect failed: ${err.message}`);
    }
  }

  async function reconnectWith(projectId, threadId) {
    appendDebug("INFO", "System", null, null, `reconnecting with proj=${projectId} thd=${threadId}`);
    teardownWorker();
    setupWorker();
    try {
      await connectWorkerWS(projectId, threadId);
    } catch (err) {
      appendDebug("ERROR", "System", null, null, `reconnect failed: ${err.message}`);
    }
  }

  async function startAll(projectId, threadId) {
    if (app.running) return;
    try {
      if (!ioWorker) {
        setupWorker();
      }
      await connectWorkerWS(projectId, threadId);
      if (!audioCapture) {
        throw new Error("audio capture not bound");
      }
      await audioCapture.startMic();
      resetRunState();
      workerSend({ type: "start", turn_id: app.currentTurn });
      setStatus("Connecting");
      chatStore.addChatMsg("system", "对话已开始，请说话...", 0);
    } catch (err) {
      appendDebug("ERROR", "System", null, null, `start failed: ${err.message}`);
      stopAll("start failed");
    }
  }

  function stopAll(reason) {
    const wasActive = app.running;
    stopping = true;
    workerSend({ type: "stop", turn_id: app.currentTurn });
    audioPlayback.stopPlayback();
    if (audioCapture) {
      audioCapture.stopMic();
    }
    app.running = false;
    setButtons(false);
    setStatus("Idle");
    if (reason && wasActive) {
      chatStore.addChatMsg("system", `对话结束: ${reason}`, 0);
    }
    stopping = false;
  }

  return {
    bindAudioCapture,
    finalizeUtterance,
    flushControlOutbox,
    initWorkerConnection,
    reconnectWith,
    requestAIReply,
    sendControlMessage,
    startAll,
    stopAll,
    syncWorkerConfig,
    workerSend,
  };
}
