import { EMPTY_LOG_TEXT } from "./constants.js";
import { escapeAttribute, redactSessionToken } from "./formatters.js";
import { createSurfaceBridge } from "../lib/bridge.js";

export function createRuntimeController({
  els,
  state,
  callTool,
  renderSurfaceSelect,
  setActions,
}) {
  const bridge = createSurfaceBridge({
    callTool,
    onFlash: ({ runtime, message }) => {
      appendLog("host.flash", { surface_id: runtime.surfaceID, message });
      setStatus(`[${runtime.surfaceID}] ${message}`, "ok");
    },
    onError: (err) => {
      setStatus(err && err.message ? err.message : String(err), "err");
    },
  });

  function setStatus(text, cls = "") {
    els.statusBadge.textContent = text;
    els.statusBadge.className = `badge ${cls}`.trim();
  }

  function appendLog(label, payload) {
    const line = `[${new Date().toLocaleTimeString()}] ${label}\n${JSON.stringify(payload, null, 2)}`;
    els.eventLog.textContent = els.eventLog.textContent === EMPTY_LOG_TEXT ? line : `${line}\n\n${els.eventLog.textContent}`;
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

  function connectChannel(entry) {
    if (state.port) {
      try {
        state.port.close();
      } catch (_) {}
    }
    const channel = new MessageChannel();
    state.port = channel.port1;
    state.runtimeBridge = {
      surfaceID: entry.surface_id,
      surfaceType: entry.surface_type || "app",
      surfaceVersion: entry.surface_version || entry.version || "1.0",
      sessionToken: state.sessionToken,
      port: state.port,
      capabilityCache: new Map(),
    };
    state.port.onmessage = (ev) => {
      const msg = ev.data || {};
      bridge.handleBridgeMessage(state.runtimeBridge, msg).catch((err) => {
        setStatus(err && err.message ? err.message : String(err), "err");
      });
      if (msg.type === "surfacefs_request" || msg.type === "host_call") {
        return;
      }
      if (msg.type === "surface_ready" || msg.type === "surface_actions") {
        setActions(msg.actions || []);
      }
      if (msg.type === "state_change" || msg.type === "surface_ready") {
        state.lastSurfaceState = msg;
        if (els.tabsNav) {
          const statusTab = els.tabsNav.querySelector('[data-tab="status"]');
          if (statusTab && statusTab.classList.contains("active")) {
             loadRuntime(true).catch(() => {});
          }
        }
      }
      appendLog(msg.type || "event", msg);
    };
    state.port.start();
    els.surfaceFrame.contentWindow.postMessage(
      {
        type: "surface_connect",
        surface_id: entry.surface_id,
        surface_type: entry.surface_type || "app",
        surface_version: entry.surface_version || entry.version || "1.0",
        session_token: state.sessionToken,
      },
      "*",
      [channel.port2],
    );
  }

  function renderCapabilities(entry) {
    if (!els.capabilitiesList) return;
    const caps = entry.allowed_host_calls || [];
    if (!caps.length) {
      els.capabilitiesList.innerHTML = '<span class="hint">此 Surface 无特殊权限要求</span>';
      return;
    }
    const html = caps.map(cap => `<span class="pill">${escapeAttribute(cap)}</span>`).join("");
    els.capabilitiesList.innerHTML = html;
  }

  async function loadSurface(surfaceID) {
    if (!surfaceID) {
      throw new Error("请先选择要加载的 Surface");
    }
    setStatus("加载中");
    setActions([]);
    const entry = await callTool("ui.surface.get", { surface_id: surfaceID });
    const session = await callTool("ui.surface.session_issue", { surface_id: surfaceID });
    state.entry = entry;
    state.sessionToken = session.surface_session_token || "";
    renderSurfaceSelect(entry.surface_id);
    els.surfaceMeta.textContent = `${entry.name || entry.surface_id}`;
    els.entryMeta.textContent = entry.entry_url || "-";
    renderSessionToken(state.sessionToken);
    renderCapabilities(entry);
    els.surfaceFrame.onload = () => connectChannel(entry);
    els.surfaceFrame.src = entry.entry_url;
    setStatus("已就绪", "ok");
  }

  async function dispatchAction() {
    if (!state.port || !state.entry) throw new Error("Surface 尚未连接");
    const action = JSON.parse(els.actionEditor.value || "{}");
    state.port.postMessage({ type: "action_call", action });
    appendLog("action_call", action);
  }

  async function loadRuntime(silent = false) {
    if (!state.entry) return;
    const result = await callTool("ui.surface.runtime_status", { surface_id: state.entry.surface_id });
    
    if (state.lastSurfaceState) {
      result.last_surface_state = state.lastSurfaceState;
    }

    if (els.runtimeStatus) {
      els.runtimeStatus.textContent = JSON.stringify(result, null, 2);
    }
    if (!silent) {
      appendLog("runtime_status", { surface_id: state.entry.surface_id, note: "已同步至状态页" });
    }
  }

  async function loadLogs() {
    if (!state.entry) return;
    const result = await callTool("ui.surface.logs_query", { surface_id: state.entry.surface_id, limit: 60 });
    appendLog("logs_query", result);
  }

  function reloadIframe() {
    if (els.surfaceFrame && els.surfaceFrame.src && els.surfaceFrame.src !== "about:blank") {
       els.surfaceFrame.contentWindow.location.reload();
    }
  }

  return {
    appendLog,
    dispatchAction,
    loadLogs,
    loadRuntime,
    loadSurface,
    renderSessionToken,
    setStatus,
    reloadIframe,
  };
}
