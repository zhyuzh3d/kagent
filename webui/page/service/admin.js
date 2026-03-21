import {
  executeLifecycleAction,
  generateService,
  getFileList,
  getServiceDetail,
  readFile,
  refreshList,
  runProbe,
  updateConfig,
  updateGovernance,
  updateManifest,
  writeFile,
} from "./components/logic.js";
import { renderDrawerTab, renderServiceList } from "./components/render.js";
import { state } from "./components/state.js";
import { escapeHTML, parseJSON, pretty, protocolLabel, showToast } from "./components/utils.js";

const els = {
  refreshBtn: document.getElementById("refreshBtn"),
  openGeneratorBtn: document.getElementById("openGeneratorBtn"),
  statusBadge: document.getElementById("statusBadge"),
  totalToolsCount: document.getElementById("totalToolsCount"),
  serviceCount: document.getElementById("serviceCount"),
  managedCount: document.getElementById("managedCount"),
  liveCount: document.getElementById("liveCount"),
  healthyCount: document.getElementById("healthyCount"),
  riskyCount: document.getElementById("riskyCount"),
  mainTabBtns: document.querySelectorAll(".header-tab"),
  sections: document.querySelectorAll(".tab-section"),
  toolsTableBody: document.getElementById("toolsTableBody"),
  toolsEmptyMsg: document.getElementById("toolsEmptyMsg"),
  serviceEmptyMsg: document.getElementById("serviceEmptyMsg"),
  drawer: document.getElementById("sideDrawer"),
  drawerOverlay: document.getElementById("drawerOverlay"),
  drawerTitle: document.getElementById("drawerTitle"),
  drawerSubTitle: document.getElementById("drawerSubTitle"),
  drawerSaveBtn: document.getElementById("drawerSaveBtn"),
  drawerCancelBtn: document.getElementById("drawerCancelBtn"),
  closeDrawerBtn: document.getElementById("closeDrawerBtn"),
  tabBtns: document.querySelectorAll(".tab-btn"),
  genModal: document.getElementById("genModal"),
  genModalBody: document.getElementById("genModalBody"),
  closeGenBtn: document.getElementById("closeGenBtn"),
  cancelGenBtn: document.getElementById("cancelGenBtn"),
  runGenBtn: document.getElementById("runGenBtn"),
};

function setMainTab(tabName) {
  state.activeMainTab = tabName;
  els.mainTabBtns.forEach((btn) => btn.classList.toggle("active", btn.dataset.tab === tabName));
  els.sections.forEach((section) => section.classList.toggle("active", section.id === `section${tabName.charAt(0).toUpperCase()}${tabName.slice(1)}`));
  if (tabName === "tools") {
    renderToolsTable();
  } else {
    renderServiceList(els, handleAction);
  }
}

function openDrawer(serviceID) {
  const service = state.managed.find((item) => item.service_id === serviceID);
  if (!service) return;
  state.selectedID = serviceID;
  els.drawerTitle.textContent = serviceID;
  els.drawerSubTitle.textContent = service.dir || service.dir_abs || "托管服务详情";
  els.drawer.classList.add("active");
  els.drawerOverlay.classList.add("active");
  setDrawerTab("governance");
}

function closeDrawer() {
  els.drawer.classList.remove("active");
  els.drawerOverlay.classList.remove("active");
}

function setDrawerTab(tabName) {
  state.activeModal = tabName;
  els.tabBtns.forEach((btn) => btn.classList.toggle("active", btn.dataset.tab === tabName));
  renderActiveTab().catch((err) => showToast(err.message || String(err), "error"));
}

async function handleAction(type, action, serviceID) {
  if (type === "drawer" && action === "open") {
    openDrawer(serviceID);
  }
}

