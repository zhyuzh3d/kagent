import { callHubTool } from "../../../lib/hubToolClient.js";

export async function callTool(toolID, args = {}) {
  return callHubTool(toolID, args);
}
