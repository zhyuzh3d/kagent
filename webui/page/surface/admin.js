const els = {
  refreshBtn: document.getElementById("refreshBtn"),
  rescanBtn: document.getElementById("rescanBtn"),
  cleanupBtn: document.getElementById("cleanupBtn"),
  rebindBtn: document.getElementById("rebindBtn"),
  statusBadge: document.getElementById("statusBadge"),
  totalCount: document.getElementById("totalCount"),
  enabledCount: document.getElementById("enabledCount"),
  readyCount: document.getElementById("readyCount"),
  surfaceList: document.getElementById("surfaceList"),
  surfaceTitle: document.getElementById("surfaceTitle"),
  surfaceEntry: document.getElementById("surfaceEntry"),
  sessionToken: document.getElementById("sessionToken"),
  toggleEnableBtn: document.getElementById("toggleEnableBtn"),
  openBtn: document.getElementById("openBtn"),
  issueSessionBtn: document.getElementById("issueSessionBtn"),
  fileSelect: document.getElementById("fileSelect"),
  loadFileBtn: document.getElementById("loadFileBtn"),
  saveFileBtn: document.getElementById("saveFileBtn"),
  fileEditor: document.getElementById("fileEditor"),
  generateName: document.getElementById("generateName"),
  generatePrompt: document.getElementById("generatePrompt"),
  generateBtn: document.getElementById("generateBtn"),
  generateOpenBtn: document.getElementById("generateOpenBtn"),
  openGenerateBtn: document.getElementById("openGenerateBtn"),
  closeGenerateBtn: document.getElementById("closeGenerateBtn"),
  generateDialog: document.getElementById("generateDialog"),
  runtimeBtn: document.getElementById("runtimeBtn"),
  logsBtn: document.getElementById("logsBtn"),
  pkgListBtn: document.getElementById("pkgListBtn"),
  infoPanel: document.getElementById("infoPanel"),
  paneDivider: document.getElementById("paneDivider"),
  paneBottom: document.getElementById("paneBottom"),
};

const state = {
  items: [],
  selectedID: "",
  sessionToken: "",
};

function setStatus(text, cls = "") {
  els.statusBadge.textContent = text;
  els.statusBadge.className = `badge ${cls}`.trim();
}

async function callTool(toolID, args = {}) {
  const resp = await fetch("/api/tool/call", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tool_id: toolID, args }),
  });
  const raw = await resp.text();
  let data = null;
  try {
    data = raw ? JSON.parse(raw) : null;
  } catch (_) {}
  if (!resp.ok || !data || data.ok !== true) {
    const msg = (data && data.error && data.error.message) || raw || `http ${resp.status}`;
    throw new Error(msg);
  }
  return data.result || {};
}

function pretty(value) {
  return JSON.stringify(value, null, 2);
}

function selected() {
  return state.items.find((item) => item.surface_id === state.selectedID) || null;
}

function encodeBase64(text) {
  return btoa(unescape(encodeURIComponent(text || "")));
}

function decodeBase64(text) {
  return decodeURIComponent(escape(atob(text || "")));
}

function updateOverview() {
  els.totalCount.textContent = String(state.items.length);
  els.enabledCount.textContent = String(state.items.filter((item) => item.enabled).length);
  els.readyCount.textContent = String(state.items.filter((item) => item.enabled && item.status === "ok").length);
}

function renderList() {
  if (!state.items.length) {
    els.surfaceList.innerHTML = '<div class="item"><div class="muted">暂无 surface</div></div>';
    return;
  }
  els.surfaceList.innerHTML = state.items.map((item) => {
    const active = item.surface_id === state.selectedID ? "active" : "";
    const statusCls = item.status === "ok" ? "ok" : (item.status === "invalid" || item.status === "missing_entry" ? "err" : "warn-text");
    return `
      <div class="item ${active}" data-id="${item.surface_id}">
        <div class="row"><strong>${item.name || item.surface_id}</strong><span class="mono muted">${item.surface_id}</span></div>
        <div class="row">
          <span class="${statusCls}">${item.status}</span>
          <span class="muted">enabled=${item.enabled}</span>
          <span class="muted">${item.surface_type}</span>
        </div>
        <div class="muted mono">${item.entry_url || "-"}</div>
      </div>
    `;
  }).join("");
  els.surfaceList.querySelectorAll(".item[data-id]").forEach((itemEl) => {
    itemEl.addEventListener("click", () => selectSurface(itemEl.dataset.id).catch((err) => setStatus(err.message, "err")));
  });
}

async function refreshList() {
  setStatus("loading");
  const result = await callTool("ui.surface.catalog_list", {});
  state.items = Array.isArray(result.items) ? result.items : [];
  if (!state.selectedID && state.items[0]) state.selectedID = state.items[0].surface_id;
  updateOverview();
  renderList();
  if (state.selectedID) await selectSurface(state.selectedID);
  setStatus("ready", "ok");
}

async function selectSurface(surfaceID) {
  state.selectedID = surfaceID;
  state.sessionToken = "";
  renderList();
  const item = selected();
  if (!item) return;
  els.surfaceTitle.textContent = `${item.name || item.surface_id} / ${item.surface_type}`;
  els.surfaceEntry.textContent = item.entry_url || "-";
  els.sessionToken.textContent = "-";
  els.fileEditor.value = "";
  switchTab(els.pkgListBtn);
  await listPackageFiles();
}