async function renderActiveTab() {
  const serviceID = state.selectedID;
  const selectedService = state.managed.find((item) => item.service_id === serviceID) || null;
  const tab = state.activeModal;
  els.drawerSaveBtn.style.display = "none";

  if (tab === "governance") {
    const detail = await getServiceDetail(serviceID);
    const service = detail.service || selectedService || {};
    renderDrawerTab(tab, {
      service,
      enabled: !!service.enabled,
      reliability: service.reliability || "unverified",
    }, {
      onGovernanceToggle: async () => {
        const nextEnabled = !service.enabled;
        await updateGovernance(serviceID, {
          enabled: nextEnabled,
          reliability: service.reliability || "unverified",
        });
        await refreshList(onRefreshDone);
        await renderActiveTab();
        showToast(nextEnabled ? "已启用服务治理" : "已禁用服务治理");
      },
      onGovernanceAction: async (action) => {
        await executeLifecycleAction(serviceID, action, onRefreshDone);
        showToast(`${action} 成功`);
        await renderActiveTab();
      },
    });
    els.drawerSaveBtn.style.display = "inline-flex";
    els.drawerSaveBtn.onclick = async () => {
      const reliability = document.getElementById("govReliabilitySelect")?.value || "unverified";
      await updateGovernance(serviceID, {
        enabled: !!service.enabled,
        reliability,
      });
      await refreshList(onRefreshDone);
      await renderActiveTab();
      showToast("治理配置已保存");
    };
    return;
  }

  if (tab === "lifecycle") {
    renderDrawerTab(tab, { service: selectedService }, {
      onLifecycle: async (action) => {
        await executeLifecycleAction(serviceID, action, onRefreshDone);
        showToast(`${action} 成功`);
        if (action !== "build") {
          await renderActiveTab();
        }
      },
    });
    return;
  }

  if (tab === "tools") {
    const detail = await getServiceDetail(serviceID);
    renderDrawerTab(tab, { tools: Array.isArray(detail.tool_views) ? detail.tool_views : [] }, {});
    return;
  }

  if (tab === "config") {
    let currentType = "config";
    const loadConfig = async (type) => {
      const detail = await getServiceDetail(serviceID);
      let content = "";
      if (type === "config") content = pretty(detail.config || {});
      else if (type === "configx") {
        content = pretty(detail.configx || {});
      } else if (type === "startup_manifest") {
        content = pretty(detail.startup_manifest || {});
      } else {
        content = pretty(detail.runtime_manifest || {});
      }
      renderDrawerTab(tab, content, {
        onConfigTypeChange: async (nextType) => {
          currentType = nextType;
          await loadConfig(nextType);
        },
      });
      els.drawerSaveBtn.style.display = "inline-flex";
        els.drawerSaveBtn.onclick = async () => {
          const raw = document.getElementById("configEditor").value;
          if (currentType === "config" || currentType === "configx") {
            await updateConfig(serviceID, parseJSON(raw), currentType);
          } else if (currentType === "startup_manifest") {
            await updateManifest(serviceID, parseJSON(raw));
          } else {
            showToast("运行时事实清单由服务注册自动写入，不允许手工编辑", "error");
            return;
          }
          await refreshList(onRefreshDone);
          showToast("保存成功");
        };
    };
    await loadConfig(currentType);
    return;
  }

  if (tab === "ops") {
    renderDrawerTab(tab, {}, {
      onProbe: async () => {
        const toolID = document.getElementById("pToolID").value.trim();
        const args = parseJSON(document.getElementById("pArgs").value);
        const output = await runProbe(serviceID, toolID, args);
        document.getElementById("pOutput").textContent = pretty(output);
      },
      onAudit: async () => {
        const detail = await getServiceDetail(serviceID);
        document.getElementById("auditOutput").textContent = pretty(detail.audits || []);
      },
    });
    return;
  }

  if (tab === "files") {
    const files = await getFileList(serviceID);
    renderDrawerTab(tab, { files }, {
      onFileLoad: async (path) => {
        const text = await readFile(serviceID, path);
        const editor = document.getElementById("fEditor");
        editor.value = text;
        els.drawerSaveBtn.style.display = "inline-flex";
        els.drawerSaveBtn.onclick = async () => {
          await writeFile(serviceID, path, editor.value);
          showToast("文件已保存");
        };
      },
    });
  }
}

