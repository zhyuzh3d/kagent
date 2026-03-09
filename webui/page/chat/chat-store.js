export function createChatStore(options) {
  const { app, chatArea } = options;

  function noteReplyTurn(turnId) {
    if (!turnId) return;
    if (turnId > app.activeTurnId) {
      app.activeTurnId = turnId;
    }
  }

  function isReplyPayloadType(type) {
    return type === "llm_delta" || type === "llm_final" || type === "tts_chunk";
  }

  function addChatMsg(role, text, turnId, isPartial = false, sessionEpoch = app.sessionEpoch) {
    const div = document.createElement("div");
    div.className = `msg ${role} ${isPartial ? "partial" : ""}`;
    div.dataset.turnId = turnId;
    div.dataset.sessionEpoch = sessionEpoch;
    div.textContent = text;
    chatArea.appendChild(div);
    chatArea.scrollTop = chatArea.scrollHeight;
    const msg = { role, text, turnId, sessionEpoch, element: div, isPartial };
    app.messages.push(msg);
    return msg;
  }

  function findUserMsg(turnId, sessionEpoch = app.sessionEpoch) {
    for (let i = app.messages.length - 1; i >= 0; i--) {
      const msg = app.messages[i];
      if (msg.role === "user" && msg.turnId === turnId && msg.sessionEpoch === sessionEpoch) {
        return msg;
      }
    }
    return null;
  }

  function updatePartialASR(text, turnId) {
    if (!text) return;
    let msg = findUserMsg(turnId);
    if (!msg) {
      msg = addChatMsg("user", text, turnId, true);
    } else {
      msg.text = text;
      msg.element.textContent = text;
      chatArea.scrollTop = chatArea.scrollHeight;
    }
  }

  function finalizeASR(text, turnId) {
    const msg = findUserMsg(turnId);
    if (msg) {
      msg.text = text;
      msg.isPartial = false;
      if (text) {
        msg.element.textContent = text;
      } else {
        msg.element.remove();
        app.messages = app.messages.filter((item) => item !== msg);
      }
      msg.element.classList.remove("partial");
      return;
    }
    if (text) {
      addChatMsg("user", text, turnId);
    }
  }

  function appendAIDelta(text, turnId) {
    if (!app.currentAIMsg || app.currentAIMsg.turnId !== turnId || app.currentAIMsg.sessionEpoch !== app.sessionEpoch) {
      app.currentAIMsg = addChatMsg("ai", "", turnId);
    }
    app.currentAIMsg.text += text;
    app.currentAIMsg.element.textContent = app.currentAIMsg.text;
    chatArea.scrollTop = chatArea.scrollHeight;
  }

  function finalizeAI(turnId) {
    if (app.currentAIMsg && app.currentAIMsg.turnId === turnId && app.currentAIMsg.sessionEpoch === app.sessionEpoch) {
      app.currentAIMsg = null;
    }
  }

  return {
    noteReplyTurn,
    isReplyPayloadType,
    addChatMsg,
    findUserMsg,
    updatePartialASR,
    finalizeASR,
    appendAIDelta,
    finalizeAI,
  };
}
