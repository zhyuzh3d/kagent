import { state } from "./state.js";
import { escapeHTML, formatPercent, formatTime, pretty, protocolLabel } from "./utils.js";

function statusMeta(service) {
  if (service.is_managed && !service.enabled) return { cls: "disabled", text: "已禁用" };
  const startupStatus = String(service?.startup?.status || "").toLowerCase();
  const status = String(service?.status || "").toLowerCase() || startupStatus;
  if (status === "active") return { cls: "running", text: "运行中" };
  if (status === "ready") return { cls: "running", text: "就绪" };
  if (status === "registered") return { cls: "running", text: "已注册" };
  if (status === "skipped") return { cls: "warning", text: "已跳过" };
  if (status === "failed") return { cls: "stopped", text: "启动失败" };
  if (status === "down" || status === "stopped") return { cls: "stopped", text: "已停止" };
  if (status === "conflict") return { cls: "warning", text: "冲突" };
  return { cls: "offline", text: service?.registered ? "已注册" : "未注册" };
}

function shouldHideStartupIssue(service) {
  const status = String(service?.status || "").toLowerCase();
  const live = status === "active" || status === "ready" || status === "registered";
  return live && !!service?.healthy;
}

function trimPath(path) {
  if (!path || !state.appRoot) return path;
  if (path.startsWith(state.appRoot)) {
    let sub = path.substring(state.appRoot.length);
    if (sub.startsWith("/") || sub.startsWith("\\")) {
      sub = sub.substring(1);
    }
    return sub ? `./${sub}` : "./";
  }
  return path;
}

function renderServiceFacts(service) {
  const startupStatus = shouldHideStartupIssue(service) ? "" : String(service?.startup?.status || "").trim();
  return `
    <div class="service-facts">
      <span class="pill subtle">${service?.is_managed ? "托管" : "仅注册"}</span>
      <span class="pill ${service.enabled ? "success" : "warning"}">${service.enabled ? "已启用" : "已禁用"}</span>
      <span class="pill">${escapeHTML(service.reliability || "unverified")}</span>
      <span class="pill mono">${service?.tool_count || 0} tools</span>
      ${startupStatus ? `<span class="pill">${escapeHTML(`startup:${startupStatus}`)}</span>` : ""}
    </div>
  `;
}

export function renderServiceList(els, onAction) {
  const tbody = document.getElementById("serviceTableBody");
  const emptyMsg = document.getElementById("serviceEmptyMsg");
  if (!tbody) return;

  const services = state.managed || [];
  if (!services.length) {
    if (emptyMsg) emptyMsg.style.display = "block";
    tbody.innerHTML = "";
    return;
  }

  if (emptyMsg) emptyMsg.style.display = "none";
  tbody.innerHTML = services.map((service) => {
    const status = statusMeta(service);
    const canManage = Boolean(service?.is_managed);
    const startupError = shouldHideStartupIssue(service) ? "" : String(service?.startup?.error || "").trim();
    return `
      <tr data-id="${escapeHTML(service.service_id)}">
        <td class="service-primary">
          <div class="service-title">${escapeHTML(service.service_id)}</div>
          <div class="service-subtitle">${escapeHTML(service.description || trimPath(service.dir) || "未提供描述")}</div>
          ${renderServiceFacts(service)}
        </td>
        <td>
          <span class="status-badge ${status.cls}">${status.text}</span>
          <div class="cell-note">${escapeHTML(service.instance_id || "未分配实例")}</div>
          ${startupError ? `<div class="cell-note err-text">${escapeHTML(startupError)}</div>` : ""}
        </td>
        <td class="mono-cell">
          <div>${service.pid || "-"}</div>
          <div class="cell-note">${escapeHTML(service.endpoint || "未暴露端点")}</div>
        </td>
        <td>
          <strong class="${service.healthy ? "ok-text" : "err-text"}">${service.healthy ? "健康" : "异常"}</strong>
          <div class="cell-note">${escapeHTML(formatTime(service.last_seen_at_ms))}</div>
        </td>
        <td class="mono-cell">
          <div>${escapeHTML(service.registered_manifest?.version || service.registration?.version || "-")}</div>
          <div class="cell-note">${escapeHTML(trimPath(service.startup_manifest_path) || trimPath(service.config_path) || "无启动路径")}</div>
        </td>
        <td class="table-actions">
          <button class="action-btn ${canManage ? "primary" : ""}" data-action="manage" data-id="${escapeHTML(service.service_id)}" ${canManage ? "" : "disabled"}>${canManage ? "治理详情" : "只读注册"}</button>
        </td>
      </tr>
    `;
  }).join("");

  tbody.querySelectorAll('button[data-action="manage"]').forEach((btn) => {
    btn.addEventListener("click", () => onAction("drawer", "open", btn.dataset.id));
  });
}

