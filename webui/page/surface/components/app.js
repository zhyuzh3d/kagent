import { callTool } from "./api.js";
import { createActionsPanel } from "./actions-panel.js";
import { createCatalogController } from "./catalog.js";
import { getElements } from "./dom.js";
import { createRuntimeController } from "./runtime.js";

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

  els.dispatchBtn.addEventListener("click", () => {
    runtimeController.dispatchAction().catch((err) => runtimeController.setStatus(err.message, "err"));
  });

  els.runtimeBtn.addEventListener("click", () => {
    runtimeController.loadRuntime().catch((err) => runtimeController.setStatus(err.message, "err"));
  });

  els.logsBtn.addEventListener("click", () => {
    runtimeController.loadLogs().catch((err) => runtimeController.setStatus(err.message, "err"));
  });
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
