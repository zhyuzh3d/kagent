import { 
  refreshList, 
  executeLifecycleAction, 
  getServiceDetail, 
  updateConfig, 
  updateManifest, 
  runProbe, 
  generateService, 
  getFileList, 
  readFile, 
  writeFile 
} from "./components/logic.js";
import { renderServiceList, renderDrawerTab } from "./components/render.js";
import { state } from "./components/state.js";
import { pretty, parseJSON } from "./components/utils.js";

const els = {
  refreshBtn: document.getElementById("refreshBtn"),
  openGeneratorBtn: document.getElementById("openGeneratorBtn"),
  statusBadge: document.getElementById("statusBadge"),
  totalToolsCount: document.getElementById("totalToolsCount"),
  serviceCount: document.getElementById("serviceCount"),
  
  // Main Tabs
  mainTabBtns: document.querySelectorAll(".header-tab"),
  sections: document.querySelectorAll(".tab-section"),
  toolsTableBody: document.getElementById("toolsTableBody"),
  toolsEmptyMsg: document.getElementById("toolsEmptyMsg"),
  serviceEmptyMsg: document.getElementById("serviceEmptyMsg"),
  
  // Drawer
  drawer: document.getElementById("sideDrawer"),
  drawerOverlay: document.getElementById("drawerOverlay"),
  drawerTitle: document.getElementById("drawerTitle"),
  drawerSubTitle: document.getElementById("drawerSubTitle"),
  drawerContent: document.getElementById("drawerContent"),
  drawerSaveBtn: document.getElementById("drawerSaveBtn"),
  drawerCancelBtn: document.getElementById("drawerCancelBtn"),
  closeDrawerBtn: document.getElementById("closeDrawerBtn"),
  tabBtns: document.querySelectorAll(".tab-btn"),
  
  // Generator Modal
  genModal: document.getElementById("genModal"),
  genModalBody: document.getElementById("genModalBody"),
  closeGenBtn: document.getElementById("closeGenBtn"),
  cancelGenBtn: document.getElementById("cancelGenBtn"),
  runGenBtn: document.getElementById("runGenBtn"),
};

// ── Main Tab Management ──────────────────

function switchMainTab(tabName) {
  state.activeMainTab = tabName;
  els.mainTabBtns.forEach(btn => {
    btn.classList.toggle("active", btn.dataset.tab === tabName);
  });
  els.sections.forEach(sec => {
    sec.classList.toggle("active", sec.id === `section${tabName.charAt(0).toUpperCase() + tabName.slice(1)}`);
  });
  if (tabName === 'tools') {
    renderToolsTable();
  } else {
    renderServiceList(els, handleAction);
  }
}

els.mainTabBtns.forEach(btn => {
  btn.onclick = () => switchMainTab(btn.dataset.tab);
});

// ── Drawer Management ──────────────────

function openDrawer(serviceID) {
  state.selectedID = serviceID;
  const svc = state.managed.find(s => s.service_id === serviceID);
  els.drawerTitle.textContent = serviceID;
  els.drawerSubTitle.textContent = svc ? svc.dir : "";
  els.drawer.classList.add("active");
  els.drawerOverlay.classList.add("active");
  
  // Default to lifecycle tab
  switchTab("lifecycle");
}

function closeDrawer() {
  els.drawer.classList.remove("active");
  els.drawerOverlay.classList.remove("active");
}

function switchTab(tabName) {
  state.activeModal = tabName; // Reuse activeModal as activeTab
  els.tabBtns.forEach(btn => {
    btn.classList.toggle("active", btn.dataset.tab === tabName);
  });
  renderActiveTab();
}

els.closeDrawerBtn.onclick = closeDrawer;
els.drawerCancelBtn.onclick = closeDrawer;
els.drawerOverlay.onclick = closeDrawer;
els.tabBtns.forEach(btn => {
  btn.onclick = () => switchTab(btn.dataset.tab);
});

// ── Dashboard Actions ──────────────────

async function handleAction(type, action, serviceID) {
  if (type === 'drawer') {
    openDrawer(serviceID);
  }
}