async function listPackageFiles() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.package_list", { surface_id: item.surface_id });
  const items = Array.isArray(result.items) ? result.items.filter((entry) => !entry.is_dir) : [];
  els.fileSelect.innerHTML = items.map((entry) => `<option value="${entry.path}">${entry.path}</option>`).join("");
  els.infoPanel.textContent = pretty(result);
}

async function loadFile() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.package_read", { surface_id: item.surface_id, path: els.fileSelect.value });
  els.fileEditor.value = decodeBase64(result.data_base64 || "");
  els.infoPanel.textContent = pretty(result);
}

async function saveFile() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.package_write", {
    surface_id: item.surface_id,
    path: els.fileSelect.value,
    data_base64: encodeBase64(els.fileEditor.value || ""),
  });
  els.infoPanel.textContent = pretty(result);
  await refreshList();
  await selectSurface(item.surface_id);
  setStatus("saved", "ok");
}

async function toggleEnable() {
  const item = selected();
  if (!item) return;
  await callTool("ui.surface.enable_set", { surface_id: item.surface_id, enabled: !item.enabled });
  await refreshList();
  await selectSurface(item.surface_id);
}

async function cleanupCatalog() {
  const result = await callTool("ui.surface.catalog_cleanup", {});
  els.infoPanel.textContent = pretty(result);
  await refreshList();
  setStatus(result.deleted_count > 0 ? `cleaned ${result.deleted_count}` : "nothing to clean", result.deleted_count > 0 ? "ok" : "");
}

async function issueSession() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.session_issue", { surface_id: item.surface_id });
  state.sessionToken = result.surface_session_token || "";
  els.sessionToken.textContent = state.sessionToken || "-";
  els.infoPanel.textContent = pretty(result);
}

async function generate(openAfter) {
  const result = await callTool("ui.surface.generate", {
    surface_name: (els.generateName.value || "").trim(),
    prompt: els.generatePrompt.value || "",
  });
  state.selectedID = result.surface_id || state.selectedID;
  await refreshList();
  if (state.selectedID) await selectSurface(state.selectedID);
  if (openAfter && state.selectedID) {
    window.open(`/page/surface/lab.html?surface_id=${encodeURIComponent(state.selectedID)}`, "_blank");
  }
  setStatus("generated", "ok");
  els.generateDialog.close();
}

function switchTab(activeBtn) {
  [els.pkgListBtn, els.runtimeBtn, els.logsBtn].forEach(b => b.classList.remove("active"));
  activeBtn.classList.add("active");
}

async function loadRuntime() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.runtime_status", { surface_id: item.surface_id });
  els.infoPanel.textContent = pretty(result);
}

async function loadLogs() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.logs_query", { surface_id: item.surface_id, limit: 80 });
  els.infoPanel.textContent = pretty(result);
}

els.refreshBtn.addEventListener("click", () => refreshList().catch((err) => setStatus(err.message, "err")));
els.rescanBtn.addEventListener("click", () => callTool("ui.surface.rescan", {}).then(refreshList).catch((err) => setStatus(err.message, "err")));
els.cleanupBtn.addEventListener("click", () => cleanupCatalog().catch((err) => setStatus(err.message, "err")));
els.rebindBtn.addEventListener("click", () => callTool("ui.surface.rebind", {}).then(refreshList).catch((err) => setStatus(err.message, "err")));
els.toggleEnableBtn.addEventListener("click", () => toggleEnable().catch((err) => setStatus(err.message, "err")));
els.openBtn.addEventListener("click", () => {
  if (state.selectedID) window.open(`/page/surface/lab.html?surface_id=${encodeURIComponent(state.selectedID)}`, "_blank");
});
els.issueSessionBtn.addEventListener("click", () => issueSession().catch((err) => setStatus(err.message, "err")));
els.loadFileBtn.addEventListener("click", () => loadFile().catch((err) => setStatus(err.message, "err")));
els.saveFileBtn.addEventListener("click", () => saveFile().catch((err) => setStatus(err.message, "err")));
els.generateBtn.addEventListener("click", () => generate(false).catch((err) => setStatus(err.message, "err")));
els.generateOpenBtn.addEventListener("click", () => generate(true).catch((err) => setStatus(err.message, "err")));
els.openGenerateBtn.addEventListener("click", () => els.generateDialog.showModal());
els.closeGenerateBtn.addEventListener("click", () => els.generateDialog.close());

els.runtimeBtn.addEventListener("click", () => { switchTab(els.runtimeBtn); loadRuntime().catch((err) => setStatus(err.message, "err")); });
els.logsBtn.addEventListener("click", () => { switchTab(els.logsBtn); loadLogs().catch((err) => setStatus(err.message, "err")); });
els.pkgListBtn.addEventListener("click", () => { switchTab(els.pkgListBtn); listPackageFiles().catch((err) => setStatus(err.message, "err")); });

let isResizing = false;
let startY = 0;
let startHeight = 0;

els.paneDivider.addEventListener("mousedown", (e) => {
  isResizing = true;
  startY = e.clientY;
  startHeight = els.paneBottom.offsetHeight;
  document.body.style.cursor = "ns-resize";
  document.body.style.userSelect = "none";
  e.preventDefault();
});

document.addEventListener("mousemove", (e) => {
  if (!isResizing) return;
  const dy = startY - e.clientY;
  const newHeight = Math.max(100, Math.min(window.innerHeight - 120, startHeight + dy));
  els.paneBottom.style.height = `${newHeight}px`;
});

document.addEventListener("mouseup", () => {
  if (isResizing) {
    isResizing = false;
    document.body.style.cursor = "";
    document.body.style.userSelect = "";
  }
});

refreshList().catch((err) => setStatus(err.message, "err"));
