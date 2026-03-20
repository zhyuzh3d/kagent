export function pretty(value) {
  return JSON.stringify(value, null, 2);
}

export function encodeBase64(text) {
  return btoa(unescape(encodeURIComponent(text || "")));
}

export function decodeBase64(text) {
  return decodeURIComponent(escape(atob(text || "")));
}

export function setStatus(els, text, cls = "") {
  if (!els.statusBadge) return;
  els.statusBadge.textContent = text;
  els.statusBadge.className = "status-badge " + cls;
}

export function parseJSON(text) {
  try {
    return JSON.parse(text || "{}");
  } catch (err) {
    throw new Error(`Invalid JSON: ${err.message}`);
  }
}
