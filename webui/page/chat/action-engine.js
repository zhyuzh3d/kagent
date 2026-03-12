function parseJSONBlock(rawText) {
  if (typeof rawText !== "string") return null;
  const text = rawText.trim();
  if (!text) return null;

  try {
    const parsed = JSON.parse(text);
    if (parsed && typeof parsed === "object") return parsed;
  } catch (_) {
  }

  const block = text.match(/```json\s*([\s\S]*?)```/i);
  if (!block || !block[1]) return null;
  try {
    const parsed = JSON.parse(block[1].trim());
    if (parsed && typeof parsed === "object") return parsed;
  } catch (_) {
  }
  return null;
}

function normalizeEnvelopeRaw(rawText) {
  if (typeof rawText !== "string") return "";
  const text = rawText.trimStart();
  if (!text) return "";
  const lower = text.toLowerCase();
  const fenceIdx = lower.indexOf("```json");
  if (fenceIdx >= 0) {
    return text.slice(fenceIdx + 7).trimStart();
  }
  return text;
}

function extractContentPreview(rawText) {
  const source = normalizeEnvelopeRaw(rawText);
  if (!source) return { found: false, complete: false, value: "" };

  let s = source;
  const objIdx = s.indexOf("{");
  if (objIdx > 0 && s.includes('"content"')) {
    s = s.slice(objIdx);
  }

  const key = '"content"';
  const keyIdx = s.indexOf(key);
  if (keyIdx < 0) return { found: false, complete: false, value: "" };
  let i = keyIdx + key.length;
  while (i < s.length && /\s/.test(s[i])) i += 1;
  if (i >= s.length || s[i] !== ":") return { found: true, complete: false, value: "" };
  i += 1;
  while (i < s.length && /\s/.test(s[i])) i += 1;
  if (i >= s.length) return { found: true, complete: false, value: "" };
  if (s[i] !== '"') return { found: true, complete: true, value: "" };
  i += 1;

  let out = "";
  let escaped = false;
  for (; i < s.length; i++) {
    const ch = s[i];
    if (escaped) {
      escaped = false;
      if (ch === "n") out += "\n";
      else if (ch === "r") out += "\r";
      else if (ch === "t") out += "\t";
      else if (ch === "b") out += "\b";
      else if (ch === "f") out += "\f";
      else if (ch === "u") {
        const unicode = s.slice(i + 1, i + 5);
        if (/^[0-9a-fA-F]{4}$/.test(unicode)) {
          out += String.fromCharCode(parseInt(unicode, 16));
          i += 4;
        } else {
          return { found: true, complete: false, value: out };
        }
      } else {
        out += ch;
      }
      continue;
    }
    if (ch === "\\") {
      escaped = true;
      continue;
    }
    if (ch === '"') {
      return { found: true, complete: true, value: out };
    }
    out += ch;
  }
  return { found: true, complete: false, value: out };
}

function looksLikeJSONEnvelope(rawText) {
  if (typeof rawText !== "string") return false;
  const text = rawText.trimStart();
  if (!text) return false;
  if (text.startsWith("{") || text.startsWith("```")) return true;
  return text.includes('"content"') || text.includes('"action"');
}

function normalizeFollowup(rawFollowup) {
  return String(rawFollowup || "").trim().toLowerCase() === "report" ? "report" : "none";
}

