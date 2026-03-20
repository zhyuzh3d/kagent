import { buildActionTemplate } from "./formatters.js";

export function createActionsPanel({ els, state }) {
  function syncActionSelection() {
    const name = els.actionSelect.value || "";
    const action = state.actions.find((item) => item.name === name) || null;
    if (!action) {
      els.actionSchema.textContent = "暂无动作 schema";
      return;
    }
    els.actionSchema.textContent = JSON.stringify(
      {
        name: action.name,
        description: action.description || "",
        args_schema: action.args_schema || {},
      },
      null,
      2,
    );
    els.actionEditor.value = JSON.stringify(buildActionTemplate(action), null, 2);
  }

  function setActions(actions) {
    state.actions = Array.isArray(actions)
      ? actions.filter((item) => item && typeof item.name === "string" && item.name.trim())
      : [];
    els.actionsBadge.textContent = `动作：${state.actions.length}`;
    if (!state.actions.length) {
      els.actionSelect.innerHTML = '<option value="">当前 Surface 未上报动作</option>';
      els.actionSchema.textContent = "暂无动作 schema";
      els.actionEditor.value = JSON.stringify({ id: `act-${Date.now()}`, name: "", args: {} }, null, 2);
      return;
    }
    els.actionSelect.innerHTML = state.actions
      .map((action, index) => {
        const text = `${action.name}${action.description ? ` / ${action.description}` : ""}`;
        return `<option value="${action.name}"${index === 0 ? " selected" : ""}>${text}</option>`;
      })
      .join("");
    syncActionSelection();
  }

  return {
    setActions,
    syncActionSelection,
  };
}
