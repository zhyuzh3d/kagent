import { hashFNV1a, nowISO, safeJSONParse } from "./utils.js";

const OVERRIDE_STORAGE_KEY = "kagent.surface.prompt_override.v1";

const DEFAULT_CONFIG = {
  role: "你是 Main Surface 的动作调度智能体。",
  rules: [
    "只输出严格 JSON：{\"content\":\"...\",\"action\":...}",
    "仅调用 allowed_actions 列表中的动作。",
    "当动作优先时可将 content 置空。",
    "不虚构执行结果，依赖 Observation 与 action record。",
  ],
  style: "concise",
};

export function loadPromptOverrideText() {
  try {
    return localStorage.getItem(OVERRIDE_STORAGE_KEY) || "";
  } catch (_) {
    return "";
  }
}

export function savePromptOverrideText(text) {
  try {
    localStorage.setItem(OVERRIDE_STORAGE_KEY, text || "");
  } catch (_) {
  }
}

export function assemblePromptBundle(input) {
  const data = input && typeof input === "object" ? input : {};
  const overrideText = typeof data.overrideText === "string" ? data.overrideText : "";
  const parsedOverride = overrideText.trim() ? safeJSONParse(overrideText) : { ok: true, value: {} };
  if (!parsedOverride.ok) {
    return {
      ok: false,
      error: `override JSON 解析失败: ${parsedOverride.error}`,
    };
  }

  const override = parsedOverride.value && typeof parsedOverride.value === "object" ? parsedOverride.value : {};
  const config = {
    role: typeof override.role === "string" && override.role.trim() ? override.role.trim() : DEFAULT_CONFIG.role,
    rules: Array.isArray(override.rules) && override.rules.length > 0 ? override.rules : DEFAULT_CONFIG.rules,
    style: typeof override.style === "string" && override.style.trim() ? override.style.trim() : DEFAULT_CONFIG.style,
  };

  const surfaces = Array.isArray(data.surfaces) ? data.surfaces : [];
  const allowedActions = Array.isArray(data.allowedActions) ? data.allowedActions : [];
  const history = Array.isArray(data.history) ? data.history : [];

  const lines = [];
  lines.push(`# Role`);
  lines.push(config.role);
  lines.push("");
  lines.push(`# Rules`);
  for (const rule of config.rules) {
    lines.push(`- ${rule}`);
  }
  lines.push("");
  lines.push(`# Runtime`);
  lines.push(`- rendered_at: ${nowISO()}`);
  lines.push(`- response_style: ${config.style}`);
  lines.push(`- surface_count: ${surfaces.length}`);
  lines.push(`- allowed_action_count: ${allowedActions.length}`);
  lines.push("");
  lines.push(`# Surfaces`);
  for (const surface of surfaces) {
    lines.push(`- ${surface.id} | frozen=${surface.frozen ? "yes" : "no"} | url=${surface.url}`);
    if (surface.manifestTitle) {
      lines.push(`  title=${surface.manifestTitle}`);
    }
    if (surface.manifestDescription) {
      lines.push(`  desc=${surface.manifestDescription}`);
    }
    if (surface.permissionsSummary) {
      lines.push(`  permissions=${surface.permissionsSummary}`);
    }
  }
  if (surfaces.length === 0) {
    lines.push("- (none)");
  }
  lines.push("");
  lines.push(`# Allowed Actions`);
  for (const action of allowedActions) {
    lines.push(`- ${action.name} :: ${action.description || "no description"}`);
  }
  if (allowedActions.length === 0) {
    lines.push("- (none)");
  }
  lines.push("");
  lines.push(`# Recent History`);
  for (const msg of history.slice(-6)) {
    lines.push(`- ${msg.role}: ${msg.text}`);
  }
  if (history.length === 0) {
    lines.push("- (none)");
  }
  lines.push("");
  lines.push(`# Output Format`);
  lines.push('严格输出 JSON：{"content":"string","action":{"id":"string","name":"string","args":{},"timeout_s":3}|null}');

  const promptText = lines.join("\n");
  const configHash = hashFNV1a(JSON.stringify(config));
  const promptHash = hashFNV1a(promptText);

  return {
    ok: true,
    promptText,
    configHash,
    promptHash,
    renderedAt: nowISO(),
  };
}
