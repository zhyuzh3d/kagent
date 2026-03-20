import { EMPTY_LOG_TEXT } from "./constants.js";
import { escapeAttribute, redactSessionToken } from "./formatters.js";

export function createRuntimeController({
  els,
  state,
  callTool,
  renderSurfaceSelect,
  setActions,
}) {
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
    state.port.onmessage = (ev) => {
      const msg = ev.data || {};
      if (msg.type === "surface_ready" || msg.type === "surface_actions") {
        setActions(msg.actions || []);
      }
      appendLog(msg.type || "event", msg);
    };
    state.port.start();
    els.surfaceFrame.contentWindow.postMessage(
      {
        type: "surface_connect",
        surface_id: entry.surface_id,
        session_token: state.sessionToken,
      },
      "*",
      [channel.port2],
    );
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
    els.surfaceMeta.textContent = `${entry.name || entry.surface_id} / ${entry.surface_id}`;
    els.entryMeta.textContent = entry.entry_url || "-";
    renderSessionToken(state.sessionToken);
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

  async function loadRuntime() {
    if (!state.entry) return;
    const result = await callTool("ui.surface.runtime_status", { surface_id: state.entry.surface_id });
    appendLog("runtime_status", result);
  }

  async function loadLogs() {
    if (!state.entry) return;
    const result = await callTool("ui.surface.logs_query", { surface_id: state.entry.surface_id, limit: 60 });
    appendLog("logs_query", result);
  }

  return {
    appendLog,
    dispatchAction,
    loadLogs,
    loadRuntime,
    loadSurface,
    renderSessionToken,
    setStatus,
  };
}