async function renderActiveTab() {
  const serviceID = state.selectedID;
  const tab = state.activeModal;
  
  els.drawerSaveBtn.style.display = "none";
  
  if (tab === 'lifecycle') {
    renderDrawerTab(tab, {}, {
      onLifecycle: async (action) => {
        await executeLifecycleAction(serviceID, action, () => renderServiceList(els, handleAction));
      }
    });
  } 
  else if (tab === 'tools') {
    const detail = await getServiceDetail(serviceID);
    const tools = (detail.service && detail.service.manifest && detail.service.manifest.provides) || [];
    renderDrawerTab(tab, { tools }, {});
  }
  else if (tab === 'config') {
    let currentType = 'config';
    const loadConfig = async (type) => {
      const detail = await getServiceDetail(serviceID);
      let content = "";
      if (type === 'config') content = pretty(detail.config);
      else if (type === 'configx') {
        const res = await readFile(serviceID, "config/configx.json").catch(() => null);
        if (res === null) {
          // Try loading example
          const example = await readFile(serviceID, "config/configx.json.example").catch(() => "");
          content = example || "{}";
          if (example) console.log("Loaded configx.json.example as fallback");
        } else {
          content = res;
        }
      }
      else content = pretty(detail.runtime_manifest);
      
      renderDrawerTab(tab, content, {
        onConfigTypeChange: (newType) => { currentType = newType; loadConfig(newType); }
      });
      els.drawerSaveBtn.style.display = "inline-flex";
      els.drawerSaveBtn.onclick = async () => {
        const val = document.getElementById("configEditor").value;
        if (currentType === 'config') await updateConfig(serviceID, parseJSON(val));
        else if (currentType === 'configx') await writeFile(serviceID, "config/configx.json", val);
        else await updateManifest(serviceID, parseJSON(val));
        alert("保存成功");
      };
    };
    await loadConfig('config');
  }
  else if (tab === 'ops') {
    renderDrawerTab(tab, {}, {
      onProbe: async () => {
        const toolID = document.getElementById("pToolID").value;
        const args = parseJSON(document.getElementById("pArgs").value);
        const out = await runProbe(serviceID, toolID, args);
        document.getElementById("pOutput").textContent = pretty(out);
      },
      onAudit: async () => {
        const detail = await getServiceDetail(serviceID);
        document.getElementById("auditOutput").textContent = pretty(detail.audit || "暂无审计日志");
      }
    });
  }
  else if (tab === 'files') {
    const files = await getFileList(serviceID);
    renderDrawerTab(tab, { files }, {
      onFileLoad: async (path) => {
        const text = await readFile(serviceID, path);
        const editor = document.createElement("textarea");
        editor.className = "editor mono";
        editor.style.marginTop = "12px";
        editor.value = text;
        const container = document.getElementById("drawerContent");
        const oldEditor = container.querySelector("textarea");
        if (oldEditor) oldEditor.remove();
        container.appendChild(editor);
        
        els.drawerSaveBtn.style.display = "inline-flex";
        els.drawerSaveBtn.onclick = async () => {
          await writeFile(serviceID, path, editor.value);
          alert("文件已保存");
        };
      }
    });
  }
}

window.toggleToolRow = (idx) => {
  const row = document.getElementById(`tool-exp-${idx}`);
  if (row) row.classList.toggle("active");
};

window.openServiceDetails = (serviceID) => {
  openDrawer(serviceID);
};

// ── Tool Registry ──────────────────────

