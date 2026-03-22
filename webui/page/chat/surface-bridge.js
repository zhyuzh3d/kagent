import { createPageSurfaceHost } from "../../lib/pageSurfaceTool.js";
import { buildPermissionProfile } from "../surface/lib/manifest.js";
import { callTool } from "./tool-call.js";
import { loadSurfaceWindowState, saveSurfaceWindowState } from "./surface-window-store.js";

const DEFAULT_MARGIN = 12;
const PANEL_MIN_WIDTH = 360;
const PANEL_MIN_HEIGHT = 260;
const DOCK_MIN_WIDTH = 320;
const DOCK_MAX_WIDTH_RATIO = 0.72;

function createPanel() {
  const panel = document.createElement("div");
  panel.className = "surface-float-panel";
  panel.innerHTML = `
    <div class="surface-float-head">
      <div class="surface-float-title">Surface:未打开</div>
      <div class="surface-float-actions">
        <button type="button" class="surface-window-light close" data-act="close" title="关闭">
          <svg viewBox="0 0 12 12" aria-hidden="true"><path d="M3 3l6 6M9 3L3 9"></path></svg>
        </button>
        <button type="button" class="surface-window-light dock" data-act="dock" title="停靠/浮动">
          <svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 2.5h7v7h-7z"></path><path d="M6 2.5v7"></path></svg>
        </button>
        <button type="button" class="surface-window-light refresh" data-act="refresh" title="刷新">
          <svg viewBox="0 0 12 12" aria-hidden="true"><path d="M9.5 4.5V2.5H7.5"></path><path d="M9.1 6A3.6 3.6 0 1 1 5.5 2.4c1 0 1.9.4 2.6 1.1l1.4 1"></path></svg>
        </button>
      </div>
    </div>
    <div class="surface-float-body">
      <div class="surface-window-workspace"></div>
    </div>
    <div class="surface-float-resize" aria-hidden="true"></div>
  `;
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

function clampGeometry(nextGeometry) {
  const maxWidth = Math.max(240, window.innerWidth - DEFAULT_MARGIN * 2);
  const maxHeight = Math.max(180, window.innerHeight - DEFAULT_MARGIN * 2);
  const minWidth = Math.min(PANEL_MIN_WIDTH, maxWidth);
  const minHeight = Math.min(PANEL_MIN_HEIGHT, maxHeight);
  const width = Math.min(Math.max(Number(nextGeometry.width) || minWidth, minWidth), maxWidth);
  const height = Math.min(Math.max(Number(nextGeometry.height) || minHeight, minHeight), maxHeight);
  const maxX = Math.max(DEFAULT_MARGIN, window.innerWidth - width - DEFAULT_MARGIN);
  const maxY = Math.max(DEFAULT_MARGIN, window.innerHeight - height - DEFAULT_MARGIN);
  return {
    x: Math.min(Math.max(Number(nextGeometry.x) || DEFAULT_MARGIN, DEFAULT_MARGIN), maxX),
    y: Math.min(Math.max(Number(nextGeometry.y) || DEFAULT_MARGIN, DEFAULT_MARGIN), maxY),
    width,
    height,
  };
}

function defaultGeometry() {
  const width = Math.min(560, Math.max(360, window.innerWidth * 0.42));
  const height = Math.min(560, Math.max(320, window.innerHeight * 0.62));
  return clampGeometry({
    x: Math.max(DEFAULT_MARGIN, window.innerWidth - width - 18),
    y: Math.max(68, window.innerHeight - height - 22),
    width,
    height,
  });
}

function clampDockWidth(width) {
  const maxWidth = Math.max(DOCK_MIN_WIDTH, Math.floor(window.innerWidth * DOCK_MAX_WIDTH_RATIO));
  return Math.min(Math.max(Number(width) || defaultDockWidth(), DOCK_MIN_WIDTH), maxWidth);
}

function defaultDockWidth() {
  return clampDockWidth(Math.round(window.innerWidth * 0.5));
}

function normalizeWindowState(rawState) {
  const source = rawState && typeof rawState === "object" ? rawState : {};
  const mode = String(source.mode || "floating").trim().toLowerCase() === "docked" ? "docked" : "floating";
  return {
    mode,
    floatGeometry: clampGeometry(source.floatGeometry || source.geometry || defaultGeometry()),
    dockWidth: clampDockWidth(source.dockWidth),
  };
}

export function createSurfaceBridge(options) {
  const root = options.root;
  const dockHost = options.dockHost;
  const dockShell = options.dockShell;
  const dockResizeHandle = options.dockResizeHandle;
  const appendDebug = typeof options.appendDebug === "function" ? options.appendDebug : () => {};
  const appendSystem = typeof options.appendSystem === "function" ? options.appendSystem : () => {};
  const onSurfaceEvent = typeof options.onSurfaceEvent === "function" ? options.onSurfaceEvent : () => {};
  const reportActionRecord = typeof options.reportActionRecord === "function" ? options.reportActionRecord : () => {};
  const onStateChange = typeof options.onStateChange === "function" ? options.onStateChange : () => {};
  const onRequestAIReply = typeof options.onRequestAIReply === "function" ? options.onRequestAIReply : () => ({ requested: false, reason: "not_implemented" });

  const registry = new Map();
  const runtimeViews = new Map();
  const layoutCache = new Map();
  const persistTimers = new Map();
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
      {
        name: "call_ai_reply",
        description: "请求 chat page 基于最新 surface 状态触发一轮 AI 回复",
        handler: async ({ args, runtime }) => {
          const reason = typeof args.reason === "string" && args.reason.trim()
            ? args.reason.trim()
            : `surface:${runtime.surfaceID}:call_ai_reply`;
          const result = await Promise.resolve(onRequestAIReply({
            surfaceID: runtime.surfaceID,
            reason,
            args: args && typeof args === "object" ? args : {},
          }));
          return result && typeof result === "object"
            ? { requested: !!result.requested, reason: result.reason || "", turn_id: result.turn_id || 0 }
            : { requested: false, reason: "invalid_reply_request_result", turn_id: 0 };
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
      if (event.type === "surface_closed") {
        detachRuntimeView(event.runtime.surfaceID);
        if (currentSurfaceID === event.runtime.surfaceID) {
          currentSurfaceID = "";
          renderWindow();
        }
      }
      updatePanelTitle();
      emitStateChange();
    },
    onError: (error) => {
      appendDebug("ERROR", "SurfaceBridge", null, null, error && error.message ? error.message : String(error));
    },
  });

  let panel = null;
  let titleEl = null;
  let workspaceEl = null;
  let interactionShieldEl = null;
  let currentSurfaceID = "";
  let currentMode = "floating";
  let floatGeometry = defaultGeometry();
  let dockWidth = defaultDockWidth();
  let interaction = null;

  function availableItems() {
    return Array.from(registry.values())
      .filter((item) => item.enabled && item.status === "ok")
      .sort((a, b) => {
        const left = String(a.name || a.surface_id || "");
        const right = String(b.name || b.surface_id || "");
        return left.localeCompare(right, "zh-CN") || String(a.surface_id || "").localeCompare(String(b.surface_id || ""), "zh-CN");
      });
  }

  function activeItem() {
    return currentSurfaceID ? registry.get(currentSurfaceID) || null : null;
  }

  function runtimeSnapshot(surfaceID) {
    return host.getRuntimeSnapshot(surfaceID);
  }

  function emitStateChange() {
    onStateChange({
      items: availableItems().map((item) => ({
        surface_id: item.surface_id,
        name: item.name || item.surface_id,
        surface_type: item.surface_type || "app",
        version: item.version || "1",
        desc: item.desc || "",
        entry_url: item.entry_url || "",
      })),
      activeSurfaceID: currentSurfaceID,
      visible: !!currentSurfaceID,
      mode: currentMode,
    });
  }

  function currentPanelRect() {
    if (!panel || !currentSurfaceID || !panel.classList.contains("open")) return null;
    const rect = panel.getBoundingClientRect();
    if (!rect.width || !rect.height) return null;
    return {
      x: Math.round(rect.left),
      y: Math.round(rect.top),
      width: Math.round(rect.width),
      height: Math.round(rect.height),
    };
  }

  function snapshotWindowState() {
    const rect = currentPanelRect();
    return normalizeWindowState({
      mode: currentMode,
      floatGeometry: currentMode === "floating"
        ? clampGeometry(floatGeometry)
        : clampGeometry(rect || floatGeometry),
      dockWidth,
    });
  }

  function persistWindowState(surfaceID, immediate = false) {
    const targetSurfaceID = String(surfaceID || "").trim();
    if (!targetSurfaceID) return;
    const state = snapshotWindowState();
    layoutCache.set(targetSurfaceID, state);
    const existing = persistTimers.get(targetSurfaceID);
    if (existing) clearTimeout(existing);
    const runSave = () => {
      persistTimers.delete(targetSurfaceID);
      saveSurfaceWindowState(targetSurfaceID, state).catch((error) => {
        appendDebug("WARN", "SurfaceBridge", null, null, `persist surface window failed: ${error.message || error}`);
      });
    };
    if (immediate) {
      runSave();
      return;
    }
    const timer = setTimeout(runSave, 180);
    persistTimers.set(targetSurfaceID, timer);
  }

  async function restoreWindowState(surfaceID) {
    const targetSurfaceID = String(surfaceID || "").trim();
    if (!targetSurfaceID) return;
    let stored = layoutCache.get(targetSurfaceID);
    if (stored === undefined) {
      try {
        stored = normalizeWindowState(await loadSurfaceWindowState(targetSurfaceID));
      } catch (error) {
        appendDebug("WARN", "SurfaceBridge", null, null, `load surface window failed: ${error.message || error}`);
        stored = normalizeWindowState(null);
      }
      layoutCache.set(targetSurfaceID, stored);
    }
    const layout = normalizeWindowState(stored);
    currentMode = layout.mode;
    floatGeometry = layout.floatGeometry;
    dockWidth = layout.dockWidth;
  }

  function updatePanelTitle() {
    if (!titleEl) return;
    const item = activeItem();
    const runtime = item ? runtimeSnapshot(item.surface_id) : null;
    const surfaceName = runtime && runtime.registration && runtime.registration.title
      ? runtime.registration.title
      : (item && item.name ? item.name : "未打开");
    titleEl.textContent = `Surface:${surfaceName}`;
  }

  function applyFloatGeometry() {
    if (!panel || currentMode !== "floating") return;
    floatGeometry = clampGeometry(floatGeometry);
    panel.style.left = `${floatGeometry.x}px`;
    panel.style.top = `${floatGeometry.y}px`;
    panel.style.width = `${floatGeometry.width}px`;
    panel.style.height = `${floatGeometry.height}px`;
  }

  function applyDockLayout() {
    if (!dockShell) return;
    dockWidth = clampDockWidth(dockWidth);
    dockShell.style.width = `${dockWidth}px`;
  }

  function mountPanelForMode() {
    if (!panel) return;
    if (currentMode === "docked") {
      if (dockHost && panel.parentElement !== dockHost) {
        dockHost.appendChild(panel);
      }
      panel.classList.add("is-docked");
      applyDockLayout();
      return;
    }
    if (root && panel.parentElement !== root) {
      root.appendChild(panel);
    }
    panel.classList.remove("is-docked");
    applyFloatGeometry();
  }

  function syncWorkspaceState() {
    if (!currentSurfaceID) return;
    const rect = currentPanelRect();
    const geometry = rect || (currentMode === "floating" ? clampGeometry(floatGeometry) : {
      x: 0,
      y: 0,
      width: clampDockWidth(dockWidth),
      height: window.innerHeight,
    });
    host.updateWorkspaceState(currentSurfaceID, {
      open: true,
      focused: true,
      minimized: false,
      maximized: currentMode === "docked",
      geometry,
    });
  }

  function renderWindow() {
    ensurePanel();
    const opened = !!currentSurfaceID;
    panel.classList.toggle("open", opened);
    dockShell?.classList.toggle("open", opened && currentMode === "docked");
    if (!opened) {
      updatePanelTitle();
      emitStateChange();
      return;
    }
    mountPanelForMode();
    updatePanelTitle();
    syncWorkspaceState();
    emitStateChange();
  }

  function handlePointerMove(event) {
    if (!interaction) return;
    if (interaction.kind === "drag") {
      floatGeometry = clampGeometry({
        ...interaction.geometry,
        x: interaction.geometry.x + (event.clientX - interaction.startX),
        y: interaction.geometry.y + (event.clientY - interaction.startY),
      });
      applyFloatGeometry();
      syncWorkspaceState();
      persistWindowState(currentSurfaceID);
      return;
    }
    if (interaction.kind === "resize") {
      floatGeometry = clampGeometry({
        ...interaction.geometry,
        width: interaction.geometry.width + (event.clientX - interaction.startX),
        height: interaction.geometry.height + (event.clientY - interaction.startY),
      });
      applyFloatGeometry();
      syncWorkspaceState();
      persistWindowState(currentSurfaceID);
      return;
    }
    if (interaction.kind === "dock-resize") {
      dockWidth = clampDockWidth(interaction.width + (interaction.startX - event.clientX));
      applyDockLayout();
      syncWorkspaceState();
      persistWindowState(currentSurfaceID);
    }
  }

  function clearInteraction() {
    if (interactionShieldEl) {
      interactionShieldEl.classList.remove("active");
      interactionShieldEl.style.cursor = "";
    }
    document.body?.classList.remove("surface-window-interacting");
    interaction = null;
  }

  function toggleDockMode() {
    if (!currentSurfaceID) return;
    currentMode = currentMode === "docked" ? "floating" : "docked";
    renderWindow();
    persistWindowState(currentSurfaceID, true);
  }

  function ensurePanel() {
    if (panel) return;
    panel = createPanel();
    titleEl = panel.querySelector(".surface-float-title");
    workspaceEl = panel.querySelector(".surface-window-workspace");
    const headEl = panel.querySelector(".surface-float-head");
    const resizeEl = panel.querySelector(".surface-float-resize");
    interactionShieldEl = document.createElement("div");
    interactionShieldEl.className = "surface-interaction-shield";
    panel.querySelector('[data-act="refresh"]').addEventListener("click", () => {
      refreshActiveSurface().catch((error) => appendSystem(error.message || String(error)));
    });
    panel.querySelector('[data-act="close"]').addEventListener("click", () => {
      selectSurface("").catch((error) => appendSystem(error.message || String(error)));
    });
    panel.querySelector('[data-act="dock"]').addEventListener("click", () => {
      toggleDockMode();
    });
    headEl.addEventListener("pointerdown", (event) => {
      if (currentMode !== "floating") return;
      if (event.button !== 0 || event.target.closest("button")) return;
      interaction = {
        kind: "drag",
        startX: event.clientX,
        startY: event.clientY,
        geometry: { ...floatGeometry },
      };
      document.body?.classList.add("surface-window-interacting");
      interactionShieldEl.classList.add("active");
      interactionShieldEl.style.cursor = "move";
      event.preventDefault();
    });
    resizeEl.addEventListener("pointerdown", (event) => {
      if (currentMode !== "floating" || event.button !== 0) return;
      interaction = {
        kind: "resize",
        startX: event.clientX,
        startY: event.clientY,
        geometry: { ...floatGeometry },
      };
      document.body?.classList.add("surface-window-interacting");
      interactionShieldEl.classList.add("active");
      interactionShieldEl.style.cursor = "nwse-resize";
      event.preventDefault();
    });
    dockResizeHandle?.addEventListener("pointerdown", (event) => {
      if (currentMode !== "docked" || event.button !== 0) return;
      interaction = {
        kind: "dock-resize",
        startX: event.clientX,
        width: dockWidth,
      };
      document.body?.classList.add("surface-window-interacting");
      interactionShieldEl.classList.add("active");
      interactionShieldEl.style.cursor = "ew-resize";
      event.preventDefault();
    });
    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", clearInteraction);
    window.addEventListener("pointercancel", clearInteraction);
    window.addEventListener("resize", () => {
      floatGeometry = clampGeometry(floatGeometry);
      dockWidth = clampDockWidth(dockWidth);
      renderWindow();
      if (currentSurfaceID) persistWindowState(currentSurfaceID);
    });
    root?.appendChild(panel);
    root?.appendChild(interactionShieldEl);
    updatePanelTitle();
  }

  function findSurfaceByTarget(target) {
    const key = String(target || "").trim();
    if (!key) return null;
    if (registry.has(key)) return registry.get(key);
    const lower = key.toLowerCase();
    return availableItems().find((item) => String(item.name || "").toLowerCase() === lower) || null;
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

  function createRuntimeView(item) {
    const hostEl = document.createElement("div");
    hostEl.className = "surface-runtime-host";
    hostEl.setAttribute("data-surface-id", item.surface_id);
    const iframe = document.createElement("iframe");
    const permissionProfile = buildPermissionProfile({
      permissions: item && item.permissions && typeof item.permissions === "object" ? item.permissions : {},
    });
    iframe.className = "surface-float-iframe";
    iframe.setAttribute("sandbox", permissionProfile.sandboxTokens.join(" "));
    if (permissionProfile.allowText) {
      iframe.setAttribute("allow", permissionProfile.allowText);
    }
    hostEl.appendChild(iframe);
    runtimeViews.set(item.surface_id, { hostEl, iframe });
    return { hostEl, iframe };
  }

  function mountRuntimeView(surfaceID) {
    if (!workspaceEl) return;
    workspaceEl.replaceChildren();
    if (!surfaceID) return;
    const view = runtimeViews.get(surfaceID);
    if (!view) return;
    workspaceEl.appendChild(view.hostEl);
  }

  function detachRuntimeView(surfaceID) {
    const sid = String(surfaceID || "").trim();
    const view = runtimeViews.get(sid);
    if (view && view.hostEl.parentElement) {
      view.hostEl.parentElement.removeChild(view.hostEl);
    }
    runtimeViews.delete(sid);
  }

  function closeSurface(surfaceID, reason = "closed", options = {}) {
    const sid = String(surfaceID || "").trim();
    if (sid && options.persist !== false) {
      persistWindowState(sid, true);
    }
    const closed = host.closeSurface(sid, reason);
    detachRuntimeView(sid);
    if (sid && sid === currentSurfaceID) {
      currentSurfaceID = "";
      renderWindow();
    }
    return closed;
  }

  async function refreshRegistry() {
    const result = await host.refreshCatalog();
    const items = Array.isArray(result && result.items) ? result.items : [];
    registry.clear();
    items.forEach((item) => {
      if (!item || typeof item !== "object") return;
      registry.set(item.surface_id, item);
    });
    if (currentSurfaceID) {
      const current = registry.get(currentSurfaceID);
      if (!current || !current.enabled || current.status !== "ok") {
        closeSurface(currentSurfaceID, "surface_unavailable");
      }
    }
    updatePanelTitle();
    emitStateChange();
    return items;
  }

  async function ensureSurfaceOpen(surfaceID) {
    const targetSurfaceID = String(surfaceID || "").trim();
    const item = registry.get(targetSurfaceID);
    if (!item) throw new Error(`surface not found: ${surfaceID}`);
    if (!item.enabled || item.status !== "ok") {
      throw new Error(`surface is unavailable: ${surfaceID}`);
    }

    if (currentSurfaceID && currentSurfaceID !== item.surface_id) {
      closeSurface(currentSurfaceID, "switch_surface");
    }

    await restoreWindowState(item.surface_id);

    const openedRuntime = runtimeSnapshot(item.surface_id);
    if (openedRuntime) {
      currentSurfaceID = item.surface_id;
      ensurePanel();
      mountRuntimeView(item.surface_id);
      renderWindow();
      return openedRuntime;
    }

    const view = createRuntimeView(item);
    currentSurfaceID = item.surface_id;
    ensurePanel();
    mountRuntimeView(item.surface_id);
    renderWindow();

    try {
      const runtime = await host.openSurface({
        surfaceID: item.surface_id,
        iframe: view.iframe,
        workspaceState: {
          open: true,
          focused: true,
          frozen: false,
          minimized: false,
          maximized: currentMode === "docked",
          geometry: currentMode === "floating"
            ? clampGeometry(floatGeometry)
            : {
                x: 0,
                y: 0,
                width: clampDockWidth(dockWidth),
                height: window.innerHeight,
              },
          z_index: 1,
        },
      });
      updatePanelTitle();
      emitStateChange();
      return runtime;
    } catch (error) {
      detachRuntimeView(item.surface_id);
      if (currentSurfaceID === item.surface_id) {
        currentSurfaceID = "";
        renderWindow();
      }
      throw error;
    }
  }

  async function refreshActiveSurface() {
    await refreshRegistry();
    if (!currentSurfaceID) return;
    const targetSurfaceID = currentSurfaceID;
    persistWindowState(targetSurfaceID, true);
    closeSurface(targetSurfaceID, "refresh", { persist: false });
    await ensureSurfaceOpen(targetSurfaceID);
  }

  async function selectSurface(surfaceID) {
    const targetSurfaceID = String(surfaceID || "").trim();
    if (!registry.size) {
      await refreshRegistry();
    }
    if (!targetSurfaceID) {
      if (currentSurfaceID) {
        closeSurface(currentSurfaceID, "manual");
      } else {
        renderWindow();
      }
      return null;
    }
    const candidate = registry.get(targetSurfaceID);
    if (!candidate || !candidate.enabled || candidate.status !== "ok") {
      await refreshRegistry();
    }
    return ensureSurfaceOpen(targetSurfaceID);
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
      await selectSurface(item.surface_id);
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
      const item = target ? findSurfaceByTarget(target) : (currentSurfaceID ? registry.get(currentSurfaceID) : availableItems()[0]);
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
      if (!runtime.ready) {
        await host.waitUntilSurfaceReady(parsed.surfaceID, 8000);
      }
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
    if (!currentSurfaceID) return;
    if (currentMode === "docked") {
      toggleDockMode();
      return;
    }
    renderWindow();
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
    selectSurface,
    setVisible(nextVisible) {
      if (nextVisible === false) {
        return selectSurface("");
      }
      if (nextVisible === true && currentSurfaceID) {
        renderWindow();
      }
      return Promise.resolve();
    },
    toggleVisible,
  };
}