function renderToolsTable() {
  const tools = state.tools || [];
  if (els.totalToolsCount) els.totalToolsCount.textContent = String(tools.length);
  if (!tools.length) {
    els.toolsTableBody.innerHTML = "";
    els.toolsEmptyMsg.style.display = "block";
    return;
  }
  els.toolsEmptyMsg.style.display = "none";
  els.toolsTableBody.innerHTML = tools.map((tool, index) => {
    const spec = tool.spec || {};
    const observed = tool.observed || {};
    const governance = tool.governance || {};
    const candidateSummary = (tool.candidates || []).slice(0, 2).map((candidate) => candidate.service_id).join(", ");
    return `
      <tr class="tool-row" onclick="window.toggleToolRow(${index})">
        <td class="tool-id-cell">${escapeHTML(tool.tool_id || spec.tool_id || "-")}</td>
        <td>
          <div>${escapeHTML(spec.description || "无描述")}</div>
          <div class="cell-note">scope=${escapeHTML((spec.scope_support || []).join(", ") || "none")} · effects=${escapeHTML(spec.side_effect || (spec.has_effects ? "write" : "read"))}</div>
        </td>
        <td>
          <a href="#" class="service-link" onclick="event.preventDefault(); event.stopPropagation(); window.openServiceDetails('${escapeHTML(governance.bound_service_id || tool.service_id || "")}')">${escapeHTML(governance.bound_service_id || tool.service_id || "unbound")}</a>
          <div class="cell-note">${escapeHTML(candidateSummary || "无候选")}</div>
        </td>
        <td>
          <div class="mono-cell">${escapeHTML(protocolLabel(spec))}</div>
          <div class="cell-note">${Number(observed.healthy_instance_count || 0)} healthy</div>
        </td>
        <td class="table-actions">
          <button class="btn-row-action" onclick="event.stopPropagation(); window.toggleToolRow(${index})">详情</button>
        </td>
      </tr>
      <tr class="tool-expand-row" id="tool-exp-${index}">
        <td colspan="5">
          <div class="expand-content">
            <div class="expand-column">
              <h5>声明事实 / Spec</h5>
              <pre class="mono result-box">${escapeHTML(pretty(spec))}</pre>
            </div>
            <div class="expand-column">
              <h5>观察与治理 / Observed + Governance</h5>
              <pre class="mono result-box">${escapeHTML(pretty({ observed, governance, candidates: tool.candidates || [] }))}</pre>
            </div>
          </div>
        </td>
      </tr>
    `;
  }).join("");
}

function onRefreshDone() {
  const services = state.managed || [];
  const tools = state.tools || [];
  const liveCount = services.filter((item) => item.active).length;
  const healthyCount = services.filter((item) => item.healthy).length;
  const riskyCount = tools.filter((item) => Number(item?.spec?.risk_lv || 0) >= 4).length;
  const managedCount = services.filter((item) => item.is_managed).length;

  if (els.serviceCount) els.serviceCount.textContent = String(services.length);
  if (els.totalToolsCount) els.totalToolsCount.textContent = String(tools.length);
  if (els.managedCount) els.managedCount.textContent = String(managedCount);
  if (els.liveCount) els.liveCount.textContent = String(liveCount);
  if (els.healthyCount) els.healthyCount.textContent = String(healthyCount);
  if (els.riskyCount) els.riskyCount.textContent = String(riskyCount);

  if (state.activeMainTab === "tools") renderToolsTable();
  else renderServiceList(els, handleAction);
}

window.toggleToolRow = (index) => {
  const row = document.getElementById(`tool-exp-${index}`);
  if (row) row.classList.toggle("active");
};

window.openServiceDetails = (serviceID) => {
  if (!serviceID) return;
  openDrawer(serviceID);
};

els.mainTabBtns.forEach((btn) => btn.addEventListener("click", () => setMainTab(btn.dataset.tab)));
els.closeDrawerBtn.onclick = closeDrawer;
els.drawerCancelBtn.onclick = closeDrawer;
els.drawerOverlay.onclick = closeDrawer;
els.tabBtns.forEach((btn) => btn.addEventListener("click", () => setDrawerTab(btn.dataset.tab)));
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
    showToast("请填写服务名称和 Prompt 指令", "error");
    return;
  }
  els.runGenBtn.disabled = true;
  els.runGenBtn.textContent = "正在生成...";
  try {
    const result = await generateService(name, prompt, build);
    if (!result?.service_id) throw new Error("生成结果缺少 service_id");
    closeGen();
    await refreshList(onRefreshDone);
    showToast(`服务 ${result.service_id} 已生成`);
  } catch (err) {
    showToast(err.message || String(err), "error");
  } finally {
    els.runGenBtn.disabled = false;
    els.runGenBtn.textContent = "生成并初始化";
  }
};

state.activeMainTab = "services";
state.activeModal = "governance";
refreshList(onRefreshDone);
