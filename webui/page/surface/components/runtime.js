import { createPageSurfaceHost } from "../../../lib/pageSurfaceTool.js";
import { EMPTY_LOG_TEXT } from "./constants.js";
import { escapeAttribute, redactSessionToken } from "./formatters.js";

function appendLogText(el, label, payload) {
  const line = `[${new Date().toLocaleTimeString()}] ${label}\n${JSON.stringify(payload, null, 2)}`;
  el.textContent = el.textContent === EMPTY_LOG_TEXT ? line : `${line}\n\n${el.textContent}`;
}

export function createRuntimeController({
  els,
  state,
  callTool,
  renderSurfaceSelect,
  setActions,
}) {
  let toastTimer = null;

  const host = createPageSurfaceHost({
    callTool,
    pageID: "surface_loader",
    pageType: "host",
    hostActions: [
      {
        name: "host.flash",
        description: "在宿主状态栏显示提示信息",
        handler: async ({ args, runtime }) => {
          const message = typeof args.message === "string" ? args.message : "(empty)";
          showToast(message, "ok");
          appendLog("host.flash", { surface_id: runtime.surfaceID, message });
          return { delivered: true };
        },
      },
      {
        name: "workspace.update",
        description: "请求宿主更新工作区状态",
        handler: async ({ args, runtime, updateWorkspaceState }) => {
          const nextState = updateWorkspaceState(args || {});
          return { workspace_state: nextState };
        },
      },
    ],
    onRuntimeEvent: (event) => {
      if (!event || !event.runtime) return;
      const runtime = event.runtime;
      const snapshot = host.getRuntimeSnapshot(runtime.surfaceID);
      if (snapshot && state.entry && state.entry.surface_id === runtime.surfaceID) {
        state.lastSurfaceState = snapshot.state || null;
        if (event.type === "surface_register" || event.type === "surface_ready") {
          setActions(snapshot.actions || []);
          renderHostActions(snapshot.host_actions || []);
        }
        if (event.type === "surface_register") {
          setActionControlsEnabled({ interactive: (snapshot.actions || []).length > 0, dispatch: false });
          setStatus("已注册，等待就绪", "warn", { toast: false });
        } else if (event.type === "surface_ready") {
          setActionControlsEnabled({ interactive: (snapshot.actions || []).length > 0, dispatch: (snapshot.actions || []).length > 0 });
          setStatus("Ready", "ok", { toast: false });
        } else if (event.type === "surface_closed") {
          setActionControlsEnabled({ interactive: false, dispatch: false });
          setStatus("已关闭", "", { toast: false });
        } else if (event.type === "state_change") {
          const lifecycle = snapshot?.state?.lifecycle_status || "";
          if (lifecycle) {
            const cls = lifecycle === "ready" ? "ok" : (lifecycle === "error" ? "err" : "warn");
            setStatus(lifecycle, cls, { toast: false });
          }
        }
      }
      appendLog(event.type, event.payload || {});
    },
    onError: (error) => {
      const message = error && error.message ? error.message : String(error);
      setStatus("Error", "err", { toast: false });
      showToast(message, "err");
      appendLog("host_error", { message });
    },
  });
  state.surfaceHost = host;

  function showToast(text, cls = "") {
    if (!els.toastViewport) return;
    const message = String(text || "").trim();
    if (!message) return;
    const durationMS = message.length > 48 || message.includes("\n") ? 5000 : 3000;
    if (toastTimer) {
      clearTimeout(toastTimer);
      toastTimer = null;
    }
    const item = document.createElement("div");
    item.className = `toast toast-${cls || "info"}`.trim();
    item.textContent = message;
    els.toastViewport.replaceChildren(item);
    requestAnimationFrame(() => item.classList.add("is-visible"));
    toastTimer = setTimeout(() => {
      item.classList.remove("is-visible");
      toastTimer = setTimeout(() => {
        if (els.toastViewport.firstChild === item) {
          els.toastViewport.replaceChildren();
        }
      }, 220);
    }, durationMS);
  }

  function setStatus(text, cls = "", options = {}) {
    const message = String(text || "").trim() || "-";
    if (els.previewStatus) {
      els.previewStatus.textContent = message;
      els.previewStatus.className = `panel-status panel-status-${cls || "idle"}`.trim();
    }
    if (options.toast !== false) {
      showToast(message, cls);
    }
  }

  function appendLog(label, payload) {
    appendLogText(els.eventLog, label, payload);
  }

  function setActionControlsEnabled(options = {}) {
    const interactive = !!options.interactive;
    const dispatch = !!options.dispatch;
    if (els.actionSelect) els.actionSelect.disabled = !interactive;
    if (els.actionEditor) els.actionEditor.disabled = !interactive;
    if (els.dispatchBtn) els.dispatchBtn.disabled = !dispatch;
  }

  function renderSessionToken(token) {
    const text = String(token || "").trim();
    if (!text) {
      els.sessionMeta.innerHTML = '<span class="mono">-</span>';
      return;
    }
    const shortText = redactSessionToken(text);
    els.sessionMeta.innerHTML = `<span class="mono" title="${escapeAttribute(text)}">${escapeAttribute(shortText)}</span>`;
  }

  function renderHostActions(items) {
    const actions = Array.isArray(items) ? items : [];
    if (!actions.length) {
      els.capabilitiesList.innerHTML = '<span class="hint">当前宿主未暴露 host actions</span>';
      return;
    }
    els.capabilitiesList.innerHTML = actions
      .map((item) => `<span class="pill" title="${escapeAttribute(item.description || "")}">${escapeAttribute(item.name || "")}</span>`)
      .join("");
  }

  function previewWorkspaceState() {
    const previewPanel = state.windowManager?.snapshotLayouts?.().preview || {};
    return {
      open: true,
      focused: true,
      frozen: false,
      minimized: !!previewPanel.collapsed,
      maximized: false,
      geometry: {
        x: Number(previewPanel.left || 0),
        y: Number(previewPanel.top || 0),
        width: Number(previewPanel.width || 0),
        height: Number(previewPanel.height || 0),
      },
      z_index: Number(previewPanel.z_index || 0),
    };
  }

  async function loadSurface(surfaceID) {
    const targetSurfaceID = String(surfaceID || "").trim();
    if (!targetSurfaceID) {
      throw new Error("请先选择要加载的 Surface");
    }
    setActionControlsEnabled({ interactive: false, dispatch: false });
    setStatus("加载中", "warn", { toast: false });
    setActions([]);
    if (state.entry && state.entry.surface_id && state.entry.surface_id !== targetSurfaceID) {
      host.closeSurface(state.entry.surface_id, "switch_surface");
    }
    const runtime = await host.openSurface({
      surfaceID: targetSurfaceID,
      iframe: els.surfaceFrame,
      workspaceState: previewWorkspaceState(),
      cacheBust: true,
    });
    state.entry = runtime.entry;
    renderSurfaceSelect(runtime.entry.surface_id);
    els.surfaceMeta.textContent = `${runtime.entry.name || runtime.entry.surface_id}`;
    els.entryMeta.textContent = runtime.entry.entry_url || "-";
    renderSessionToken(runtime.sessionToken);
    renderHostActions(host.getRuntimeSnapshot(runtime.surfaceID)?.host_actions || []);
    setStatus("等待 Surface Ready", "warn", { toast: false });
    await host.waitUntilSurfaceReady(runtime.surfaceID, 8000);
    const actions = host.getRuntimeSnapshot(runtime.surfaceID)?.actions || [];
    setActionControlsEnabled({ interactive: actions.length > 0, dispatch: actions.length > 0 });
    setStatus("Ready", "ok", { toast: false });
  }

  async function dispatchAction() {
    if (!state.entry) throw new Error("Surface 尚未连接");
    const payload = JSON.parse(els.actionEditor.value || "{}");
    const actionName = typeof payload.name === "string" && payload.name.trim()
      ? payload.name.trim()
      : String(els.actionSelect.value || "").trim();
    if (!actionName) {
      throw new Error("action.name 不能为空");
    }
    const result = await host.callSurfaceAction(
      state.entry.surface_id,
      actionName,
      payload.args && typeof payload.args === "object" ? payload.args : {},
      {
        actionID: typeof payload.id === "string" ? payload.id : undefined,
        timeoutMS: Number.isFinite(payload.timeout_ms) ? payload.timeout_ms : undefined,
      },
    );
    appendLog("action_result", result);
  }

  async function loadRuntime(silent = false) {
    if (!state.entry) return;
    const result = await callTool("ui.surface.runtime_status", { surface_id: state.entry.surface_id });
    const snapshot = host.getRuntimeSnapshot(state.entry.surface_id);
    const merged = {
      ...result,
      runtime_protocol: snapshot,
    };
    if (els.runtimeStatus) {
      els.runtimeStatus.textContent = JSON.stringify(merged, null, 2);
    }
    if (!silent) {
      appendLog("runtime_status", { surface_id: state.entry.surface_id });
    }
  }

  async function loadLogs() {
    if (!state.entry) return;
    const result = await callTool("ui.surface.logs_query", { surface_id: state.entry.surface_id, limit: 60 });
    appendLog("logs_query", result);
  }

  function reloadIframe() {
    if (!state.entry || !state.entry.surface_id) return Promise.resolve();
    return loadSurface(state.entry.surface_id);
  }

  function syncWorkspaceStateFromLayout() {
    if (!state.entry || !state.surfaceHost) return;
    state.surfaceHost.updateWorkspaceState(state.entry.surface_id, previewWorkspaceState());
  }

  function reportError(error) {
    const message = error && error.message ? error.message : String(error);
    setStatus("Error", "err", { toast: false });
    showToast(message, "err");
    appendLog("ui_error", { message });
  }

  setActionControlsEnabled({ interactive: false, dispatch: false });
  setStatus("未加载", "", { toast: false });

  return {
    appendLog,
    dispatchAction,
    loadLogs,
    loadRuntime,
    loadSurface,
    reloadIframe,
    reportError,
    renderSessionToken,
    setStatus,
    showToast,
    syncWorkspaceStateFromLayout,
  };
}
