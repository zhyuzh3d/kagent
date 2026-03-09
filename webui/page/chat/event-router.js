export function createEventRouter(options) {
  const {
    app,
    chatStore,
    audioPlayback,
    setStatus,
    flashIndicator,
    appendDebug,
  } = options;

  function handleEvent(msg) {
    const type = msg.type;
    if (!type) return;

    if (type === "status") {
      const value = msg.value || "";
      if ((value === "Thinking" || value === "Speaking") && (msg.turn_id || 0) > 0) {
        chatStore.noteReplyTurn(msg.turn_id);
      }
      setStatus(value);
      if (value === "Error") flashIndicator("error");
      else flashIndicator("receive");
      appendDebug("INFO", "AppState", msg.turn_id || 0, msg.detail || "", `Status update: ${value}`);
      return;
    }

    if (type === "error") {
      setStatus("Error");
      flashIndicator("error");
      appendDebug("ERROR", "AppState", msg.turn_id || 0, null, `code=${msg.code || ""} message=${msg.message || ""}`);
      return;
    }

    if (type === "tts_warn") {
      flashIndicator("receive");
      const seq = msg.seq ? ` seq=${msg.seq}` : "";
      appendDebug("WARN", "TTSBackend", msg.turn_id || 0, msg.text || null, `code=${msg.code || "tts_warn"}${seq} message=${msg.message || "tts segment warning"}`);
      return;
    }

    if (type === "turn_nack") {
      if ((msg.turn_id || 0) === app.replyOnsetGuardTurn) {
        app.replyOnsetGuardUntil = 0;
        app.replyOnsetGuardTurn = 0;
        app.replyOnsetGuardLoggedTurn = 0;
      }
      appendDebug("WARN", "SessionControl", msg.turn_id || 0, null, `turn_nack received, activeReply=${app.activeTurnId}`);
      return;
    }

    if (msg.turn_id && (type === "asr_partial" || type === "asr_final") && msg.turn_id !== app.currentTurn) {
      appendDebug("DEBUG", "Network", msg.turn_id, null, `Stale msg dropped: type=${type} (currentInput=${app.currentTurn})`);
      return;
    }

    if (msg.turn_id && chatStore.isReplyPayloadType(type) && msg.turn_id < app.activeTurnId) {
      appendDebug("DEBUG", "Network", msg.turn_id, null, `Stale msg dropped: type=${type} (activeReply=${app.activeTurnId})`);
      return;
    }

    if (msg.turn_id && chatStore.isReplyPayloadType(type)) {
      chatStore.noteReplyTurn(msg.turn_id);
    }

    flashIndicator("receive");

    if (type === "asr_partial") {
      chatStore.updatePartialASR(msg.text || "", msg.turn_id);
      return;
    }
    if (type === "asr_final") {
      chatStore.finalizeASR(msg.text || "", msg.turn_id);
      return;
    }
    if (type === "llm_delta") {
      chatStore.appendAIDelta(msg.text || "", msg.turn_id);
      return;
    }
    if (type === "llm_final") {
      chatStore.finalizeAI(msg.turn_id);
      return;
    }
    if (type === "tts_chunk") {
      audioPlayback.queueChunkMeta(msg);
    }
  }

  return {
    handleEvent,
  };
}
