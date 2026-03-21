import { callTool } from "./api.js";
import { createActionsPanel } from "./actions-panel.js";
import { createCatalogController } from "./catalog.js";
import { getElements } from "./dom.js";
import { createRuntimeController } from "./runtime.js";
import { initWindowManager } from "./window-manager.js";

const els = getElements();
const state = {
  entry: null,
  sessionToken: "",
  port: null,
  catalogItems: [],
  actions: [],
};

const actionsPanel = createActionsPanel({ els, state });
const catalogController = createCatalogController({ els, state, callTool });
const runtimeController = createRuntimeController({
  els,
  state,
  callTool,
  renderSurfaceSelect: catalogController.renderSurfaceSelect,
  setActions: actionsPanel.setActions,
});
const windowManager = initWindowManager();

function bindEvents() {
  els.surfaceSelect.addEventListener("change", () => {
    const selectedID = (els.surfaceSelect.value || "").trim();
    if (!selectedID) return;
    runtimeController.loadSurface(selectedID).catch((err) => runtimeController.setStatus(err.message, "err"));
  });

  els.actionSelect.addEventListener("change", () => {
    actionsPanel.syncActionSelection();
  });

  els.loadBtn.addEventListener("click", () => {
    runtimeController.loadSurface((els.surfaceSelect.value || "").trim()).catch((err) => runtimeController.setStatus(err.message, "err"));
  });

  els.dispatchBtn?.addEventListener("click", () => {
    runtimeController.dispatchAction().catch((err) => runtimeController.setStatus(err.message, "err"));
  });

  els.actionTabsNav?.addEventListener("click", (ev) => {
    const btn = ev.target.closest(".inner-tab-btn");
    if (!btn) return;
    const tabID = btn.dataset.innerTab;
    if (!tabID) return;
    document.querySelectorAll(".inner-tab-btn").forEach(b => b.classList.remove("active"));
    btn.classList.add("active");
    document.querySelectorAll(".inner-tab-pane").forEach(p => p.classList.remove("active"));
    const target = document.getElementById(`inner-tab-${tabID}`);
    if (target) target.classList.add("active");
  });

  els.clearLogsBtn?.addEventListener("click", () => {
    if (els.eventLog) els.eventLog.textContent = "（空）";
  });

  if (els.refreshFrameBtn) {
    els.refreshFrameBtn.addEventListener("click", () => {
      runtimeController.reloadIframe();
    });
  }

  // 标签页切换逻辑
  if (els.tabsNav) {
    els.tabsNav.addEventListener("click", (ev) => {
      const btn = ev.target.closest(".tab-btn");
      if (!btn) return;
      const tabID = btn.dataset.tab;
      if (!tabID) return;
      document.querySelectorAll(".tab-btn").forEach(b => b.classList.remove("active"));
      btn.classList.add("active");
      document.querySelectorAll(".tab-pane").forEach(p => p.classList.remove("active"));
      const target = document.getElementById(`tab-${tabID}`);
      if (target) target.classList.add("active");

      if (tabID === "status") runtimeController.loadRuntime().catch(() => {});
    });
  }

  els.resetLayoutBtn?.addEventListener("click", () => {
    windowManager?.resetLayout();
  });

  // 状态面板自动实时刷新
  setInterval(() => {
    const statusTab = document.getElementById("tab-status");
    if (statusTab && statusTab.classList.contains("active")) {
      runtimeController.loadRuntime(true).catch(() => {});
    }
  }, 2000);
}

async function bootstrap() {
  bindEvents();
  const params = new URLSearchParams(location.search);
  const surfaceID = params.get("surface_id") || "";
  try {
    const selectedID = await catalogController.loadCatalog(surfaceID);
    if (surfaceID || selectedID) {
      await runtimeController.loadSurface(surfaceID || selectedID);
    }
  } catch (err) {
    runtimeController.setStatus(err.message || String(err), "err");
  }
}

bootstrap();
