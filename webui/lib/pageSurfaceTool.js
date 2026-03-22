function createID(prefix) {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
}

function asObject(value, fallback = {}) {
  return value && typeof value === "object" ? value : fallback;
}

function normalizeActionDescriptor(action) {
  const item = asObject(action, null);
  if (!item) return null;
  const name = typeof item.name === "string" ? item.name.trim() : "";
  if (!name) return null;
  return {
    name,
    description: typeof item.description === "string" ? item.description : "",
    args_schema: asObject(item.args_schema),
    result_schema: asObject(item.result_schema),
    timeout_ms_default: Number.isFinite(item.timeout_ms_default) ? item.timeout_ms_default : 0,
    side_effect: typeof item.side_effect === "string" ? item.side_effect : "none",
    streaming: !!item.streaming,
  };
}

function buildSurfaceLoadURL(rawURL) {
  const url = new URL(String(rawURL || ""), window.location.origin);
  const nonce = `${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
  url.searchParams.set("_surface_reload", nonce);
  return url.toString();
}

export function createPageSurfaceHost(options = {}) {
  const callTool = typeof options.callTool === "function" ? options.callTool : null;
  if (!callTool) {
    throw new Error("callTool is required");
  }
  const pageInfo = {
    page_id: typeof options.pageID === "string" && options.pageID.trim() ? options.pageID.trim() : "page",
    page_type: typeof options.pageType === "string" && options.pageType.trim() ? options.pageType.trim() : "host",
    protocol_version: typeof options.protocolVersion === "string" && options.protocolVersion.trim()
      ? options.protocolVersion.trim()
      : "page-surface.v1",
  };

  const runtimes = new Map();
  const hostActions = new Map();
  const notifyEvent = typeof options.onRuntimeEvent === "function" ? options.onRuntimeEvent : () => {};
  const notifyError = typeof options.onError === "function" ? options.onError : () => {};

  const initialHostActions = Array.isArray(options.hostActions) ? options.hostActions : [];
  initialHostActions.forEach((action) => registerHostAction(action));

  function registerHostAction(action) {
    const item = asObject(action, null);
    const name = item && typeof item.name === "string" ? item.name.trim() : "";
    if (!name) return;
    hostActions.set(name, {
      name,
      description: typeof item.description === "string" ? item.description : "",
      handler: typeof item.handler === "function" ? item.handler : async () => ({ ok: false, reason: "not_implemented" }),
    });
  }

  function hostActionDescriptors() {
    return Array.from(hostActions.values()).map((item) => ({
      name: item.name,
      description: item.description,
    }));
  }

  function emit(eventType, runtime, payload = {}) {
    notifyEvent({
      type: eventType,
      surface_id: runtime ? runtime.surfaceID : "",
      runtime,
      payload,
    });
  }

  function buildWorkspaceState(runtime, patch = {}) {
    return {
      open: true,
      focused: true,
      frozen: false,
      minimized: false,
      maximized: false,
      geometry: {
        x: 0,
        y: 0,
        width: 0,
        height: 0,
      },
      z_index: 0,
      ...runtime.workspaceState,
      ...patch,
      geometry: {
        ...(runtime.workspaceState && runtime.workspaceState.geometry ? runtime.workspaceState.geometry : {}),
        ...(patch && patch.geometry ? patch.geometry : {}),
      },
    };
  }

  function getRuntime(surfaceID) {
    return runtimes.get(String(surfaceID || "").trim()) || null;
  }

  function getRuntimeSnapshot(surfaceID) {
    const runtime = getRuntime(surfaceID);
    if (!runtime) return null;
    return {
      surface_id: runtime.surfaceID,
      surface_type: runtime.surfaceType,
      surface_version: runtime.surfaceVersion,
      ready: runtime.ready,
      registration: runtime.registration,
      state: runtime.state,
      workspace_state: runtime.workspaceState,
      host_actions: hostActionDescriptors(),
      actions: Array.from(runtime.actions.values()),
    };
  }

  async function refreshCatalog() {
    return callTool("ui.surface.catalog_list", {});
  }

  function rejectAllWaiters(runtime, reason) {
    runtime.actionWaiters.forEach((waiter, actionID) => {
      clearTimeout(waiter.timer);
      waiter.reject(new Error(reason || `surface closed: ${actionID}`));
    });
    runtime.actionWaiters.clear();
    runtime.hostWaiters.forEach((waiter) => {
      clearTimeout(waiter.timer);
      waiter.reject(new Error(reason || "surface closed"));
    });
    runtime.hostWaiters.clear();
  }

  function post(runtime, payload) {
    if (!runtime.port) {
      throw new Error("surface port is not ready");
    }
    runtime.port.postMessage(payload);
  }

  async function handleHostActionCall(runtime, msg) {
    const requestID = typeof msg.request_id === "string" && msg.request_id.trim()
      ? msg.request_id.trim()
      : createID("host");
    const actionName = typeof msg.action_name === "string" ? msg.action_name.trim() : "";
    const args = asObject(msg.args);
    const descriptor = hostActions.get(actionName);
    if (!descriptor) {
      post(runtime, {
        type: "host_action_result",
        request_id: requestID,
        action_name: actionName,
        ok: false,
        error: { message: "host action is not allowed" },
      });
      return;
    }
    try {
      const result = await descriptor.handler({
        runtime,
        args,
        updateWorkspaceState: (patch) => updateWorkspaceState(runtime.surfaceID, patch),
      });
      post(runtime, {
        type: "host_action_result",
        request_id: requestID,
        action_name: actionName,
        ok: true,
        result: result == null ? {} : result,
      });
    } catch (error) {
      post(runtime, {
        type: "host_action_result",
        request_id: requestID,
        action_name: actionName,
        ok: false,
        error: { message: error && error.message ? error.message : String(error) },
      });
    }
  }

  function handleRuntimeMessage(runtime, msg) {
    const data = asObject(msg, null);
    if (!data) return;
    if (data.type === "surface_register") {
      runtime.registration = {
        title: typeof data.title === "string" ? data.title : runtime.entry.name || runtime.surfaceID,
        description: typeof data.description === "string" ? data.description : runtime.entry.desc || "",
        protocol_version: typeof data.protocol_version === "string" ? data.protocol_version : pageInfo.protocol_version,
      };
      runtime.actions.clear();
      const registeredActions = Array.isArray(data.actions) ? data.actions : [];
      registeredActions.forEach((action) => {
        const descriptor = normalizeActionDescriptor(action);
        if (descriptor) runtime.actions.set(descriptor.name, descriptor);
      });
      runtime.registered = true;
      post(runtime, {
        type: "surface_register_ack",
        request_id: typeof data.request_id === "string" ? data.request_id : "",
        protocol_version: pageInfo.protocol_version,
        page_info: pageInfo,
        host_actions: hostActionDescriptors(),
        workspace_state: runtime.workspaceState,
      });
      emit("surface_register", runtime, getRuntimeSnapshot(runtime.surfaceID));
      return;
    }
    if (data.type === "surface_ready") {
      runtime.ready = true;
      runtime.state = {
        lifecycle_status: "ready",
        business_state: {},
        visible_text: "",
        state_version: 0,
        updated_at_ms: Date.now(),
        ...asObject(data.state),
      };
      if (typeof runtime.readyResolve === "function") {
        runtime.readyResolve(getRuntimeSnapshot(runtime.surfaceID));
        runtime.readyResolve = null;
        runtime.readyReject = null;
      }
      emit("surface_ready", runtime, getRuntimeSnapshot(runtime.surfaceID));
      return;
    }
    if (data.type === "state_change") {
      runtime.state = {
        ...runtime.state,
        ...data,
      };
      emit("state_change", runtime, runtime.state);
      return;
    }
    if (data.type === "action_result") {
      const actionID = typeof data.action_id === "string" ? data.action_id.trim() : "";
      const waiter = actionID ? runtime.actionWaiters.get(actionID) : null;
      if (!waiter) return;
      clearTimeout(waiter.timer);
      runtime.actionWaiters.delete(actionID);
      waiter.resolve(data);
      return;
    }
    if (data.type === "host_action_call") {
      handleHostActionCall(runtime, data).catch((error) => notifyError(error));
      return;
    }
    if (data.type === "surface_log" || data.type === "stream_open" || data.type === "stream_chunk" || data.type === "stream_end" || data.type === "stream_error" || data.type === "surface_close") {
      emit(data.type, runtime, data);
      if (data.type === "surface_close") {
        closeSurface(runtime.surfaceID, "surface_close");
      }
    }
  }

  async function openSurface({ surfaceID, iframe, workspaceState = {}, cacheBust = false }) {
    const targetSurfaceID = String(surfaceID || "").trim();
    if (!targetSurfaceID) throw new Error("surface_id is required");
    if (!iframe || typeof iframe !== "object") throw new Error("iframe is required");
    const entry = await callTool("ui.surface.get", { surface_id: targetSurfaceID });
    const session = await callTool("ui.surface.session_issue", { surface_id: targetSurfaceID });
    const sessionToken = typeof session.surface_session_token === "string" ? session.surface_session_token : "";
    if (!sessionToken) throw new Error("surface session token is empty");

    const existing = getRuntime(targetSurfaceID);
    if (existing) {
      closeSurface(targetSurfaceID, "reopen");
    }

    const runtime = {
      surfaceID: targetSurfaceID,
      surfaceType: typeof entry.surface_type === "string" ? entry.surface_type : "app",
      surfaceVersion: typeof entry.version === "string" ? entry.version : "1.0",
      entry,
      iframe,
      port: null,
      ready: false,
      registered: false,
      registration: null,
      readyPromise: null,
      readyResolve: null,
      readyReject: null,
      state: {
        lifecycle_status: "starting",
        business_state: {},
        visible_text: "",
        state_version: 0,
        updated_at_ms: Date.now(),
      },
      workspaceState: buildWorkspaceState({ workspaceState: {} }, workspaceState),
      actions: new Map(),
      actionWaiters: new Map(),
      hostWaiters: new Map(),
      sessionToken,
    };
    runtime.readyPromise = new Promise((resolve, reject) => {
      runtime.readyResolve = resolve;
      runtime.readyReject = reject;
    });
    runtimes.set(targetSurfaceID, runtime);
    iframe.onload = () => {
      const channel = new MessageChannel();
      runtime.port = channel.port1;
      runtime.port.onmessage = (event) => {
        try {
          handleRuntimeMessage(runtime, event.data);
        } catch (error) {
          notifyError(error);
        }
      };
      runtime.port.start();
      runtime.iframe.contentWindow.postMessage({
        type: "surface_connect",
        request_id: createID("connect"),
        surface_id: runtime.surfaceID,
        surface_type: runtime.surfaceType,
        surface_version: runtime.surfaceVersion,
        surface_session_token: runtime.sessionToken,
        page_info: pageInfo,
        workspace_state: runtime.workspaceState,
      }, "*", [channel.port2]);
    };
    iframe.src = cacheBust ? buildSurfaceLoadURL(entry.entry_url) : entry.entry_url;
    emit("surface_opening", runtime, { entry });
    return runtime;
  }

  function closeSurface(surfaceID, reason = "closed") {
    const runtime = getRuntime(surfaceID);
    if (!runtime) return false;
    if (!runtime.ready && typeof runtime.readyReject === "function") {
      runtime.readyReject(new Error(`surface closed before ready: ${reason}`));
      runtime.readyResolve = null;
      runtime.readyReject = null;
    }
    rejectAllWaiters(runtime, reason);
    if (runtime.port) {
      try {
        runtime.port.close();
      } catch (_) {}
    }
    if (runtime.iframe) {
      runtime.iframe.onload = null;
    }
    runtimes.delete(runtime.surfaceID);
    emit("surface_closed", runtime, { reason });
    return true;
  }

  function callSurfaceAction(surfaceID, actionName, args = {}, options = {}) {
    const runtime = getRuntime(surfaceID);
    if (!runtime || !runtime.port) {
      return Promise.reject(new Error("surface runtime is not ready"));
    }
    if (!runtime.ready) {
      return Promise.reject(new Error("surface is not ready"));
    }
    const descriptor = runtime.actions.get(String(actionName || "").trim());
    if (!descriptor) {
      return Promise.reject(new Error(`surface action is not registered: ${actionName}`));
    }
    const actionID = typeof options.actionID === "string" && options.actionID.trim()
      ? options.actionID.trim()
      : createID("action");
    const timeoutMS = Number.isFinite(options.timeoutMS) && options.timeoutMS > 0
      ? options.timeoutMS
      : (descriptor.timeout_ms_default > 0 ? descriptor.timeout_ms_default : 10000);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        runtime.actionWaiters.delete(actionID);
        reject(new Error(`surface action timeout: ${actionName}`));
      }, timeoutMS);
      runtime.actionWaiters.set(actionID, { resolve, reject, timer });
      post(runtime, {
        type: "action_call",
        action: {
          id: actionID,
          name: descriptor.name,
          args: asObject(args),
        },
      });
    });
  }

  function updateWorkspaceState(surfaceID, patch = {}) {
    const runtime = getRuntime(surfaceID);
    if (!runtime) return null;
    runtime.workspaceState = buildWorkspaceState(runtime, patch);
    if (runtime.port) {
      post(runtime, {
        type: "workspace_state_change",
        workspace_state: runtime.workspaceState,
      });
    }
    emit("workspace_state_change", runtime, runtime.workspaceState);
    return runtime.workspaceState;
  }

  function waitUntilSurfaceReady(surfaceID, timeoutMS = 8000) {
    const runtime = getRuntime(surfaceID);
    if (!runtime) {
      return Promise.reject(new Error("surface runtime is not found"));
    }
    if (runtime.ready) {
      return Promise.resolve(getRuntimeSnapshot(runtime.surfaceID));
    }
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        reject(new Error(`surface ready timeout: ${runtime.surfaceID}`));
      }, timeoutMS);
      runtime.readyPromise
        .then((snapshot) => {
          clearTimeout(timer);
          resolve(snapshot);
        })
        .catch((error) => {
          clearTimeout(timer);
          reject(error);
        });
    });
  }

  return {
    callSurfaceAction,
    closeSurface,
    getRuntime,
    getRuntimeSnapshot,
    openSurface,
    refreshCatalog,
    registerHostAction,
    updateWorkspaceState,
    waitUntilSurfaceReady,
  };
}
