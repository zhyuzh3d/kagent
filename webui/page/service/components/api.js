export async function callTool(toolID, args = {}) {
  const normalizedToolID = String(toolID || "").trim();
  const resp = await fetch(`/api/tool/call?tool_id=${encodeURIComponent(normalizedToolID)}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ args }),
  });
  const raw = await resp.text();
  let data = null;
  try {
    data = raw ? JSON.parse(raw) : null;
  } catch (_) {}
  if (!resp.ok || !data || data.ok !== true) {
    const msg = (data && data.error && data.error.message) || raw || `http ${resp.status}`;
    throw new Error(msg);
  }
  return data.result || {};
}
