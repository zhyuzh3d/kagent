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
  if (!els?.statusBadge) return;
  els.statusBadge.textContent = text;
  els.statusBadge.className = `status-badge ${cls}`.trim();
}

export function parseJSON(text) {
  try {
    return JSON.parse(text || "{}");
  } catch (err) {
    throw new Error(`Invalid JSON: ${err.message}`);
  }
}

export function escapeHTML(value) {
  return String(value == null ? "" : value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

export function formatTime(ts) {
  const value = Number(ts || 0);
  if (!value) return "未见上报";
  return new Date(value).toLocaleString();
}

export function formatPercent(value) {
  if (typeof value !== "number" || Number.isNaN(value)) return "-";
  return `${Math.round(value * 100)}%`;
}

export function protocolLabel(spec = {}) {
  if (spec.streaming) {
    return `WS / ${spec.streaming_mode || "stream"}`;
  }
  return String(spec.protocol || "http").toUpperCase();
}
export function showToast(message, type = "success") {
  const toast = document.createElement("div");
  toast.className = `toast toast-${type}`;
  toast.textContent = message;
  Object.assign(toast.style, {
    position: "fixed",
    bottom: "24px",
    left: "50%",
    transform: "translateX(-50%)",
    padding: "12px 24px",
    background: type === "success" ? "var(--accent)" : "var(--err)",
    color: "#fff",
    borderRadius: "8px",
    boxShadow: "var(--shadow-md)",
    zIndex: "9999",
    fontSize: "14px",
    fontWeight: "700",
    transition: "opacity 300ms, transform 300ms",
  });
  document.body.appendChild(toast);
  setTimeout(() => (toast.style.opacity = "0"), 2700);
  setTimeout(() => toast.remove(), 3000);
}
