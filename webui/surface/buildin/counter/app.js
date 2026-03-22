import { createSurfaceRuntime } from "../../../lib/surfaceTool.js";

const state = {
  count: 0,
  stateVersion: 0,
};

const countEl = document.getElementById("countValue");
const stateTextEl = document.getElementById("stateText");

function businessState() {
  return { count: state.count };
}

function syncStateText(note = "connected") {
  stateTextEl.textContent = JSON.stringify({
    note,
    state_version: state.stateVersion,
    ...businessState(),
  }, null, 2);
}

function render() {
  countEl.textContent = String(state.count);
  syncStateText();
}

async function runAction(name, args = {}, helpers = {}) {
  switch (name) {
    case "get_state":
      return {
        count: state.count,
        state: businessState(),
      };
    case "set_count":
      if (!Number.isFinite(args.count)) {
        throw new Error("count must be a number");
      }
      state.count = Math.trunc(args.count);
      break;
    case "increment":
      state.count += Number.isFinite(args.step) ? Math.trunc(args.step) : 1;
      break;
    case "reset":
      state.count = 0;
      break;
    case "flash_notice":
      await helpers.callHostAction("host.flash", {
        message: typeof args.message === "string" && args.message.trim() ? args.message.trim() : `count=${state.count}`,
      });
      return {
        flashed: true,
        count: state.count,
      };
    default:
      throw new Error(`unknown action: ${name}`);
  }
  state.stateVersion += 1;
  helpers.setState?.(businessState(), "ready");
  render();
  helpers.emitStateChange?.(`action.${name}`);
  return {
    count: state.count,
    state: businessState(),
  };
}

const runtime = createSurfaceRuntime({
  surfaceType: "buildin",
  surfaceVersion: "1.0",
  title: "Counter",
  description: "最小 surface 示例，展示 register/ready/action/state 协议。",
  actions: [
    { name: "get_state", description: "读取当前计数器状态", args_schema: {}, result_schema: { count: "number" }, timeout_ms_default: 2000, side_effect: "none", streaming: false },
    { name: "set_count", description: "设置计数器数值", args_schema: { count: "number" }, result_schema: { count: "number" }, timeout_ms_default: 2000, side_effect: "write", streaming: false },
    { name: "increment", description: "按步长增加计数", args_schema: { step: "number" }, result_schema: { count: "number" }, timeout_ms_default: 2000, side_effect: "write", streaming: false },
    { name: "reset", description: "重置为 0", args_schema: {}, result_schema: { count: "number" }, timeout_ms_default: 2000, side_effect: "write", streaming: false },
    { name: "flash_notice", description: "请求宿主显示提示", args_schema: { message: "string" }, result_schema: { flashed: "boolean" }, timeout_ms_default: 4000, side_effect: "host", streaming: false },
  ],
  initialState: businessState(),
  getVisibleText: () => String(state.count),
  getStateVersion: () => state.stateVersion,
  onReady: async ({ emitLog, setState }) => {
    setState(businessState(), "ready");
    emitLog("info", "counter ready");
    render();
  },
  onAction: async ({ action, callHostAction, setState, emitStateChange }) => {
    const result = await runAction(action.name, action.args || {}, {
      callHostAction,
      setState,
      emitStateChange,
    });
    return result;
  },
});

document.getElementById("decBtn").addEventListener("click", async () => {
  await runAction("increment", { step: -1 }, {
    setState: runtime.setState,
    emitStateChange: runtime.emitStateChange,
  });
});

document.getElementById("resetBtn").addEventListener("click", async () => {
  await runAction("reset", {}, {
    setState: runtime.setState,
    emitStateChange: runtime.emitStateChange,
  });
});

document.getElementById("incBtn").addEventListener("click", async () => {
  await runAction("increment", { step: 1 }, {
    setState: runtime.setState,
    emitStateChange: runtime.emitStateChange,
  });
});

document.getElementById("flashBtn").addEventListener("click", async () => {
  await runAction("flash_notice", { message: `count=${state.count}` }, {
    callHostAction: runtime.callHostAction,
  });
});

render();
