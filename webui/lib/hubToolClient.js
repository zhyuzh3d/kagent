function normalizeArgs(args) {
  return args && typeof args === "object" ? args : {};
}

export async function callHubTool(toolID, args = {}, options = {}) {
  const normalizedToolID = String(toolID || "").trim();
  if (!normalizedToolID) {
    throw new Error("tool_id is required");
  }
  const headers = new Headers(options.headers || {});
  headers.set("Content-Type", "application/json");
  const payload = {
    args: normalizeArgs(args),
  };
  if (options.context && typeof options.context === "object") {
    payload.context = options.context;
  }
  if (options.tool_id_in_body === true) {
    payload.tool_id = normalizedToolID;
  }
  const url = options.tool_id_in_body === true
    ? "/api/tool/call"
    : `/api/tool/call?tool_id=${encodeURIComponent(normalizedToolID)}`;
  const resp = await fetch(url, {
    method: "POST",
    headers,
    body: JSON.stringify(payload),
    signal: options.signal,
  });
  const raw = await resp.text();
  let data = null;
  try {
    data = raw ? JSON.parse(raw) : null;
  } catch (_) {
    data = null;
  }
  if (!resp.ok || !data || data.ok !== true) {
    const message = data && data.error && data.error.message
      ? data.error.message
      : (data && (data.error || data.message)) || raw || `http ${resp.status}`;
    throw new Error(message);
  }
  return data.result;
}
