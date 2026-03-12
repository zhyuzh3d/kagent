import {
  cloneConfig,
  getConfigValue,
  loadRuntimeConfig,
  saveRuntimeConfig,
  setConfigValue,
  validateFieldValue,
} from "./config-store.js";

function groupFields(configInfo, activeTab) {
  const groups = new Map();
  const fields = (configInfo && configInfo.fields) || {};
  for (const [path, meta] of Object.entries(fields)) {
    if (!meta || !meta.show || !meta.editable) continue;
    if (meta.tab !== activeTab) continue;
    const key = meta.group || "未分组";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push([path, meta]);
  }
  return Array.from(groups.entries());
}

function scopeLabel(scope) {
  if (!scope) return "";
  return scope.toUpperCase();
}

function formatValue(value) {
  if (value === undefined || value === null) return "";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function collectChangedPaths(prev, next, prefix = "", out = []) {
  if (prev === next) return out;
  const prevIsObj = prev && typeof prev === "object" && !Array.isArray(prev);
  const nextIsObj = next && typeof next === "object" && !Array.isArray(next);
  if (!prevIsObj || !nextIsObj) {
    if (JSON.stringify(prev) !== JSON.stringify(next)) {
      out.push(prefix || "(root)");
    }
    return out;
  }
  const keys = new Set([...Object.keys(prev), ...Object.keys(next)]);
  for (const key of keys) {
    const path = prefix ? `${prefix}.${key}` : key;
    collectChangedPaths(prev[key], next[key], path, out);
  }
  return out;
}

export function createConfigDrawer(options) {
  const {
    mount,
    initialConfig,
    configInfo,
    onConfigApplied,
    onConfigSaved,
    onDebug,
  } = options;

  const state = {
    effectiveConfig: cloneConfig(initialConfig || {}),
    draftConfig: cloneConfig(initialConfig || {}),
    configInfo: configInfo || { tabs: [], fields: {} },
    activeTab: (configInfo && configInfo.tabs && configInfo.tabs[0] && configInfo.tabs[0].key) || "chat",
    open: false,
    saving: false,
    dirty: false,
    statusText: "",
    statusTone: "",
    fieldErrors: {},
  };

  mount.innerHTML = `
    <div class="config-backdrop" data-role="backdrop"></div>
    <aside class="config-drawer" data-role="drawer">
      <div class="config-drawer-head">
        <div>
          <h2>配置中心</h2>
          <p>仅展示 config_info.json 允许编辑的字段。</p>
        </div>
        <button type="button" class="btn-ghost" data-role="close">关闭</button>
      </div>
      <div class="config-tabs" data-role="tabs"></div>
      <div class="config-status" data-role="status"></div>
      <div class="config-body" data-role="body"></div>
      <div class="config-drawer-foot">
        <button type="button" class="btn-ghost" data-role="reload">重载</button>
        <button type="button" class="btn-ghost" data-role="reset">撤销</button>
        <button type="button" class="btn-primary" data-role="save">保存</button>
      </div>
    </aside>
  `;

  const refs = {
    backdrop: mount.querySelector('[data-role="backdrop"]'),
    drawer: mount.querySelector('[data-role="drawer"]'),
    tabs: mount.querySelector('[data-role="tabs"]'),
    body: mount.querySelector('[data-role="body"]'),
    status: mount.querySelector('[data-role="status"]'),
    close: mount.querySelector('[data-role="close"]'),
    reload: mount.querySelector('[data-role="reload"]'),
    reset: mount.querySelector('[data-role="reset"]'),
    save: mount.querySelector('[data-role="save"]'),
  };

  function setStatus(text, tone) {
    state.statusText = text || "";
    state.statusTone = tone || "";
    refs.status.textContent = state.statusText;
    refs.status.className = `config-status ${state.statusTone}`.trim();
  }

  function emitConfigApplied() {
    if (typeof onConfigApplied === "function") {
      onConfigApplied(cloneConfig(state.draftConfig));
    }
  }

  function markDirtyStatus() {
    state.dirty = true;
    setStatus("本地草稿未保存。", "pending");
  }

  function revertDraftToEffective(statusText) {
    state.draftConfig = cloneConfig(state.effectiveConfig);
    state.fieldErrors = {};
    state.dirty = false;
    emitConfigApplied();
    if (statusText) {
      setStatus(statusText, "success");
    }
  }

  function renderTabs() {
    const tabs = (state.configInfo && state.configInfo.tabs) || [];
    refs.tabs.innerHTML = "";
    for (const tab of tabs) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = `config-tab ${tab.key === state.activeTab ? "active" : ""}`;
      btn.textContent = tab.label || tab.key;
      btn.addEventListener("click", () => {
        state.activeTab = tab.key;
        render();
      });
      refs.tabs.appendChild(btn);
    }
  }

  function makeFieldControl(path, meta, currentValue) {
    const control = meta.control || "text";
    const wrapper = document.createElement("div");
    wrapper.className = "config-field";

    const top = document.createElement("div");
    top.className = "config-field-top";

    const label = document.createElement("label");
    label.className = "config-field-label";
    label.textContent = meta.label || path;
    top.appendChild(label);

    if (meta.scope) {
      const badge = document.createElement("span");
      badge.className = `scope-badge scope-${meta.scope}`;
      badge.textContent = scopeLabel(meta.scope);
      top.appendChild(badge);
    }

    wrapper.appendChild(top);

    if (meta.description) {
      const desc = document.createElement("p");
      desc.className = "config-field-desc";
      desc.textContent = meta.description;
      wrapper.appendChild(desc);
    }

    const controls = document.createElement("div");
    controls.className = "config-field-controls";

    const updateField = (nextValue) => {
      const result = validateFieldValue(meta, nextValue);
      if (!result.ok) {
        state.fieldErrors[path] = result.error;
        render();
        return;
      }
      delete state.fieldErrors[path];
      setConfigValue(state.draftConfig, path, result.value);
      emitConfigApplied();
      markDirtyStatus();
      render();
    };

    if (control === "slider") {
      const range = document.createElement("input");
      range.type = "range";
      range.min = String(meta.min ?? 0);
      range.max = String(meta.max ?? 100);
      range.step = String(meta.step ?? 1);
      range.value = formatValue(currentValue);
      range.addEventListener("change", () => updateField(range.value));

      const number = document.createElement("input");
      number.type = "number";
      number.min = String(meta.min ?? "");
      number.max = String(meta.max ?? "");
      number.step = String(meta.step ?? 1);
      number.value = formatValue(currentValue);
      number.addEventListener("change", () => updateField(number.value));

      controls.appendChild(range);
      controls.appendChild(number);
    } else if (control === "number") {
      const input = document.createElement("input");
      input.type = "number";
      input.min = String(meta.min ?? "");
      input.max = String(meta.max ?? "");
      input.step = String(meta.step ?? 1);
      input.value = formatValue(currentValue);
      input.addEventListener("change", () => updateField(input.value));
      controls.appendChild(input);
    } else if (control === "textarea") {
      const textarea = document.createElement("textarea");
      textarea.rows = 4;
      textarea.value = formatValue(currentValue);
      textarea.addEventListener("change", () => updateField(textarea.value));
      controls.appendChild(textarea);
    } else {
      const input = document.createElement("input");
      input.type = "text";
      input.value = formatValue(currentValue);
      input.addEventListener("change", () => updateField(input.value));
      controls.appendChild(input);
    }

    wrapper.appendChild(controls);

    const bottom = document.createElement("div");
    bottom.className = "config-field-bottom";
    if (meta.applyHint) {
      const hint = document.createElement("span");
      hint.className = "config-field-hint";
      hint.textContent = meta.applyHint;
      bottom.appendChild(hint);
    }
    if (state.fieldErrors[path]) {
      const err = document.createElement("span");
      err.className = "config-field-error";
      err.textContent = state.fieldErrors[path];
      bottom.appendChild(err);
    }
    wrapper.appendChild(bottom);

    return wrapper;
  }

  function renderBody() {
    refs.body.innerHTML = "";
    const groups = groupFields(state.configInfo, state.activeTab);
    if (groups.length === 0) {
      const empty = document.createElement("div");
      empty.className = "config-empty";
      empty.textContent = "当前标签还没有可编辑字段。";
      refs.body.appendChild(empty);
      return;
    }
    for (const [groupName, items] of groups) {
      const section = document.createElement("section");
      section.className = "config-group";

      const title = document.createElement("h3");
      title.textContent = groupName;
      section.appendChild(title);

      for (const [path, meta] of items) {
        section.appendChild(makeFieldControl(path, meta, getConfigValue(state.draftConfig, path)));
      }

      refs.body.appendChild(section);
    }
  }

  function render() {
    mount.classList.toggle("open", state.open);
    refs.drawer.classList.toggle("saving", state.saving);
    refs.save.disabled = state.saving;
    refs.reset.disabled = state.saving;
    refs.reload.disabled = state.saving;
    renderTabs();
    renderBody();
    setStatus(state.statusText, state.statusTone);
  }

  async function handleSave() {
    if (Object.keys(state.fieldErrors).length > 0) {
      setStatus("请先修正表单中的配置错误。", "error");
      return;
    }
    state.saving = true;
    setStatus("正在保存配置...", "pending");
    render();
    try {
      const previousEffective = cloneConfig(state.effectiveConfig);
      const effective = await saveRuntimeConfig(state.draftConfig);
      state.effectiveConfig = cloneConfig(effective);
      state.draftConfig = cloneConfig(effective);
      state.dirty = false;
      emitConfigApplied();
      if (typeof onConfigSaved === "function") {
        onConfigSaved({
          source: "config_drawer",
          changedPaths: collectChangedPaths(previousEffective, effective),
          config: cloneConfig(effective),
        });
      }
      setStatus("配置已保存。", "success");
      if (typeof onDebug === "function") {
        onDebug("INFO", "ConfigPanel", null, null, "runtime config saved");
      }
    } catch (err) {
      setStatus(`保存失败：${err.message || err}`, "error");
      if (typeof onDebug === "function") {
        onDebug("ERROR", "ConfigPanel", null, null, `config save failed: ${err.message || err}`);
      }
    } finally {
      state.saving = false;
      render();
    }
  }

  async function handleReload() {
    state.saving = true;
    setStatus("正在重载配置...", "pending");
    render();
    try {
      const effective = await loadRuntimeConfig();
      state.effectiveConfig = cloneConfig(effective);
      state.draftConfig = cloneConfig(effective);
      state.fieldErrors = {};
      state.dirty = false;
      emitConfigApplied();
      setStatus("已从服务端重载配置。", "success");
    } catch (err) {
      setStatus(`重载失败：${err.message || err}`, "error");
    } finally {
      state.saving = false;
      render();
    }
  }

  function handleReset() {
    revertDraftToEffective("已撤销未保存修改。");
    render();
  }

  refs.backdrop.addEventListener("click", () => api.close());
  refs.close.addEventListener("click", () => api.close());
  refs.save.addEventListener("click", handleSave);
  refs.reload.addEventListener("click", handleReload);
  refs.reset.addEventListener("click", handleReset);

  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !state.open) return;
    api.close();
  });

  const api = {
    open() {
      state.open = true;
      render();
    },
    close() {
      if (state.dirty) {
        revertDraftToEffective("已关闭面板，未保存改动已撤销。");
      }
      state.open = false;
      render();
    },
    toggle() {
      state.open = !state.open;
      render();
    },
    setConfig(nextConfig) {
      state.effectiveConfig = cloneConfig(nextConfig || {});
      state.draftConfig = cloneConfig(nextConfig || {});
      state.fieldErrors = {};
      state.dirty = false;
      render();
    },
    setConfigInfo(nextInfo) {
      state.configInfo = nextInfo || { tabs: [], fields: {} };
      if (!((state.configInfo.tabs || []).some((tab) => tab.key === state.activeTab))) {
        state.activeTab = (state.configInfo.tabs && state.configInfo.tabs[0] && state.configInfo.tabs[0].key) || "chat";
      }
      render();
    },
  };

  render();
  return api;
}
