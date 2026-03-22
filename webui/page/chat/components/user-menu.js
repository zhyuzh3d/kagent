export function setupUserMenu(options) {
  const {
    user,
    avatarBtn,
    menu,
    menuName,
    logoutBtn,
    reportClientLog,
    pageName = "chat",
    doc = document,
  } = options;

  const name = (user && user.username) || "?";
  avatarBtn.textContent = name.charAt(0).toUpperCase();
  menuName.textContent = name;

  avatarBtn.addEventListener("click", (event) => {
    event.stopPropagation();
    menu.classList.toggle("open");
  });

  doc.addEventListener("click", () => {
    menu.classList.remove("open");
  });

  window.onerror = (msg, url, line, col, error) => {
    reportClientLog({
      level: "CRITICAL",
      module: "Browser",
      content: `JS Error: ${msg} at ${url}:${line}:${col}. Error: ${error ? error.stack : ""}`,
      page: pageName,
    });
  };

  window.onunhandledrejection = (event) => {
    reportClientLog({
      level: "CRITICAL",
      module: "Browser",
      content: `Unhandled Promise Rejection: ${event.reason}`,
      page: pageName,
    });
  };

  logoutBtn.addEventListener("click", async () => {
    try {
      await fetch("/api/tool/call?tool_id=account.auth.logout", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ args: {} }),
      });
    } catch (_) {
    }
    window.location.href = "/page/account/";
  });
}
