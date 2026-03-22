import { createSurfaceRuntime } from "../../../lib/surfaceTool.js";

const els = {
  goalInput: document.getElementById("goalInput"),
  phaseText: document.getElementById("phaseText"),
  subtaskMeta: document.getElementById("subtaskMeta"),
  retryMeta: document.getElementById("retryMeta"),
  currentSubtaskChip: document.getElementById("currentSubtaskChip"),
  subtaskList: document.getElementById("subtaskList"),
  promptBox: document.getElementById("promptBox"),
  captureMeta: document.getElementById("captureMeta"),
  captureImage: document.getElementById("captureImage"),
  observationBox: document.getElementById("observationBox"),
  commandBox: document.getElementById("commandBox"),
  resultBox: document.getElementById("resultBox"),
  blockedChip: document.getElementById("blockedChip"),
  startBtn: document.getElementById("startBtn"),
  stepBtn: document.getElementById("stepBtn"),
  pauseBtn: document.getElementById("pauseBtn"),
  resumeBtn: document.getElementById("resumeBtn"),
  abortBtn: document.getElementById("abortBtn"),
};

const state = {
  phase: "idle",
  goal: els.goalInput.value.trim(),
  subtasks: [],
  current_subtask_index: 0,
  last_observation: null,
  last_commands: [],
  last_tool_results: [],
  retry_count: 0,
  latest_capture: null,
  latest_capture_meta: null,
  blocked_reason: "",
  generated_prompt: "",
  state_version: 0,
  auto_running: false,
};

let autoTimer = 0;

function sleep(ms) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}

function currentSubtask() {
  return state.subtasks[state.current_subtask_index] || null;
}

function lifecycleStatus() {
  switch (state.phase) {
    case "planning":
    case "observing":
    case "executing":
    case "reflecting":
      return "busy";
    case "error":
    case "blocked":
      return "error";
    case "paused":
      return "idle";
    default:
      return "ready";
  }
}

function businessState() {
  return {
    phase: state.phase,
    goal: state.goal,
    subtasks: clone(state.subtasks),
    current_subtask_index: state.current_subtask_index,
    last_observation: clone(state.last_observation),
    last_commands: clone(state.last_commands),
    last_tool_results: clone(state.last_tool_results),
    retry_count: state.retry_count,
    latest_capture: state.latest_capture,
    blocked_reason: state.blocked_reason,
    generated_prompt: state.generated_prompt,
  };
}

