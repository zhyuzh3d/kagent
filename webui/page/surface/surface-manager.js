import { buildPermissionProfile, extractManifestFromHTML } from "./manifest.js";
import { createID } from "./utils.js";

function buildReloadURL(rawURL) {
  const u = new URL(rawURL, location.origin);
  u.searchParams.set("_reload", String(Date.now()));
  return u.toString();
}

function summarizePermissions(profile) {
  const sandboxText = profile.sandboxTokens.join(" ");
  const allowText = profile.allowText || "(none)";
  return `sandbox=[${sandboxText}] allow=[${allowText}]`;
}

export function createSurfaceManager(options) {
  const workspaceEl = options.workspaceEl;
  const notify = typeof options.notify === "function" ? options.notify : () => {};
  const onStateChange = typeof options.onStateChange === "function" ? options.onStateChange : () => {};
  const onSurfaceEvent = typeof options.onSurfaceEvent === "function" ? options.onSurfaceEvent : () => {};
  const surfaceMap = new Map();

  function emitState() {
    onStateChange(listSurfaces());
  }

  function setEntryStatus(entry, text, cls) {
    entry.stateEl.textContent = text;
    entry.stateEl.classList.remove("ok", "err");
    if (cls === "ok" || cls === "err") {
      entry.stateEl.classList.add(cls);
    }
  }

  function disposePort(entry) {
    if (!entry.port) return;
    try {
      entry.port.close();
    } catch (_) {
    }
    entry.port = null;
  }

  function connectMessageChannel(entry) {
    disposePort(entry);
    if (!entry.iframe || !entry.iframe.contentWindow) {
      setEntryStatus(entry, "iframe 未就绪", "err");
      return;
    }
    const channel = new MessageChannel();
    entry.port = channel.port1;
    entry.sessionToken = createID("st");
    entry.port.onmessage = (ev) => {
      const msg = ev && ev.data ? ev.data : null;
      if (!msg || typeof msg !== "object") return;
      if (entry.frozen && msg.type !== "surface_heartbeat") return;
      if (msg.type === "surface_ready") {
        setEntryStatus(entry, "ready", "ok");
      }
      onSurfaceEvent({
        surface_id: entry.id,
        ts: Date.now(),
        message: msg,
      });
    };
    entry.port.start();
    try {
      entry.iframe.contentWindow.postMessage(
        {
          type: "surface_connect",
          surface_id: entry.id,
          session_token: entry.sessionToken,
        },
        "*",
        [channel.port2],
      );
      setEntryStatus(entry, "channel connected", "ok");
    } catch (err) {
      setEntryStatus(entry, `握手失败: ${err.message}`, "err");
    }
  }

  function attachIframe(entry, urlForLoad) {
    const iframe = document.createElement("iframe");
    iframe.setAttribute("sandbox", entry.permissionProfile.sandboxTokens.join(" "));
    if (entry.permissionProfile.allowText) {
      iframe.setAttribute("allow", entry.permissionProfile.allowText);
    } else {
      iframe.removeAttribute("allow");
    }
    iframe.src = urlForLoad;
    iframe.addEventListener("load", () => {
      connectMessageChannel(entry);
    });
    entry.bodyEl.replaceChildren(iframe);
    entry.iframe = iframe;
    setEntryStatus(entry, "loading...");
  }

  function toggleFreeze(entry) {
    entry.frozen = !entry.frozen;
    entry.freezeBtn.textContent = entry.frozen ? "解冻" : "冻结";
    setEntryStatus(entry, entry.frozen ? "frozen" : "running");
    emitState();
  }

  function toggleMinimize(entry) {
    entry.minimized = !entry.minimized;
    entry.rootEl.classList.toggle("minimized", entry.minimized);
    entry.minBtn.textContent = entry.minimized ? "展开" : "最小";
  }

  function toggleMaximize(entry) {
    entry.maximized = !entry.maximized;
    entry.rootEl.classList.toggle("maximized", entry.maximized);
    entry.maxBtn.textContent = entry.maximized ? "还原" : "最大";
  }

  function removeSurface(surfaceID) {
    const entry = surfaceMap.get(surfaceID);
    if (!entry) return false;
    disposePort(entry);
    if (entry.rootEl && entry.rootEl.parentElement) {
      entry.rootEl.parentElement.removeChild(entry.rootEl);
    }
    surfaceMap.delete(surfaceID);
    emitState();
    notify(`Surface 已关闭: ${surfaceID}`);
    return true;
  }

  function reloadSurface(surfaceID) {
    const entry = surfaceMap.get(surfaceID);
    if (!entry) return false;
    attachIframe(entry, buildReloadURL(entry.url));
    notify(`Surface 重载: ${surfaceID}`);
    return true;
  }

  function sendToSurface(surfaceID, payload, options2) {
    const opts = options2 && typeof options2 === "object" ? options2 : {};
    const entry = surfaceMap.get(surfaceID);
    if (!entry || !entry.port) return false;
    if (entry.frozen && !opts.force) return false;
    try {
      entry.port.postMessage(payload);
      return true;
    } catch (_) {
      return false;
    }
  }

  function setFrozen(surfaceID, frozen) {
    const entry = surfaceMap.get(surfaceID);
    if (!entry) return false;
    entry.frozen = !!frozen;
    entry.freezeBtn.textContent = entry.frozen ? "解冻" : "冻结";
    setEntryStatus(entry, entry.frozen ? "frozen" : "running");
    emitState();
    return true;
  }

  function listDeclaredActions() {
    const output = [];
    for (const entry of surfaceMap.values()) {
      if (entry.frozen) {
        continue;
      }
      for (const action of entry.manifest.actions || []) {
        output.push({
          name: `surface.call.${entry.id}.${action.name}`,
          description: action.description || `call ${action.name} on ${entry.id}`,
          source: entry.id,
        });
      }
    }
    return output;
  }

  function listSurfaces() {
    const output = [];
    for (const entry of surfaceMap.values()) {
      output.push({
        id: entry.id,
        type: entry.manifest.surface_type || "",
        version: entry.manifest.surface_version || "",
        url: entry.url,
        frozen: entry.frozen,
        minimized: entry.minimized,
        manifestTitle: entry.manifest.title || "",
        manifestDescription: entry.manifest.description || "",
        permissionsSummary: summarizePermissions(entry.permissionProfile),
      });
    }
    return output;
  }

  async function loadSurface(rawURL) {
    const value = String(rawURL == null ? "" : rawURL).trim();
    if (!value) throw new Error("surface URL 不能为空");
    const url = new URL(value, location.origin);
    if (url.origin !== location.origin) {
      throw new Error("仅允许加载同源 localhost Surface");
    }

    const response = await fetch(url.toString(), { cache: "no-store" });
    if (!response.ok) {
      throw new Error(`fetch surface 失败: ${response.status}`);
    }
    const htmlText = await response.text();
    const extracted = extractManifestFromHTML(htmlText, url.toString());
    const manifest = extracted.manifest;
    if (!manifest.surface_id) {
      throw new Error("manifest 缺少 surface_id");
    }
    if (!manifest.surface_type) {
      throw new Error("manifest 缺少 surface_type");
    }
    if (!manifest.surface_version) {
      throw new Error("manifest 缺少 surface_version");
    }
    if (surfaceMap.has(manifest.surface_id)) {
      throw new Error(`surface_id 已存在: ${manifest.surface_id}`);
    }
    const profile = buildPermissionProfile(manifest);
    if (profile.requiresConfirm) {
      const detail = profile.extraSandboxTokens.join(", ");
      const confirmed = window.confirm(
        `Surface 申请了额外 sandbox 权限: ${detail}\n` +
        `是否以该权限模板加载？`,
      );
      if (!confirmed) {
        throw new Error("用户拒绝额外 sandbox 权限");
      }
    }

    const surfaceID = manifest.surface_id;
    const rootEl = document.createElement("div");
    rootEl.className = "surface-window";
    rootEl.dataset.surfaceId = surfaceID;

    const headEl = document.createElement("div");
    headEl.className = "surface-head";
    const titleEl = document.createElement("span");
    titleEl.className = "surface-title";
    titleEl.textContent = `${manifest.title || surfaceID} (${surfaceID})`;
    const stateEl = document.createElement("span");
    stateEl.className = "state";
    stateEl.textContent = extracted.parseError ? `manifest error: ${extracted.parseError}` : "init";

    const authBtn = document.createElement("button");
    authBtn.textContent = "授权";
    const freezeBtn = document.createElement("button");
    freezeBtn.textContent = "冻结";
    const minBtn = document.createElement("button");
    minBtn.textContent = "最小";
    const maxBtn = document.createElement("button");
    maxBtn.textContent = "最大";
    const reloadBtn = document.createElement("button");
    reloadBtn.textContent = "重载";
    const closeBtn = document.createElement("button");
    closeBtn.textContent = "关闭";

    headEl.appendChild(titleEl);
    headEl.appendChild(stateEl);
    headEl.appendChild(authBtn);
    headEl.appendChild(freezeBtn);
    headEl.appendChild(minBtn);
    headEl.appendChild(maxBtn);
    headEl.appendChild(reloadBtn);
    headEl.appendChild(closeBtn);

    const bodyEl = document.createElement("div");
    bodyEl.className = "surface-body";
    rootEl.appendChild(headEl);
    rootEl.appendChild(bodyEl);

    const entry = {
      id: surfaceID,
      url: url.toString(),
      manifest,
      permissionProfile: profile,
      sessionToken: "",
      frozen: false,
      minimized: false,
      maximized: false,
      rootEl,
      bodyEl,
      stateEl,
      freezeBtn,
      minBtn,
      maxBtn,
      iframe: null,
      port: null,
    };

    authBtn.addEventListener("click", () => {
      window.alert(
        `Surface: ${entry.id}\n` +
        `Type: ${entry.manifest.surface_type || "(unknown)"}\n` +
        `Version: ${entry.manifest.surface_version || "(unknown)"}\n` +
        `URL: ${entry.url}\n` +
        `${summarizePermissions(entry.permissionProfile)}`,
      );
    });
    freezeBtn.addEventListener("click", () => toggleFreeze(entry));
    minBtn.addEventListener("click", () => toggleMinimize(entry));
    maxBtn.addEventListener("click", () => toggleMaximize(entry));
    reloadBtn.addEventListener("click", () => reloadSurface(entry.id));
    closeBtn.addEventListener("click", () => removeSurface(entry.id));

    workspaceEl.appendChild(rootEl);
    surfaceMap.set(entry.id, entry);
    attachIframe(entry, url.toString());

    notify(`Surface 已加载: ${entry.id}`);
    emitState();
    return entry.id;
  }

  return {
    loadSurface,
    reloadSurface,
    removeSurface,
    sendToSurface,
    setFrozen,
    listDeclaredActions,
    listSurfaces,
  };
}
