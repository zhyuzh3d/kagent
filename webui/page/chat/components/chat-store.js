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

function pad2(value) {
  return String(value).padStart(2, "0");
}

function chineseLunarDayName(day) {
  const days = ["", "初一", "初二", "初三", "初四", "初五", "初六", "初七", "初八", "初九", "初十", "十一", "十二", "十三", "十四", "十五", "十六", "十七", "十八", "十九", "二十", "廿一", "廿二", "廿三", "廿四", "廿五", "廿六", "廿七", "廿八", "廿九", "三十"];
  return days[day] || String(day || "").trim();
}

function buildSemanticTimeFields(tsMS) {
  const sourceMS = Number.isFinite(tsMS) && tsMS > 0 ? tsMS : Date.now();
  const date = new Date(sourceMS);
  if (Number.isNaN(date.getTime())) {
    return {
      createdLocalYMDHMS: "",
      createdLocalWeekday: "",
      createdLocalLunar: "",
      createdAtMS: 0,
    };
  }
  const createdLocalYMDHMS = `${date.getFullYear()}年${pad2(date.getMonth() + 1)}月${pad2(date.getDate())}日 ${pad2(date.getHours())}:${pad2(date.getMinutes())}:${pad2(date.getSeconds())}`;
  let createdLocalWeekday = "";
  try {
    createdLocalWeekday = new Intl.DateTimeFormat("zh-CN", { weekday: "long" }).format(date);
  } catch (_) {}
  let createdLocalLunar = "";
  try {
    const parts = new Intl.DateTimeFormat("zh-CN-u-ca-chinese", {
      year: "numeric",
      month: "long",
      day: "numeric",
    }).formatToParts(date);
    const month = parts.find((part) => part.type === "month")?.value || "";
    const day = Number(parts.find((part) => part.type === "day")?.value || 0);
    if (month && day > 0) {
      createdLocalLunar = `农历${month}${chineseLunarDayName(day)}`;
    }
  } catch (_) {}
  return {
    createdAtMS: sourceMS,
    createdLocalYMDHMS,
    createdLocalWeekday,
    createdLocalLunar,
  };
}

function safeParseObject(raw) {
  if (typeof raw !== "string" || !raw.trim()) return null;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch (_) {
    return null;
  }
}

function normalizeText(value) {
  return typeof value === "string" ? value.trim() : "";
}

function normalizeObserverSource(payload, fallback = {}) {
  const data = payload && typeof payload === "object" ? payload : {};
  const category = normalizeText(fallback.category);
  const messageType = normalizeText(fallback.messageType);
  let kind = normalizeText(fallback.sourceKind) || normalizeText(data.source_kind);
  let id = normalizeText(fallback.sourceId) || normalizeText(data.source_id);
  let label = normalizeText(fallback.sourceLabel) || normalizeText(data.source_label);
  const surfaceID = normalizeText(data.surface_id) || normalizeText(data.active_surface_id);
  const surfaceLabel = normalizeText(data.surface_title) || normalizeText(data.surface_name);

  if (!kind) {
    if (surfaceID) {
      kind = "surface";
    } else if (category === "surface_context" || messageType === "surface_registry_sync" || messageType === "surface_active_change") {
      kind = "page";
    } else {
      kind = "system";
    }
  }
  if (!id) {
    if (kind === "surface") id = surfaceID || "surface";
    else if (kind === "page") id = "page/chat";
    else id = "chat_server";
  }
  if (!label) {
    if (kind === "surface") label = surfaceLabel || id;
    else if (kind === "page") label = "Chat Page";
    else label = "Chat Server";
  }
  return { kind, id, label };
}

