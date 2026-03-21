function postToRuntime(runtime, payload) {
  if (!runtime || !runtime.port) {
    throw new Error("surface port is not ready");
  }
  runtime.port.postMessage(payload);
}

export function isBridgeMessageType(type) {
  return type === "surfacefs_request" || type === "host_call";
}

export function createSurfaceBridge({ callTool, onFlash, onHostCall, onError } = {}) {
  const callToolFn = typeof callTool === "function" ? callTool : null;
  const flashFn = typeof onFlash === "function" ? onFlash : () => {};
  const hostCallFn = typeof onHostCall === "function" ? onHostCall : () => {};
  const errorFn = typeof onError === "function" ? onError : () => {};

  async function ensureCapability(runtime, scope, pathPrefix = ".") {
    if (!callToolFn) {
      throw new Error("callTool is not configured");
    }
    if (!runtime.capabilityCache) {
      runtime.capabilityCache = new Map();
    }
    const key = `${scope}|${pathPrefix}`;
    const cached = runtime.capabilityCache.get(key);
    if (cached && Number.isFinite(cached.exp_ms) && cached.exp_ms - Date.now() > 1000) {
      return cached.token;
    }
    const payload = await callToolFn("ui.surface.capability_issue", {
      surface_session_token: runtime.sessionToken,
      scope,
      path_prefix: pathPrefix,
      ttl_seconds: 300,
    });
    const token = payload && typeof payload.capability_token === "string" ? payload.capability_token : "";
    if (!token) {
      throw new Error("surfacefs capability token is empty");
    }
    runtime.capabilityCache.set(key, {
      token,
      exp_ms: Number.isFinite(payload.exp_ms) ? payload.exp_ms : Date.now() + 4 * 60 * 1000,
    });
    return token;
  }

  async function handleSurfaceFSRequest(runtime, msg) {
    if (!callToolFn) {
      throw new Error("callTool is not configured");
    }
    const requestID = typeof msg.request_id === "string" ? msg.request_id : `fs-${Date.now()}`;
    const op = typeof msg.op === "string" ? msg.op : "";
    const relPath = typeof msg.path === "string" ? msg.path : ".";
    try {
      if (op === "read") {
        const capabilityToken = await ensureCapability(runtime, "fs.read", ".");
        const payload = await callToolFn("ui.surface.fs_read", {
          capability_token: capabilityToken,
          surface_id: runtime.surfaceID,
          path: relPath,
        });
        postToRuntime(runtime, { type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "write") {
        const capabilityToken = await ensureCapability(runtime, "fs.write", ".");
        const dataBase64 = typeof msg.data_base64 === "string" ? msg.data_base64 : "";
        const payload = await callToolFn("ui.surface.fs_write", {
          capability_token: capabilityToken,
          surface_id: runtime.surfaceID,
          path: relPath,
          data_base64: dataBase64,
        });
        postToRuntime(runtime, { type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "list") {
        const capabilityToken = await ensureCapability(runtime, "fs.list", ".");
        const payload = await callToolFn("ui.surface.fs_list", {
          capability_token: capabilityToken,
          surface_id: runtime.surfaceID,
          path: relPath,
        });
        postToRuntime(runtime, { type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "delete") {
        const capabilityToken = await ensureCapability(runtime, "fs.delete", ".");
        const payload = await callToolFn("ui.surface.fs_delete", {
          capability_token: capabilityToken,
          surface_id: runtime.surfaceID,
          path: relPath,
          recursive: !!msg.recursive,
        });
        postToRuntime(runtime, { type: "surfacefs_response", request_id: requestID, ok: true, payload });
        return;
      }
      if (op === "sign_static") {
        const capabilityToken = await ensureCapability(runtime, "fs.static", relPath);
        const payload = await callToolFn("ui.surface.fs_sign_static", {
          capability_token: capabilityToken,
          surface_id: runtime.surfaceID,
          path: relPath,
        });
        const signedURL = payload && typeof payload.url === "string" ? payload.url : "";
        if (!signedURL) {
          throw new Error("sign_static result url is empty");
        }
        postToRuntime(runtime, {
          type: "surfacefs_response",
          request_id: requestID,
          ok: true,
          payload: { url: signedURL, path: relPath },
        });
        return;
      }
      throw new Error(`unsupported surfacefs op: ${op}`);
    } catch (err) {
      postToRuntime(runtime, {
        type: "surfacefs_response",
        request_id: requestID,
        ok: false,
        error: err && err.message ? err.message : String(err),
      });
    }
  }

  function handleHostCall(runtime, msg) {
    const callID = typeof msg.call_id === "string" ? msg.call_id : `host-${Date.now()}`;
    const capability = typeof msg.capability === "string" ? msg.capability.trim() : "";
    const args = msg.args && typeof msg.args === "object" ? msg.args : {};
    let ok = true;
    let payload = {};
    try {
      if (capability === "flash") {
        const message = typeof args.message === "string" ? args.message : "(empty)";
        flashFn({ runtime, capability, args, message });
        payload = { delivered: true };
      } else if (capability === "chat" || capability === "tts" || capability === "asr" || capability === "isr") {
        payload = { accepted: false, reason: "not_implemented_yet" };
        ok = false;
      } else {
        payload = { accepted: false, reason: "unsupported_capability" };
        ok = false;
      }
    } catch (err) {
      ok = false;
      payload = { accepted: false, reason: err && err.message ? err.message : String(err) };
    }
    hostCallFn({ runtime, capability, args, payload, ok });
    postToRuntime(runtime, {
      type: "host_call_result",
      call_id: callID,
      capability,
      ok,
      payload,
    });
  }

  async function handleBridgeMessage(runtime, msg) {
    if (!msg || typeof msg !== "object") {
      return false;
    }
    if (msg.type === "surfacefs_request") {
      try {
        await handleSurfaceFSRequest(runtime, msg);
      } catch (err) {
        errorFn(err);
      }
      return true;
    }
    if (msg.type === "host_call") {
      try {
        handleHostCall(runtime, msg);
      } catch (err) {
        errorFn(err);
      }
      return true;
    }
    return false;
  }

  return {
    ensureCapability,
    handleBridgeMessage,
    handleHostCall,
    handleSurfaceFSRequest,
  };
}
