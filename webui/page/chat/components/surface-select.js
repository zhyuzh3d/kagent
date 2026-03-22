function escapeOptionHTML(text) {
  return String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

export function renderSurfaceSelect(selectEl, payload) {
  const state = payload && typeof payload === "object" ? payload : {};
  const items = Array.isArray(state.items) ? state.items : [];
  const activeSurfaceID = typeof state.activeSurfaceID === "string" ? state.activeSurfaceID : "";
  const currentValue = activeSurfaceID || (selectEl && typeof selectEl.value === "string" ? selectEl.value : "");
  const options = ['<option value="">请选择Surface...</option>'];
  items.forEach((item) => {
    if (!item || typeof item !== "object") return;
    const surfaceID = typeof item.surface_id === "string" ? item.surface_id : "";
    if (!surfaceID) return;
    const label = typeof item.name === "string" && item.name.trim() ? item.name.trim() : surfaceID;
    const meta = [item.surface_type || "", item.version ? `v${item.version}` : ""].filter(Boolean).join(" / ");
    const selected = surfaceID === currentValue ? " selected" : "";
    const text = meta ? `${label} (${meta})` : label;
    options.push(`<option value="${escapeOptionHTML(surfaceID)}"${selected}>${escapeOptionHTML(text)}</option>`);
  });
  selectEl.innerHTML = options.join("");
  selectEl.value = activeSurfaceID || "";
}
