import { state } from "./state.js";

export function renderServiceList(els, onAction) {
  const tbody = document.getElementById("serviceTableBody");
  const emptyMsg = document.getElementById("emptyMsg");
  if (!tbody) return;
  
  const services = state.managed || [];
  const countEl = document.getElementById("serviceCount");
  if (countEl) countEl.textContent = `${services.length} 个服务`;

  if (!services.length) {
    if (emptyMsg) emptyMsg.style.display = "block";
    tbody.innerHTML = "";
    return;
  }
  
  if (emptyMsg) emptyMsg.style.display = "none";
  tbody.innerHTML = services.map(srv => {
    const isRunning = srv.active;
    const statusCls = isRunning ? "running" : (srv.registered ? "stopped" : "offline");
    const statusText = isRunning ? "正在运行" : (srv.registered ? "已停止" : "未注册");
    const pulseCls = isRunning ? "status-pulse" : "";
    const toolCount = (srv.manifest && srv.manifest.provides) ? srv.manifest.provides.length : 0;
    const version = (srv.manifest && srv.manifest.version) || "-";
    
    return `
      <tr data-id="${srv.service_id}">
        <td class="service-id-cell">
          <div style="font-weight: 700;">${srv.service_id}</div>
          <div style="font-size: 11px; color: var(--muted);">${srv.manifest?.description || ''}</div>
        </td>
        <td>
          <span class="status-badge ${statusCls} ${pulseCls}">${statusText}</span>
        </td>
        <td class="mono-cell">${srv.pid || "-"}</td>
        <td>
          <b style="color: ${srv.healthy ? 'var(--ok)' : 'var(--err)'}">${srv.healthy ? '健康' : '异常'}</b>
        </td>
        <td class="mono-cell">${version}</td>
        <td class="mono-cell">${srv.endpoint || "-"}</td>
        <td style="text-align: right;">
          <button class="action-btn primary" data-action="manage" data-id="${srv.service_id}">管理详情</button>
        </td>
      </tr>
    `;
  }).join("");

  // 绑定管理按钮
  tbody.querySelectorAll('button[data-action="manage"]').forEach(btn => {
    btn.addEventListener('click', () => {
      onAction('drawer', 'open', btn.dataset.id);
    });
  });
}

export function renderDrawerTab(tabName, data, handlers) {
  const content = document.getElementById("drawerContent");
  const tplId = `tpl-${tabName}`;
  const tpl = document.getElementById(tplId);
  if (!content || !tpl) return;

  content.innerHTML = "";
  content.appendChild(tpl.content.cloneNode(true));

  // 初始化不同 Tab 的逻辑
  if (tabName === 'tools') {
    const list = document.getElementById("toolsListContainer");
    const tools = data.tools || [];
    if (!tools.length) {
      list.innerHTML = `<div class="empty-state">该服务暂未向 Hub 注册任何工具</div>`;
      return;
    }
    list.innerHTML = tools.map(t => `
      <div class="tool-item">
        <div class="tool-header">
          <div style="display: flex; flex-direction: column; gap: 2px;">
            <div class="tool-id-label" style="font-size: 13px;">${t.tool_id}</div>
            <div class="tool-desc" style="font-size: 12px; opacity: 0.8;">${t.description || "无描述"}</div>
          </div>
          <div class="mono" style="font-size: 10px; color: var(--muted); opacity: 0.6;">${t.version || '1.0.0'}</div>
        </div>
        <div class="tool-details">
          <div class="schema-block">
            <h5 style="font-size: 10px; margin-bottom: 6px; color: var(--muted); letter-spacing: 0.05em;">入参定义 (INPUT SCHEMA)</h5>
            <pre class="mono result-box" style="background: #f8fafc; padding: 10px; font-size: 11px; border: 1px solid var(--line); border-radius: 6px;">${JSON.stringify(t.input_schema || {}, null, 2)}</pre>
          </div>
          ${t.output_schema ? `
          <div class="schema-block" style="margin-top: 16px;">
            <h5 style="font-size: 10px; margin-bottom: 6px; color: var(--muted); letter-spacing: 0.05em;">出参定义 (OUTPUT SCHEMA)</h5>
            <pre class="mono result-box" style="background: #f8fafc; padding: 10px; font-size: 11px; border: 1px solid var(--line); border-radius: 6px;">${JSON.stringify(t.output_schema, null, 2)}</pre>
          </div>` : ""}
          <div style="margin-top: 16px; display: flex; gap: 16px; font-size: 11px; border-top: 1px dashed var(--line); padding-top: 10px; color: var(--muted);">
            <span>通信范式: <b style="color: var(--text)">${t.streaming ? '流式 (WebSocket)' : '请求-响应 (HTTP)'}</b></span>
            <span>副作用: <b style="color: var(--text)">${t.side_effect || '无'}</b></span>
          </div>
        </div>
      </div>
    `).join("");

    list.querySelectorAll('.tool-header').forEach(hdr => {
      hdr.onclick = () => hdr.parentElement.classList.toggle("active");
    });
  }
  else if (tabName === 'lifecycle') {
    const srv = state.managed.find(s => s.service_id === state.selectedID);
    const metaContainer = document.getElementById("serviceMetadata");
    if (srv && metaContainer) {
      metaContainer.innerHTML = `
        <div style="font-size: 14px; font-weight: 700; margin-bottom: 8px; color: var(--accent);">元数据概览</div>
        <div style="font-size: 12px; display: grid; grid-template-columns: 80px 1fr; gap: 4px;">
          <span class="muted">服务版本:</span> <b>${srv.manifest?.version || '-'}</b>
          <span class="muted">可见性:</span> <b>${srv.manifest?.visibility || 'internal'}</b>
          <span class="muted">可靠性:</span> <b>${srv.manifest?.reliability || 'untrusted'}</b>
          <span class="muted">文件路径:</span> <span class="mono">${srv.dir}</span>
        </div>
        ${srv.manifest?.description ? `
        <div style="margin-top: 8px; font-size: 12px; border-top: 1px dashed #bae6fd; padding-top: 8px; color: #0369a1;">
          ${srv.manifest.description}
        </div>` : ""}
      `;
    }

    content.querySelectorAll('button[data-action]').forEach(btn => {
      btn.onclick = () => handlers.onLifecycle(btn.dataset.action);
    });
  } 
  else if (tabName === 'config') {
    const selector = document.getElementById("configTypeSelect");
    const editor = document.getElementById("configEditor");
    
    selector.onchange = () => handlers.onConfigTypeChange(selector.value);
    editor.value = typeof data === 'string' ? data : JSON.stringify(data, null, 2);
  }
  else if (tabName === 'ops') {
    const pRunBtn = document.getElementById("pRunBtn");
    const auditBtn = document.getElementById("auditBtn");
    
    if (pRunBtn) pRunBtn.onclick = () => handlers.onProbe();
    if (auditBtn) auditBtn.onclick = () => handlers.onAudit();
  }
  else if (tabName === 'files') {
    const sel = document.getElementById("fSelect");
    const loadBtn = document.getElementById("fLoadBtn");
    if (sel) {
      sel.innerHTML = (data.files || []).map(f => `<option value="${f.path}">${f.path}</option>`).join("");
      loadBtn.onclick = () => handlers.onFileLoad(sel.value);
    }
  }
}
