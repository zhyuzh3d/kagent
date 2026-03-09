const DEFAULT_PUBLIC_CONFIG = {
  app: {
    debug: { logLevel: "info" },
    ui: { showDebugPanelByDefault: false }
  },
  chat: {
    frontend: {
      voiceThreshold: 0.018,
      utteranceSilenceMs: 500,
      bargeInThreshold: 0.08,
      bargeInMinFrames: 5,
      bargeInCooldownMs: 500,
      replyOnsetGuardMs: 1200,
      preRollMaxFrames: 5,
      silentTailFrames: 50,
      frameSamples16k: 320
    }
  }
};

export function cloneConfig(value) {
  return JSON.parse(JSON.stringify(value));
}

function readNumber(value, fallback) {
  return Number.isFinite(value) ? value : fallback;
}

export async function loadRuntimeConfig() {
  try {
    const resp = await fetch("/api/config", { cache: "no-store" });
    if (!resp.ok) throw new Error(`status ${resp.status}`);
    return await resp.json();
  } catch (_) {
    return cloneConfig(DEFAULT_PUBLIC_CONFIG);
  }
}

export async function saveRuntimeConfig(config) {
  const resp = await fetch("/api/config", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json"
    },
    body: JSON.stringify(config)
  });
  if (!resp.ok) {
    throw new Error(`status ${resp.status}`);
  }
  return await resp.json();
}

export async function loadConfigInfo() {
  try {
    const resp = await fetch("/json/config_info.json", { cache: "no-store" });
    if (!resp.ok) throw new Error(`status ${resp.status}`);
    return await resp.json();
  } catch (_) {
    return { tabs: [], fields: {} };
  }
}

export function applyChatFrontendConfig(app, config) {
  const frontend = (((config || {}).chat || {}).frontend) || {};
  app.publicConfig = config || cloneConfig(DEFAULT_PUBLIC_CONFIG);
  app.voiceThreshold = readNumber(frontend.voiceThreshold, app.voiceThreshold);
  app.utteranceSilenceMs = readNumber(frontend.utteranceSilenceMs, app.utteranceSilenceMs);
  app.bargeInThreshold = readNumber(frontend.bargeInThreshold, app.bargeInThreshold);
  app.bargeInMinFrames = readNumber(frontend.bargeInMinFrames, app.bargeInMinFrames);
  app.bargeInCooldownMs = readNumber(frontend.bargeInCooldownMs, app.bargeInCooldownMs);
  app.replyOnsetGuardMs = readNumber(frontend.replyOnsetGuardMs, app.replyOnsetGuardMs);
  app.preRollMaxFrames = readNumber(frontend.preRollMaxFrames, app.preRollMaxFrames);
  app.silentTailFrames = readNumber(frontend.silentTailFrames, app.silentTailFrames);
  app.frameSamples16k = readNumber(frontend.frameSamples16k, app.frameSamples16k);
}

export function buildWorkerConfig(config) {
  const frontend = (((config || {}).chat || {}).frontend) || {};
  return {
    utteranceSilenceMs: readNumber(frontend.utteranceSilenceMs, DEFAULT_PUBLIC_CONFIG.chat.frontend.utteranceSilenceMs),
    utteranceEndDebounceMs: 2000
  };
}

export function getConfigValue(config, path) {
  if (!config || !path) return undefined;
  const parts = path.split(".");
  let current = config;
  for (const part of parts) {
    if (!current || typeof current !== "object" || !(part in current)) {
      return undefined;
    }
    current = current[part];
  }
  return current;
}

export function setConfigValue(config, path, value) {
  if (!config || !path) return config;
  const parts = path.split(".");
  let current = config;
  for (let i = 0; i < parts.length - 1; i++) {
    const key = parts[i];
    if (!current[key] || typeof current[key] !== "object" || Array.isArray(current[key])) {
      current[key] = {};
    }
    current = current[key];
  }
  current[parts[parts.length - 1]] = value;
  return config;
}

export function validateFieldValue(meta, rawValue) {
  const control = meta && meta.control ? meta.control : "text";
  if (control === "number" || control === "slider") {
    const value = Number(rawValue);
    if (!Number.isFinite(value)) {
      return { ok: false, error: "请输入有效数字。" };
    }
    if (Number.isFinite(meta.min) && value < meta.min) {
      return { ok: false, error: `不能小于 ${meta.min}。` };
    }
    if (Number.isFinite(meta.max) && value > meta.max) {
      return { ok: false, error: `不能大于 ${meta.max}。` };
    }
    return { ok: true, value };
  }
  if (control === "textarea" || control === "text") {
    return { ok: true, value: String(rawValue ?? "") };
  }
  return { ok: true, value: rawValue };
}
