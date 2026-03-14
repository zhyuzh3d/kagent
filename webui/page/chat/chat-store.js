function prettyJSON(raw) {
  if (!raw || typeof raw !== "string") return "";
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch (_) {
    return raw;
  }
}

function actionBadgeText(actionJSON) {
  if (!actionJSON || typeof actionJSON !== "string") return "";
  try {
    const parsed = JSON.parse(actionJSON);
    const t = String(parsed.type || "").trim();
    return t ? `A:${t}` : "A";
  } catch (_) {
    return "A";
  }
}

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
    app.workerSend({ type: "send_control", control: "fetch_history", extra: { limit: (app.pullHistorySize || 10) * 5, before_id: 0, show_more: !!app.showMore } });
  });

  chatArea.addEventListener("scroll", () => {
    if (chatArea.scrollTop < 20 && !app.isFetchingHistory && app.hasMoreHistory && app.running) {
      app.isFetchingHistory = true;
      app.historyLoadingEl = document.createElement("div");
      app.historyLoadingEl.className = "history-loading";
      app.historyLoadingEl.textContent = "加载历史中...";
      app.historyLoadingEl.style.cssText = "text-align:center; padding:10px; color:var(--muted); font-size:12px;";
      chatArea.prepend(app.historyLoadingEl);

      const beforeID = getOldestBeforeID();
      app.workerSend({ type: "send_control", control: "fetch_history", extra: { limit: app.pullHistorySize || 10, before_id: beforeID, show_more: !!app.showMore } });
    }

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
      return true;
    }
    return false;
  }

  function isReplyPayloadType(type) {
    return type === "llm_delta" || type === "llm_final" || type === "tts_chunk";
  }

  function isDefaultVisibleRole(role) {
    return role === "user" || role === "assistant" || role === "ai";
  }

  function applyRoleVisibility(msg) {
    if (!msg || !msg.element) return;
    if (app.showMore) {
      msg.element.style.display = "";
      return;
    }
    msg.element.style.display = isDefaultVisibleRole(msg.role) ? "" : "none";
  }

  function buildDebugText(msg) {
    const lines = [];
    if (msg.createdLocalYMDHMS || msg.createdLocalWeekday || msg.createdLocalLunar) {
      lines.push([msg.createdLocalYMDHMS, msg.createdLocalWeekday, msg.createdLocalLunar].filter(Boolean).join(" "));
    }
    if (msg.actionJSON) {
      lines.push("action_json:");
      lines.push(prettyJSON(msg.actionJSON));
    }
    if (msg.parseError) {
      lines.push(`parse_error: ${msg.parseError}`);
    }
    if (msg.rawData) {
      lines.push("raw_data:");
      lines.push(prettyJSON(msg.rawData));
    }
    return lines.join("\n");
  }

  function renderMessage(msg) {
    if (!msg) return;
    const mainText = typeof msg.say === "string" ? msg.say : (typeof msg.text === "string" ? msg.text : "");
    msg.text = mainText;
    msg.say = mainText;

    msg.element.className = `msg ${msg.role} ${msg.isPartial ? "partial" : ""}`.trim();
    msg.element.dataset.turnId = msg.turnId || 0;
    msg.element.dataset.msgId = msg.msgId || "";
    msg.element.dataset.storeId = msg.storeId || 0;
    msg.element.dataset.sessionEpoch = msg.sessionEpoch || 0;

    msg.mainEl.textContent = mainText || "";
    msg.metaEl.textContent = msg.aside || "";
    msg.metaEl.style.display = msg.aside ? "block" : "none";

    const badge = actionBadgeText(msg.actionJSON || "");
    msg.actionBadgeEl.textContent = badge;
    msg.actionBadgeEl.style.display = badge ? "inline-flex" : "none";

    msg.element.classList.toggle("malformed", !!msg.parseError);

    const debugText = buildDebugText(msg);
    msg.debugEl.textContent = debugText;
    msg.debugEl.style.display = app.showMore && debugText ? "block" : "none";

    applyRoleVisibility(msg);
  }

  function createMessageRecord(input) {
    const msg = {
      role: input.role || "system",
      say: typeof input.say === "string" ? input.say : (typeof input.text === "string" ? input.text : ""),
      text: typeof input.say === "string" ? input.say : (typeof input.text === "string" ? input.text : ""),
      aside: typeof input.aside === "string" ? input.aside : "",
      actionJSON: typeof input.actionJSON === "string" ? input.actionJSON : "",
      rawData: typeof input.rawData === "string" ? input.rawData : "",
      parseError: typeof input.parseError === "string" ? input.parseError : "",
      turnId: Number.isFinite(input.turnId) ? input.turnId : 0,
      sessionEpoch: Number.isFinite(input.sessionEpoch) ? input.sessionEpoch : app.sessionEpoch,
      msgId: typeof input.msgId === "string" ? input.msgId : "",
      storeId: Number.isFinite(input.storeId) ? input.storeId : 0,
      createdLocalYMDHMS: typeof input.createdLocalYMDHMS === "string" ? input.createdLocalYMDHMS : "",
      createdLocalWeekday: typeof input.createdLocalWeekday === "string" ? input.createdLocalWeekday : "",
      createdLocalLunar: typeof input.createdLocalLunar === "string" ? input.createdLocalLunar : "",
      isPartial: !!input.isPartial,
    };

    const div = document.createElement("div");
    const mainEl = document.createElement("div");
    mainEl.className = "msg-main";
    const metaEl = document.createElement("div");
    metaEl.className = "msg-aside";
    const actionBadgeEl = document.createElement("span");
    actionBadgeEl.className = "msg-action-badge";
    const debugEl = document.createElement("pre");
    debugEl.className = "msg-debug";

    div.appendChild(actionBadgeEl);
    div.appendChild(mainEl);
    div.appendChild(metaEl);
    div.appendChild(debugEl);

    msg.element = div;
    msg.mainEl = mainEl;
    msg.metaEl = metaEl;
    msg.actionBadgeEl = actionBadgeEl;
    msg.debugEl = debugEl;
    renderMessage(msg);
    return msg;
  }

  function addChatMsg(role, text, turnId, isPartial = false, sessionEpoch = app.sessionEpoch, extra = {}) {
    const msg = createMessageRecord({
      role,
      say: text,
      turnId,
      isPartial,
      sessionEpoch,
      aside: typeof extra.aside === "string" ? extra.aside : "",
      actionJSON: typeof extra.actionJSON === "string" ? extra.actionJSON : "",
      rawData: typeof extra.rawData === "string" ? extra.rawData : "",
      parseError: typeof extra.parseError === "string" ? extra.parseError : "",
    });
    chatArea.appendChild(msg.element);
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
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
      msg.say = text;
      msg.text = text;
      msg.isPartial = true;
      renderMessage(msg);
      if (!maybeShowJump()) {
        chatArea.scrollTop = chatArea.scrollHeight;
      }
    }
  }

  function finalizeASR(text, turnId) {
    const msg = findUserMsg(turnId);
    if (msg) {
      msg.say = text || "";
      msg.text = text || "";
      msg.isPartial = false;
      if (!text) {
        msg.element.remove();
        app.messages = app.messages.filter((item) => item !== msg);
      } else {
        renderMessage(msg);
      }
      return;
    }
    if (text) {
      addChatMsg("user", text, turnId);
    }
  }

  function findAIMsg(turnId, sessionEpoch = app.sessionEpoch) {
    for (let i = app.messages.length - 1; i >= 0; i--) {
      const msg = app.messages[i];
      if ((msg.role === "assistant" || msg.role === "ai") && msg.turnId === turnId && msg.sessionEpoch === sessionEpoch) {
        return msg;
      }
    }
    return null;
  }

  function appendAIDelta(text, turnId) {
    if (!app.currentAIMsg || app.currentAIMsg.turnId !== turnId || app.currentAIMsg.sessionEpoch !== app.sessionEpoch) {
      app.currentAIMsg = addChatMsg("assistant", "", turnId);
    }
    app.currentAIMsg.say += text;
    app.currentAIMsg.text = app.currentAIMsg.say;
    renderMessage(app.currentAIMsg);
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
  }

  function getAIMsgText(turnId) {
    const msg = findAIMsg(turnId);
    return msg ? msg.say : "";
  }

  function setAIMsgText(turnId, text) {
    const clean = typeof text === "string" ? text : "";
    let msg = findAIMsg(turnId);
    if (!msg) {
      if (!clean) return;
      msg = addChatMsg("assistant", clean, turnId);
      if (app.currentAIMsg && app.currentAIMsg.turnId === turnId) {
        app.currentAIMsg = msg;
      }
      return;
    }
    msg.say = clean;
    msg.text = clean;
    renderMessage(msg);
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
  }

  function setAIMsgMeta(turnId, meta = {}) {
    const msg = findAIMsg(turnId);
    if (!msg) return;
    if (typeof meta.say === "string") {
      msg.say = meta.say;
      msg.text = meta.say;
    }
    if (typeof meta.aside === "string") msg.aside = meta.aside;
    if (typeof meta.actionJSON === "string") msg.actionJSON = meta.actionJSON;
    if (typeof meta.rawData === "string") msg.rawData = meta.rawData;
    if (typeof meta.parseError === "string") msg.parseError = meta.parseError;
    renderMessage(msg);
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
      const role = (m.role === "assistant") ? "assistant" : (m.role || "system");
      const say = (typeof m.say === "string" && m.say) ? m.say : (typeof m.content === "string" ? m.content : "");
      const record = createMessageRecord({
        role,
        say,
        aside: typeof m.aside === "string" ? m.aside : "",
        actionJSON: typeof m.action_json === "string" ? m.action_json : "",
        rawData: typeof m.raw_data === "string" ? m.raw_data : "",
        parseError: typeof m.parse_error === "string" ? m.parse_error : "",
        msgId: m.message_id || "",
        storeId: Number.isFinite(m.store_id) ? m.store_id : 0,
        turnId: Number.isFinite(m.turn_id) ? m.turn_id : 0,
        sessionEpoch: 0,
        createdLocalYMDHMS: m.created_at_local_ymdhms || "",
        createdLocalWeekday: m.created_at_local_weekday || "",
        createdLocalLunar: m.created_at_local_lunar || "",
      });
      fragment.appendChild(record.element);
      batch.push(record);
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
  }

  function rerenderAll() {
    app.messages.forEach((msg) => renderMessage(msg));
  }

  function setShowMore(enabled) {
    app.showMore = !!enabled;
    rerenderAll();
  }

  function getOldestBeforeID() {
    for (const msg of app.messages) {
      if (msg.storeId > 0) return msg.storeId;
    }
    return 0;
  }

  function clearForJump() {
    app.messages.forEach((m) => {
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
    setAIMsgMeta,
    removeAIMsg,
    finalizeAI,
    handleHistorySync,
    getOldestBeforeID,
    clearForJump,
    setShowMore,
  };
}
