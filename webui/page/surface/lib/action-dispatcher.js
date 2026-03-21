import { createID, nowISO } from "./utils.js";

export const GLOBAL_ACTION_DESCRIPTORS = [
  { name: "ui.toast", description: "在主界面显示提示消息" },
  { name: "surface.send_event", description: "向指定 Surface 发送业务事件" },
  { name: "surface.freeze", description: "冻结指定 Surface 消息通道" },
  { name: "surface.unfreeze", description: "恢复指定 Surface 消息通道" },
  { name: "surface.reload", description: "重载指定 Surface iframe" },
  { name: "records.query", description: "查询 Action 执行记录" },
];

function normalizeActionCall(rawCall) {
  if (!rawCall || typeof rawCall !== "object") {
    throw new Error("action 必须是 object");
  }
  const id = typeof rawCall.id === "string" && rawCall.id.trim() ? rawCall.id.trim() : createID("ac");
  const name = typeof rawCall.name === "string" ? rawCall.name.trim() : "";
  if (!name) throw new Error("action.name 不能为空");
  const args = rawCall.args && typeof rawCall.args === "object" ? rawCall.args : {};
  const timeoutS = Number.isFinite(rawCall.timeout_s) ? rawCall.timeout_s : null;
  return { id, name, args, timeout_s: timeoutS };
}

function stringifyShort(value) {
  try {
    const text = JSON.stringify(value);
    if (text.length > 220) return `${text.slice(0, 220)}...`;
    return text;
  } catch (_) {
    return String(value);
  }
}

function getSurfaceIDFromCall(call) {
  if (call.name === "surface.freeze" || call.name === "surface.unfreeze" || call.name === "surface.reload" || call.name === "surface.send_event") {
    return typeof call.args.surface_id === "string" ? call.args.surface_id : "";
  }
  if (call.name.startsWith("surface.call.")) {
    const parts = call.name.split(".");
    return parts.length >= 4 ? parts[2] : "";
  }
  return "";
}

function withTimeout(promise, timeoutS) {
  if (!Number.isFinite(timeoutS) || timeoutS <= 0) {
    return promise;
  }
  const ms = Math.min(60000, Math.max(100, timeoutS * 1000));
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      setTimeout(() => reject(new Error(`action timeout after ${ms}ms`)), ms);
    }),
  ]);
}

function validateArgs(call) {
  let argsText = "";
  try {
    argsText = JSON.stringify(call.args || {});
  } catch (_) {
    throw new Error("action.args 必须可序列化");
  }
  if (argsText.length > 32768) {
    throw new Error("action.args 过大（>32KB）");
  }
  if (call.timeout_s != null && (!Number.isFinite(call.timeout_s) || call.timeout_s <= 0 || call.timeout_s > 60)) {
    throw new Error("timeout_s 必须在 (0,60] 范围");
  }
}

