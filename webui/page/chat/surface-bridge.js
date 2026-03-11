function createPanel(root) {
  const panel = document.createElement("div");
  panel.className = "surface-float-panel";
  panel.innerHTML = `
    <div class="surface-float-head">
      <div class="surface-float-title">Counter Surface</div>
      <div class="surface-float-actions">
        <button type="button" data-act="freeze">冻结</button>
        <button type="button" data-act="reload">重载</button>
        <button type="button" data-act="close">关闭</button>
      </div>
    </div>
    <div class="surface-float-body"></div>
    <div class="surface-float-status">idle</div>
  `;
  root.appendChild(panel);
  return panel;
}

function normalizeActionName(rawName) {
  const name = typeof rawName === "string" ? rawName.trim() : "";
  const aliases = new Map([
    ["surface.get_state", "get_state"],
    ["surface.call.counter.get_state", "get_state"],
    ["counter.get_state", "get_state"],
    ["get_state", "get_state"],
    ["surface.call.counter.set_count", "set_count"],
    ["counter.set_count", "set_count"],
    ["set_count", "set_count"],
    ["surface.call.counter.increment", "increment"],
    ["counter.increment", "increment"],
    ["increment", "increment"],
    ["surface.call.counter.reset", "reset"],
    ["counter.reset", "reset"],
    ["reset", "reset"],
  ]);
  return aliases.get(name) || "";
}

function toSurfaceAction(action) {
  if (!action || typeof action !== "object") return null;
  const name = normalizeActionName(action.name);
  if (!name) return null;
  const args = action.args && typeof action.args === "object" ? action.args : {};
  return {
    id: typeof action.id === "string" && action.id.trim() ? action.id.trim() : `chat-act-${Date.now()}-${Math.floor(Math.random() * 100000)}`,
    name,
    args,
  };
}

