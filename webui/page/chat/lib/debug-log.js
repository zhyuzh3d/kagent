function snippet(text) {
  if (!text) return '""';
  const clean = text.trim();
  if (clean.length > 10) return `"${clean.substring(0, 10)}..."`;
  return `"${clean}"`;
}

export function createDebugLogger(options) {
  const {
    debugEl,
    callTool,
    pageName = "chat",
  } = options;

  function reportClientLog(payload) {
    callTool("hub.system.report_log", payload).catch(() => {});
  }

  function appendDebug(level, module, turnId, text, action, source = "PAGE") {
    let finalAction = action;
    let finalText = text;
    let finalTurnId = turnId;

    if (arguments.length === 3) {
      finalAction = turnId;
      finalTurnId = null;
      finalText = null;
    } else if (arguments.length === 4) {
      if (text === "PAGE" || text === "SURF") {
        finalAction = turnId;
        finalTurnId = null;
        finalText = null;
        source = text;
      } else {
        finalAction = text;
        finalTurnId = turnId;
        finalText = null;
      }
    }

    reportClientLog({
      level,
      module: module || "Client",
      content: finalAction || finalText || "empty",
      page: source === "SURF" ? source : pageName,
    });

    if (!debugEl) return;

    const parts = [];
    const d = new Date();
    const tzOffset = d.getTimezoneOffset() * 60000;
    const localISOTime = new Date(d - tzOffset).toISOString().slice(0, 19).replace("T", " ");
    parts.push(localISOTime);
    parts.push(`[${level}]`);
    if (module) parts.push(`[${module}]`);
    if (finalTurnId !== undefined && finalTurnId !== null) {
      parts.push(`[Turn:${finalTurnId}]`);
    }
    if (finalText !== null && finalText !== undefined) {
      parts.push(`${snippet(finalText)} -> ${finalAction}`);
    } else {
      parts.push(`-> ${finalAction}`);
    }

    debugEl.textContent += parts.join(" ") + "\n";
    debugEl.scrollTop = debugEl.scrollHeight;
  }

  return {
    appendDebug,
    reportClientLog,
  };
}