export function createActionDispatcher(options) {
  const surfaceManager = options.surfaceManager;
  const recordStore = options.recordStore;
  const notify = typeof options.notify === "function" ? options.notify : () => {};
  const getAllowedActionNames = typeof options.getAllowedActionNames === "function"
    ? options.getAllowedActionNames
    : () => new Set();
  const surfaceSerial = new Map();

  function runWithSurfaceSerial(surfaceID, taskFn) {
    if (!surfaceID) {
      return taskFn();
    }
    const prev = surfaceSerial.get(surfaceID) || Promise.resolve();
    const next = prev.catch(() => {}).then(taskFn);
    const cleanup = next.finally(() => {
      if (surfaceSerial.get(surfaceID) === cleanup) {
        surfaceSerial.delete(surfaceID);
      }
    });
    surfaceSerial.set(surfaceID, cleanup);
    return next;
  }

  async function runAction(call) {
    if (call.name === "ui.toast") {
      const message = typeof call.args.message === "string" ? call.args.message : "toast";
      notify(message);
      return { shown: true, message };
    }
    if (call.name === "surface.freeze") {
      const surfaceID = typeof call.args.surface_id === "string" ? call.args.surface_id : "";
      if (!surfaceID) throw new Error("surface_id 不能为空");
      const ok = surfaceManager.setFrozen(surfaceID, true);
      if (!ok) throw new Error(`surface 不存在: ${surfaceID}`);
      return { frozen: true, surface_id: surfaceID };
    }
    if (call.name === "surface.unfreeze") {
      const surfaceID = typeof call.args.surface_id === "string" ? call.args.surface_id : "";
      if (!surfaceID) throw new Error("surface_id 不能为空");
      const ok = surfaceManager.setFrozen(surfaceID, false);
      if (!ok) throw new Error(`surface 不存在: ${surfaceID}`);
      return { frozen: false, surface_id: surfaceID };
    }
    if (call.name === "surface.reload") {
      const surfaceID = typeof call.args.surface_id === "string" ? call.args.surface_id : "";
      if (!surfaceID) throw new Error("surface_id 不能为空");
      const ok = surfaceManager.reloadSurface(surfaceID);
      if (!ok) throw new Error(`surface 不存在: ${surfaceID}`);
      return { reloaded: true, surface_id: surfaceID };
    }
    if (call.name === "surface.send_event") {
      const surfaceID = typeof call.args.surface_id === "string" ? call.args.surface_id : "";
      const eventName = typeof call.args.event === "string" ? call.args.event : "";
      if (!surfaceID || !eventName) throw new Error("surface_id/event 不能为空");
      const payload = call.args.payload && typeof call.args.payload === "object" ? call.args.payload : {};
      const ok = surfaceManager.sendToSurface(surfaceID, {
        type: "dispatch_event",
        event: eventName,
        payload,
        request_id: call.id,
      });
      if (!ok) throw new Error(`send_event 失败: surface=${surfaceID}（可能冻结或未就绪）`);
      return { delivered: true, surface_id: surfaceID, event: eventName };
    }
    if (call.name === "records.query") {
      const rows = recordStore.query(call.args || {});
      return { rows, count: rows.length };
    }

    if (call.name.startsWith("surface.call.")) {
      const parts = call.name.split(".");
      if (parts.length < 4) {
        throw new Error("surface.call 命名格式错误");
      }
      const surfaceID = parts[2];
      const actionName = parts.slice(3).join(".");
      if (!surfaceID || !actionName) {
        throw new Error("surface.call 命名格式错误");
      }
      const ok = surfaceManager.sendToSurface(surfaceID, {
        type: "action_call",
        action: {
          id: call.id,
          name: actionName,
          args: call.args || {},
          timeout_s: call.timeout_s,
        },
      });
      if (!ok) {
        throw new Error(`surface.call 失败: surface=${surfaceID}（可能冻结或未就绪）`);
      }
      return { dispatched: true, surface_id: surfaceID, action: actionName };
    }

    throw new Error(`未知 action: ${call.name}`);
  }

  async function execute(rawCall, meta) {
    const metaInfo = meta && typeof meta === "object" ? meta : {};
    const call = normalizeActionCall(rawCall);
    validateArgs(call);
    const allowedNames = getAllowedActionNames();
    const surfaceID = getSurfaceIDFromCall(call);

    const started = Date.now();
    const baseRecord = {
      record_id: createID("record"),
      message_id: metaInfo.messageId || "",
      action_id: call.id,
      action_name: call.name,
      args_json: call.args,
      ts_start: nowISO(),
      duration_ms: 0,
      status: "fail",
      result_json: null,
      error: "",
    };

    try {
      if (!allowedNames.has(call.name)) {
        throw new Error(`action 不在 allowed_actions: ${call.name}`);
      }
      const result = await runWithSurfaceSerial(
        surfaceID,
        () => withTimeout(runAction(call), call.timeout_s),
      );
      const ended = Date.now();
      const record = {
        ...baseRecord,
        status: "ok",
        ts_end: nowISO(),
        duration_ms: ended - started,
        result_json: result,
      };
      recordStore.add(record);
      return {
        ok: true,
        record,
        result,
        observation: `action=${record.action_name} status=ok duration_ms=${record.duration_ms} record_id=${record.record_id}`,
      };
    } catch (err) {
      const ended = Date.now();
      const message = err && err.message ? err.message : String(err);
      const record = {
        ...baseRecord,
        status: "fail",
        ts_end: nowISO(),
        duration_ms: ended - started,
        result_json: null,
        error: message,
      };
      recordStore.add(record);
      return {
        ok: false,
        record,
        error: message,
        observation: `action=${record.action_name} status=fail error=${stringifyShort(message)} record_id=${record.record_id}`,
      };
    }
  }

  return {
    execute,
  };
}
