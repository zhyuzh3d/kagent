export function createEventRouter(options) {
  const {
    app,
    chatStore,
    audioPlayback,
    setStatus,
    flashIndicator,
    appendDebug,
    onLLMDelta,
    onLLMFinal,
  } = options;

  function syncCurrentTurn(msg) {
    const turnId = Number(msg && msg.turn_id);
    if (!Number.isFinite(turnId) || turnId <= 0) return;
    if (turnId > app.currentTurn) {
      app.currentTurn = turnId;
    }
  }

  function handleEvent(msg) {
    const type = msg.type;
    if (!type) return;
    syncCurrentTurn(msg);

    if (type === "action_report") {
      const payload = msg.payload && typeof msg.payload === "object" ? msg.payload : {};
      chatStore.addChatMsg("observer", "", msg.turn_id || 0, false, app.sessionEpoch, {
        actionJSON: JSON.stringify(payload),
        aside: "",
      });
      return;
    }

    if (type === "state_change") {
      const action = {
        type: "state_change",
        surface_id: msg.surface_id || "",
        surface_type: msg.surface_type || "",
        surface_version: msg.surface_version || "",
        state_version: Number.isFinite(msg.state_version) ? msg.state_version : 0,
        delta_or_state: msg.business_state && typeof msg.business_state === "object" ? msg.business_state : {},
      };
      chatStore.addChatMsg("observer", "", msg.turn_id || 0, false, app.sessionEpoch, {
        actionJSON: JSON.stringify(action),
      });
      return;
    }

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

    if (type === "history_sync") {
      chatStore.handleHistorySync(msg.messages || [], msg.has_more);
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
      if (typeof onLLMDelta === "function") {
        const handled = onLLMDelta({
          turnId: msg.turn_id,
          text: msg.text || "",
          message: msg,
        });
        if (handled && handled.handled) {
          if (typeof handled.content === "string") {
            chatStore.setAIMsgText(msg.turn_id, handled.content);
          }
          return;
        }
      }
      chatStore.appendAIDelta(msg.text || "", msg.turn_id);
      return;
    }
    if (type === "llm_final") {
      const finalText = (typeof msg.text === "string" && msg.text) ? msg.text : chatStore.getAIMsgText(msg.turn_id);
      if (typeof onLLMFinal === "function") {
        onLLMFinal({
          turnId: msg.turn_id,
          text: finalText,
          message: msg,
        });
      }
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