function renderToolsTable() {
  const allTools = [];
  state.managed.forEach(srv => {
    if (srv.manifest && srv.manifest.provides) {
      srv.manifest.provides.forEach(p => {
        allTools.push({ ...p, serviceID: srv.service_id });
      });
    }
  });

  allTools.sort((a, b) => a.tool_id.localeCompare(b.tool_id));
  if (els.totalToolsCount) els.totalToolsCount.textContent = allTools.length;

  if (allTools.length === 0) {
    els.toolsTableBody.innerHTML = "";
    els.toolsEmptyMsg.style.display = "block";
    return;
  }
  els.toolsEmptyMsg.style.display = "none";

  els.toolsTableBody.innerHTML = allTools.map((t, idx) => `
    <tr class="tool-row" onclick="window.toggleToolRow(${idx})">
      <td class="tool-id-cell">${t.tool_id}</td>
      <td style="color: var(--muted); font-size: 13px;">${t.description || "原子工具能力说明"}</td>
      <td>
        <a href="#" class="service-link" onclick="event.stopPropagation(); event.preventDefault(); window.openServiceDetails('${t.serviceID}')">
          ${t.serviceID}
        </a>
      </td>
      <td style="font-size: 11px; color: var(--muted); text-transform: uppercase;">
        ${t.streaming ? 'Streaming' : 'REST/RPC'}
      </td>
      <td style="text-align: right;">
        <button class="btn-row-action" onclick="event.stopPropagation(); window.toggleToolRow(${idx})">协议</button>
      </td>
    </tr>
    <tr class="tool-expand-row" id="tool-exp-${idx}">
      <td colspan="5">
        <div class="expand-content">
          <div class="expand-column">
            <h5>入参定义 (Input Schema)</h5>
            <pre class="mono result-box" style="background: #f8fafc; padding: 12px; font-size: 11px; border: 1px solid var(--line); border-radius: 6px; max-height: 400px; overflow-y: auto;">${JSON.stringify(t.input_schema || {}, null, 2)}</pre>
          </div>
          <div class="expand-column">
            <h5>响应定义 (Output Schema)</h5>
            <pre class="mono result-box" style="background: #f8fafc; padding: 12px; font-size: 11px; border: 1px solid var(--line); border-radius: 6px; max-height: 400px; overflow-y: auto;">${JSON.stringify(t.output_schema || {}, null, 2)}</pre>
          </div>
        </div>
      </td>
    </tr>
  `).join("");
}

// ── Global Actions ────────────────────

els.refreshBtn.onclick = () => refreshList(onRefreshDone);

const closeGen = () => els.genModal.classList.remove("active");
els.closeGenBtn.onclick = closeGen;
els.cancelGenBtn.onclick = closeGen;

els.openGeneratorBtn.onclick = () => {
  const tpl = document.getElementById("tpl-generator");
  els.genModalBody.innerHTML = "";
  els.genModalBody.appendChild(tpl.content.cloneNode(true));
  els.genModal.classList.add("active");
};

els.runGenBtn.onclick = async () => {
  const name = document.getElementById("genName").value.trim();
  const prompt = document.getElementById("genPrompt").value.trim();
  const build = document.getElementById("genBuildFlag").checked;
  
  if (!name || !prompt) {
    alert("请填写服务名称和 Prompt 指令");
    return;
  }
  
  els.runGenBtn.disabled = true;
  els.runGenBtn.innerText = "正在生成...";
  
  try {
    const res = await generateService(name, prompt);
    if (res.ok) {
      alert(`服务 ${name} 创建成功!`);
      closeGen();
      refreshList(onRefreshDone);
    } else {
      alert("生成失败: " + (res.error?.message || "内部错误"));
    }
  } catch (e) {
    alert("发生错误: " + e.message);
  } finally {
    els.runGenBtn.disabled = false;
    els.runGenBtn.innerText = "生成并初始化";
  }
};

// ── Init ──────────────────────────────

function onRefreshDone() {
  if (els.serviceCount) els.serviceCount.textContent = state.managed.length;
  
  // Update total tools count
  let totalTools = 0;
  state.managed.forEach(s => {
    if (s.manifest && s.manifest.provides) totalTools += s.manifest.provides.length;
  });
  if (els.totalToolsCount) els.totalToolsCount.textContent = totalTools;

  if (state.activeMainTab === 'tools') {
    renderToolsTable();
  } else {
    renderServiceList(els, handleAction);
  }
};

refreshList(onRefreshDone);

// Initial tab
state.activeMainTab = 'services';