export function createSurfaceBridge(options) {
  const root = options.root;
  const appendDebug = typeof options.appendDebug === "function" ? options.appendDebug : () => {};
  const onSurfaceEvent = typeof options.onSurfaceEvent === "function" ? options.onSurfaceEvent : () => {};
  const surfaceURL = options.surfaceURL || "/surface/demo-counter.html";

  let panel = null;
  let bodyEl = null;
  let statusEl = null;
  let freezeBtn = null;
  let iframe = null;
  let port = null;
  let frozen = false;
  let ready = false;
  let visible = false;
  let sessionToken = "";

  const stateCache = new Map();
  const capabilityCache = new Map();
  const pendingActions = [];
  const actionWaiters = new Map();

  function setStatus(text) {
    if (statusEl) statusEl.textContent = text;
  }

  function disposePort() {
    if (!port) return;
    try {
      port.close();
    } catch (_) {
    }
    port = null;
  }

  function cacheStateFromMessage(surfaceId, payload) {
    if (!surfaceId || !payload || typeof payload !== "object") return null;
    const current = stateCache.get(surfaceId) || {};
    const next = {
      surface_id: surfaceId,
      event_type: typeof payload.event_type === "string" ? payload.event_type : (current.event_type || "state_change"),
      business_state: payload.business_state && typeof payload.business_state === "object" ? payload.business_state : (current.business_state || {}),
      visible_text: typeof payload.visible_text === "string" ? payload.visible_text : (current.visible_text || ""),
      status: typeof payload.status === "string" ? payload.status : (current.status || "ready"),
      state_version: Number.isFinite(payload.state_version) ? payload.state_version : (Number.isFinite(current.state_version) ? current.state_version : 0),
      updated_at_ms: Number.isFinite(payload.updated_at_ms) ? payload.updated_at_ms : Date.now(),
    };
    stateCache.set(surfaceId, next);
    return next;
  }

  function emitStateChange(state) {
    if (!state) return;
    onSurfaceEvent({
      type: "state_change",
      surface_id: state.surface_id,
      payload: state,
    });
  }

  function resolveAction(actionId, result) {
    const waiter = actionWaiters.get(actionId);
    if (!waiter) return;
    actionWaiters.delete(actionId);
    clearTimeout(waiter.timer);
    waiter.resolve(result);
  }

  function rejectAction(actionId, reason) {
    const waiter = actionWaiters.get(actionId);
    if (!waiter) return;
    actionWaiters.delete(actionId);
    clearTimeout(waiter.timer);
    waiter.resolve({ ok: false, reason: reason || "action_failed" });
  }

  function postActionCall(surfaceAction, resolve) {
    if (!port || !ready) {
      pendingActions.push({ surfaceAction, resolve });
      setStatus(`queued(${pendingActions.length})`);
      appendDebug("INFO", "SurfaceCounter", null, JSON.stringify(surfaceAction.args || {}), `queue ${surfaceAction.name}`);
      return;
    }
    const timeout = setTimeout(() => {
      rejectAction(surfaceAction.id, "action timeout");
    }, 4500);
    actionWaiters.set(surfaceAction.id, { resolve, timer: timeout });
    try {
      port.postMessage({
        type: "action_call",
        action: surfaceAction,
      });
      appendDebug("INFO", "SurfaceCounter", null, JSON.stringify(surfaceAction.args || {}), `dispatch ${surfaceAction.name}`);
    } catch (err) {
      clearTimeout(timeout);
      actionWaiters.delete(surfaceAction.id);
      resolve({ ok: false, reason: err.message || String(err) });
    }
  }

  function flushPendingActions() {
    while (pendingActions.length > 0) {
      const item = pendingActions.shift();
      if (!item) continue;
      postActionCall(item.surfaceAction, item.resolve);
    }
  }

  function connectChannel() {
    disposePort();
    if (!iframe || !iframe.contentWindow) {
      setStatus("iframe not ready");
      return;
    }
    const channel = new MessageChannel();
    port = channel.port1;
    sessionToken = `counter-${Date.now()}`;
    ready = false;

    port.onmessage = (ev) => {
      const msg = ev && ev.data ? ev.data : null;
      if (!msg || typeof msg !== "object") return;
      if (frozen && msg.type !== "surface_heartbeat") return;

      if (msg.type === "surface_ready") {
        ready = true;
        setStatus("ready");
        const surfaceId = typeof msg.surface_id === "string" ? msg.surface_id : "counter";
        const caps = msg.capabilities && typeof msg.capabilities === "object" ? msg.capabilities : {};
        capabilityCache.set(surfaceId, {
          get_state: !!caps.get_state,
        });
        if (msg.state && typeof msg.state === "object") {
          const state = cacheStateFromMessage(surfaceId, msg.state);
          emitStateChange(state);
        }
        flushPendingActions();
        return;
      }

      if (msg.type === "state_change") {
        const surfaceId = typeof msg.surface_id === "string" ? msg.surface_id : "counter";
        const state = cacheStateFromMessage(surfaceId, msg);
        appendDebug("INFO", "SurfaceCounter", null, JSON.stringify(state.business_state || {}), "state_change");
        emitStateChange(state);
        return;
      }

      if (msg.type === "action_result") {
        const actionId = typeof msg.action_id === "string" ? msg.action_id : "";
        const surfaceId = typeof msg.surface_id === "string" ? msg.surface_id : "counter";
        const state = cacheStateFromMessage(surfaceId, {
          business_state: msg.business_state,
          visible_text: msg.visible_text,
          status: typeof msg.status === "string" ? msg.status : "ready",
          state_version: msg.state_version,
          updated_at_ms: msg.updated_at_ms,
          event_type: "action_result",
        });
        const result = {
          ok: (msg.status || "ok") === "ok",
          status: typeof msg.status === "string" ? msg.status : "ok",
          reason: typeof msg.error === "string" ? msg.error : "",
          action_id: actionId,
          action_name: typeof msg.action_name === "string" ? msg.action_name : "",
          surface_id: surfaceId,
          result: msg.result && typeof msg.result === "object" ? msg.result : {},
          business_state: state ? state.business_state : {},
          state_version: state ? state.state_version : 0,
          effect: {
            source: "surface.action_result",
            business_state: state ? state.business_state : {},
            visible_text: state ? state.visible_text : "",
          },
        };
        resolveAction(actionId, result);
        onSurfaceEvent({
          type: "action_result",
          surface_id: surfaceId,
          payload: result,
        });
        return;
      }

      if (msg.type === "surface_log") {
        appendDebug("WARN", "SurfaceCounter", null, null, msg.message || "surface_log");
      }
    };

    port.start();
    try {
      iframe.contentWindow.postMessage(
        {
          type: "surface_connect",
          surface_id: "counter",
          session_token: sessionToken,
        },
        "*",
        [channel.port2],
      );
      setStatus("connecting...");
    } catch (err) {
      setStatus(`connect failed: ${err.message}`);
    }
  }

  function ensureIframe(forceReload = false) {
    if (!panel || !bodyEl) return;
    if (iframe && !forceReload) return;
    ready = false;
    const nextIframe = document.createElement("iframe");
    nextIframe.className = "surface-float-iframe";
    nextIframe.setAttribute("sandbox", "allow-scripts allow-downloads");
    nextIframe.src = forceReload ? `${surfaceURL}?_reload=${Date.now()}` : surfaceURL;
    nextIframe.addEventListener("load", () => {
      connectChannel();
    });
    bodyEl.replaceChildren(nextIframe);
    iframe = nextIframe;
    setStatus("loading...");
  }

  function ensurePanel() {
    if (panel) return;
    panel = createPanel(root);
    bodyEl = panel.querySelector(".surface-float-body");
    statusEl = panel.querySelector(".surface-float-status");
    freezeBtn = panel.querySelector('[data-act="freeze"]');

    panel.querySelector('[data-act="reload"]').addEventListener("click", () => {
      ensureIframe(true);
    });
    panel.querySelector('[data-act="close"]').addEventListener("click", () => {
      setVisible(false);
    });
    freezeBtn.addEventListener("click", () => {
      frozen = !frozen;
      freezeBtn.textContent = frozen ? "解冻" : "冻结";
      setStatus(frozen ? "frozen" : (ready ? "ready" : "connecting..."));
    });
  }

  function setVisible(nextVisible) {
    ensurePanel();
    visible = !!nextVisible;
    panel.classList.toggle("open", visible);
    if (visible) {
      ensureIframe(false);
    }
  }

  function toggleVisible() {
    setVisible(!visible);
  }

  function dispatchAction(action) {
    if (!visible) {
      setVisible(true);
    }
    if (frozen) {
      return Promise.resolve({ ok: false, reason: "surface frozen" });
    }
    const surfaceAction = toSurfaceAction(action);
    if (!surfaceAction) {
      return Promise.resolve({ ok: false, reason: "invalid action" });
    }

    if (surfaceAction.name === "get_state") {
      const cached = stateCache.get("counter");
      if (cached) {
        return Promise.resolve({
          ok: true,
          status: "ok",
          action_id: surfaceAction.id,
          action_name: "surface.get_state",
          surface_id: "counter",
          result: { from_cache: true },
          business_state: cached.business_state || {},
          state_version: cached.state_version || 0,
          effect: {
            source: "surface.cache",
            business_state: cached.business_state || {},
            visible_text: cached.visible_text || "",
          },
        });
      }
    }

    return new Promise((resolve) => {
      postActionCall(surfaceAction, resolve);
    });
  }

  function getCachedState(surfaceId = "counter") {
    return stateCache.get(surfaceId) || null;
  }

  function hasCapability(surfaceId = "counter", capability = "get_state") {
    const caps = capabilityCache.get(surfaceId);
    if (!caps) return false;
    return !!caps[capability];
  }

  return {
    setVisible,
    toggleVisible,
    dispatchAction,
    getCachedState,
    hasCapability,
  };
}