function escapeHTML(text) {
  return String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function syncState(eventType = "") {
  state.state_version += 1;
  runtime.setState(businessState(), lifecycleStatus());
  render();
  if (eventType) {
    runtime.emitStateChange(eventType);
  }
}

function setPhase(phase, eventType = "") {
  state.phase = phase;
  syncState(eventType || `phase.${phase}`);
}

function stopAutoLoop() {
  state.auto_running = false;
  if (autoTimer) {
    window.clearTimeout(autoTimer);
    autoTimer = 0;
  }
}

function scheduleAutoLoop() {
  if (!state.auto_running) return;
  if (["paused", "blocked", "done", "error", "idle"].includes(state.phase)) return;
  if (autoTimer) return;
  autoTimer = window.setTimeout(() => {
    autoTimer = 0;
    missionStep({ auto: true }).catch(handleError);
  }, 1200);
}

function render() {
  const subtask = currentSubtask();
  els.phaseText.textContent = state.phase + (state.blocked_reason ? ` · ${state.blocked_reason}` : "");
  els.subtaskMeta.textContent = `subtasks: ${state.subtasks.length}`;
  els.retryMeta.textContent = `retry: ${state.retry_count}`;
  els.currentSubtaskChip.textContent = subtask ? `${state.current_subtask_index + 1}/${state.subtasks.length}` : "No subtask";
  els.blockedChip.textContent = state.blocked_reason || "ok";
  els.promptBox.textContent = state.generated_prompt || "(empty)";
  els.captureMeta.textContent = state.latest_capture_meta
    ? `${state.latest_capture_meta.width}x${state.latest_capture_meta.height}`
    : "No capture";
  els.captureImage.src = state.latest_capture || "";
  els.captureImage.style.display = state.latest_capture ? "block" : "none";
  els.observationBox.textContent = state.last_observation ? JSON.stringify(state.last_observation, null, 2) : "(empty)";
  els.commandBox.textContent = state.last_commands.length ? JSON.stringify(state.last_commands, null, 2) : "(empty)";
  els.resultBox.textContent = state.last_tool_results.length ? JSON.stringify(state.last_tool_results, null, 2) : "(empty)";
  els.subtaskList.innerHTML = state.subtasks.length
    ? state.subtasks.map((item, index) => {
      const cls = index === state.current_subtask_index ? "subtask-item active" : "subtask-item";
      return `<article class="${cls}">
        <div class="subtask-title">${escapeHTML(item.goal || item.id || `step-${index + 1}`)}</div>
        <div class="subtask-meta">done_when=${escapeHTML(item.done_when || "")}</div>
        <div class="subtask-meta">turns=${Number(item.turns || 0)} / max=${Number(item.max_turns || 6)}</div>
      </article>`;
    }).join("")
    : '<article class="subtask-item">等待 mission.start 或手动 Step。</article>';
}

function parseJSONBlock(text) {
  const raw = String(text || "").trim();
  if (!raw) return null;
  const cleaned = raw.startsWith("```")
    ? raw.replace(/^```(?:json)?/i, "").replace(/```$/, "").trim()
    : raw;
  try {
    return JSON.parse(cleaned);
  } catch (_) {}
  const start = cleaned.indexOf("{");
  const end = cleaned.lastIndexOf("}");
  if (start >= 0 && end > start) {
    try {
      return JSON.parse(cleaned.slice(start, end + 1));
    } catch (_) {}
  }
  return null;
}

async function planMission(goal) {
  const plannerPrompt = [
    "你是 GUI Agent Planner。",
    "请把用户目标拆成 4-8 个顺序子任务。",
    "返回 JSON：{\"subtasks\":[{\"id\":\"s1\",\"goal\":\"...\",\"done_when\":\"...\",\"hints\":[\"...\"],\"max_turns\":6}]}。",
    "不要输出 JSON 以外的任何文字。",
    `用户目标：${goal}`,
  ].join("\n");
  try {
    const result = await runtime.callTool("ai.llm.generate", {
      input: plannerPrompt,
      system_prompt: "只输出 JSON。",
    });
    const parsed = parseJSONBlock(result && result.text);
    const items = parsed && Array.isArray(parsed.subtasks) ? parsed.subtasks : [];
    if (items.length) {
      return items.map((item, index) => ({
        id: item.id || `s${index + 1}`,
        goal: item.goal || `step-${index + 1}`,
        done_when: item.done_when || "",
        hints: Array.isArray(item.hints) ? item.hints : [],
        max_turns: Number.isFinite(item.max_turns) ? Math.max(2, Math.min(12, item.max_turns)) : 6,
        turns: 0,
      }));
    }
  } catch (_) {}
  return [
    { id: "s1", goal: "打开新的浏览器标签页", done_when: "看到一个新的空白标签页或地址栏已聚焦", hints: ["优先使用快捷键"], max_turns: 4, turns: 0 },
    { id: "s2", goal: "导航到 doubao.com", done_when: "界面显示 Doubao 首页或已进入对应站点", hints: ["可直接输入地址并提交"], max_turns: 5, turns: 0 },
    { id: "s3", goal: "进入可执行画图的页面模式", done_when: "截图中能看到画图模式或画图输入区域", hints: ["寻找画图、图片生成、文生图按钮"], max_turns: 8, turns: 0 },
    { id: "s4", goal: "生成适合当前目标的文生图提示词", done_when: "surface 内已保存 generated_prompt", hints: ["如果已经有提示词则跳过"], max_turns: 3, turns: 0 },
    { id: "s5", goal: "聚焦画图输入框", done_when: "输入框已聚焦且可输入文本", hints: ["必要时先点击输入区域"], max_turns: 6, turns: 0 },
    { id: "s6", goal: "录入生成的提示词", done_when: "输入框包含 generated_prompt 或明显包含其核心片段", hints: ["优先使用 text_insert"], max_turns: 5, turns: 0 },
    { id: "s7", goal: "触发生成", done_when: "已点击生成按钮或按下提交键，界面进入生成态", hints: ["观察按钮文本或 loading"], max_turns: 5, turns: 0 },
  ];
}

async function ensureGeneratedPrompt() {
  if (state.generated_prompt) return state.generated_prompt;
  const result = await runtime.callTool("ai.llm.generate", {
    input: `请为以下目标生成一段适合文生图的中文提示词，只输出提示词正文，不要解释：${state.goal}`,
    system_prompt: "你是画图提示词生成器，只输出最终提示词。",
  });
  state.generated_prompt = String(result && result.text || "").trim();
  syncState("prompt.generated");
  return state.generated_prompt;
}

async function captureScreen() {
  const result = await runtime.callTool("autogui.screen.capture", {});
  const base64 = String(result && result.png_base64 || "").trim();
  if (!base64) {
    throw new Error("capture returned empty image");
  }
  state.latest_capture = `data:image/png;base64,${base64}`;
  state.latest_capture_meta = {
    width: Number(result.width || 0),
    height: Number(result.height || 0),
  };
  syncState("capture.updated");
  return state.latest_capture;
}

function controllerSchema() {
  return {
    type: "object",
    properties: {
      ui_summary: { type: "string" },
      subtask_status: { type: "string" },
      advance_to_next_subtask: { type: "boolean" },
      blocked_reason: { type: "string" },
      confidence: { type: "number" },
      commands: {
        type: "array",
        items: {
          type: "object",
          properties: {
            kind: { type: "string" },
          },
        },
      },
    },
    required: ["ui_summary", "subtask_status", "advance_to_next_subtask", "commands", "confidence"],
  };
}

async function askController(subtask) {
  const instruction = [
    "你是 GUI Visual Controller。",
    "基于当前截图、子任务和执行历史，返回下一步 JSON。",
    "每轮 commands 最多 3 条，只允许这些 kind：mouse_move, mouse_click, mouse_scroll, key_press, text_insert, wait。",
    "坐标必须是屏幕绝对坐标。",
    "如果该子任务已经完成，可设置 subtask_status=done 并 advance_to_next_subtask=true。",
    "如果无法安全推进，可设置 subtask_status=blocked 并给出 blocked_reason。",
    `Mission: ${state.goal}`,
    `Current subtask: ${subtask.goal}`,
    `Done when: ${subtask.done_when}`,
    `Hints: ${(subtask.hints || []).join(" | ")}`,
    `Generated prompt: ${state.generated_prompt || "(empty)"}`,
    `Recent commands: ${JSON.stringify(state.last_commands)}`,
    `Recent tool results: ${JSON.stringify(state.last_tool_results)}`,
  ].join("\n");

  const result = await runtime.callTool("ai.vision.isr", {
    instruction,
    images: [state.latest_capture],
    response_schema: controllerSchema(),
    system_prompt: "你是谨慎的 GUI Agent controller，只输出 JSON。",
    temperature: 0.1,
  });
  const raw = result && result.json ? result.json : parseJSONBlock(result && result.text);
  if (!raw) {
    throw new Error("vision controller returned empty json");
  }
  return normalizeControllerOutput(raw);
}

function normalizeControllerOutput(raw) {
  const commands = Array.isArray(raw.commands) ? raw.commands.slice(0, 3).map((item) => item && typeof item === "object" ? item : null).filter(Boolean) : [];
  return {
    ui_summary: String(raw.ui_summary || ""),
    subtask_status: ["continue", "done", "blocked"].includes(String(raw.subtask_status || "")) ? String(raw.subtask_status) : "continue",
    advance_to_next_subtask: !!raw.advance_to_next_subtask,
    blocked_reason: String(raw.blocked_reason || ""),
    confidence: Number.isFinite(raw.confidence) ? raw.confidence : 0,
    commands,
  };
}

function substitutePrompt(text) {
  if (!text) return text;
  if (text.includes("{{generated_prompt}}")) {
    return text.replaceAll("{{generated_prompt}}", state.generated_prompt || "");
  }
  return text;
}

async function executeCommand(command) {
  const kind = String(command.kind || "").trim();
  switch (kind) {
    case "mouse_move":
      return runtime.callTool("autogui.mouse.move", {
        x: clampNumber(command.x, 0, 6000),
        y: clampNumber(command.y, 0, 6000),
      });
    case "mouse_click":
      if (Number.isFinite(command.x) && Number.isFinite(command.y)) {
        await runtime.callTool("autogui.mouse.move", {
          x: clampNumber(command.x, 0, 6000),
          y: clampNumber(command.y, 0, 6000),
        });
      }
      return runtime.callTool("autogui.mouse.click", {
        button: ["left", "right", "center"].includes(String(command.button || "")) ? String(command.button) : "left",
        double: !!command.double,
      });
    case "mouse_scroll":
      return runtime.callTool("autogui.mouse.scroll", {
        amount: clampNumber(command.amount, 1, 20),
        direction: String(command.direction || "down") === "up" ? "up" : "down",
      });
    case "key_press":
      return runtime.callTool("autogui.keyboard.press", {
        key: String(command.key || "").trim(),
        modifiers: Array.isArray(command.modifiers) ? command.modifiers.map((item) => String(item)) : [],
      });
    case "text_insert":
      return runtime.callTool("autogui.text.insert", {
        text: substitutePrompt(String(command.text || "")).slice(0, 1000),
        mode: String(command.mode || "paste_preferred") === "type_only" ? "type_only" : "paste_preferred",
        clear_before: !!command.clear_before,
        submit: !!command.submit,
      });
    case "wait": {
      const durationMS = clampNumber(command.duration_ms, 200, 5000);
      await sleep(durationMS);
      return { waited_ms: durationMS };
    }
    default:
      throw new Error(`unsupported command kind: ${kind}`);
  }
}

function clampNumber(value, min, max) {
  const num = Number(value);
  if (!Number.isFinite(num)) return min;
  return Math.min(max, Math.max(min, num));
}

function markBlocked(reason) {
  stopAutoLoop();
  state.blocked_reason = reason || "blocked";
  setPhase("blocked", "phase.blocked");
}

function completeCurrentSubtask() {
  const subtask = currentSubtask();
  if (!subtask) return;
  subtask.completed = true;
  state.current_subtask_index += 1;
  state.retry_count = 0;
  if (state.current_subtask_index >= state.subtasks.length) {
    stopAutoLoop();
    setPhase("done", "phase.done");
    return;
  }
  setPhase("planning", "subtask.advance");
}

async function missionStart(goal) {
  stopAutoLoop();
  state.goal = String(goal || els.goalInput.value || "").trim();
  if (!state.goal) {
    throw new Error("goal is required");
  }
  state.phase = "planning";
  state.subtasks = await planMission(state.goal);
  state.current_subtask_index = 0;
  state.last_observation = null;
  state.last_commands = [];
  state.last_tool_results = [];
  state.retry_count = 0;
  state.latest_capture = null;
  state.latest_capture_meta = null;
  state.blocked_reason = "";
  state.generated_prompt = "";
  state.auto_running = true;
  syncState("mission.start");
  scheduleAutoLoop();
  return businessState();
}

async function missionPause() {
  stopAutoLoop();
  setPhase("paused", "mission.pause");
  return { paused: true };
}

async function missionResume() {
  if (state.phase === "done") {
    return { resumed: false, reason: "mission_done" };
  }
  state.blocked_reason = "";
  state.auto_running = true;
  setPhase("observing", "mission.resume");
  scheduleAutoLoop();
  return { resumed: true };
}

async function missionAbort() {
  stopAutoLoop();
  state.blocked_reason = "aborted";
  setPhase("idle", "mission.abort");
  return { aborted: true };
}

async function missionStep({ auto = false } = {}) {
  if (!state.goal) {
    state.goal = String(els.goalInput.value || "").trim();
  }
  if (!state.goal) {
    throw new Error("goal is required");
  }
  if (!state.subtasks.length) {
    state.subtasks = await planMission(state.goal);
    state.current_subtask_index = 0;
    syncState("mission.plan");
  }
  const subtask = currentSubtask();
  if (!subtask) {
    setPhase("done", "phase.done");
    return { done: true };
  }
  if (subtask.goal.includes("提示词") && !state.generated_prompt) {
    setPhase("planning", "prompt.planning");
    await ensureGeneratedPrompt();
  }
  subtask.turns = Number(subtask.turns || 0) + 1;
  if (subtask.turns > Number(subtask.max_turns || 6)) {
    markBlocked(`subtask max_turns reached: ${subtask.goal}`);
    return { blocked: true };
  }

  setPhase("observing", "phase.observing");
  await captureScreen();

  const controller = await askController(subtask);
  state.last_observation = controller;
  syncState("controller.output");

  if (controller.subtask_status === "blocked") {
    markBlocked(controller.blocked_reason || "controller blocked");
    return { blocked: true };
  }

  if (!controller.commands.length && (controller.advance_to_next_subtask || controller.subtask_status === "done")) {
    completeCurrentSubtask();
    if (state.auto_running || auto) scheduleAutoLoop();
    return { advanced: true };
  }

  if (!controller.commands.length) {
    state.retry_count += 1;
    syncState("controller.no_command");
    if (state.retry_count >= 3 || controller.confidence < 0.35) {
      markBlocked(controller.blocked_reason || "controller returned no actionable commands");
    } else if (state.auto_running || auto) {
      scheduleAutoLoop();
    }
    return { idle: true };
  }

  state.last_commands = controller.commands;
  setPhase("executing", "phase.executing");
  const results = [];
  for (const command of controller.commands) {
    // eslint-disable-next-line no-await-in-loop
    const result = await executeCommand(command);
    results.push({ kind: command.kind, result });
  }
  state.last_tool_results = results;
  state.retry_count = controller.confidence >= 0.5 ? 0 : state.retry_count + 1;
  syncState("commands.executed");

  setPhase("reflecting", "phase.reflecting");
  await captureScreen();

  if (controller.advance_to_next_subtask || controller.subtask_status === "done") {
    completeCurrentSubtask();
  } else if (state.retry_count >= 4) {
    markBlocked("too many low-confidence rounds");
    return { blocked: true };
  } else {
    setPhase("observing", "phase.observing");
  }
  if (state.auto_running || auto) {
    scheduleAutoLoop();
  }
  return { ok: true, phase: state.phase };
}

function handleError(error) {
  stopAutoLoop();
  state.blocked_reason = error && error.message ? error.message : String(error);
  state.phase = "error";
  syncState("phase.error");
}

const runtime = createSurfaceRuntime({
  surfaceID: "6fbe413d-fd44-4140-8c4a-9aa0185797de",
  surfaceType: "buildin",
  surfaceVersion: "1.0",
  title: "Visual Agent",
  description: "视觉闭环 GUI Agent surface，面向 doubao.com 文生图任务。",
  actions: [
    { name: "mission.start", description: "开始一条新 mission", args_schema: { goal: "string" }, result_schema: { phase: "string" }, timeout_ms_default: 8000, side_effect: "write", streaming: false },
    { name: "mission.pause", description: "暂停自动执行", args_schema: {}, result_schema: { paused: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "mission.resume", description: "恢复自动执行", args_schema: {}, result_schema: { resumed: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "mission.abort", description: "终止当前 mission", args_schema: {}, result_schema: { aborted: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "mission.step", description: "执行单步循环", args_schema: {}, result_schema: { ok: "boolean" }, timeout_ms_default: 20000, side_effect: "write", streaming: false },
    { name: "mission.get_state", description: "获取当前 mission 状态", args_schema: {}, result_schema: { phase: "string" }, timeout_ms_default: 3000, side_effect: "none", streaming: false },
  ],
  initialState: businessState(),
  getVisibleText: () => `${state.phase}:${currentSubtask() ? currentSubtask().goal : "idle"}`,
  getStateVersion: () => state.state_version,
  onReady: async ({ setState, emitLog }) => {
    setState(businessState(), "ready");
    render();
    emitLog("info", "visual_agent ready");
  },
  onAction: async ({ action }) => {
    switch (action.name) {
      case "mission.start":
        return missionStart(action.args && action.args.goal);
      case "mission.pause":
        return missionPause();
      case "mission.resume":
        return missionResume();
      case "mission.abort":
        return missionAbort();
      case "mission.step":
        return missionStep({ auto: false });
      case "mission.get_state":
        return businessState();
      default:
        throw new Error(`unknown action: ${action.name}`);
    }
  },
});

els.startBtn.addEventListener("click", () => missionStart(els.goalInput.value).catch(handleError));
els.stepBtn.addEventListener("click", () => missionStep({ auto: false }).catch(handleError));
els.pauseBtn.addEventListener("click", () => missionPause().catch(handleError));
els.resumeBtn.addEventListener("click", () => missionResume().catch(handleError));
els.abortBtn.addEventListener("click", () => missionAbort().catch(handleError));
els.goalInput.addEventListener("change", () => {
  state.goal = els.goalInput.value.trim();
  syncState("goal.update");
});

render();
