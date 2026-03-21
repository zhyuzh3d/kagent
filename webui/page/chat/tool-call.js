export async function callTool(toolID, args = {}, context = null) {
  const normalizedToolID = String(toolID || "").trim();
  const payload = {
    args: args && typeof args === "object" ? args : {},
  };
  if (context && typeof context === "object") {
    payload.context = context;
  }
  const resp = await fetch(`/api/tool/call?tool_id=${encodeURIComponent(normalizedToolID)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const raw = await resp.text();
  let data = null;
  try {
    data = raw ? JSON.parse(raw) : null;
  } catch (_) {
    data = null;
  }
  if (!resp.ok) {
    const errMessage = data && data.error && data.error.message
      ? data.error.message
      : (data && (data.error || data.message)) || raw || `http ${resp.status}`;
    throw new Error(errMessage);
  }
  if (!data || data.ok !== true) {
    const errMessage = data && data.error && data.error.message
      ? data.error.message
      : (data && (data.error || data.message)) || "tool call failed";
    throw new Error(errMessage);
  }
  return data.result;
}
