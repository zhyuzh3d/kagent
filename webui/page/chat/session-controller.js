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

  function handleVadUtteranceEnd() {
    if (!app.running) return;
    app.utteranceActive = false;
    app.sustainedHighRmsCount = 0;

    let finalText = "";
    const msgObj = chatStore.findUserMsg(app.currentTurn);
    if (msgObj) {
      finalText = msgObj.text;
      msgObj.isPartial = false;
      msgObj.element.classList.remove("partial");
    }
    app.replyOnsetGuardUntil = Date.now() + app.replyOnsetGuardMs;
    app.replyOnsetGuardTurn = app.currentTurn;
    app.replyOnsetGuardLoggedTurn = 0;
    workerSend({ type: "send_control", control: "trigger_llm", turn_id: app.currentTurn, text: finalText });
    appendDebug("INFO", "SessionControl", app.currentTurn, finalText, "Trigger LLM explicitly (trigger_llm)");
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
          appendDebug("INFO", "Network", null, null, "ws connected (via worker)");
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

  async function startAll() {
    if (app.running) return;
    try {
      setupWorker();
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
    teardownWorker();
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
  };
}
