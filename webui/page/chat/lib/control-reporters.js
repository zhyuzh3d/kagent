export function createControlMessageReporters(options = {}) {
  const getSessionController = typeof options.getSessionController === "function" ? options.getSessionController : () => null;
  const getTurnId = typeof options.getTurnId === "function" ? options.getTurnId : () => 0;

  function send(msg) {
    const sessionController = getSessionController();
    if (!sessionController || typeof sessionController.sendControlMessage !== "function") return false;
    sessionController.sendControlMessage(msg);
    return true;
  }

  function resolveTurnId(payload) {
    return Number.isFinite(payload && payload.turnId) ? payload.turnId : getTurnId();
  }

  function reportActionRecord(payload) {
    const p = payload && typeof payload === "object" ? payload : {};
    const actionName = typeof p.actionName === "string" ? p.actionName : "";
    if (!actionName) return;
    send({
      type: "send_control",
      control: "action_result",
      reason: typeof p.category === "string" && p.category ? p.category : "dispatch",
      turn_id: resolveTurnId(p),
      text: typeof p.content === "string" ? p.content : "",
      extra: {
        action_id: (typeof p.actionId === "string" && p.actionId) ? p.actionId : `act-${Date.now()}-${Math.floor(Math.random() * 100000)}`,
        action_name: actionName,
        action_status: typeof p.status === "string" && p.status ? p.status : "unknown",
        action_followup: typeof p.followup === "string" && p.followup ? p.followup : "none",
        action_surface_id: typeof p.actionSurfaceID === "string" ? p.actionSurfaceID : "",
        surface_type: typeof p.actionSurfaceType === "string" ? p.actionSurfaceType : "",
        surface_version: typeof p.actionSurfaceVersion === "string" ? p.actionSurfaceVersion : "",
        action_manual_confirm: typeof p.manualConfirm === "string" ? p.manualConfirm : "",
        action_block_reason: typeof p.blockReason === "string" ? p.blockReason : "",
        action_args: p.args && typeof p.args === "object" ? p.args : {},
        action_result: p.result && typeof p.result === "object" ? p.result : {},
        action_effect: p.effect && typeof p.effect === "object" ? p.effect : {},
        action_state: p.state && typeof p.state === "object" ? p.state : {},
      },
    });
  }

  function reportStateChange(payload) {
    const p = payload && typeof payload === "object" ? payload : {};
    send({
      type: "send_control",
      control: "state_change",
      turn_id: resolveTurnId(p),
      extra: {
        surface_id: typeof p.surface_id === "string" ? p.surface_id : "",
        surface_type: typeof p.surface_type === "string" ? p.surface_type : "",
        surface_version: typeof p.surface_version === "string" ? p.surface_version : "",
        surface_name: typeof p.surface_name === "string" ? p.surface_name : "",
        surface_title: typeof p.surface_title === "string" ? p.surface_title : "",
        event_type: typeof p.event_type === "string" ? p.event_type : "state_change",
        business_state: p.business_state && typeof p.business_state === "object" ? p.business_state : {},
        visible_text: typeof p.visible_text === "string" ? p.visible_text : "",
        status: typeof p.status === "string" ? p.status : "ready",
        state_version: Number.isFinite(p.state_version) ? p.state_version : 0,
        updated_at_ms: Number.isFinite(p.updated_at_ms) ? p.updated_at_ms : Date.now(),
      },
    });
  }

  function reportConfigChange(payload) {
    const p = payload && typeof payload === "object" ? payload : {};
    send({
      type: "send_control",
      control: "config_change",
      turn_id: getTurnId(),
      extra: {
        config_source: typeof p.source === "string" && p.source ? p.source : "config_drawer",
        config_changed_paths: Array.isArray(p.changedPaths) ? p.changedPaths : [],
        config_snapshot: p.config && typeof p.config === "object" ? p.config : {},
      },
    });
  }

  function reportSurfaceContext(payload) {
    const p = payload && typeof payload === "object" ? payload : {};
    const controls = Array.isArray(p.contextControls) ? p.contextControls : [];
    controls.forEach((msg) => {
      send({
        ...msg,
        turn_id: getTurnId(),
      });
    });
  }

  return {
    reportActionRecord,
    reportStateChange,
    reportConfigChange,
    reportSurfaceContext,
  };
}
