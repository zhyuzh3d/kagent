import { callHubTool } from "./hubToolClient.js";

function createID(prefix) {
  return `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2, 10)}`;
}

function asObject(value, fallback = {}) {
  return value && typeof value === "object" ? value : fallback;
}

function cloneState(state) {
  return JSON.parse(JSON.stringify(asObject(state)));
}

export function createSurfaceRuntime(options = {}) {
  const config = {
    surfaceID: typeof options.surfaceID === "string" && options.surfaceID.trim() ? options.surfaceID.trim() : "",
    surfaceType: typeof options.surfaceType === "string" && options.surfaceType.trim() ? options.surfaceType.trim() : "app",
    surfaceVersion: typeof options.surfaceVersion === "string" && options.surfaceVersion.trim() ? options.surfaceVersion.trim() : "1.0",
    title: typeof options.title === "string" ? options.title : "",
    description: typeof options.description === "string" ? options.description : "",
    protocolVersion: typeof options.protocolVersion === "string" && options.protocolVersion.trim()
      ? options.protocolVersion.trim()
      : "page-surface.v1",
    actions: Array.isArray(options.actions) ? options.actions : [],
  };

  const actionHandler = typeof options.onAction === "function" ? options.onAction : async () => ({});
  const connectHandler = typeof options.onConnect === "function" ? options.onConnect : async () => {};
  const readyHandler = typeof options.onReady === "function" ? options.onReady : async () => {};
  const eventHandler = typeof options.onEvent === "function" ? options.onEvent : async () => {};

  let port = null;
  let connectPayload = null;
  let registerAck = null;
  let readySent = false;
  let businessState = cloneState(options.initialState);
  let lifecycleStatus = typeof options.lifecycleStatus === "string" ? options.lifecycleStatus : "starting";

  const hostWaiters = new Map();

  function surfaceID() {
    if (connectPayload && typeof connectPayload.surface_id === "string" && connectPayload.surface_id.trim()) {
      return connectPayload.surface_id.trim();
    }
    return config.surfaceID;
  }

  function post(message) {
    if (!port) {
      throw new Error("surface port is not ready");
    }
    port.postMessage(message);
  }

  function currentSnapshot() {
    return {
      lifecycle_status: lifecycleStatus,
      business_state: cloneState(businessState),
      visible_text: typeof options.getVisibleText === "function" ? String(options.getVisibleText() || "") : "",
      state_version: Number.isFinite(options.getStateVersion?.()) ? options.getStateVersion() : 0,
      updated_at_ms: Date.now(),
    };
  }

  function emitRegister() {
    if (!port) return;
    post({
      type: "surface_register",
      request_id: createID("register"),
      surface_id: surfaceID(),
      surface_type: config.surfaceType,
      surface_version: config.surfaceVersion,
      protocol_version: config.protocolVersion,
      title: config.title,
      description: config.description,
      actions: config.actions,
    });
  }

  function emitReady() {
    if (readySent) return;
    readySent = true;
    if (!port) return;
    post({
      type: "surface_ready",
      state: currentSnapshot(),
    });
  }

  function emitStateChange(eventType = "state_change") {
    if (!port) return;
    post({
      type: "state_change",
      event_type: eventType,
      ...currentSnapshot(),
    });
  }

  function emitActionResult(action, payload = {}) {
    if (!port) return;
    post({
      type: "action_result",
      action_id: action && typeof action.id === "string" ? action.id : "",
      action_name: action && typeof action.name === "string" ? action.name : "",
      status: payload.ok === false ? "error" : "ok",
      result: payload.result == null ? {} : payload.result,
      error: payload.ok === false ? (payload.error || "surface action failed") : "",
      ...currentSnapshot(),
    });
  }

  function emitLog(level, message, extra = {}) {
    if (!port) return;
    post({
      type: "surface_log",
      level: typeof level === "string" ? level : "info",
      message: typeof message === "string" ? message : String(message || ""),
      extra: asObject(extra),
      updated_at_ms: Date.now(),
    });
  }

  async function callHostAction(actionName, args = {}, options = {}) {
    if (!port) {
      throw new Error("surface port is not ready");
    }
    const requestID = createID("host");
    const timeoutMS = Number.isFinite(options.timeoutMS) && options.timeoutMS > 0 ? options.timeoutMS : 8000;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        hostWaiters.delete(requestID);
        reject(new Error(`host action timeout: ${actionName}`));
      }, timeoutMS);
      hostWaiters.set(requestID, { resolve, reject, timer });
      post({
        type: "host_action_call",
        request_id: requestID,
        action_name: String(actionName || "").trim(),
        args: asObject(args),
      });
    });
  }

  async function callTool(toolID, args = {}, options = {}) {
    const sessionToken = connectPayload
      ? String(connectPayload.surface_session_token || connectPayload.session_token || "").trim()
      : "";
    if (!sessionToken) {
      throw new Error("surface session token is missing");
    }
    return callHubTool(toolID, args, {
      ...options,
      headers: {
        ...(options.headers || {}),
        "X-Surface-Token": sessionToken,
      },
    });
  }

  function setBusinessState(nextState, nextLifecycleStatus = lifecycleStatus) {
    businessState = cloneState(nextState);
    lifecycleStatus = String(nextLifecycleStatus || lifecycleStatus || "ready");
  }

  async function handlePortMessage(message) {
    const data = asObject(message, null);
    if (!data) return;
    if (data.type === "surface_register_ack") {
      registerAck = data;
      await readyHandler({
        registerAck,
        callHostAction,
        callTool,
        emitStateChange,
        emitLog,
        getState: () => cloneState(businessState),
        setState: (nextState, nextLifecycle) => setBusinessState(nextState, nextLifecycle),
      });
      emitReady();
      return;
    }
    if (data.type === "workspace_state_change") {
      await eventHandler({ type: "workspace_state_change", payload: data.workspace_state || {} });
      return;
    }
    if (data.type === "dispatch_event") {
      await eventHandler({ type: "dispatch_event", payload: data });
      return;
    }
    if (data.type === "host_action_result") {
      const requestID = typeof data.request_id === "string" ? data.request_id : "";
      const waiter = hostWaiters.get(requestID);
      if (!waiter) return;
      clearTimeout(waiter.timer);
      hostWaiters.delete(requestID);
      if (data.ok === false) {
        waiter.reject(new Error(data.error && data.error.message ? data.error.message : "host action failed"));
      } else {
        waiter.resolve(data.result == null ? {} : data.result);
      }
      return;
    }
    if (data.type === "action_call") {
      const action = asObject(data.action, {});
      try {
        const result = await actionHandler({
          action,
          registerAck,
          connectPayload,
          callHostAction,
          callTool,
          getState: () => cloneState(businessState),
          setState: (nextState, nextLifecycle) => setBusinessState(nextState, nextLifecycle),
          emitStateChange,
          emitLog,
        });
        emitActionResult(action, {
          ok: true,
          result: result == null ? {} : result,
        });
      } catch (error) {
        emitActionResult(action, {
          ok: false,
          error: error && error.message ? error.message : String(error),
        });
      }
    }
  }

  function handleWindowMessage(event) {
    const data = asObject(event.data, null);
    if (!data || data.type !== "surface_connect" || !event.ports || !event.ports[0]) {
      return;
    }
    connectPayload = data;
    port = event.ports[0];
    port.onmessage = (messageEvent) => {
      handlePortMessage(messageEvent.data).catch((error) => {
        try {
          emitLog("error", error && error.message ? error.message : String(error));
        } catch (_) {}
      });
    };
    port.start();
    Promise.resolve(connectHandler({
      connectPayload,
      callHostAction,
      callTool,
      emitLog,
      setState: (nextState, nextLifecycle) => setBusinessState(nextState, nextLifecycle),
    })).then(() => {
      emitRegister();
    }).catch((error) => {
      emitLog("error", error && error.message ? error.message : String(error));
      emitRegister();
    });
  }

  window.addEventListener("message", handleWindowMessage);

  return {
    callHostAction,
    callTool,
    destroy() {
      window.removeEventListener("message", handleWindowMessage);
      hostWaiters.forEach((waiter) => {
        clearTimeout(waiter.timer);
        waiter.reject(new Error("surface runtime destroyed"));
      });
      hostWaiters.clear();
      if (port) {
        try {
          port.close();
        } catch (_) {}
      }
      port = null;
    },
    emitActionResult,
    emitLog,
    emitReady,
    emitStateChange,
    getRegisterAck() {
      return registerAck;
    },
    getState() {
      return cloneState(businessState);
    },
    setState(nextState, nextLifecycleStatus = lifecycleStatus) {
      setBusinessState(nextState, nextLifecycleStatus);
    },
  };
}
