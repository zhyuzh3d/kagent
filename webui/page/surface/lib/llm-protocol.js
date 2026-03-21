function extractContentPreview(jsonText) {
  if (typeof jsonText !== "string" || !jsonText) {
    return { found: false, complete: false, value: "" };
  }
  const keyIdx = jsonText.indexOf('"content"');
  if (keyIdx < 0) return { found: false, complete: false, value: "" };

  let i = keyIdx + '"content"'.length;
  while (i < jsonText.length && /\s/.test(jsonText[i])) i += 1;
  if (i >= jsonText.length || jsonText[i] !== ":") {
    return { found: true, complete: false, value: "" };
  }
  i += 1;
  while (i < jsonText.length && /\s/.test(jsonText[i])) i += 1;
  if (i >= jsonText.length) return { found: true, complete: false, value: "" };
  if (jsonText[i] !== '"') {
    return { found: true, complete: true, value: "" };
  }
  i += 1;

  let out = "";
  let escaped = false;
  for (; i < jsonText.length; i++) {
    const ch = jsonText[i];
    if (escaped) {
      escaped = false;
      if (ch === "n") out += "\n";
      else if (ch === "r") out += "\r";
      else if (ch === "t") out += "\t";
      else if (ch === "b") out += "\b";
      else if (ch === "f") out += "\f";
      else if (ch === "u") {
        const unicode = jsonText.slice(i + 1, i + 5);
        if (/^[0-9a-fA-F]{4}$/.test(unicode)) {
          out += String.fromCharCode(parseInt(unicode, 16));
          i += 4;
        } else {
          return { found: true, complete: false, value: out };
        }
      } else {
        out += ch;
      }
      continue;
    }
    if (ch === "\\") {
      escaped = true;
      continue;
    }
    if (ch === '"') {
      return { found: true, complete: true, value: out };
    }
    out += ch;
  }
  return { found: true, complete: false, value: out };
}

export function createLLMProtocolSession(handlers) {
  let fullText = "";
  let lastContent = "";

  function ingest(chunk) {
    const textChunk = String(chunk == null ? "" : chunk);
    if (!textChunk) return;
    fullText += textChunk;

    const preview = extractContentPreview(fullText);
    if (preview.found && preview.value !== lastContent) {
      lastContent = preview.value;
      if (handlers && typeof handlers.onContentDelta === "function") {
        handlers.onContentDelta({ content: lastContent, complete: preview.complete, raw: fullText });
      }
    }
  }

  async function finish(meta) {
    const metadata = meta && typeof meta === "object" ? meta : {};
    const messageId = metadata.messageId || "";
    let parsed;
    try {
      parsed = JSON.parse(fullText);
    } catch (err) {
      if (handlers && typeof handlers.onParseError === "function") {
        handlers.onParseError({ messageId, error: err, raw: fullText });
      }
      fullText = "";
      lastContent = "";
      return { ok: false, messageId, error: err };
    }

    const content = typeof parsed.content === "string" ? parsed.content : "";
    if (handlers && typeof handlers.onMessageFinal === "function") {
      handlers.onMessageFinal({ messageId, content, action: parsed.action || null, raw: fullText });
    }

    let actionResult = null;
    if (parsed.action && handlers && typeof handlers.onActionCall === "function") {
      actionResult = await handlers.onActionCall(parsed.action, { messageId, content, raw: fullText });
    }

    fullText = "";
    lastContent = "";
    return { ok: true, messageId, content, actionResult };
  }

  function reset() {
    fullText = "";
    lastContent = "";
  }

  return {
    ingest,
    finish,
    reset,
  };
}