function renderToolCard(tool) {
  const spec = tool?.spec || {};
  const observed = tool?.observed || {};
  const governance = tool?.governance || {};
  const candidates = Array.isArray(tool?.candidates) ? tool.candidates : [];
  return `
    <article class="tool-card collapsed">
      <div class="tool-card-head expandable-head">
        <div>
          <div class="tool-id-label">${escapeHTML(tool.tool_id || spec.tool_id || "-")}</div>
          <div class="tool-desc">${escapeHTML(spec.description || "无描述")}</div>
        </div>
        <div class="tool-head-meta">
          <span class="pill">${escapeHTML(protocolLabel(spec))}</span>
          <span class="pill mono">${escapeHTML(governance.bound_service_id || tool.service_id || "unbound")}</span>
          <span class="toggle-arrow"></span>
        </div>
      </div>
      <div class="tool-card-body">
        <div class="tool-grid">
          <div>
            <div class="tool-metric">副作用</div>
            <div>${escapeHTML(spec.side_effect || (spec.has_effects ? "write" : "read"))}</div>
          </div>
          <div>
            <div class="tool-metric">风险等级</div>
            <div>${Number(spec.risk_lv || 0)}</div>
          </div>
          <div>
            <div class="tool-metric">治理状态</div>
            <div>${escapeHTML(governance.binding_reason || (governance.enabled ? "auto" : governance.conflict_reason || "disabled"))}</div>
          </div>
          <div>
            <div class="tool-metric">健康实例</div>
            <div>${Number(observed.healthy_instance_count || 0)}</div>
          </div>
          <div>
            <div class="tool-metric">成功率</div>
            <div>${escapeHTML(formatPercent(governance.success_rate))}</div>
          </div>
          <div>
            <div class="tool-metric">调用次数</div>
            <div>${Number(governance.call_count || 0)}</div>
          </div>
        </div>
        <div class="tool-sections">
          <div class="schema-block">
            <h5>输入定义</h5>
            <pre class="mono result-box">${escapeHTML(pretty(spec.input_schema || {}))}</pre>
          </div>
          <div class="schema-block">
            <h5>输出定义</h5>
            <pre class="mono result-box">${escapeHTML(pretty(spec.output_schema || {}))}</pre>
          </div>
        </div>
        <div class="tool-extra">
          <div class="tool-meta-row">
            <span>Caller: <b>${escapeHTML((spec.allowed_caller_types || []).join(", ") || "all")}</b></span>
            <span>Scope: <b>${escapeHTML((spec.scope_support || []).join(", ") || "none")}</b></span>
            <span>Capability: <b>${escapeHTML((spec.capabilities_required || []).join(", ") || "none")}</b></span>
          </div>
          <div class="tool-meta-row">
            <span>Endpoint: <b class="mono">${escapeHTML(observed.endpoint || "n/a")}</b></span>
            <span>最近上报: <b>${escapeHTML(formatTime(observed.last_seen_at_ms))}</b></span>
            <span>${spec.hub_only ? "仅 Hub 可信入口" : "可经标准路由访问"}</span>
          </div>
        </div>
        <div class="candidate-list">
          ${(candidates.length ? candidates : [{ service_id: "none", service_name: "无候选者", enabled: false }]).map((candidate) => `
            <div class="candidate-item">
              <div>
                <strong class="mono">${escapeHTML(candidate.service_id || "-")}</strong>
                <div class="cell-note">${escapeHTML(candidate.service_name || "")}</div>
              </div>
              <div class="cell-note">${escapeHTML(candidate.status || "")}</div>
              <div class="cell-note">${escapeHTML(formatPercent(candidate.success_rate))}</div>
            </div>
          `).join("")}
        </div>
      </div>
    </article>
  `;
}

