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
  } = options;

  let ioWorker = null;
  let audioCapture = null;
  let stopping = false;
  let pageCloseBound = false;

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
    workerSend({ type: "send_control", control: "trigger_llm", turn_id: turnId, text: finalText });
    appendDebug("INFO", "SessionControl", turnId, finalText, "Trigger LLM explicitly (trigger_llm)");
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
    ioWorker = new Worker(workerURL);
    syncWorkerConfig();
    ioWorker.onmessage = (event) => {
      const msg = event.data;
      if (!msg) return;
      switch (msg.type) {
        case "ws_open":
          const limit = (app.publicConfig && app.publicConfig.chat && app.publicConfig.chat.session && app.publicConfig.chat.session.maxHistoryMessages) || app.initialHistorySize || 20;
          appendDebug("INFO", "Network", null, null, `ws connected (via worker), fetching history sliding window limit=${limit}`);
          if (limit > 0) {
            workerSend({ type: "send_control", control: "fetch_history", extra: { limit: limit, before_id: 0, show_more: !!app.showMore } });
          }
          break;
        case "ws_close":
          appendDebug("WARN", "Network", null, null, "ws closed (via worker)");
          if (app.running && !stopping) stopAll("ws closed");
          break;
        case "ws_error":
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
      workerSend({ type: "send_control", control: "page_close", turn_id: app.currentTurn || 0 });
    });
  }

  async function connectWorkerWS() {
    const wsUrl = `ws://${location.host}/ws`;
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

  async function initWorkerConnection() {
    if (ioWorker) return;
    bindPageCloseSignal();
    setupWorker();
    try {
      await connectWorkerWS();
    } catch (err) {
      appendDebug("ERROR", "System", null, null, `ws initial connect failed: ${err.message}`);
    }
  }

  async function startAll() {
    if (app.running) return;
    try {
      if (!ioWorker) {
        setupWorker();
      }
      await connectWorkerWS();
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
    startAll,
    stopAll,
    syncWorkerConfig,
    workerSend,
    finalizeUtterance,
    initWorkerConnection,
  };
}
