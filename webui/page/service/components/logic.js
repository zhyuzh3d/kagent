import { callTool } from "./api.js";
import { state, getSelectedService } from "./state.js";
import { pretty, parseJSON, setStatus, encodeBase64, decodeBase64 } from "./utils.js";

export async function refreshList(onDone) {
  const badge = document.getElementById("statusBadge");
  if (badge) { badge.textContent = "REFRESHING..."; badge.className = "status-badge"; }
  
  try {
    const result = await callTool("hub.admin.services.list", {});
    state.managed = Array.isArray(result.managed) ? result.managed : [];
    if (onDone) onDone();
    if (badge) { badge.textContent = "READY"; badge.className = "status-badge running"; }
  } catch (err) {
    if (badge) { badge.textContent = "ERROR"; badge.className = "status-badge stopped"; }
    console.error("List refresh failed:", err);
  }
}

export async function executeLifecycleAction(serviceID, action, onDone) {
  const toolID = `hub.admin.service.${action}`;
  try {
    await callTool(toolID, { service_id: serviceID });
    await refreshList(onDone);
  } catch (err) {
    alert(`Action ${action} failed: ${err.message}`);
  }
}

// Data fetching for modals
export async function getServiceDetail(serviceID) {
  return await callTool("hub.admin.service.get", { service_id: serviceID });
}

export async function updateConfig(serviceID, configJson) {
  await callTool("hub.admin.service.config.update", { service_id: serviceID, config_json: configJson });
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
