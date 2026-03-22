import { callHubTool } from "../../lib/hubToolClient.js";

export async function callTool(toolID, args = {}, context = null) {
  return callHubTool(toolID, args, { context });
}