function normalizeAction(rawAction, payload) {
  if (!rawAction || typeof rawAction !== "object") return null;
  const nameRaw = typeof rawAction.name === "string" ? rawAction.name.trim() : "";
  if (!nameRaw) return null;
  const args = rawAction.args && typeof rawAction.args === "object" ? rawAction.args : {};

  const aliases = new Map([
    ["surface.get_state", "surface.get_state"],
    ["surface.call.counter.get_state", "surface.get_state"],
    ["counter.get_state", "surface.get_state"],
    ["get_state", "surface.get_state"],
    ["get_surfaces", "get_surfaces"],
    ["surface.get_surfaces", "get_surfaces"],
    ["surface.list", "get_surfaces"],
    ["open_surface", "open_surface"],
    ["surface.open_surface", "open_surface"],
    ["surface.open", "open_surface"],
    ["close_surface", "close_surface"],
    ["surface.close_surface", "close_surface"],
    ["surface.close", "close_surface"],
    ["surface.call.counter.set_count", "surface.call.counter.set_count"],
    ["counter.set_count", "surface.call.counter.set_count"],
    ["set_count", "surface.call.counter.set_count"],
    ["surface.call.counter.increment", "surface.call.counter.increment"],
    ["counter.increment", "surface.call.counter.increment"],
    ["increment", "surface.call.counter.increment"],
    ["surface.call.counter.reset", "surface.call.counter.reset"],
    ["counter.reset", "surface.call.counter.reset"],
    ["reset", "surface.call.counter.reset"],
  ]);
  const canonical = aliases.get(nameRaw);
  if (!canonical) return null;

  const followup = normalizeFollowup(rawAction.followup || payload.followup);
  const normalized = {
    id: typeof rawAction.id === "string" && rawAction.id.trim() ? rawAction.id.trim() : `act-${Date.now()}-${Math.floor(Math.random() * 100000)}`,
    name: canonical,
    followup,
    args: {},
  };

  if (canonical === "get_surfaces") {
    normalized.followup = "report";
    normalized.args = {};
    return normalized;
  }
  if (canonical === "open_surface" || canonical === "close_surface") {
    normalized.followup = "report";
    const target = typeof args.target === "string" && args.target.trim()
      ? args.target.trim()
      : (typeof args.surface_id === "string" && args.surface_id.trim() ? args.surface_id.trim() : "counter");
    normalized.args = { target };
    return normalized;
  }
  if (canonical === "surface.call.counter.set_count") {
    if (!Number.isFinite(args.count)) return null;
    normalized.args = { count: Math.floor(args.count) };
    return normalized;
  }
  if (canonical === "surface.call.counter.increment") {
    const step = Number.isFinite(args.step) ? args.step : 1;
    normalized.args = { step };
    return normalized;
  }
  if (canonical === "surface.call.counter.reset") {
    normalized.args = {};
    return normalized;
  }
  normalized.args = {
    surface_id: typeof args.surface_id === "string" && args.surface_id.trim()
      ? args.surface_id.trim()
      : (typeof args.target === "string" && args.target.trim() ? args.target.trim() : "counter"),
  };
  return normalized;
}

function inferActionSurfaceID(actionName, args, fallback = "counter") {
  const action = typeof actionName === "string" ? actionName : "";
  const payload = args && typeof args === "object" ? args : {};
  if (typeof payload.surface_id === "string" && payload.surface_id.trim()) {
    return payload.surface_id.trim();
  }
  if (typeof payload.target === "string" && payload.target.trim()) {
    return payload.target.trim();
  }
  if (action === "get_surfaces") {
    return "surface_registry";
  }
  return fallback;
}

function stableObjectString(value) {
  if (!value || typeof value !== "object") return "{}";
  const keys = Object.keys(value).sort();
  const out = {};
  for (const key of keys) out[key] = value[key];
  return JSON.stringify(out);
}

function pickActionObject(payload) {
  if (!payload || typeof payload !== "object") return null;
  const candidates = [payload.action, payload.action_call, payload.actionCall, payload.call];
  for (const item of candidates) {
    if (item && typeof item === "object") return item;
  }
  return null;
}