function observerPayloadFingerprint(category, messageType, payload) {
  const data = payload && typeof payload === "object" ? payload : {};
  const cat = normalizeText(category);
  const typ = normalizeText(messageType);
  if (cat === "surface_context" && typ === "surface_registry_sync") {
    const registry = Array.isArray(data.registry) ? data.registry : [];
    const ids = registry.map((item) => normalizeText(item && item.surface_id)).filter(Boolean).sort();
    return JSON.stringify({
      category: cat,
      message_type: typ,
      active_surface_id: normalizeText(data.active_surface_id),
      registry_ids: ids,
      context_version: Number.isFinite(data.context_version) ? data.context_version : 0,
    });
  }
  if (cat === "surface_context" && typ === "surface_active_change") {
    return JSON.stringify({
      category: cat,
      message_type: typ,
      active_surface_id: normalizeText(data.active_surface_id),
      context_version: Number.isFinite(data.context_version) ? data.context_version : 0,
    });
  }
  if (cat === "surface_context" && typ === "surface_runtime_context") {
    const runtime = data.runtime_context && typeof data.runtime_context === "object" ? data.runtime_context : {};
    return JSON.stringify({
      category: cat,
      message_type: typ,
      surface_id: normalizeText(data.surface_id) || normalizeText(runtime.surface_id),
      mode: normalizeText(runtime.mode),
      open: !!runtime.open,
      ready: !!runtime.ready,
      context_version: Number.isFinite(data.context_version) ? data.context_version : 0,
      actions_len: Array.isArray(runtime.actions) ? runtime.actions.length : 0,
    });
  }
  if (cat === "surface") {
    return JSON.stringify({
      category: cat,
      message_type: typ,
      surface_id: normalizeText(data.surface_id),
      event_type: normalizeText(data.event_type),
      state_version: Number.isFinite(data.state_version) ? data.state_version : 0,
      visible_text: normalizeText(data.visible_text),
    });
  }
  return "";
}

function buildAssistantMatchKey(turnId, sessionEpoch) {
  if (!Number.isFinite(turnId) || turnId <= 0) return "";
  const epoch = Number.isFinite(sessionEpoch) ? sessionEpoch : 0;
  return `assistant:${turnId}:${epoch}`;
}

