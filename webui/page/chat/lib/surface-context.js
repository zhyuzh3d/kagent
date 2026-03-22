function normalizeRegistryItem(item) {
  const source = item && typeof item === "object" ? item : {};
  return {
    surface_id: typeof source.surface_id === "string" ? source.surface_id.trim() : "",
    name: typeof source.name === "string" && source.name.trim() ? source.name.trim() : (typeof source.surface_id === "string" ? source.surface_id.trim() : ""),
    surface_type: typeof source.surface_type === "string" && source.surface_type.trim() ? source.surface_type.trim() : "app",
    version: typeof source.version === "string" && source.version.trim() ? source.version.trim() : "1",
    desc: typeof source.desc === "string" ? source.desc : "",
    entry_url: typeof source.entry_url === "string" ? source.entry_url : "",
  };
}

function cloneValue(value, fallback = null) {
  if (value == null) return fallback;
  try {
    return JSON.parse(JSON.stringify(value));
  } catch (_) {
    return fallback;
  }
}

function normalizeActionDescriptor(action) {
  const source = action && typeof action === "object" ? action : {};
  const name = typeof source.name === "string" ? source.name.trim() : "";
  if (!name) return null;
  const cloned = cloneValue(source, {});
  return {
    ...cloned,
    name,
    description: typeof source.description === "string" ? source.description : (typeof cloned.description === "string" ? cloned.description : ""),
  };
}

function normalizeRuntimeContext(runtime, registryMap, mode) {
  const source = runtime && typeof runtime === "object" ? runtime : {};
  const surfaceID = typeof source.surface_id === "string" ? source.surface_id.trim() : "";
  if (!surfaceID) return null;
  const entry = registryMap.get(surfaceID) || null;
  const actionsRaw = Array.isArray(source.registered_actions_raw) ? source.registered_actions_raw : source.actions;
  const actions = Array.isArray(actionsRaw)
    ? actionsRaw.map(normalizeActionDescriptor).filter(Boolean).sort((a, b) => a.name.localeCompare(b.name, "zh-CN"))
    : [];
  const registration = source.registration && typeof source.registration === "object" ? source.registration : {};
  const registerPayload = source.register_payload && typeof source.register_payload === "object" ? source.register_payload : {};
  const hostActions = Array.isArray(source.host_actions)
    ? source.host_actions.map((item) => cloneValue(item, {})).filter((item) => item && typeof item === "object")
    : [];
  const workspaceState = source.workspace_state && typeof source.workspace_state === "object" ? cloneValue(source.workspace_state, {}) : {};
  const state = source.state && typeof source.state === "object" ? cloneValue(source.state, {}) : {};
  return {
    surface_id: surfaceID,
    surface_type: typeof source.surface_type === "string" && source.surface_type.trim()
      ? source.surface_type.trim()
      : (entry && entry.surface_type ? entry.surface_type : "app"),
    surface_version: typeof source.surface_version === "string" && source.surface_version.trim()
      ? source.surface_version.trim()
      : (entry && entry.version ? entry.version : "1"),
    title: typeof registration.title === "string" && registration.title.trim()
      ? registration.title.trim()
      : (entry && entry.name ? entry.name : surfaceID),
    description: typeof registration.description === "string"
      ? registration.description
      : (entry && entry.desc ? entry.desc : ""),
    catalog_name: entry && typeof entry.name === "string" ? entry.name : surfaceID,
    protocol_version: typeof registration.protocol_version === "string"
      ? registration.protocol_version
      : "",
    registration: cloneValue(registration, {}),
    register_payload: cloneValue(registerPayload, {}),
    registered_at_ms: Number.isFinite(source.registered_at_ms) ? source.registered_at_ms : 0,
    host_actions: hostActions,
    workspace_state: workspaceState,
    state: state,
    open: true,
    ready: !!source.ready,
    mode: mode === "docked" ? "docked" : "floating",
    actions,
  };
}