export function createChatActionEngine(options) {
  const chatStore = options.chatStore;
  const surfaceBridge = options.surfaceBridge;
  const appendDebug = typeof options.appendDebug === "function" ? options.appendDebug : () => { };
  const appendSystem = typeof options.appendSystem === "function" ? options.appendSystem : () => { };
  const reportActionRecord = typeof options.reportActionRecord === "function" ? options.reportActionRecord : () => { };
  const reportStateChange = typeof options.reportStateChange === "function" ? options.reportStateChange : () => { };

  const streamStates = new Map();
  const actionRateWindow = [];
  const actionDedup = new Map();
  let inflightActions = 0;
  const rateWindowMs = 60 * 1000;
  const rateLimit = 10;
  const dedupeMs = 3000;

  function ensureState(turnId) {
    let state = streamStates.get(turnId);
    if (!state) {
      state = {
        raw: "",
        mode: "unknown",
        content: "",
      };
      streamStates.set(turnId, state);
    }
    return state;
  }

  function handleAssistantDelta(turnId, deltaText) {
    const piece = typeof deltaText === "string" ? deltaText : "";
    if (!piece) return null;
    const state = ensureState(turnId);
    state.raw += piece;

    const preview = extractContentPreview(state.raw);
    if (preview.found) {
      state.mode = "json";
      state.content = preview.value;
      return { handled: true, content: state.content };
    }

    if (state.mode === "json") {
      return { handled: true, content: state.content };
    }

    if (state.mode === "unknown") {
      if (looksLikeJSONEnvelope(state.raw)) {
        state.mode = "json_candidate";
        return { handled: true, content: state.content };
      }
      state.mode = "plain";
      return null;
    }

    if (state.mode === "json_candidate") {
      return { handled: true, content: state.content };
    }

    return null;
  }

  function resolveFinalPayload(turnId, finalText) {
    const state = streamStates.get(turnId);
    const candidates = [];
    if (typeof finalText === "string" && finalText.trim()) {
      candidates.push(finalText.trim());
    }
    if (state && typeof state.raw === "string" && state.raw.trim()) {
      candidates.push(state.raw.trim());
    }
    for (const candidate of candidates) {
      const payload = parseJSONBlock(candidate);
      if (payload && typeof payload === "object" && ("content" in payload || pickActionObject(payload))) {
        return payload;
      }
    }
    return null;
  }

  function evaluateActionGuard(action) {
    const now = Date.now();
    while (actionRateWindow.length > 0 && now - actionRateWindow[0] > rateWindowMs) {
      actionRateWindow.shift();
    }
    if (actionRateWindow.length >= rateLimit) {
      return "rate_limit";
    }
    const key = `${action.name}|${stableObjectString(action.args || {})}`;
    const last = actionDedup.get(key) || 0;
    if (now - last <= dedupeMs) {
      return "quota_limit";
    }
    actionRateWindow.push(now);
    actionDedup.set(key, now);
    return "";
  }

  async function requestManualConfirm(blockReason, action) {
    const reasonText = blockReason === "rate_limit" ? "动作调用频率过高" : "检测到短时间重复动作";
    const actionText = `${action.name} ${JSON.stringify(action.args || {})}`;
    const ok = window.confirm(`${reasonText}。是否仍继续执行？\n\n${actionText}`);
    return ok ? "confirm" : "cancel";
  }

  async function executeAction(turnId, action, contentText) {
    const blockReason = evaluateActionGuard(action);
    let manualConfirm = "";
    const targetSurfaceID = inferActionSurfaceID(action.name, action.args);
    inflightActions += 1;
    const concurrentHint = inflightActions;
    if (blockReason) {
      manualConfirm = await requestManualConfirm(blockReason, action);
      if (manualConfirm === "cancel") {
        appendSystem("已取消本次受限动作。", 0);
        reportActionRecord({
          turnId,
          actionId: action.id,
          category: "dispatch",
          actionName: action.name,
          actionSurfaceID: targetSurfaceID,
          actionSurfaceType: "app",
          actionSurfaceVersion: "1",
          status: "cancelled",
          followup: action.followup,
          content: contentText,
          args: action.args || {},
          result: { reason: "user_cancelled", concurrent_actions: concurrentHint },
          effect: {},
          state: {},
          manualConfirm,
          blockReason,
        });
        inflightActions = Math.max(0, inflightActions - 1);
        return;
      }
    }

    try {
      const result = await surfaceBridge.dispatchAction(action);
      if (!result || !result.ok) {
        const reason = result && result.reason ? result.reason : "dispatch_failed";
        appendDebug("WARN", "ActionEngine", turnId, null, `action dispatch failed: ${reason}`);
        appendSystem(`action 执行失败: ${reason}`);
        reportActionRecord({
          turnId,
          actionId: action.id,
          category: "dispatch",
          actionName: action.name,
          actionSurfaceID: result && result.surface_id ? result.surface_id : targetSurfaceID,
          actionSurfaceType: result && typeof result.surface_type === "string" ? result.surface_type : "app",
          actionSurfaceVersion: result && typeof result.surface_version === "string" ? result.surface_version : "1",
          status: "fail",
          followup: action.followup,
          content: contentText,
          args: action.args || {},
          result: { reason, concurrent_actions: concurrentHint },
          effect: result && result.effect && typeof result.effect === "object" ? result.effect : {},
          state: result && result.business_state && typeof result.business_state === "object" ? result.business_state : {},
          manualConfirm,
          blockReason,
        });
        return;
      }

      appendDebug("INFO", "ActionEngine", turnId, JSON.stringify(action.args || {}), `action executed: ${action.name}`);
      const finalResult = result.result && typeof result.result === "object" ? { ...result.result } : {};
      finalResult.concurrent_actions = concurrentHint;
      reportActionRecord({
        turnId,
        actionId: action.id,
        category: "dispatch",
        actionName: action.name,
        actionSurfaceID: result.surface_id || targetSurfaceID,
        actionSurfaceType: typeof result.surface_type === "string" ? result.surface_type : "app",
        actionSurfaceVersion: typeof result.surface_version === "string" ? result.surface_version : "1",
        status: result.status || "ok",
        followup: action.followup,
        content: contentText,
        args: action.args || {},
        result: finalResult,
        effect: result.effect && typeof result.effect === "object" ? result.effect : {},
        state: result.business_state && typeof result.business_state === "object" ? result.business_state : {},
        manualConfirm,
        blockReason,
      });
    } finally {
      inflightActions = Math.max(0, inflightActions - 1);
    }
  }

  async function handleAssistantFinal(turnId, finalText) {
    const payload = resolveFinalPayload(turnId, finalText);
    if (!payload) {
      streamStates.delete(turnId);
      return;
    }
    const state = streamStates.get(turnId);
    let content = typeof payload.content === "string" ? payload.content.trim() : "";
    if (!content && state && typeof state.content === "string") {
      content = state.content.trim();
    }
    const rawAction = pickActionObject(payload);
    if (!content && rawAction) {
      const rawName = typeof rawAction.name === "string" ? rawAction.name : "unknown_action";
      content = `已执行动作：${rawName}`;
    }
    if (content) {
      chatStore.setAIMsgText(turnId, content);
    }
    if (!rawAction) {
      appendDebug("INFO", "ActionEngine", turnId, content, "parsed envelope without action");
      streamStates.delete(turnId);
      return;
    }

    const action = normalizeAction(rawAction, payload);
    if (!action) {
      appendDebug("WARN", "ActionEngine", turnId, null, "invalid or unsupported action");
      appendSystem("检测到不合法 action，已忽略。");
      reportActionRecord({
        turnId,
        actionId: typeof rawAction.id === "string" ? rawAction.id : "",
        category: "dispatch",
        actionName: typeof rawAction.name === "string" ? rawAction.name : "invalid_action",
        actionSurfaceID: inferActionSurfaceID(typeof rawAction.name === "string" ? rawAction.name : "", rawAction && typeof rawAction.args === "object" ? rawAction.args : {}),
        actionSurfaceType: "app",
        actionSurfaceVersion: "1",
        status: "fail",
        followup: normalizeFollowup(rawAction.followup || payload.followup),
        content,
        args: rawAction && typeof rawAction.args === "object" ? rawAction.args : {},
        result: { reason: "invalid_or_unsupported_action" },
        effect: {},
        state: {},
      });
      streamStates.delete(turnId);
      return;
    }

    try {
      await executeAction(turnId, action, content);
    } finally {
      streamStates.delete(turnId);
    }
  }

  function handleSurfaceEffect(turnId, evt) {
    if (!evt || typeof evt !== "object") return;
    if ((evt.type === "state_change" || evt.type === "surface_open") && evt.payload && typeof evt.payload === "object") {
      reportStateChange({
        turnId,
        surface_id: evt.surface_id || "counter",
        surface_type: typeof evt.payload.surface_type === "string" ? evt.payload.surface_type : "app",
        surface_version: typeof evt.payload.surface_version === "string" ? evt.payload.surface_version : "1",
        event_type: evt.payload.event_type || (evt.type === "surface_open" ? "surface_open" : "state_change"),
        business_state: evt.payload.business_state && typeof evt.payload.business_state === "object" ? evt.payload.business_state : {},
        visible_text: typeof evt.payload.visible_text === "string" ? evt.payload.visible_text : "",
        status: typeof evt.payload.status === "string" ? evt.payload.status : "ready",
        state_version: Number.isFinite(evt.payload.state_version) ? evt.payload.state_version : 0,
        updated_at_ms: Number.isFinite(evt.payload.updated_at_ms) ? evt.payload.updated_at_ms : Date.now(),
      });
    }
  }

  return {
    handleAssistantDelta,
    handleAssistantFinal,
    handleSurfaceEffect,
  };
}
