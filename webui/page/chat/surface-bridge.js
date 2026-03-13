function createPanel(root) {
  const panel = document.createElement("div");
  panel.className = "surface-float-panel";
  panel.innerHTML = `
    <div class="surface-float-head">
      <div class="surface-float-title">Surface Manager</div>
      <div class="surface-float-actions">
        <button type="button" data-act="refresh">刷新</button>
        <button type="button" data-act="close">关闭</button>
      </div>
    </div>
    <div class="surface-float-status">idle</div>
    <div class="surface-float-body">
      <div class="surface-manager-list"></div>
      <div class="surface-manager-workspace"></div>
    </div>
  `;
  root.appendChild(panel);
  return panel;
}

function toCanonicalActionName(rawName) {
  const name = typeof rawName === "string" ? rawName.trim() : "";
  if (!name) return "";
  const aliases = new Map([
    ["get_surfaces", "get_surfaces"],
    ["surface.get_surfaces", "get_surfaces"],
    ["surface.list", "get_surfaces"],
    ["open_surface", "open_surface"],
    ["surface.open_surface", "open_surface"],
    ["surface.open", "open_surface"],
    ["close_surface", "close_surface"],
    ["surface.close_surface", "close_surface"],
    ["surface.close", "close_surface"],
    ["surface.get_state", "surface.get_state"],
    ["get_state", "surface.get_state"],
  ]);
  const lower = name.toLowerCase();
  if (aliases.has(lower)) return aliases.get(lower);
  if (lower.startsWith("surface.call.")) return name;
  return "";
}

function toActionPayload(action) {
  if (!action || typeof action !== "object") return null;
  const canonicalName = toCanonicalActionName(action.name);
  if (!canonicalName) return null;
  return {
    id: typeof action.id === "string" && action.id.trim()
      ? action.id.trim()
      : `surface-act-${Date.now()}-${Math.floor(Math.random() * 100000)}`,
    name: canonicalName,
    args: action.args && typeof action.args === "object" ? action.args : {},
  };
}