function normalizeClosedRuntimeContext(previous, registryMap, surfaceID) {
  const entry = registryMap.get(surfaceID) || null;
  const base = previous && typeof previous === "object" ? previous : {};
  return {
    surface_id: surfaceID,
    surface_type: typeof base.surface_type === "string" && base.surface_type.trim()
      ? base.surface_type.trim()
      : (entry && entry.surface_type ? entry.surface_type : "app"),
    surface_version: typeof base.surface_version === "string" && base.surface_version.trim()
      ? base.surface_version.trim()
      : (entry && entry.version ? entry.version : "1"),
    title: typeof base.title === "string" && base.title.trim()
      ? base.title.trim()
      : (entry && entry.name ? entry.name : surfaceID),
    description: typeof base.description === "string"
      ? base.description
      : (entry && entry.desc ? entry.desc : ""),
    catalog_name: typeof base.catalog_name === "string" && base.catalog_name.trim()
      ? base.catalog_name.trim()
      : (entry && entry.name ? entry.name : surfaceID),
    protocol_version: typeof base.protocol_version === "string" ? base.protocol_version : "",
    registration: base.registration && typeof base.registration === "object" ? cloneValue(base.registration, {}) : {},
    register_payload: base.register_payload && typeof base.register_payload === "object" ? cloneValue(base.register_payload, {}) : {},
    registered_at_ms: Number.isFinite(base.registered_at_ms) ? base.registered_at_ms : 0,
    host_actions: Array.isArray(base.host_actions) ? cloneValue(base.host_actions, []) : [],
    workspace_state: base.workspace_state && typeof base.workspace_state === "object" ? cloneValue(base.workspace_state, {}) : {},
    state: base.state && typeof base.state === "object" ? cloneValue(base.state, {}) : {},
    open: false,
    ready: false,
    mode: "closed",
    actions: Array.isArray(base.actions) ? cloneValue(base.actions, []) : [],
  };
}

function stableJSON(value) {
  return JSON.stringify(value);
}

function clone(value) {
  return cloneValue(value, value);
}

export function createSurfaceContextStore(options = {}) {
  const now = typeof options.now === "function" ? options.now : () => Date.now();

  let contextVersion = 0;
  let registry = [];
  let activeSurfaceID = "";
  let runtimeRecords = new Map();
  let snapshot = {
    context_version: 0,
    updated_at_ms: 0,
    reason: "init",
    registry: [],
    active_surface_id: "",
    open_surfaces: [],
  };

  function rebuildSnapshot(reason, updatedAtMS) {
    const openSurfaces = Array.from(runtimeRecords.values())
      .filter((item) => item.open)
      .sort((a, b) => a.surface_id.localeCompare(b.surface_id, "zh-CN"));
    snapshot = {
      context_version: contextVersion,
      updated_at_ms: updatedAtMS,
      reason,
      registry: clone(registry),
      active_surface_id: activeSurfaceID,
      open_surfaces: clone(openSurfaces),
    };
  }

  function sync(input = {}) {
    const reason = typeof input.reason === "string" && input.reason.trim() ? input.reason.trim() : "surface_context_sync";
    const nextRegistry = Array.isArray(input.registry)
      ? input.registry.map(normalizeRegistryItem).filter((item) => item.surface_id)
      : [];
    nextRegistry.sort((a, b) => {
      return a.name.localeCompare(b.name, "zh-CN") || a.surface_id.localeCompare(b.surface_id, "zh-CN");
    });
    const registryMap = new Map(nextRegistry.map((item) => [item.surface_id, item]));
    const nextActiveSurfaceID = typeof input.activeSurfaceID === "string" ? input.activeSurfaceID.trim() : "";
    const nextMode = input.mode === "docked" ? "docked" : "floating";
    const runtimeSnapshots = Array.isArray(input.runtimes) ? input.runtimes : [];
    const nextRuntimeRecords = new Map(runtimeRecords);
    const seen = new Set();
    let changed = stableJSON(nextRegistry) !== stableJSON(registry) || nextActiveSurfaceID !== activeSurfaceID;

    runtimeSnapshots.forEach((runtime) => {
      const normalized = normalizeRuntimeContext(runtime, registryMap, nextMode);
      if (!normalized) return;
      seen.add(normalized.surface_id);
      const previous = nextRuntimeRecords.get(normalized.surface_id) || null;
      if (stableJSON(previous) !== stableJSON(normalized)) {
        changed = true;
      }
      nextRuntimeRecords.set(normalized.surface_id, normalized);
    });

    Array.from(nextRuntimeRecords.keys()).forEach((surfaceID) => {
      if (seen.has(surfaceID)) return;
      const previous = nextRuntimeRecords.get(surfaceID);
      const closed = normalizeClosedRuntimeContext(previous, registryMap, surfaceID);
      if (stableJSON(previous) !== stableJSON(closed)) {
        changed = true;
        nextRuntimeRecords.set(surfaceID, closed);
      }
    });

    if (!changed && snapshot.context_version > 0) {
      return clone(snapshot);
    }

    registry = nextRegistry;
    activeSurfaceID = nextActiveSurfaceID;
    runtimeRecords = nextRuntimeRecords;
    contextVersion += 1;
    rebuildSnapshot(reason, now());
    return clone(snapshot);
  }

  function getSnapshot() {
    return clone(snapshot);
  }

  function listRuntimeRecords() {
    return Array.from(runtimeRecords.values())
      .sort((a, b) => a.surface_id.localeCompare(b.surface_id, "zh-CN"))
      .map((item) => clone(item));
  }

  return {
    sync,
    getSnapshot,
    listRuntimeRecords,
  };
}
