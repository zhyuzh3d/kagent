export function createChatStore(options) {
  const { app, chatArea } = options;

  function noteReplyTurn(turnId) {
    if (!turnId) return;
    if (turnId > app.activeTurnId) {
      app.activeTurnId = turnId;
    }
  }

  app.hasMoreHistory = true;

  const jumpBtn = document.createElement("button");
  jumpBtn.className = "jump-to-bottom";
  jumpBtn.innerHTML = "↓ 新消息";
  jumpBtn.style.cssText = "position:absolute; bottom:20px; right:20px; background:var(--accent); color:#fff; border:none; border-radius:20px; padding:8px 16px; font-size:12px; cursor:pointer; display:none; z-index:10; box-shadow:0 4px 12px rgba(0,0,0,0.3);";
  document.body.appendChild(jumpBtn);

  let jumpModeActive = false;

  jumpBtn.addEventListener("click", () => {
    jumpModeActive = false;
    jumpBtn.style.display = "none";
    clearForJump();
    app.workerSend({ type: "send_control", control: "fetch_history", extra: { limit: (app.pullHistorySize || 10) * 5, cursor: 0 } });
  });

  chatArea.addEventListener("scroll", () => {
    // 1. Check for pulling old history
    if (chatArea.scrollTop < 20 && !app.isFetchingHistory && app.hasMoreHistory && app.running) {
      app.isFetchingHistory = true;
      app.historyLoadingEl = document.createElement("div");
      app.historyLoadingEl.className = "history-loading";
      app.historyLoadingEl.textContent = "加载历史中...";
      app.historyLoadingEl.style.cssText = "text-align:center; padding:10px; color:var(--muted); font-size:12px;";
      chatArea.prepend(app.historyLoadingEl);

      const cursor = getOldestCursor();
      app.workerSend({ type: "send_control", control: "fetch_history", extra: { limit: app.pullHistorySize || 10, cursor } });
    }

    // 2. Hide jump button if scrolled to bottom
    if (chatArea.scrollHeight - chatArea.scrollTop - chatArea.clientHeight < 50) {
      if (jumpModeActive) {
        jumpModeActive = false;
        jumpBtn.style.display = "none";
      }
    }
  });

  function maybeShowJump() {
    if (chatArea.scrollHeight - chatArea.scrollTop - chatArea.clientHeight > 100) {
      jumpModeActive = true;
      jumpBtn.style.display = "block";
      return true; // We intercepted the scroll
    }
    return false;
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
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
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
      if (!maybeShowJump()) {
        chatArea.scrollTop = chatArea.scrollHeight;
      }
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
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
  }

  function findAIMsg(turnId, sessionEpoch = app.sessionEpoch) {
    for (let i = app.messages.length - 1; i >= 0; i--) {
      const msg = app.messages[i];
      if (msg.role === "ai" && msg.turnId === turnId && msg.sessionEpoch === sessionEpoch) {
        return msg;
      }
    }
    return null;
  }

  function getAIMsgText(turnId) {
    const msg = findAIMsg(turnId);
    return msg ? msg.text : "";
  }

  function setAIMsgText(turnId, text) {
    const clean = typeof text === "string" ? text : "";
    let msg = findAIMsg(turnId);
    if (!msg) {
      if (!clean) return;
      msg = addChatMsg("ai", clean, turnId);
      if (app.currentAIMsg && app.currentAIMsg.turnId === turnId) {
        app.currentAIMsg = msg;
      }
      return;
    }
    msg.text = clean;
    msg.element.textContent = clean;
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
  }

  function removeAIMsg(turnId) {
    const msg = findAIMsg(turnId);
    if (!msg) return;
    msg.element.remove();
    app.messages = app.messages.filter((item) => item !== msg);
    if (app.currentAIMsg === msg) {
      app.currentAIMsg = null;
    }
  }

  function finalizeAI(turnId) {
    if (app.currentAIMsg && app.currentAIMsg.turnId === turnId && app.currentAIMsg.sessionEpoch === app.sessionEpoch) {
      app.currentAIMsg = null;
    }
  }

  function handleHistorySync(historyMessages, hasMore) {
    app.hasMoreHistory = hasMore;
    app.isFetchingHistory = false;

    if (app.historyLoadingEl) {
      app.historyLoadingEl.remove();
      app.historyLoadingEl = null;
    }

    if (!historyMessages || historyMessages.length === 0) {
      if (!hasMore && app.messages.length > 0 && !app.historyNoMoreEl) {
        app.historyNoMoreEl = document.createElement("div");
        app.historyNoMoreEl.className = "history-no-more";
        app.historyNoMoreEl.textContent = "— 已显示全部历史 —";
        chatArea.prepend(app.historyNoMoreEl);
      }
      return;
    }

    const scrollHeightBefore = chatArea.scrollHeight;
    const scrollTopBefore = chatArea.scrollTop;

    const fragment = document.createDocumentFragment();
    const batch = [];
    for (const m of historyMessages) {
      const div = document.createElement("div");
      div.className = `msg ${m.role}`;
      div.dataset.msgId = m.message_id || "";
      div.dataset.ts = m.created_at_ms || 0;
      div.textContent = m.content;
      fragment.appendChild(div);
      batch.push({
        role: m.role,
        text: m.content,
        msgId: m.message_id,
        ts: m.created_at_ms,
        turnId: 0,
        sessionEpoch: 0,
        element: div,
        isPartial: false
      });
    }

    chatArea.prepend(fragment);
    app.messages = batch.concat(app.messages);

    chatArea.scrollTop = scrollTopBefore + (chatArea.scrollHeight - scrollHeightBefore);

    if (!hasMore && !app.historyNoMoreEl) {
      app.historyNoMoreEl = document.createElement("div");
      app.historyNoMoreEl.className = "history-no-more";
      app.historyNoMoreEl.textContent = "— 已显示全部历史 —";
      chatArea.prepend(app.historyNoMoreEl);
    }

    trimDOMNodes();
  }

  function trimDOMNodes() {
    const maxNodes = (app.pullHistorySize || 10) * 5;
    if (app.messages.length > maxNodes) {
      // Very basic trim to prevent complete tab freeze
      // A more complete sliding window requires intersection observers.
      // For now, if we exceed limits significantly, we trim older messages (if scrolling down)
      // or newer messages (if scrolling up).
      // Let's rely on jumpToBottom for resetting when new messages arrive.
    }
  }

  function getOldestCursor() {
    for (const msg of app.messages) {
      if (msg.ts > 0) return msg.ts;
    }
    return 0;
  }

  function clearForJump() {
    app.messages.forEach(m => {
      if (m.element) m.element.remove();
    });
    app.messages = [];
    if (app.historyNoMoreEl) {
      app.historyNoMoreEl.remove();
      app.historyNoMoreEl = null;
    }
    if (app.historyLoadingEl) {
      app.historyLoadingEl.remove();
      app.historyLoadingEl = null;
    }
    app.hasMoreHistory = true;
  }

  return {
    noteReplyTurn,
    isReplyPayloadType,
    addChatMsg,
    findUserMsg,
    updatePartialASR,
    finalizeASR,
    appendAIDelta,
    getAIMsgText,
    setAIMsgText,
    removeAIMsg,
    finalizeAI,
    handleHistorySync,
    getOldestCursor,
    clearForJump,
  };
}