function buildObserverMatchKey(turnId, category, messageType, sourceKey, payloadFingerprint) {
  if (!Number.isFinite(turnId) || turnId <= 0) return "";
  const cat = normalizeText(category);
  const typ = normalizeText(messageType);
  if (!cat || !typ) return "";
  const src = normalizeText(sourceKey);
  const fp = normalizeText(payloadFingerprint);
  return `observer:${turnId}:${cat}:${typ}:${src}:${fp}`;
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

  function isPrimaryConversationRole(role) {
    return role === "user" || role === "assistant" || role === "ai";
  }

  function hasPrimaryContent(msg) {
    if (!msg) return false;
    return typeof msg.say === "string" && msg.say.trim().length > 0;
  }

  function hasExpandedContent(msg) {
    if (!msg) return false;
    return !!(
      (typeof msg.aside === "string" && msg.aside.trim()) ||
      (typeof msg.actionJSON === "string" && msg.actionJSON.trim()) ||
      (typeof msg.rawData === "string" && msg.rawData.trim()) ||
      (typeof msg.parseError === "string" && msg.parseError.trim()) ||
      (typeof msg.createdLocalYMDHMS === "string" && msg.createdLocalYMDHMS.trim()) ||
      (typeof msg.createdLocalWeekday === "string" && msg.createdLocalWeekday.trim()) ||
      (typeof msg.createdLocalLunar === "string" && msg.createdLocalLunar.trim())
    );
  }

  function applyRoleVisibility(msg) {
    if (!msg || !msg.element) return;
    if (app.showMore) {
      msg.element.style.display = "";
      return;
    }
    msg.element.style.display = isPrimaryConversationRole(msg.role) && hasPrimaryContent(msg) ? "" : "none";
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
    msg.element.dataset.category = msg.category || "";
    msg.element.dataset.messageType = msg.messageType || "";
    msg.element.dataset.sourceKey = msg.sourceKey || "";

    msg.mainEl.textContent = mainText || "";
    msg.mainEl.style.display = mainText ? "block" : "none";

    const expandedAside = app.showMore ? (msg.aside || "") : "";
    msg.metaEl.textContent = expandedAside;
    msg.metaEl.style.display = expandedAside ? "block" : "none";

    const badge = actionBadgeText(msg.actionJSON || "");
    msg.actionBadgeEl.textContent = badge;
    msg.actionBadgeEl.style.display = app.showMore && badge ? "inline-flex" : "none";

    msg.element.classList.toggle("malformed", !!msg.parseError);
    msg.element.classList.toggle("msg-expanded-only", !mainText && hasExpandedContent(msg));

    const debugText = buildDebugText(msg);
    msg.debugEl.textContent = debugText;
    msg.debugEl.style.display = app.showMore && debugText ? "block" : "none";

    applyRoleVisibility(msg);
  }

  function createMessageRecord(input) {
    const category = normalizeText(input.category);
    const messageType = normalizeText(input.messageType);
    const payloadJSON = typeof input.payloadJSON === "string" ? input.payloadJSON : "";
    const payload = safeParseObject(payloadJSON);
    const source = normalizeObserverSource(payload, {
      category,
      messageType,
      sourceKind: input.sourceKind,
      sourceId: input.sourceId,
      sourceLabel: input.sourceLabel,
    });
    const sourceKey = `${source.kind}:${source.id}`;
    const payloadFingerprint = typeof input.payloadFingerprint === "string" && input.payloadFingerprint
      ? input.payloadFingerprint
      : observerPayloadFingerprint(category, messageType, payload);
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
      createdAtMS: Number.isFinite(input.createdAtMS) ? input.createdAtMS : 0,
      isPartial: !!input.isPartial,
      category,
      messageType,
      payloadJSON,
      sourceKind: source.kind,
      sourceId: source.id,
      sourceLabel: source.label,
      sourceKey,
      payloadFingerprint,
      matchKey: typeof input.matchKey === "string" ? input.matchKey : "",
      isTemporary: !!input.isTemporary || !(typeof input.msgId === "string" && input.msgId) && !(Number.isFinite(input.storeId) && input.storeId > 0),
    };
    if (!msg.matchKey) {
      if (msg.role === "assistant" || msg.role === "ai") {
        msg.matchKey = buildAssistantMatchKey(msg.turnId, msg.sessionEpoch);
      } else if (msg.role === "observer") {
        msg.matchKey = buildObserverMatchKey(msg.turnId, msg.category, msg.messageType, msg.sourceKey, msg.payloadFingerprint);
      }
    }
    if (!msg.createdLocalYMDHMS || !msg.createdLocalWeekday || !msg.createdLocalLunar) {
      const semanticTime = buildSemanticTimeFields(msg.createdAtMS);
      msg.createdAtMS = msg.createdAtMS || semanticTime.createdAtMS;
      if (!msg.createdLocalYMDHMS) msg.createdLocalYMDHMS = semanticTime.createdLocalYMDHMS;
      if (!msg.createdLocalWeekday) msg.createdLocalWeekday = semanticTime.createdLocalWeekday;
      if (!msg.createdLocalLunar) msg.createdLocalLunar = semanticTime.createdLocalLunar;
    }

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

  function normalizeStoredMessage(item, options = {}) {
    const payloadJSON = typeof item.payload_json === "string" ? item.payload_json : "";
    const payload = safeParseObject(payloadJSON);
    const category = normalizeText(item.category);
    const messageType = normalizeText(item.message_type);
    const source = normalizeObserverSource(payload, {
      category,
      messageType,
      sourceKind: item.source_kind,
      sourceId: item.source_id,
      sourceLabel: item.source_label,
    });
    const sourceKey = `${source.kind}:${source.id}`;
    const payloadFingerprint = observerPayloadFingerprint(category, messageType, payload);
    const role = item.role === "assistant" ? "assistant" : item.role || "system";
    const turnId = Number.isFinite(item.turn_id) ? item.turn_id : 0;
    const sessionEpoch = Number.isFinite(options.sessionEpoch) ? options.sessionEpoch : 0;
    const say = typeof item.say === "string" && item.say ? item.say : typeof item.content === "string" ? item.content : "";
    const matchKey = role === "assistant"
      ? buildAssistantMatchKey(turnId, sessionEpoch)
      : (role === "observer" ? buildObserverMatchKey(turnId, category, messageType, sourceKey, payloadFingerprint) : "");
    return {
      role,
      say,
      aside: typeof item.aside === "string" ? item.aside : "",
      actionJSON: typeof item.action_json === "string" ? item.action_json : "",
      rawData: typeof item.raw_data === "string" ? item.raw_data : "",
      parseError: typeof item.parse_error === "string" ? item.parse_error : "",
      msgId: typeof item.message_id === "string" ? item.message_id : "",
      storeId: Number.isFinite(item.store_id) ? item.store_id : 0,
      turnId,
      sessionEpoch,
      createdLocalYMDHMS: item.created_at_local_ymdhms || "",
      createdLocalWeekday: item.created_at_local_weekday || "",
      createdLocalLunar: item.created_at_local_lunar || "",
      category,
      messageType,
      payloadJSON,
      sourceKind: source.kind,
      sourceId: source.id,
      sourceLabel: source.label,
      sourceKey,
      payloadFingerprint,
      matchKey,
      isTemporary: false,
    };
  }

  function applyStoredToRecord(record, stored) {
    if (!record || !stored) return;
    if (stored.msgId) record.msgId = stored.msgId;
    if (stored.storeId > 0) record.storeId = stored.storeId;
    if (stored.turnId > 0) record.turnId = stored.turnId;
    record.say = stored.say || record.say || "";
    record.text = record.say;
    record.aside = stored.aside || "";
    record.actionJSON = stored.actionJSON || "";
    record.rawData = stored.rawData || "";
    record.parseError = stored.parseError || "";
    record.category = stored.category || record.category || "";
    record.messageType = stored.messageType || record.messageType || "";
    record.payloadJSON = stored.payloadJSON || "";
    record.sourceKind = stored.sourceKind || record.sourceKind || "";
    record.sourceId = stored.sourceId || record.sourceId || "";
    record.sourceLabel = stored.sourceLabel || record.sourceLabel || "";
    record.sourceKey = stored.sourceKey || record.sourceKey || "";
    record.payloadFingerprint = stored.payloadFingerprint || record.payloadFingerprint || "";
    if (stored.matchKey) record.matchKey = stored.matchKey;
    if (stored.createdLocalYMDHMS) record.createdLocalYMDHMS = stored.createdLocalYMDHMS;
    if (stored.createdLocalWeekday) record.createdLocalWeekday = stored.createdLocalWeekday;
    if (stored.createdLocalLunar) record.createdLocalLunar = stored.createdLocalLunar;
    record.isPartial = false;
    record.isTemporary = false;
    renderMessage(record);
  }

  function findRecordByMessageID(messageId) {
    if (!messageId) return null;
    for (let i = app.messages.length - 1; i >= 0; i--) {
      const msg = app.messages[i];
      if (msg.msgId === messageId) return msg;
    }
    return null;
  }

  function findReconcileTarget(stored, options = {}) {
    if (!stored || !stored.role) return null;
    const expectedEpoch = Number.isFinite(options.sessionEpoch) ? options.sessionEpoch : stored.sessionEpoch;
    if (stored.role === "assistant") {
      for (let i = app.messages.length - 1; i >= 0; i--) {
        const msg = app.messages[i];
        if (!(msg.role === "assistant" || msg.role === "ai")) continue;
        if (stored.turnId > 0 && msg.turnId !== stored.turnId) continue;
        if (expectedEpoch > 0 && msg.sessionEpoch !== expectedEpoch) continue;
        if (stored.matchKey && msg.matchKey && stored.matchKey !== msg.matchKey) continue;
        if (msg.msgId) continue;
        return msg;
      }
      return null;
    }
    if (stored.role === "observer") {
      for (let i = app.messages.length - 1; i >= 0; i--) {
        const msg = app.messages[i];
        if (msg.role !== "observer") continue;
        if (msg.msgId) continue;
        if (stored.turnId > 0 && msg.turnId !== stored.turnId) continue;
        if (stored.category && msg.category && msg.category !== stored.category) continue;
        if (stored.messageType && msg.messageType && msg.messageType !== stored.messageType) continue;
        if (stored.matchKey && msg.matchKey && stored.matchKey !== msg.matchKey) continue;
        if (stored.sourceKey && msg.sourceKey && msg.sourceKey !== stored.sourceKey) continue;
        if (stored.payloadFingerprint && msg.payloadFingerprint && msg.payloadFingerprint !== stored.payloadFingerprint) continue;
        return msg;
      }
    }
    return null;
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
      createdAtMS: Number.isFinite(extra.createdAtMS) ? extra.createdAtMS : 0,
      createdLocalYMDHMS: typeof extra.createdLocalYMDHMS === "string" ? extra.createdLocalYMDHMS : "",
      createdLocalWeekday: typeof extra.createdLocalWeekday === "string" ? extra.createdLocalWeekday : "",
      createdLocalLunar: typeof extra.createdLocalLunar === "string" ? extra.createdLocalLunar : "",
      category: typeof extra.category === "string" ? extra.category : "",
      messageType: typeof extra.messageType === "string" ? extra.messageType : "",
      payloadJSON: typeof extra.payloadJSON === "string" ? extra.payloadJSON : "",
      sourceKind: typeof extra.sourceKind === "string" ? extra.sourceKind : "",
      sourceId: typeof extra.sourceId === "string" ? extra.sourceId : "",
      sourceLabel: typeof extra.sourceLabel === "string" ? extra.sourceLabel : "",
      payloadFingerprint: typeof extra.payloadFingerprint === "string" ? extra.payloadFingerprint : "",
      matchKey: typeof extra.matchKey === "string" ? extra.matchKey : "",
      isTemporary: extra.isTemporary !== false,
    });
    chatArea.appendChild(msg.element);
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
    app.messages.push(msg);
    return msg;
  }

  function appendStoredMessage(message, options = {}) {
    const item = message && typeof message === "object" ? message : null;
    if (!item) return null;
    const stored = normalizeStoredMessage(item, { sessionEpoch: options.sessionEpoch });
    if (stored.msgId) {
      const existing = findRecordByMessageID(stored.msgId);
      if (existing) {
        applyStoredToRecord(existing, stored);
        return existing;
      }
    }
    const target = findReconcileTarget(stored, { sessionEpoch: options.sessionEpoch });
    if (target) {
      applyStoredToRecord(target, stored);
      return target;
    }
    const record = createMessageRecord(stored);
    chatArea.appendChild(record.element);
    if (!maybeShowJump()) {
      chatArea.scrollTop = chatArea.scrollHeight;
    }
    app.messages.push(record);
    return record;
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
      app.currentAIMsg = addChatMsg("assistant", "", turnId, false, app.sessionEpoch, {
        category: "chat",
        messageType: "assistant_message",
        isTemporary: true,
        matchKey: buildAssistantMatchKey(turnId, app.sessionEpoch),
      });
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
      msg = addChatMsg("assistant", clean, turnId, false, app.sessionEpoch, {
        category: "chat",
        messageType: "assistant_message",
        isTemporary: true,
        matchKey: buildAssistantMatchKey(turnId, app.sessionEpoch),
      });
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
    const existingMsgIds = new Set(app.messages.map((item) => item.msgId).filter((id) => !!id));

    for (const m of historyMessages) {
      const stored = normalizeStoredMessage(m, { sessionEpoch: 0 });
      if (stored.msgId && existingMsgIds.has(stored.msgId)) {
        continue;
      }
      const target = findReconcileTarget(stored, { sessionEpoch: 0 });
      if (target) {
        applyStoredToRecord(target, stored);
        if (stored.msgId) existingMsgIds.add(stored.msgId);
        continue;
      }
      const record = createMessageRecord(stored);
      fragment.appendChild(record.element);
      batch.push(record);
      if (stored.msgId) existingMsgIds.add(stored.msgId);
    }

    if (batch.length > 0) {
      chatArea.prepend(fragment);
      app.messages = batch.concat(app.messages);
    }

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
    appendStoredMessage,
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
