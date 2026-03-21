import { callTool } from "./api.js";
import { state } from "./state.js";
import { decodeBase64, encodeBase64, setStatus } from "./utils.js";

function normalizeServiceRecord(record, toolsByServiceID) {
  const serviceID = String(record?.service_id || "").trim();
  const startup = record?.startup || null;
  const registeredManifest = record?.registered_manifest || null;
  const serviceTools = toolsByServiceID.get(serviceID) || [];
  const description = record?.description || (serviceTools[0] && serviceTools[0].spec?.description) || "";
  const startupStatus = String(startup?.status || "").trim();
  return {
    ...record,
    service_id: serviceID,
    startup,
    is_managed: Boolean(record?.dir),
    description,
    tool_count: serviceTools.length || (Array.isArray(registeredManifest?.provides) ? registeredManifest.provides.length : 0),
    registered: Boolean(record?.registered),
    active: Boolean(record?.active),
    healthy: Boolean(record?.healthy),
    status: String(record?.status || "").trim() || startupStatus,
    instance_id: String(record?.instance_id || "").trim(),
    endpoint: String(record?.endpoint || "").trim(),
    pid: Number(record?.pid || 0) || 0,
    registered_manifest: registeredManifest,
    tools: serviceTools,
  };
}

function mergeServiceData(result) {
  const services = Array.isArray(result?.services) ? result.services : [];
  const tools = Array.isArray(result?.tools) ? result.tools : [];
  const toolsByServiceID = new Map();

  for (const tool of tools) {
    const serviceID = String(tool?.service_id || tool?.governance?.bound_service_id || "").trim();
    if (!serviceID) continue;
    if (!toolsByServiceID.has(serviceID)) toolsByServiceID.set(serviceID, []);
    toolsByServiceID.get(serviceID).push(tool);
  }

  const merged = services.map((item) => normalizeServiceRecord(item, toolsByServiceID));
  merged.sort((a, b) => a.service_id.localeCompare(b.service_id));
  tools.sort((a, b) => String(a?.tool_id || "").localeCompare(String(b?.tool_id || "")));
  return { managed: merged, registered: services, tools };
}

export async function refreshList(onDone) {
  setStatus({ statusBadge: document.getElementById("statusBadge") }, "同步中");
  try {
    const result = await callTool("hub.admin.services.list", {});
    state.appRoot = result.app_root || "";
    const merged = mergeServiceData(result);
    state.managed = merged.managed;
    state.registered = merged.registered;
    state.tools = merged.tools;
    if (state.selectedID && !state.managed.some((item) => item.service_id === state.selectedID)) {
      state.selectedID = "";
    }
    if (onDone) onDone(result);
    setStatus({ statusBadge: document.getElementById("statusBadge") }, "已同步", "running");
  } catch (err) {
    setStatus({ statusBadge: document.getElementById("statusBadge") }, "同步失败", "stopped");
    console.error("List refresh failed:", err);
  }
}

export async function executeLifecycleAction(serviceID, action, onDone) {
  const toolID = `hub.admin.service.${action}`;
  await callTool(toolID, { service_id: serviceID });
  await refreshList(onDone);
}

export async function getServiceDetail(serviceID) {
  return await callTool("hub.admin.service.get", { service_id: serviceID });
}

export async function updateGovernance(serviceID, governance) {
  await callTool("hub.admin.service.governance.update", {
    service_id: serviceID,
    enabled: !!governance?.enabled,
    reliability: String(governance?.reliability || "unverified"),
  });
}

export async function updateConfig(serviceID, configJson, type) {
  await callTool("hub.admin.service.config.update", { service_id: serviceID, config_json: configJson, type });
}

export async function updateManifest(serviceID, manifestJson) {
  await callTool("hub.admin.service.manifest.update", { service_id: serviceID, manifest: manifestJson });
}

export async function runProbe(serviceID, toolID, args) {
  return await callTool("hub.admin.tool.probe", { service_id: serviceID, tool_id: toolID, args });
}

export async function generateService(name, prompt, buildAfter) {
  const result = await callTool("hub.admin.service.generate", { service_name: name, prompt });
  if (buildAfter && result.service_id) {
    await callTool("hub.admin.service.build", { service_id: result.service_id });
  }
  return result;
}

export async function getFileList(serviceID) {
  const result = await callTool("hub.admin.service.files.list", { service_id: serviceID });
  return Array.isArray(result.items) ? result.items.filter((it) => !it.is_dir) : [];
}

export async function readFile(serviceID, path) {
  const result = await callTool("hub.admin.service.files.read", { service_id: serviceID, path });
  return decodeBase64(result.data_base64 || "");
}

export async function writeFile(serviceID, path, content) {
  await callTool("hub.admin.service.files.write", {
    service_id: serviceID,
    path,
    data_base64: encodeBase64(content),
  });
}
