export async function runAuthGuard() {
  try {
    const resp = await fetch("/api/tool/call?tool_id=account.auth.me", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ args: {} }),
      cache: "no-store",
    });
    if (!resp.ok) throw new Error("not authenticated");
    const data = await resp.json();
    if (!data || data.ok !== true || !data.result) throw new Error("not authenticated");
    window.__kagentUser = {
      user_id: data.result.user_id || "",
      username: data.result.username || "",
    };
    return window.__kagentUser;
  } catch (error) {
    window.location.href = "/page/account/?redirect=" + encodeURIComponent(window.location.pathname + window.location.search);
    throw error;
  }
}
