import { createPageSurfaceHost } from "../../lib/pageSurfaceTool.js";
import { callTool } from "./tool-call.js";

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

function escapeHTML(text) {
  return String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
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
  if (lower.startsWith("surface.action.")) return `surface.call.${name.slice("surface.action.".length)}`;
  if (lower.startsWith("tool.call.")) return name;
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

function parseSurfaceCallName(name) {
  const parts = String(name || "").split(".");
  if (parts.length < 4) return null;
  if (parts[0] !== "surface" || parts[1] !== "call") return null;
  return {
    surfaceID: parts[2],
    actionName: parts.slice(3).join("."),
  };
}

function parseToolCallName(name) {
  const text = String(name || "").trim();
  if (!text.toLowerCase().startsWith("tool.call.")) return "";
  return text.slice("tool.call.".length).trim();
}

export function createSurfaceBridge(options) {
  const root = options.root;
  const appendDebug = typeof options.appendDebug === "function" ? options.appendDebug : () => {};
  const appendSystem = typeof options.appendSystem === "function" ? options.appendSystem : () => {};
  const onSurfaceEvent = typeof options.onSurfaceEvent === "function" ? options.onSurfaceEvent : () => {};
  const reportActionRecord = typeof options.reportActionRecord === "function" ? options.reportActionRecord : () => {};

  const registry = new Map();
  const runtimeViews = new Map();
  const host = createPageSurfaceHost({
    callTool,
    pageID: "chat",
    pageType: "host",
    hostActions: [
      {
        name: "host.flash",
        description: "在 chat 面板输出系统提示",
        handler: async ({ args, runtime }) => {
          const message = typeof args.message === "string" ? args.message : "(empty)";
          appendSystem(`[surface:${runtime.surfaceID}] ${message}`);
          return { delivered: true };
        },
      },
    ],
    onRuntimeEvent: (event) => {
      if (!event || !event.runtime) return;
      const snapshot = host.getRuntimeSnapshot(event.runtime.surfaceID);
      if (snapshot) {
        onSurfaceEvent({
          type: event.type,
          surface_id: snapshot.surface_id,
          payload: snapshot.state || {},
        });
      }
      renderRegistry();
    },
    onError: (error) => {
      appendDebug("ERROR", "SurfaceBridge", null, null, error && error.message ? error.message : String(error));
    },
  });

  let panel = null;
  let statusEl = null;
  let listEl = null;
  let workspaceEl = null;
  let visible = false;

  function setStatus(text) {
    if (statusEl) statusEl.textContent = text;
  }

  function ensurePanel() {
    if (panel) return;
    panel = createPanel(root);
    statusEl = panel.querySelector(".surface-float-status");
    listEl = panel.querySelector(".surface-manager-list");
    workspaceEl = panel.querySelector(".surface-manager-workspace");
    panel.querySelector('[data-act="refresh"]').addEventListener("click", () => {
      refreshRegistry().catch((error) => appendSystem(error.message || String(error)));
    });
    panel.querySelector('[data-act="close"]').addEventListener("click", () => {
      setVisible(false);
    });
  }

  function availableItems() {
    return Array.from(registry.values()).filter((item) => item.enabled && item.status === "ok");
  }

  function findSurfaceByTarget(target) {
    const key = String(target || "").trim();
    if (!key) return null;
    if (registry.has(key)) return registry.get(key);
    const lower = key.toLowerCase();
    return availableItems().find((item) => String(item.name || "").toLowerCase() === lower) || null;
  }

  function runtimeSnapshot(surfaceID) {
    return host.getRuntimeSnapshot(surfaceID);
  }

  function snapshotSurfaceDescriptor(surfaceID) {
    const item = registry.get(surfaceID);
    const runtime = runtimeSnapshot(surfaceID);
    return {
      surface_id: surfaceID,
      surface_type: item && item.surface_type ? item.surface_type : "app",
      surface_version: item && item.version ? item.version : "1",
      name: item && item.name ? item.name : surfaceID,
      desc: item && item.desc ? item.desc : "",
      status: item ? item.status : "unknown",
      enabled: !!(item && item.enabled),
      available: !!(item && item.enabled && item.status === "ok"),
      visible: !!runtime,
      ready: !!(runtime && runtime.ready),
      entry_url: item && item.entry_url ? item.entry_url : "",
      actions: runtime ? runtime.actions : [],
      business_state: runtime && runtime.state ? runtime.state.business_state || {} : {},
      visible_text: runtime && runtime.state ? runtime.state.visible_text || "" : "",
      state_version: runtime && runtime.state ? runtime.state.state_version || 0 : 0,
    };
  }

  function renderRegistry() {
    if (!listEl) return;
    const rows = Array.from(registry.values()).map((item) => {
      const opened = !!runtimeSnapshot(item.surface_id);
      return `
        <div class="surface-manager-item" data-surface-id="${escapeHTML(item.surface_id)}">
          <div class="surface-manager-meta">
            <div><strong>${escapeHTML(item.name || item.surface_id)}</strong></div>
            <div>${escapeHTML(item.surface_id)}</div>
            <div>${escapeHTML(item.surface_type)} / v${escapeHTML(item.version || "1")}</div>
            <div>${escapeHTML(item.status || "unknown")}</div>
          </div>
          <div class="surface-manager-actions">
            <label>
              <input type="checkbox" data-act="enable" ${item.enabled ? "checked" : ""} ${item.status === "ok" ? "" : "disabled"} />
              启用
            </label>
            <button type="button" data-act="${opened ? "close_surface" : "open_surface"}" ${item.enabled && item.status === "ok" ? "" : "disabled"}>
              ${opened ? "关闭" : "打开"}
            </button>
          </div>
        </div>
      `;
    });
    listEl.innerHTML = rows.length ? rows.join("") : `<div class="surface-manager-empty">暂无 surface 包</div>`;

    listEl.querySelectorAll("[data-act='enable']").forEach((inputEl) => {
      inputEl.addEventListener("change", async (event) => {
        const itemEl = event.target.closest(".surface-manager-item");
        if (!itemEl) return;
        const surfaceID = itemEl.getAttribute("data-surface-id");
        const enabled = !!event.target.checked;
        try {
          await callTool("ui.surface.enable_set", { surface_id: surfaceID, enabled });
          const item = registry.get(surfaceID);
          if (item) item.enabled = enabled;
          renderRegistry();
        } catch (error) {
          event.target.checked = !enabled;
          appendSystem(error.message || String(error));
        }
      });
    });

    listEl.querySelectorAll("[data-act='open_surface']").forEach((button) => {
      button.addEventListener("click", async (event) => {
        const itemEl = event.target.closest(".surface-manager-item");
        if (!itemEl) return;
        try {
          await ensureSurfaceOpen(itemEl.getAttribute("data-surface-id"));
          renderRegistry();
        } catch (error) {
          appendSystem(error.message || String(error));
        }
      });
    });

    listEl.querySelectorAll("[data-act='close_surface']").forEach((button) => {
      button.addEventListener("click", (event) => {
        const itemEl = event.target.closest(".surface-manager-item");
        if (!itemEl) return;
        closeSurface(itemEl.getAttribute("data-surface-id"), "manual");
      });
    });
  }

  function createRuntimeView(item) {
    const hostEl = document.createElement("div");
    hostEl.className = "surface-runtime-host";
    hostEl.setAttribute("data-surface-id", item.surface_id);
    hostEl.innerHTML = `<div class="surface-runtime-title">${escapeHTML(item.name || item.surface_id)}</div>`;
    const iframe = document.createElement("iframe");
    iframe.className = "surface-float-iframe";
    iframe.setAttribute("sandbox", "allow-scripts allow-downloads");
    hostEl.appendChild(iframe);
    workspaceEl.appendChild(hostEl);
    runtimeViews.set(item.surface_id, { hostEl, iframe });
    return iframe;
  }

  async function refreshRegistry() {
    const result = await host.refreshCatalog();
    const items = Array.isArray(result && result.items) ? result.items : [];
    registry.clear();
    items.forEach((item) => {
      if (!item || typeof item !== "object") return;
      registry.set(item.surface_id, item);
    });
    setStatus(`surfaces=${registry.size}`);
    renderRegistry();
    return items;
  }

  async function ensureSurfaceOpen(surfaceID) {
    const item = registry.get(String(surfaceID || "").trim());
    if (!item) throw new Error(`surface not found: ${surfaceID}`);
    if (runtimeSnapshot(item.surface_id)) return runtimeSnapshot(item.surface_id);
    const iframe = createRuntimeView(item);
    const runtime = await host.openSurface({
      surfaceID: item.surface_id,
      iframe,
      workspaceState: {
        open: true,
        focused: true,
        frozen: false,
        minimized: false,
        maximized: false,
        geometry: { x: 0, y: 0, width: 0, height: 0 },
        z_index: 0,
      },
    });
    return runtime;
  }

  function closeSurface(surfaceID, reason = "closed") {
    const sid = String(surfaceID || "").trim();
    const closed = host.closeSurface(sid, reason);
    const view = runtimeViews.get(sid);
    if (view && view.hostEl.parentElement) {
      view.hostEl.parentElement.removeChild(view.hostEl);
    }
    runtimeViews.delete(sid);
    renderRegistry();
    return closed;
  }

  function setVisible(nextVisible) {
    ensurePanel();
    visible = !!nextVisible;
    panel.classList.toggle("open", visible);
    if (visible) {
      refreshRegistry().catch((error) => appendSystem(error.message || String(error)));
    }
  }

  async function dispatchAction(rawAction) {
    const action = toActionPayload(rawAction);
    if (!action) return { ok: false, reason: "invalid_action" };
    if (!registry.size) {
      await refreshRegistry();
    }
    if (action.name === "get_surfaces") {
      const surfaces = availableItems().map((item) => snapshotSurfaceDescriptor(item.surface_id));
      return { ok: true, result: { total: surfaces.length, surfaces } };
    }
    if (action.name === "open_surface") {
      const target = action.args.target || action.args.surface_id || "";
      const item = findSurfaceByTarget(target);
      if (!item) return { ok: false, reason: `surface_not_found:${target}` };
      await ensureSurfaceOpen(item.surface_id);
      return { ok: true, result: { opened: true, surface: snapshotSurfaceDescriptor(item.surface_id) } };
    }
    if (action.name === "close_surface") {
      const target = action.args.target || action.args.surface_id || "";
      const item = findSurfaceByTarget(target);
      if (!item) return { ok: false, reason: `surface_not_found:${target}` };
      closeSurface(item.surface_id, "ai_action");
      return { ok: true, result: { closed: true, surface_id: item.surface_id } };
    }
    if (action.name === "surface.get_state") {
      const target = action.args.target || action.args.surface_id || "";
      const item = target ? findSurfaceByTarget(target) : availableItems()[0];
      if (!item) return { ok: false, reason: `surface_not_found:${target}` };
      const snapshot = runtimeSnapshot(item.surface_id);
      if (!snapshot) return { ok: false, reason: "surface_closed" };
      return {
        ok: true,
        result: {
          surface: snapshotSurfaceDescriptor(item.surface_id),
          state: snapshot.state || {},
        },
      };
    }
    if (action.name.startsWith("surface.call.")) {
      const parsed = parseSurfaceCallName(action.name);
      if (!parsed) return { ok: false, reason: "invalid_surface_call" };
      const runtime = runtimeSnapshot(parsed.surfaceID) || await ensureSurfaceOpen(parsed.surfaceID);
      if (!runtime) return { ok: false, reason: "surface_closed" };
      const result = await host.callSurfaceAction(parsed.surfaceID, parsed.actionName, action.args || {}, { actionID: action.id });
      reportActionRecord({
        turnId: 0,
        actionId: action.id,
        category: "surface_action",
        actionName: action.name,
        actionSurfaceID: parsed.surfaceID,
        status: result.status || "ok",
        args: action.args || {},
        result,
        state: result.business_state || {},
      });
      return { ok: true, result };
    }
    if (action.name.startsWith("tool.call.")) {
      const toolID = parseToolCallName(action.name);
      const result = await callTool(toolID, action.args || {});
      return { ok: true, result };
    }
    return { ok: false, reason: "unsupported_action" };
  }

  function toggleVisible() {
    setVisible(!visible);
  }

  function getCachedState(surfaceID) {
    const runtime = runtimeSnapshot(surfaceID);
    return runtime ? runtime.state || null : null;
  }

  function hasCapability(surfaceID, capability = "get_state") {
    const runtime = runtimeSnapshot(surfaceID);
    if (!runtime) return false;
    if (capability === "get_state") {
      return Array.isArray(runtime.actions) && runtime.actions.some((item) => item.name === "get_state");
    }
    return false;
  }

  return {
    dispatchAction,
    getCachedState,
    hasCapability,
    refreshRegistry,
    setVisible,
    toggleVisible,
  };
}
