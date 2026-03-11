import { dedupeStrings } from "./utils.js";

export const DEFAULT_SANDBOX_TOKENS = ["allow-scripts", "allow-downloads"];

function normalizeAction(raw) {
  if (!raw || typeof raw !== "object") return null;
  const name = typeof raw.name === "string" ? raw.name.trim() : "";
  if (!name) return null;
  return {
    name,
    description: typeof raw.description === "string" ? raw.description.trim() : "",
    args_schema: raw.args_schema && typeof raw.args_schema === "object" ? raw.args_schema : {},
  };
}

export function normalizeManifest(raw, sourceURL) {
  const data = raw && typeof raw === "object" ? raw : {};
  const permissions = data.permissions && typeof data.permissions === "object" ? data.permissions : {};
  const actionsRaw = Array.isArray(data.actions) ? data.actions : [];
  const actions = actionsRaw.map(normalizeAction).filter(Boolean);
  return {
    surface_id: typeof data.surface_id === "string" ? data.surface_id.trim() : "",
    title: typeof data.title === "string" ? data.title.trim() : "",
    description: typeof data.description === "string" ? data.description.trim() : "",
    permissions: {
      sandbox: dedupeStrings(Array.isArray(permissions.sandbox) ? permissions.sandbox : []),
      allow: dedupeStrings(Array.isArray(permissions.allow) ? permissions.allow : []),
    },
    actions,
    source_url: sourceURL,
  };
}

export function extractManifestFromHTML(htmlText, sourceURL) {
  if (typeof htmlText !== "string") {
    return { manifest: normalizeManifest({}, sourceURL), found: false, parseError: "" };
  }
  const parser = new DOMParser();
  const doc = parser.parseFromString(htmlText, "text/html");
  const script = doc.querySelector('script#surface-manifest[type="application/json"], script[data-surface-manifest]');
  if (!script || !script.textContent) {
    return { manifest: normalizeManifest({}, sourceURL), found: false, parseError: "" };
  }
  try {
    const parsed = JSON.parse(script.textContent.trim());
    return { manifest: normalizeManifest(parsed, sourceURL), found: true, parseError: "" };
  } catch (err) {
    return {
      manifest: normalizeManifest({}, sourceURL),
      found: true,
      parseError: err && err.message ? err.message : "manifest parse failed",
    };
  }
}

export function buildPermissionProfile(manifest) {
  const requestedSandbox = dedupeStrings([
    ...DEFAULT_SANDBOX_TOKENS,
    ...(manifest.permissions && Array.isArray(manifest.permissions.sandbox) ? manifest.permissions.sandbox : []),
  ]);
  const extraSandbox = requestedSandbox.filter((item) => !DEFAULT_SANDBOX_TOKENS.includes(item));
  const allowFeatures = dedupeStrings(
    manifest.permissions && Array.isArray(manifest.permissions.allow) ? manifest.permissions.allow : [],
  );
  return {
    sandboxTokens: requestedSandbox,
    extraSandboxTokens: extraSandbox,
    allowFeatures,
    allowText: allowFeatures.join("; "),
    requiresConfirm: extraSandbox.length > 0,
  };
}
