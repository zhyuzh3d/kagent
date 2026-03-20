export function normalizeSurfaceGroup(surfaceType) {
  if (surfaceType === "ext") return "extension";
  if (surfaceType === "buildin" || surfaceType === "custom" || surfaceType === "extension") return surfaceType;
  return "custom";
}

export function buildSurfaceOptionLabel(item) {
  const parts = [item.name || item.surface_id, item.surface_id];
  if (item.enabled === false) {
    parts.push("未启用");
  }
  return parts.join(" / ");
}

export function buildActionTemplate(action) {
  const argsSchema = action && action.args_schema && typeof action.args_schema === "object" ? action.args_schema : {};
  const args = {};
  for (const [key, value] of Object.entries(argsSchema)) {
    if (typeof value === "string") {
      if (value === "number") args[key] = 0;
      else if (value === "boolean") args[key] = false;
      else if (value === "array") args[key] = [];
      else if (value === "object") args[key] = {};
      else args[key] = "";
      continue;
    }
    args[key] = value;
  }
  return {
    id: `act-${Date.now()}`,
    name: action && action.name ? action.name : "",
    args,
  };
}

export function redactSessionToken(token) {
  const text = String(token || "").trim();
  if (!text) return "-";
  if (text.length <= 16) return text;
  return `${text.slice(0, 8)}...${text.slice(-8)}`;
}

export function escapeAttribute(text) {
  return String(text || "").replace(/"/g, "&quot;");
}
