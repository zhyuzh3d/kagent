const els = {
  refreshBtn: document.getElementById("refreshBtn"),
  rescanBtn: document.getElementById("rescanBtn"),
  rebindBtn: document.getElementById("rebindBtn"),
  statusBadge: document.getElementById("statusBadge"),
  totalCount: document.getElementById("totalCount"),
  enabledCount: document.getElementById("enabledCount"),
  readyCount: document.getElementById("readyCount"),
  surfaceList: document.getElementById("surfaceList"),
  surfaceTitle: document.getElementById("surfaceTitle"),
  surfaceEntry: document.getElementById("surfaceEntry"),
  sessionToken: document.getElementById("sessionToken"),
  capToken: document.getElementById("capToken"),
  toggleEnableBtn: document.getElementById("toggleEnableBtn"),
  openBtn: document.getElementById("openBtn"),
  issueSessionBtn: document.getElementById("issueSessionBtn"),
  issueCapBtn: document.getElementById("issueCapBtn"),
  fileSelect: document.getElementById("fileSelect"),
  loadFileBtn: document.getElementById("loadFileBtn"),
  saveFileBtn: document.getElementById("saveFileBtn"),
  fileEditor: document.getElementById("fileEditor"),
  generateName: document.getElementById("generateName"),
  generatePrompt: document.getElementById("generatePrompt"),
  generateBtn: document.getElementById("generateBtn"),
  generateOpenBtn: document.getElementById("generateOpenBtn"),
  runtimeBtn: document.getElementById("runtimeBtn"),
  logsBtn: document.getElementById("logsBtn"),
  pkgListBtn: document.getElementById("pkgListBtn"),
  infoPanel: document.getElementById("infoPanel"),
};

const state = {
  items: [],
  selectedID: "",
  sessionToken: "",
  capabilityToken: "",
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
  state.capabilityToken = "";
  renderList();
  const item = selected();
  if (!item) return;
  els.surfaceTitle.textContent = `${item.name || item.surface_id} / ${item.surface_type}`;
  els.surfaceEntry.textContent = item.entry_url || "-";
  els.sessionToken.textContent = "-";
  els.capToken.textContent = "-";
  els.fileEditor.value = "";
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

async function issueSession() {
  const item = selected();
  if (!item) return;
  const result = await callTool("ui.surface.session_issue", { surface_id: item.surface_id });
  state.sessionToken = result.surface_session_token || "";
  els.sessionToken.textContent = state.sessionToken || "-";
  els.infoPanel.textContent = pretty(result);
}

async function issueCapability() {
  const item = selected();
  if (!item) return;
  if (!state.sessionToken) await issueSession();
  const result = await callTool("ui.surface.capability_issue", {
    surface_session_token: state.sessionToken,
    scope: "fs.write",
    path_prefix: ".",
  });
  state.capabilityToken = result.capability_token || "";
  els.capToken.textContent = state.capabilityToken || "-";
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
    window.open(`/page/surface/index.html?surface_id=${encodeURIComponent(state.selectedID)}`, "_blank");
  }
  setStatus("generated", "ok");
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
els.rebindBtn.addEventListener("click", () => callTool("ui.surface.rebind", {}).then(refreshList).catch((err) => setStatus(err.message, "err")));
els.toggleEnableBtn.addEventListener("click", () => toggleEnable().catch((err) => setStatus(err.message, "err")));
els.openBtn.addEventListener("click", () => {
  if (state.selectedID) window.open(`/page/surface/index.html?surface_id=${encodeURIComponent(state.selectedID)}`, "_blank");
});
els.issueSessionBtn.addEventListener("click", () => issueSession().catch((err) => setStatus(err.message, "err")));
els.issueCapBtn.addEventListener("click", () => issueCapability().catch((err) => setStatus(err.message, "err")));
els.loadFileBtn.addEventListener("click", () => loadFile().catch((err) => setStatus(err.message, "err")));
els.saveFileBtn.addEventListener("click", () => saveFile().catch((err) => setStatus(err.message, "err")));
els.generateBtn.addEventListener("click", () => generate(false).catch((err) => setStatus(err.message, "err")));
els.generateOpenBtn.addEventListener("click", () => generate(true).catch((err) => setStatus(err.message, "err")));
els.runtimeBtn.addEventListener("click", () => loadRuntime().catch((err) => setStatus(err.message, "err")));
els.logsBtn.addEventListener("click", () => loadLogs().catch((err) => setStatus(err.message, "err")));
els.pkgListBtn.addEventListener("click", () => listPackageFiles().catch((err) => setStatus(err.message, "err")));

refreshList().catch((err) => setStatus(err.message, "err"));