function escapeHTML(text) {
  return String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function parseSurfaceCallName(name) {
  const parts = String(name || "").split(".");
  if (parts.length < 4) return null;
  if (parts[0] !== "surface" || parts[1] !== "call") return null;
  return {
    surfaceID: parts[2],
    actionName: parts.slice(3).join("."),
  };
}

async function fetchJSON(url, options = {}) {
  const resp = await fetch(url, options);
  const text = await resp.text();
  let data = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch (_) {
    data = null;
  }
  if (!resp.ok) {
    const message = data && data.error ? data.error : (text || `http ${resp.status}`);
    throw new Error(message);
  }
  return data;
}

function encodePathSegments(path) {
  const raw = String(path || "").split("/").filter(Boolean);
  return raw.map((seg) => encodeURIComponent(seg)).join("/");
}

export function createSurfaceBridge(options) {
  const root = options.root;
  const appendDebug = typeof options.appendDebug === "function" ? options.appendDebug : () => {};
  const appendSystem = typeof options.appendSystem === "function" ? options.appendSystem : () => {};
  const onSurfaceEvent = typeof options.onSurfaceEvent === "function" ? options.onSurfaceEvent : () => {};
  const reportActionRecord = typeof options.reportActionRecord === "function" ? options.reportActionRecord : () => {};

  const registry = new Map();
  const runtimes = new Map();
  const actionWaiters = new Map();

  let panel = null;
  let statusEl = null;
  let listEl = null;
  let workspaceEl = null;
  let visible = false;

  function setStatus(text) {
    if (statusEl) statusEl.textContent = text;
  }

  function findSurfaceByTarget(target) {
    const key = String(target || "").trim();
    if (!key) return null;
    if (registry.has(key)) return registry.get(key);
    const lower = key.toLowerCase();
    for (const item of registry.values()) {
      if (String(item.name || "").trim().toLowerCase() === lower) return item;
    }
    return null;
  }

  function surfaceMeta(surfaceID) {
    const item = registry.get(surfaceID);
    return {
      surface_id: surfaceID,
      surface_type: item && item.surface_type ? item.surface_type : "app",
      surface_version: item && item.version ? item.version : "1",
    };
  }

  function availableRegistryItems() {
    const out = [];
    for (const item of registry.values()) {
      if (item.enabled && item.status === "ok") out.push(item);
    }
    return out;
  }

  function snapshotSurfaceDescriptor(surfaceID) {
    const item = registry.get(surfaceID);
    const runtime = runtimes.get(surfaceID);
    const state = runtime && runtime.state ? runtime.state : {};
    const actions = runtime ? Array.from(runtime.actions.values()) : [];
    return {
      surface_id: surfaceID,
      surface_type: item && item.surface_type ? item.surface_type : "app",
      surface_version: item && item.version ? item.version : "1",
      name: item && item.name ? item.name : surfaceID,
      desc: item && item.desc ? item.desc : "",
      status: item ? item.status : "unknown",
      enabled: !!(item && item.enabled),
      available: !!(item && item.enabled && item.status === "ok"),
      visible: !!(runtime && runtime.open),
      ready: !!(runtime && runtime.ready),
      entry: item && item.entry ? item.entry : "",
      entry_url: item && item.entry_url ? item.entry_url : "",
      error: item && item.error ? item.error : "",
      capabilities: runtime ? runtime.capabilities : {},
      actions: actions.map((it) => ({ name: it.name, description: it.description || "" })),
      state_version: Number.isFinite(state.state_version) ? state.state_version : 0,
      business_state: state.business_state && typeof state.business_state === "object" ? state.business_state : {},
      visible_text: typeof state.visible_text === "string" ? state.visible_text : "",
    };
  }

  function emitSurfaceState(evtType, surfaceID, payload) {
    onSurfaceEvent({
      type: evtType,
      surface_id: surfaceID,
      payload: payload && typeof payload === "object" ? payload : {},
    });
  }

  function resolveAction(actionID, result) {
    const waiter = actionWaiters.get(actionID);
    if (!waiter) return;
    clearTimeout(waiter.timer);
    actionWaiters.delete(actionID);
    waiter.resolve(result);
  }

  function rejectActionsForSurface(surfaceID, reason) {
    for (const [actionID, waiter] of actionWaiters.entries()) {
      if (waiter.surfaceID !== surfaceID) continue;
      clearTimeout(waiter.timer);
      waiter.resolve({ ok: false, reason: reason || "surface_closed" });
      actionWaiters.delete(actionID);
    }
  }

  function registerRuntimeActions(runtime, actions) {
    if (!Array.isArray(actions)) return;
    for (const action of actions) {
      if (!action || typeof action !== "object") continue;
      const name = typeof action.name === "string" ? action.name.trim() : "";
      if (!name) continue;
      runtime.actions.set(name, {
        name,
        description: typeof action.description === "string" ? action.description : "",
        args_schema: action.args_schema && typeof action.args_schema === "object" ? action.args_schema : {},
      });
    }
  }

  function ensurePanel() {
    if (panel) return;
    panel = createPanel(root);
    statusEl = panel.querySelector(".surface-float-status");
    listEl = panel.querySelector(".surface-manager-list");
    workspaceEl = panel.querySelector(".surface-manager-workspace");
    panel.querySelector('[data-act="refresh"]').addEventListener("click", () => {
      refreshRegistry().catch((err) => {
        appendDebug("ERROR", "SurfaceBridge", null, null, `refresh surfaces failed: ${err.message || err}`);
      });
    });
    panel.querySelector('[data-act="close"]').addEventListener("click", () => {
      setVisible(false);
    });
  }

  function renderRegistry() {
    if (!listEl) return;
    const rows = [];
    for (const item of registry.values()) {
      const runtime = runtimes.get(item.surface_id);
      const opened = !!(runtime && runtime.open);
      const disabled = item.status !== "ok";
      const statusText = `${item.status}${item.error ? ` (${item.error})` : ""}`;
      rows.push(`
        <div class="surface-manager-item" data-surface-id="${escapeHTML(item.surface_id)}">
          <div class="surface-manager-meta">
            <div><strong>${escapeHTML(item.name || item.surface_id)}</strong></div>
            <div>${escapeHTML(item.surface_id)}</div>
            <div>${escapeHTML(item.surface_type)} / v${escapeHTML(item.version || "1")}</div>
            <div>${escapeHTML(statusText)}</div>
          </div>
          <div class="surface-manager-actions">
            <label>
              <input type="checkbox" data-act="enable" ${item.enabled ? "checked" : ""} ${disabled ? "disabled" : ""}/>
              启用
            </label>
            <button type="button" data-act="${opened ? "close_surface" : "open_surface"}" ${item.enabled && item.status === "ok" ? "" : "disabled"}>
              ${opened ? "关闭" : "打开"}
            </button>
          </div>
        </div>
      `);
    }
    if (rows.length === 0) {
      listEl.innerHTML = `<div class="surface-manager-empty">暂无 surface 包</div>`;
    } else {
      listEl.innerHTML = rows.join("");
    }
    listEl.querySelectorAll("[data-act='enable']").forEach((inputEl) => {
      inputEl.addEventListener("change", async (ev) => {
        const itemEl = ev.target.closest(".surface-manager-item");
        if (!itemEl) return;
        const surfaceID = itemEl.getAttribute("data-surface-id");
        const enabled = !!ev.target.checked;
        try {
          await fetchJSON(`/api/surfaces/${encodeURIComponent(surfaceID)}/enable`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ enabled }),
          });
          const current = registry.get(surfaceID);
          if (current) current.enabled = enabled;
          renderRegistry();
        } catch (err) {
          ev.target.checked = !enabled;
          appendSystem(`更新 surface 启用状态失败: ${err.message || err}`);
        }
      });
    });
    listEl.querySelectorAll("[data-act='open_surface']").forEach((btnEl) => {
      btnEl.addEventListener("click", async (ev) => {
        const itemEl = ev.target.closest(".surface-manager-item");
        if (!itemEl) return;
        const surfaceID = itemEl.getAttribute("data-surface-id");
        try {
          await ensureSurfaceOpen(surfaceID);
          renderRegistry();
        } catch (err) {
          appendSystem(`打开 surface 失败: ${err.message || err}`);
        }
      });
    });
    listEl.querySelectorAll("[data-act='close_surface']").forEach((btnEl) => {
      btnEl.addEventListener("click", (ev) => {
        const itemEl = ev.target.closest(".surface-manager-item");
        if (!itemEl) return;
        const surfaceID = itemEl.getAttribute("data-surface-id");
        closeSurface(surfaceID, "user_click");
        renderRegistry();
      });
    });
  }

  function createRuntimeView(item) {
    const host = document.createElement("div");
    host.className = "surface-runtime-host";
    host.setAttribute("data-surface-id", item.surface_id);
    const title = document.createElement("div");
    title.className = "surface-runtime-title";
    title.textContent = `${item.name || item.surface_id} (${item.surface_id})`;
    const iframe = document.createElement("iframe");
    iframe.className = "surface-float-iframe";
    iframe.setAttribute("sandbox", "allow-scripts allow-downloads");
    iframe.src = item.entry_url;
    host.appendChild(title);
    host.appendChild(iframe);
    workspaceEl.appendChild(host);
    return { host, iframe };
  }

  async function refreshRegistry() {
    const data = await fetchJSON("/api/surfaces", { cache: "no-store" });
    const items = Array.isArray(data && data.items) ? data.items : [];
    registry.clear();
    for (const item of items) {
      if (!item || typeof item !== "object") continue;
      const surfaceID = typeof item.surface_id === "string" ? item.surface_id.trim() : "";
      if (!surfaceID) continue;
      registry.set(surfaceID, {
        surface_id: surfaceID,
        surface_type: typeof item.surface_type === "string" ? item.surface_type : "app",
        name: typeof item.name === "string" ? item.name : surfaceID,
        version: typeof item.version === "string" ? item.version : "1",
        entry: typeof item.entry === "string" ? item.entry : "",
        entry_url: typeof item.entry_url === "string" ? item.entry_url : "",
        desc: typeof item.desc === "string" ? item.desc : "",
        status: typeof item.status === "string" ? item.status : "invalid",
        error: typeof item.error === "string" ? item.error : "",
        enabled: !!item.enabled,
      });
    }
    setStatus(`surfaces=${registry.size}`);
    renderRegistry();
    return items;
  }

  function runtimeFromSurfaceID(surfaceID) {
    const sid = String(surfaceID || "").trim();
    if (!sid) return null;
    return runtimes.get(sid) || null;
  }

  async function ensureCapability(runtime, scope, pathPrefix = ".") {
    const key = `${scope}|${pathPrefix}`;
    const cached = runtime.capabilityCache.get(key);
    if (cached && Number.isFinite(cached.exp_ms) && cached.exp_ms - Date.now() > 1000) {
      return cached.token;
    }
    const payload = await fetchJSON("/api/surfacefs/capability", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        surface_session_token: runtime.sessionToken,
        scope,
        path_prefix: pathPrefix,
        ttl_seconds: 300,
      }),
    });
    const token = payload && typeof payload.capability_token === "string" ? payload.capability_token : "";
    if (!token) throw new Error("surfacefs capability token is empty");
    runtime.capabilityCache.set(key, {
      token,
      exp_ms: Number.isFinite(payload.exp_ms) ? payload.exp_ms : Date.now() + 4 * 60 * 1000,
    });
    return token;
  }

  async function handleSurfaceFSRequest(runtime, msg) {
    const requestID = typeof msg.request_id === "string" ? msg.request_id : `fs-${Date.now()}`;
    const op = typeof msg.op === "string" ? msg.op : "";
    const relPath = typeof msg.path === "string" ? msg.path : ".";
    try {
      if (op === "read") {
        const capabilityToken = await ensureCapability(runtime, "fs.read", ".");
        const payload = await fetchJSON("/api/surfacefs/read", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ capability_token: capabilityToken, surface_id: runtime.surfaceID, path: relPath }),
        });
        runtime.port.postMessage({ type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "write") {
        const capabilityToken = await ensureCapability(runtime, "fs.write", ".");
        const dataBase64 = typeof msg.data_base64 === "string" ? msg.data_base64 : "";
        const payload = await fetchJSON("/api/surfacefs/write", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            capability_token: capabilityToken,
            surface_id: runtime.surfaceID,
            path: relPath,
            data_base64: dataBase64,
          }),
        });
        runtime.port.postMessage({ type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "list") {
        const capabilityToken = await ensureCapability(runtime, "fs.list", ".");
        const payload = await fetchJSON("/api/surfacefs/list", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ capability_token: capabilityToken, surface_id: runtime.surfaceID, path: relPath }),
        });
        runtime.port.postMessage({ type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "delete") {
        const capabilityToken = await ensureCapability(runtime, "fs.delete", ".");
        const payload = await fetchJSON("/api/surfacefs/delete", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            capability_token: capabilityToken,
            surface_id: runtime.surfaceID,
            path: relPath,
            recursive: !!msg.recursive,
          }),
        });
        runtime.port.postMessage({ type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "sign_static") {
        const capabilityToken = await ensureCapability(runtime, "fs.static", relPath);
        const urlPath = encodePathSegments(relPath);
        const signedURL = `/surfacefs/static/${encodeURIComponent(runtime.surfaceID)}/${urlPath}?st=${encodeURIComponent(capabilityToken)}`;
        runtime.port.postMessage({
          type: "surfacefs_response",
          request_id: requestID,
          ok: true,
          payload: { url: signedURL, path: relPath },
        });
        return;
      }
      throw new Error(`unsupported surfacefs op: ${op}`);
    } catch (err) {
      runtime.port.postMessage({
        type: "surfacefs_response",
        request_id: requestID,
        ok: false,
        error: err && err.message ? err.message : String(err),
      });
    }
  }

  function recordHostCall(runtime, capability, args, result, ok) {
    reportActionRecord({
      turnId: 0,
      actionId: `host-${Date.now()}-${Math.floor(Math.random() * 100000)}`,
      category: "surface_host",
      actionName: `surface.host.${capability}`,
      actionSurfaceID: runtime.surfaceID,
      actionSurfaceType: runtime.surfaceType,
      actionSurfaceVersion: runtime.surfaceVersion,
      status: ok ? "ok" : "fail",
      followup: "none",
      content: "",
      args,
      result: result && typeof result === "object" ? result : {},
      effect: { source: "surface.host.proxy", capability },
      state: runtime.state && runtime.state.business_state ? runtime.state.business_state : {},
    });
  }

  function handleHostCall(runtime, msg) {
    const callID = typeof msg.call_id === "string" ? msg.call_id : `host-${Date.now()}`;
    const capability = typeof msg.capability === "string" ? msg.capability.trim() : "";
    const args = msg.args && typeof msg.args === "object" ? msg.args : {};
    let ok = true;
    let payload = {};
    if (capability === "flash") {
      const message = typeof args.message === "string" ? args.message : "(empty)";
      appendSystem(`[surface:${runtime.surfaceID}] ${message}`);
      payload = { delivered: true };
    } else if (capability === "chat" || capability === "tts" || capability === "asr" || capability === "isr") {
      payload = { accepted: false, reason: "not_implemented_yet" };
      ok = false;
    } else {
      payload = { accepted: false, reason: "unsupported_capability" };
      ok = false;
    }
    recordHostCall(runtime, capability, args, payload, ok);
    runtime.port.postMessage({
      type: "host_call_result",
      call_id: callID,
      capability,
      ok,
      payload,
    });
  }

  function onRuntimeMessage(runtime, msg) {
    if (!msg || typeof msg !== "object") return;

    if (msg.type === "surface_ready") {
      runtime.ready = true;
      runtime.capabilities = msg.capabilities && typeof msg.capabilities === "object" ? msg.capabilities : {};
      registerRuntimeActions(runtime, msg.actions);
      if (runtime.capabilities.get_state && !runtime.actions.has("get_state")) {
        runtime.actions.set("get_state", { name: "get_state", description: "读取当前状态", args_schema: {} });
      }
      if (msg.state && typeof msg.state === "object") {
        runtime.state = { ...msg.state };
        emitSurfaceState("surface_open", runtime.surfaceID, {
          ...runtime.state,
          event_type: "surface_open",
        });
      }
      setStatus(`${runtime.surfaceID} ready`);
      return;
    }

    if (msg.type === "surface_register_actions") {
      registerRuntimeActions(runtime, msg.actions);
      return;
    }

    if (msg.type === "state_change") {
      runtime.state = { ...msg };
      emitSurfaceState("state_change", runtime.surfaceID, runtime.state);
      return;
    }

    if (msg.type === "action_result") {
      const actionID = typeof msg.action_id === "string" ? msg.action_id : "";
      if (msg.business_state && typeof msg.business_state === "object") {
        const nextState = runtime.state && typeof runtime.state === "object" ? { ...runtime.state } : {};
        nextState.business_state = msg.business_state;
        nextState.visible_text = typeof msg.visible_text === "string" ? msg.visible_text : (nextState.visible_text || "");
        nextState.state_version = Number.isFinite(msg.state_version) ? msg.state_version : (Number.isFinite(nextState.state_version) ? nextState.state_version : 0);
        runtime.state = nextState;
      }
      resolveAction(actionID, {
        ok: (msg.status || "ok") === "ok",
        status: typeof msg.status === "string" ? msg.status : "ok",
        reason: typeof msg.error === "string" ? msg.error : "",
        action_id: actionID,
        action_name: typeof msg.action_name === "string" ? msg.action_name : "",
        surface_id: runtime.surfaceID,
        surface_type: runtime.surfaceType,
        surface_version: runtime.surfaceVersion,
        result: msg.result && typeof msg.result === "object" ? msg.result : {},
        business_state: runtime.state && runtime.state.business_state ? runtime.state.business_state : {},
        state_version: runtime.state && Number.isFinite(runtime.state.state_version) ? runtime.state.state_version : 0,
        effect: {
          source: "surface.action_result",
          business_state: runtime.state && runtime.state.business_state ? runtime.state.business_state : {},
          visible_text: runtime.state && typeof runtime.state.visible_text === "string" ? runtime.state.visible_text : "",
        },
      });
      return;
    }

    if (msg.type === "surfacefs_request") {
      handleSurfaceFSRequest(runtime, msg).catch((err) => {
        appendDebug("ERROR", "SurfaceFS", null, null, err.message || String(err));
      });
      return;
    }

    if (msg.type === "host_call") {
      handleHostCall(runtime, msg);
      return;
    }
  }

  async function createRuntime(item) {
    const sessionPayload = await fetchJSON(`/api/surfaces/${encodeURIComponent(item.surface_id)}/session-token`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "{}",
    });
    const sessionToken = sessionPayload && typeof sessionPayload.surface_session_token === "string"
      ? sessionPayload.surface_session_token
      : "";
    if (!sessionToken) throw new Error("surface session token is empty");

    const view = createRuntimeView(item);
    const runtime = {
      surfaceID: item.surface_id,
      surfaceType: item.surface_type || "app",
      surfaceVersion: item.version || "1",
      open: true,
      ready: false,
      iframe: view.iframe,
      hostEl: view.host,
      port: null,
      state: {},
      actions: new Map(),
      capabilities: {},
      sessionToken,
      capabilityCache: new Map(),
    };
    runtimes.set(runtime.surfaceID, runtime);

    view.iframe.addEventListener("load", () => {
      if (!runtime.open) return;
      const channel = new MessageChannel();
      runtime.port = channel.port1;
      runtime.port.onmessage = (ev) => onRuntimeMessage(runtime, ev.data);
      runtime.port.start();
      try {
        runtime.iframe.contentWindow.postMessage({
          type: "surface_connect",
          surface_id: runtime.surfaceID,
          surface_type: runtime.surfaceType,
          surface_version: runtime.surfaceVersion,
          session_token: runtime.sessionToken,
        }, "*", [channel.port2]);
      } catch (err) {
        appendDebug("ERROR", "SurfaceBridge", null, null, `surface connect failed: ${err.message || err}`);
      }
    }, { once: true });

    return runtime;
  }

  async function waitRuntimeReady(runtime, timeoutMs = 4000) {
    const start = Date.now();
    while (Date.now() - start < timeoutMs) {
      if (!runtime.open) return false;
      if (runtime.ready) return true;
      // eslint-disable-next-line no-await-in-loop
      await new Promise((resolve) => setTimeout(resolve, 40));
    }
    return runtime.ready;
  }

  async function ensureSurfaceOpen(surfaceID) {
    const item = registry.get(surfaceID);
    if (!item) throw new Error(`surface 不存在: ${surfaceID}`);
    if (!item.enabled || item.status !== "ok") throw new Error(`surface 不可用: ${surfaceID}`);
    const existing = runtimes.get(surfaceID);
    if (existing && existing.open) return existing;
    const runtime = await createRuntime(item);
    const ready = await waitRuntimeReady(runtime, 4500);
    if (!ready) {
      throw new Error(`surface 打开超时: ${surfaceID}`);
    }
    renderRegistry();
    return runtime;
  }

  function closeSurface(surfaceID, reason = "manual") {
    const runtime = runtimes.get(surfaceID);
    if (!runtime) return false;
    runtime.open = false;
    if (runtime.port) {
      try { runtime.port.close(); } catch (_) {}
      runtime.port = null;
    }
    rejectActionsForSurface(surfaceID, reason === "ai_action" ? "surface_closed" : reason);
    if (runtime.hostEl && runtime.hostEl.parentElement) {
      runtime.hostEl.parentElement.removeChild(runtime.hostEl);
    }
    runtimes.delete(surfaceID);

    emitSurfaceState("state_change", surfaceID, {
      surface_id: surfaceID,
      surface_type: runtime.surfaceType,
      surface_version: runtime.surfaceVersion,
      event_type: "surface_closed",
      business_state: {},
      visible_text: "",
      status: "closed",
      state_version: Number.isFinite(runtime.state && runtime.state.state_version) ? runtime.state.state_version + 1 : 1,
      updated_at_ms: Date.now(),
    });
    return true;
  }

  function setVisible(nextVisible) {
    ensurePanel();
    visible = !!nextVisible;
    panel.classList.toggle("open", visible);
    if (visible) {
      refreshRegistry().catch((err) => {
        appendDebug("ERROR", "SurfaceBridge", null, null, `refresh surfaces failed: ${err.message || err}`);
      });
    }
  }

  async function dispatchAction(rawAction) {
    const action = toActionPayload(rawAction);
    if (!action) return { ok: false, reason: "invalid_action" };
    if (registry.size === 0) {
      await refreshRegistry();
    }

    if (action.name === "get_surfaces") {
      const surfaces = availableRegistryItems().map((it) => snapshotSurfaceDescriptor(it.surface_id));
      return {
        ok: true,
        status: "ok",
        action_id: action.id,
        action_name: action.name,
        surface_id: "surface_registry",
        surface_type: "meta",
        surface_version: "1",
        result: { total: surfaces.length, surfaces },
        business_state: {},
        state_version: 0,
        effect: { source: "surface.registry", surfaces },
      };
    }

    if (action.name === "open_surface") {
      const target = typeof action.args.target === "string" && action.args.target.trim()
        ? action.args.target.trim()
        : (typeof action.args.surface_id === "string" && action.args.surface_id.trim() ? action.args.surface_id.trim() : "");
      const item = target ? findSurfaceByTarget(target) : (availableRegistryItems().length === 1 ? availableRegistryItems()[0] : null);
      if (!item) return { ok: false, reason: `surface_not_found:${target}` };
      const runtime = await ensureSurfaceOpen(item.surface_id);
      const descriptor = snapshotSurfaceDescriptor(runtime.surfaceID);
      return {
        ok: true,
        status: "ok",
        action_id: action.id,
        action_name: action.name,
        surface_id: runtime.surfaceID,
        surface_type: runtime.surfaceType,
        surface_version: runtime.surfaceVersion,
        result: { opened: true, surface: descriptor },
        business_state: descriptor.business_state,
        state_version: descriptor.state_version || 0,
        effect: {
          source: "surface.open",
          business_state: descriptor.business_state || {},
          visible_text: descriptor.visible_text || "",
        },
      };
    }

    if (action.name === "close_surface") {
      const target = typeof action.args.target === "string" && action.args.target.trim()
        ? action.args.target.trim()
        : (typeof action.args.surface_id === "string" && action.args.surface_id.trim() ? action.args.surface_id.trim() : "");
      let item = target ? findSurfaceByTarget(target) : null;
      if (!item && !target && runtimes.size === 1) {
        const firstID = Array.from(runtimes.keys())[0];
        item = registry.get(firstID) || null;
      }
      if (!item) return { ok: false, reason: `surface_not_found:${target}` };
      const closed = closeSurface(item.surface_id, "ai_action");
      renderRegistry();
      const meta = surfaceMeta(item.surface_id);
      return {
        ok: true,
        status: "ok",
        action_id: action.id,
        action_name: action.name,
        surface_id: item.surface_id,
        surface_type: meta.surface_type,
        surface_version: meta.surface_version,
        result: { closed: true, already_closed: !closed },
        business_state: {},
        state_version: 0,
        effect: { source: "surface.close", business_state: {}, visible_text: "" },
      };
    }

    if (action.name === "surface.get_state") {
      const target = typeof action.args.surface_id === "string" && action.args.surface_id.trim()
        ? action.args.surface_id.trim()
        : (typeof action.args.target === "string" && action.args.target.trim() ? action.args.target.trim() : "");
      let item = target ? findSurfaceByTarget(target) : null;
      if (!item && !target) {
        if (runtimes.size === 1) {
          const firstID = Array.from(runtimes.keys())[0];
          item = registry.get(firstID) || null;
        } else if (availableRegistryItems().length === 1) {
          item = availableRegistryItems()[0];
        }
      }
      if (!item) return { ok: false, reason: `surface_not_found:${target}` };
      const runtime = runtimeFromSurfaceID(item.surface_id);
      if (!runtime || !runtime.open) return { ok: false, reason: "surface_closed" };
      const state = runtime.state && typeof runtime.state === "object" ? runtime.state : {};
      return {
        ok: true,
        status: "ok",
        action_id: action.id,
        action_name: action.name,
        surface_id: runtime.surfaceID,
        surface_type: runtime.surfaceType,
        surface_version: runtime.surfaceVersion,
        result: { from_cache: true },
        business_state: state.business_state && typeof state.business_state === "object" ? state.business_state : {},
        state_version: Number.isFinite(state.state_version) ? state.state_version : 0,
        effect: {
          source: "surface.cache",
          business_state: state.business_state && typeof state.business_state === "object" ? state.business_state : {},
          visible_text: typeof state.visible_text === "string" ? state.visible_text : "",
        },
      };
    }

    if (action.name.startsWith("surface.call.")) {
      const parsed = parseSurfaceCallName(action.name);
      if (!parsed) return { ok: false, reason: "invalid_surface_call_name" };
      const runtime = runtimeFromSurfaceID(parsed.surfaceID);
      if (!runtime || !runtime.open) return { ok: false, reason: "surface_closed" };
      if (!runtime.ready || !runtime.port) return { ok: false, reason: "surface_not_ready" };
      if (!runtime.actions.has(parsed.actionName)) {
        return { ok: false, reason: `action_not_registered:${parsed.actionName}` };
      }
      return new Promise((resolve) => {
        const timer = setTimeout(() => {
          actionWaiters.delete(action.id);
          resolve({ ok: false, reason: "surface_action_timeout" });
        }, 5000);
        actionWaiters.set(action.id, { resolve, timer, surfaceID: runtime.surfaceID });
        runtime.port.postMessage({
          type: "action_call",
          action: {
            id: action.id,
            name: parsed.actionName,
            args: action.args || {},
          },
        });
      });
    }

    return { ok: false, reason: "unsupported_action" };
  }

  function toggleVisible() {
    setVisible(!visible);
  }

  function getCachedState(surfaceID) {
    const runtime = runtimeFromSurfaceID(surfaceID);
    if (!runtime || !runtime.state) return null;
    return runtime.state;
  }

  function hasCapability(surfaceID, capability = "get_state") {
    const runtime = runtimeFromSurfaceID(surfaceID);
    if (!runtime) return false;
    return !!runtime.capabilities[capability];
  }

  return {
    setVisible,
    toggleVisible,
    dispatchAction,
    getCachedState,
    hasCapability,
    refreshRegistry,
  };
}
