import { SURFACE_GROUP_LABELS, SURFACE_GROUP_ORDER } from "./constants.js";
import { buildSurfaceOptionLabel, normalizeSurfaceGroup } from "./formatters.js";

export function createCatalogController({ els, state, callTool }) {
  function renderSurfaceSelect(selectedID = "") {
    const grouped = new Map();
    for (const key of SURFACE_GROUP_ORDER) {
      grouped.set(key, []);
    }
    for (const item of state.catalogItems) {
      const group = normalizeSurfaceGroup(item.surface_type);
      if (!grouped.has(group)) grouped.set(group, []);
      grouped.get(group).push(item);
    }

    const parts = ['<option value="">请选择 Surface</option>'];
    for (const key of SURFACE_GROUP_ORDER) {
      const items = grouped.get(key) || [];
      if (!items.length) continue;
      parts.push(`<optgroup label="${SURFACE_GROUP_LABELS[key]}">`);
      for (const item of items) {
        const selected = item.surface_id === selectedID ? " selected" : "";
        parts.push(`<option value="${item.surface_id}"${selected}>${buildSurfaceOptionLabel(item)}</option>`);
      }
      parts.push("</optgroup>");
    }

    els.surfaceSelect.innerHTML = parts.join("");
    if (selectedID) {
      els.surfaceSelect.value = selectedID;
    }
  }

  async function loadCatalog(preferredID = "") {
    const result = await callTool("ui.surface.catalog_list", {});
    const items = Array.isArray(result.items) ? result.items : [];
    state.catalogItems = items
      .filter((item) => item && item.status === "ok" && item.available !== false && item.entry_url)
      .sort((a, b) => {
        const groupDiff =
          SURFACE_GROUP_ORDER.indexOf(normalizeSurfaceGroup(a.surface_type)) -
          SURFACE_GROUP_ORDER.indexOf(normalizeSurfaceGroup(b.surface_type));
        if (groupDiff !== 0) return groupDiff;
        return String(a.name || a.surface_id).localeCompare(String(b.name || b.surface_id), "zh-CN");
      });

    const fallbackID =
      preferredID ||
      (state.entry && state.entry.surface_id) ||
      (state.catalogItems[0] && state.catalogItems[0].surface_id) ||
      "";
    renderSurfaceSelect(fallbackID);
    return fallbackID;
  }

  return {
    loadCatalog,
    renderSurfaceSelect,
  };
}
