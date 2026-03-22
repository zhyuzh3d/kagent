const STORAGE_KEY = "surface-loader-window-layout:v3";
const PANEL_LAYOUT_VERSION = 3;
const DRAG_THRESHOLD = 6;
const RESIZE_THRESHOLD = 4;
const FALLBACK_MIN_WIDTH = 280;
const FALLBACK_MIN_HEIGHT = 160;
const CONTROL_WIDTH = 450;
const LOG_HEIGHT = 200;

export function initWindowManager(options = {}) {
  const desktop = document.querySelector(".window-desktop");
  const windowNodes = Array.from(document.querySelectorAll(".panel-window"));
  const onLayoutChange = typeof options.onLayoutChange === "function" ? options.onLayoutChange : null;

  if (!desktop || !windowNodes.length) {
    return {
      resetLayout() {},
      snapshotLayouts() {
        return {};
      },
    };
  }

  const panels = windowNodes.map((node, index) => createPanel(node, index));
  let interaction = null;
  let resizeFrame = 0;

  applyInitialLayout();
  panels.forEach(bindPanelInteractions);
  activatePanel(panels.find((panel) => panel.id === "control") || panels[0]);

  const resizeObserver = typeof ResizeObserver === "function"
    ? new ResizeObserver(() => scheduleViewportSync())
    : null;

  resizeObserver?.observe(desktop);
  window.addEventListener("resize", scheduleViewportSync);

  function createPanel(node, index) {
    const header = node.querySelector(".window-drag-handle");
    const collapseBtn = node.querySelector(".window-collapse-btn");
    const resizer = node.querySelector(".window-resizer");

    const panel = {
      id: node.dataset.panelId || node.id || `panel-${index + 1}`,
      node,
      header,
      collapseBtn,
      resizer,
      baseZ: toNumber(node.dataset.baseZ, (index + 1) * 10),
      minWidth: toNumber(node.dataset.minWidth, FALLBACK_MIN_WIDTH),
      minHeight: toNumber(node.dataset.minHeight, FALLBACK_MIN_HEIGHT),
      collapsed: false,
      lastExpandedRect: null,
    };

    node.style.zIndex = String(panel.baseZ);
    header?.setAttribute("tabindex", "0");
    header?.setAttribute("title", "单击折叠或恢复，拖拽可移动窗口");
    resizer?.setAttribute("title", "拖拽调整窗口大小");
    resizer?.setAttribute("aria-label", "拖拽调整窗口大小");

    return panel;
  }

  function applyInitialLayout() {
    const bounds = getDesktopBounds();
    const defaults = buildDefaultLayout(bounds);
    const storedPanels = readStoredLayout();

    panels.forEach((panel) => {
      const fallback = defaults[panel.id] || buildFallbackPanel(bounds, panel.baseZ);
      const layout = normalizeLayoutState(storedPanels?.[panel.id], panel, bounds, fallback);
      applyPanelLayout(panel, layout, { persist: false });
    });

    persistLayout();
  }

  function resetLayout() {
    clearStoredLayout();
    const bounds = getDesktopBounds();
    const defaults = buildDefaultLayout(bounds);

    panels.forEach((panel) => {
      const fallback = defaults[panel.id] || buildFallbackPanel(bounds, panel.baseZ);
      const layout = normalizeLayoutState(null, panel, bounds, fallback);
      applyPanelLayout(panel, layout, { persist: false });
    });

    activatePanel(panels.find((panel) => panel.id === "control") || panels[0]);
    persistLayout();
  }

  function bindPanelInteractions(panel) {
    panel.node.addEventListener("pointerdown", () => {
      activatePanel(panel);
    }, { capture: true });

    panel.header?.addEventListener("keydown", (event) => {
      if (event.target !== panel.header) return;
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      activatePanel(panel);
      toggleCollapse(panel);
    });

    panel.collapseBtn?.addEventListener("click", (event) => {
      event.stopPropagation();
      activatePanel(panel);
      toggleCollapse(panel);
    });

    panel.header?.addEventListener("pointerdown", (event) => {
      if (event.button !== 0) return;
      if (event.target.closest("button, input, select, textarea, a")) return;

      activatePanel(panel);
      interaction = {
        kind: "drag",
        panel,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        originRect: getVisibleRect(panel),
        moved: false,
      };

      panel.header.setPointerCapture(event.pointerId);
      event.preventDefault();
    });

    panel.header?.addEventListener("pointermove", (event) => {
      if (!interaction || interaction.kind !== "drag") return;
      if (interaction.panel !== panel || interaction.pointerId !== event.pointerId) return;

      const dx = event.clientX - interaction.startX;
      const dy = event.clientY - interaction.startY;

      if (!interaction.moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) {
        return;
      }
      if (!interaction.moved) {
        interaction.moved = true;
        beginInteraction(panel, "is-dragging");
      }

      const bounds = getDesktopBounds();
      const nextRect = clampRectToBounds(
        {
          ...interaction.originRect,
          left: interaction.originRect.left + dx,
          top: interaction.originRect.top + dy,
        },
        panel,
        bounds,
        { useContentMinSize: !panel.collapsed },
      );

      applyVisibleRect(panel, nextRect);
      syncCollapsedAnchor(panel);
    });

    panel.header?.addEventListener("pointerup", (event) => {
      if (!interaction || interaction.kind !== "drag") return;
      if (interaction.panel !== panel || interaction.pointerId !== event.pointerId) return;

      safeReleasePointer(panel.header, event.pointerId);
      const moved = interaction.moved;
      finishInteraction(panel);
      interaction = null;

      if (!moved) {
        toggleCollapse(panel);
        return;
      }

      syncCollapsedAnchor(panel);
      persistLayout();
    });

    panel.header?.addEventListener("pointercancel", (event) => {
      if (!interaction || interaction.kind !== "drag") return;
      if (interaction.panel !== panel || interaction.pointerId !== event.pointerId) return;
      safeReleasePointer(panel.header, event.pointerId);
      finishInteraction(panel);
      interaction = null;
    });

    panel.resizer?.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || panel.collapsed) return;

      activatePanel(panel);
      interaction = {
        kind: "resize",
        panel,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        originRect: getExpandedRect(panel),
        moved: false,
      };

      panel.resizer.setPointerCapture(event.pointerId);
      event.preventDefault();
    });

    panel.resizer?.addEventListener("pointermove", (event) => {
      if (!interaction || interaction.kind !== "resize") return;
      if (interaction.panel !== panel || interaction.pointerId !== event.pointerId) return;

      const dx = event.clientX - interaction.startX;
      const dy = event.clientY - interaction.startY;

      if (!interaction.moved && Math.hypot(dx, dy) < RESIZE_THRESHOLD) {
        return;
      }
      if (!interaction.moved) {
        interaction.moved = true;
        beginInteraction(panel, "is-resizing");
      }

      const bounds = getDesktopBounds();
      const width = clampNumber(
        interaction.originRect.width + dx,
        getMinWidth(panel, bounds),
        Math.max(getMinWidth(panel, bounds), bounds.width - interaction.originRect.left),
      );
      const height = clampNumber(
        interaction.originRect.height + dy,
        getMinHeight(panel, bounds),
        Math.max(getMinHeight(panel, bounds), bounds.height - interaction.originRect.top),
      );

      const nextRect = normalizeExpandedRect(
        {
          ...interaction.originRect,
          width,
          height,
        },
        panel,
        bounds,
      );

      applyVisibleRect(panel, nextRect);
      panel.lastExpandedRect = { ...nextRect };
    });

    panel.resizer?.addEventListener("pointerup", (event) => {
      if (!interaction || interaction.kind !== "resize") return;
      if (interaction.panel !== panel || interaction.pointerId !== event.pointerId) return;

      safeReleasePointer(panel.resizer, event.pointerId);
      finishInteraction(panel);
      interaction = null;
      persistLayout();
    });

    panel.resizer?.addEventListener("pointercancel", (event) => {
      if (!interaction || interaction.kind !== "resize") return;
      if (interaction.panel !== panel || interaction.pointerId !== event.pointerId) return;
      safeReleasePointer(panel.resizer, event.pointerId);
      finishInteraction(panel);
      interaction = null;
    });
  }

  function activatePanel(activePanel) {
    panels.forEach((panel) => {
      const isActive = panel === activePanel;
      panel.node.classList.toggle("is-active", isActive);
      panel.node.style.zIndex = String(panel.baseZ + (isActive ? 1 : 0));
    });
  }

  function toggleCollapse(panel) {
    const bounds = getDesktopBounds();

    if (panel.collapsed) {
      const fallback = buildDefaultLayout(bounds)[panel.id] || buildFallbackPanel(bounds, panel.baseZ);
      const expandedRect = normalizeExpandedRect(
        panel.lastExpandedRect || fallback,
        panel,
        bounds,
      );

      panel.collapsed = false;
      panel.node.classList.remove("collapsed");
      applyVisibleRect(panel, expandedRect);
      panel.lastExpandedRect = { ...expandedRect };
      syncPanelAccessibility(panel);
      persistLayout();
      return;
    }

    const currentRect = getExpandedRect(panel);
    panel.lastExpandedRect = normalizeExpandedRect(currentRect, panel, bounds);
    panel.collapsed = true;
    panel.node.classList.add("collapsed");

    const collapsedRect = clampRectToBounds(
      {
        left: panel.lastExpandedRect.left,
        top: panel.lastExpandedRect.top,
        width: panel.lastExpandedRect.width,
        height: getCollapsedHeight(panel),
      },
      panel,
      bounds,
      { useContentMinSize: false },
    );

    applyVisibleRect(panel, collapsedRect);
    syncCollapsedAnchor(panel);
    syncPanelAccessibility(panel);
    persistLayout();
  }

  function applyPanelLayout(panel, layout, options = {}) {
    panel.collapsed = Boolean(layout.collapsed);
    panel.lastExpandedRect = normalizeExpandedRect(
      layout.lastExpandedRect || layout,
      panel,
      getDesktopBounds(),
    );

    panel.node.classList.toggle("collapsed", panel.collapsed);

    if (panel.collapsed) {
      applyVisibleRect(panel, {
        left: layout.left,
        top: layout.top,
        width: layout.width,
        height: getCollapsedHeight(panel),
      });
      syncCollapsedAnchor(panel);
    } else {
      applyVisibleRect(panel, normalizeExpandedRect(layout, panel, getDesktopBounds()));
    }

    syncPanelAccessibility(panel);
    if (options.persist !== false) {
      persistLayout();
    }
  }

  function syncPanelAccessibility(panel) {
    panel.header?.setAttribute("aria-expanded", String(!panel.collapsed));
  }

  function beginInteraction(panel, className) {
    document.body.classList.add("window-interacting");
    panel.node.classList.add(className);
    if (className === "is-resizing") {
      panel.resizer?.classList.add("dragging");
    }
  }

  function finishInteraction(panel) {
    document.body.classList.remove("window-interacting");
    panel.node.classList.remove("is-dragging", "is-resizing");
    panel.resizer?.classList.remove("dragging");
  }

  function scheduleViewportSync() {
    if (resizeFrame) return;
    resizeFrame = window.requestAnimationFrame(() => {
      resizeFrame = 0;
      const bounds = getDesktopBounds();

      panels.forEach((panel) => {
        const fitted = fitLayoutToBounds(snapshotPanelLayout(panel), panel, bounds);
        applyPanelLayout(panel, fitted, { persist: false });
      });

      persistLayout();
    });
  }

  function persistLayout() {
    try {
      const payload = {
        version: PANEL_LAYOUT_VERSION,
        panels: snapshotLayouts(),
      };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(payload));
    } catch (_) {
      // Ignore storage failures. Layout should still work for the current session.
    }
    if (onLayoutChange) {
      onLayoutChange(snapshotLayouts());
    }
  }

  function readStoredLayout() {
    try {
      const raw = localStorage.getItem(STORAGE_KEY);
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (!parsed || parsed.version !== PANEL_LAYOUT_VERSION || !parsed.panels) {
        return null;
      }
      return parsed.panels;
    } catch (_) {
      return null;
    }
  }

  function clearStoredLayout() {
    try {
      localStorage.removeItem(STORAGE_KEY);
    } catch (_) {
      // Ignore storage failures. Reset still applies in-memory.
    }
  }

  function snapshotPanelLayout(panel) {
    const visibleRect = getVisibleRect(panel);
    const expandedRect = panel.collapsed
      ? normalizeExpandedRect(panel.lastExpandedRect || visibleRect, panel, getDesktopBounds())
      : normalizeExpandedRect(visibleRect, panel, getDesktopBounds());

    return {
      left: visibleRect.left,
      top: visibleRect.top,
      width: visibleRect.width,
      height: visibleRect.height,
      collapsed: panel.collapsed,
      z_index: Number(panel.node.style.zIndex || panel.baseZ || 0),
      lastExpandedRect: expandedRect,
    };
  }

  function snapshotLayouts() {
    const out = {};
    panels.forEach((panel) => {
      out[panel.id] = snapshotPanelLayout(panel);
    });
    return out;
  }

  function syncCollapsedAnchor(panel) {
    if (!panel.collapsed || !panel.lastExpandedRect) return;
    const visibleRect = getVisibleRect(panel);
    panel.lastExpandedRect = {
      ...panel.lastExpandedRect,
      left: visibleRect.left,
      top: visibleRect.top,
      width: visibleRect.width,
    };
  }

  function getDesktopBounds() {
    return {
      width: Math.max(0, desktop.clientWidth),
      height: Math.max(0, desktop.clientHeight),
    };
  }

  function getVisibleRect(panel) {
    const desktopRect = desktop.getBoundingClientRect();
    const panelRect = panel.node.getBoundingClientRect();
    return {
      left: panelRect.left - desktopRect.left,
      top: panelRect.top - desktopRect.top,
      width: panelRect.width,
      height: panelRect.height,
    };
  }

  function getExpandedRect(panel) {
    if (panel.collapsed && panel.lastExpandedRect) {
      return { ...panel.lastExpandedRect };
    }
    return getVisibleRect(panel);
  }

  function applyVisibleRect(panel, rect) {
    panel.node.style.left = `${Math.round(rect.left)}px`;
    panel.node.style.top = `${Math.round(rect.top)}px`;
    panel.node.style.width = `${Math.round(rect.width)}px`;
    panel.node.style.height = `${Math.round(rect.height)}px`;
  }

  function normalizeLayoutState(rawLayout, panel, bounds, fallback) {
    const fallbackExpanded = normalizeExpandedRect(fallback, panel, bounds);
    const rawExpanded = rawLayout?.lastExpandedRect || rawLayout || fallbackExpanded;
    const expandedRect = normalizeExpandedRect(rawExpanded, panel, bounds);

    if (rawLayout?.collapsed) {
      const collapsedRect = clampRectToBounds(
        {
          left: toNumber(rawLayout.left, expandedRect.left),
          top: toNumber(rawLayout.top, expandedRect.top),
          width: clampNumber(
            toNumber(rawLayout.width, expandedRect.width),
            getMinWidth(panel, bounds),
            bounds.width,
          ),
          height: getCollapsedHeight(panel),
        },
        panel,
        bounds,
        { useContentMinSize: false },
      );

      return {
        ...collapsedRect,
        collapsed: true,
        lastExpandedRect: {
          ...expandedRect,
          left: collapsedRect.left,
          top: collapsedRect.top,
          width: collapsedRect.width,
        },
      };
    }

    return {
      ...expandedRect,
      collapsed: false,
      lastExpandedRect: expandedRect,
    };
  }

  function normalizeExpandedRect(rect, panel, bounds) {
    const minWidth = getMinWidth(panel, bounds);
    const minHeight = getMinHeight(panel, bounds);
    const width = clampNumber(toNumber(rect?.width, minWidth), minWidth, Math.max(minWidth, bounds.width));
    const height = clampNumber(toNumber(rect?.height, minHeight), minHeight, Math.max(minHeight, bounds.height));

    return clampRectToBounds(
      {
        left: toNumber(rect?.left, 0),
        top: toNumber(rect?.top, 0),
        width,
        height,
      },
      panel,
      bounds,
    );
  }

  function fitLayoutToBounds(layout, panel, bounds) {
    const rawExpanded = layout?.lastExpandedRect || layout || {
      left: 0,
      top: 0,
      width: Math.max(1, bounds.width),
      height: Math.max(1, bounds.height),
    };
    const fittedExpanded = clampRectToBounds(
      {
        left: toNumber(rawExpanded.left, 0),
        top: toNumber(rawExpanded.top, 0),
        width: clampNumber(toNumber(rawExpanded.width, bounds.width), 1, Math.max(1, bounds.width)),
        height: clampNumber(toNumber(rawExpanded.height, bounds.height), 1, Math.max(1, bounds.height)),
      },
      panel,
      bounds,
      { useContentMinSize: false },
    );

    if (layout?.collapsed) {
      const collapsedRect = clampRectToBounds(
        {
          left: toNumber(layout.left, fittedExpanded.left),
          top: toNumber(layout.top, fittedExpanded.top),
          width: clampNumber(toNumber(layout.width, fittedExpanded.width), 1, Math.max(1, bounds.width)),
          height: Math.min(getCollapsedHeight(panel), Math.max(1, bounds.height)),
        },
        panel,
        bounds,
        { useContentMinSize: false },
      );

      return {
        ...collapsedRect,
        collapsed: true,
        lastExpandedRect: {
          ...fittedExpanded,
          left: collapsedRect.left,
          top: collapsedRect.top,
        },
      };
    }

    return {
      ...fittedExpanded,
      collapsed: false,
      lastExpandedRect: fittedExpanded,
    };
  }

  function clampRectToBounds(rect, panel, bounds, options = {}) {
    const minWidth = options.useContentMinSize === false
      ? Math.min(getMinWidth(panel, bounds), bounds.width)
      : getMinWidth(panel, bounds);
    const minHeight = options.useContentMinSize === false
      ? Math.min(getCollapsedHeight(panel), bounds.height)
      : getMinHeight(panel, bounds);
    const width = clampNumber(rect.width, minWidth, Math.max(1, bounds.width));
    const height = clampNumber(rect.height, minHeight, Math.max(1, bounds.height));
    const maxLeft = Math.max(0, bounds.width - width);
    const maxTop = Math.max(0, bounds.height - height);

    return {
      left: clampNumber(rect.left, 0, maxLeft),
      top: clampNumber(rect.top, 0, maxTop),
      width,
      height,
    };
  }

  function getCollapsedHeight(panel) {
    const headerHeight = panel.header?.offsetHeight || 44;
    return headerHeight;
  }

  function getMinWidth(panel, bounds) {
    return Math.min(panel.minWidth || FALLBACK_MIN_WIDTH, Math.max(240, bounds.width));
  }

  function getMinHeight(panel, bounds) {
    return Math.min(panel.minHeight || FALLBACK_MIN_HEIGHT, Math.max(getCollapsedHeight(panel), bounds.height));
  }

  function buildDefaultLayout(bounds) {
    const controlWidth = Math.min(CONTROL_WIDTH, Math.max(0, bounds.width));
    const leftWidth = Math.max(0, bounds.width - controlWidth);
    const logHeight = Math.min(LOG_HEIGHT, Math.max(0, bounds.height));
    const previewHeight = Math.max(0, bounds.height - logHeight);

    return {
      preview: {
        left: 0,
        top: 0,
        width: leftWidth,
        height: previewHeight,
      },
      log: {
        left: 0,
        top: Math.max(0, bounds.height - logHeight),
        width: leftWidth,
        height: logHeight,
      },
      control: {
        left: Math.max(0, bounds.width - controlWidth),
        top: 0,
        width: controlWidth,
        height: bounds.height,
      },
    };
  }

  function buildFallbackPanel(bounds, baseZ) {
    const padding = 20;
    return {
      left: padding,
      top: padding + Math.max(0, baseZ - 10),
      width: Math.max(320, bounds.width - padding * 2),
      height: Math.max(240, bounds.height - padding * 2),
    };
  }

  return {
    resetLayout,
    snapshotLayouts,
  };
}

function safeReleasePointer(node, pointerId) {
  if (!node || pointerId == null) return;
  try {
    node.releasePointerCapture(pointerId);
  } catch (_) {
    // Ignore missing capture state.
  }
}

function clampNumber(value, min, max) {
  const lower = Math.min(min, max);
  const upper = Math.max(min, max);
  if (!Number.isFinite(value)) {
    return lower;
  }
  return Math.min(upper, Math.max(lower, value));
}

function toNumber(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