export function renderDrawerTab(tabName, data, handlers) {
  const content = document.getElementById("drawerContent");
  const tpl = document.getElementById(`tpl-${tabName}`);
  if (!content || !tpl) return;

  content.innerHTML = "";
  content.appendChild(tpl.content.cloneNode(true));

  if (tabName === "governance") {
    const info = data?.service || {};
    const infoCard = document.getElementById("govInfo");
    const toggleBtn = document.getElementById("govToggleBtn");
    const buildBtn = document.getElementById("govBuildBtn");
    const drainBtn = document.getElementById("govDrainBtn");
    const reliabilitySelect = document.getElementById("govReliabilitySelect");
    if (infoCard) {
      infoCard.innerHTML = `
        <div class="drawer-summary">
          <div>
            <div class="summary-label">服务 ID</div>
            <div class="summary-value mono">${escapeHTML(info.service_id || "-")}</div>
          </div>
          <div>
            <div class="summary-label">服务版本</div>
            <div class="summary-value mono">${escapeHTML(info.registered_manifest?.version || info.registration?.version || "-")}</div>
          </div>
          <div>
            <div class="summary-label">运行状态</div>
            <div class="summary-value">${escapeHTML(statusMeta(info).text)}</div>
          </div>
          <div>
            <div class="summary-label">当前实例</div>
            <div class="summary-value mono">${escapeHTML(info.instance_id || "未分配")}</div>
          </div>
        </div>
        <div class="drawer-kv">
          <span>描述</span><b>${escapeHTML(info.description || "未提供描述")}</b>
          <span>工作目录</span><b class="mono">${escapeHTML(trimPath(info.dir) || trimPath(info.dir_abs) || "未托管")}</b>
          <span>端点</span><b class="mono">${escapeHTML(info.endpoint || "未暴露")}</b>
          <span>启动清单</span><b class="mono">${escapeHTML(trimPath(info.startup_manifest_path) || "无")}</b>
        </div>
      `;
    }
    if (toggleBtn) {
      toggleBtn.textContent = data?.enabled ? "禁用服务" : "启用服务";
      toggleBtn.style.color = data?.enabled ? "var(--err)" : "";
      toggleBtn.onclick = () => handlers.onGovernanceToggle();
    }
    if (buildBtn) buildBtn.onclick = () => handlers.onGovernanceAction("build");
    if (drainBtn) drainBtn.onclick = () => handlers.onGovernanceAction("drain");
    if (reliabilitySelect) reliabilitySelect.value = data?.reliability || "unverified";
    return;
  }

  if (tabName === "tools") {
    const list = document.getElementById("toolsListContainer");
    const tools = Array.isArray(data?.tools) ? data.tools : [];
    list.innerHTML = tools.length ? tools.map(renderToolCard).join("") : `<div class="empty-state">该服务当前没有完整工具视图</div>`;

    list.querySelectorAll(".expandable-head").forEach((head) => {
      head.onclick = () => {
        head.closest(".tool-card").classList.toggle("collapsed");
      };
    });
    return;
  }

  if (tabName === "lifecycle") {
    const service = data?.service || state.managed.find((item) => item.service_id === state.selectedID);
    const metaContainer = document.getElementById("serviceMetadata");
    if (service && metaContainer) {
      const startupStatus = shouldHideStartupIssue(service) ? "已忽略（当前实时态正常）" : (service.startup?.status || "无");
      const startupError = shouldHideStartupIssue(service) ? "已忽略（当前实时态正常）" : (service.startup?.error || "无");
      metaContainer.innerHTML = `
        <div class="drawer-summary">
          <div>
            <div class="summary-label">服务版本</div>
            <div class="summary-value mono">${escapeHTML(service.registered_manifest?.version || service.registration?.version || "-")}</div>
          </div>
          <div>
            <div class="summary-label">运行状态</div>
            <div class="summary-value">${escapeHTML(statusMeta(service).text)}</div>
          </div>
          <div>
            <div class="summary-label">治理可靠度</div>
            <div class="summary-value mono">${escapeHTML(service.reliability || "unverified")}</div>
          </div>
          <div>
            <div class="summary-label">健康度</div>
            <div class="summary-value">${service.healthy ? "健康" : "异常"}</div>
          </div>
          <div>
            <div class="summary-label">启用状态</div>
            <div class="summary-value ${service.enabled ? "ok-text" : "err-text"}">${service.enabled ? "已启用" : "已禁用"}</div>
          </div>
          <div>
            <div class="summary-label">工具数量</div>
            <div class="summary-value mono">${service.tool_count || 0}</div>
          </div>
        </div>
        <div class="drawer-kv">
          <span>工作目录</span><b class="mono">${escapeHTML(trimPath(service.dir) || trimPath(service.dir_abs) || "未托管")}</b>
          <span>启动清单</span><b class="mono">${escapeHTML(trimPath(service.startup_manifest_path) || "无")}</b>
          <span>运行时清单</span><b class="mono">${escapeHTML(trimPath(service.runtime_manifest_path) || "无")}</b>
          <span>配置路径</span><b class="mono">${escapeHTML(trimPath(service.config_path) || "无")}</b>
          <span>端点</span><b class="mono">${escapeHTML(service.endpoint || "未暴露")}</b>
          <span>实例</span><b class="mono">${escapeHTML(service.instance_id || "未分配")}</b>
          <span>当前状态</span><b class="mono">${escapeHTML(service.status || "无")}</b>
          <span>Startup 状态</span><b class="mono">${escapeHTML(startupStatus)}</b>
          <span>Startup 错误</span><b class="mono">${escapeHTML(startupError)}</b>
        </div>
      `;
    }
    content.querySelectorAll("button[data-action]").forEach((btn) => {
      btn.onclick = () => handlers.onLifecycle(btn.dataset.action);
    });
    return;
  }

  if (tabName === "config") {
    const selector = document.getElementById("configTypeSelect");
    const editor = document.getElementById("configEditor");

    if (selector) {
      selector.value = state.configType || "config";
      selector.onchange = () => {
        state.configType = selector.value;
        handlers.onConfigTypeChange(selector.value);
      };
    }
    if (editor) {
      editor.value = typeof data === "string" ? data : pretty(data);
    }
    return;
  }

  if (tabName === "ops") {
    const pRunBtn = document.getElementById("pRunBtn");
    const auditBtn = document.getElementById("auditBtn");
    if (pRunBtn) pRunBtn.onclick = () => handlers.onProbe();
    if (auditBtn) auditBtn.onclick = () => handlers.onAudit();
    return;
  }

  if (tabName === "files") {
    const sel = document.getElementById("fSelect");
    const loadBtn = document.getElementById("fLoadBtn");
    sel.innerHTML = (data.files || []).map((item) => `<option value="${escapeHTML(item.path)}">${escapeHTML(item.path)}</option>`).join("");
    loadBtn.onclick = () => handlers.onFileLoad(sel.value);
  }
}
