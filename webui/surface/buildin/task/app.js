import { createSurfaceRuntime } from "../../../lib/surfaceTool.js";

const state = {
  phase: "idle",
  tasks: [],
  timeline: [],
  stateVersion: 0,
};

const taskInputEl = document.getElementById("taskInput");
const taskListEl = document.getElementById("taskList");
const timelineListEl = document.getElementById("timelineList");
const statusChipEl = document.getElementById("statusChip");

function snapshot() {
  return {
    phase: state.phase,
    tasks: state.tasks.map((item) => ({ ...item })),
    timeline: state.timeline.map((item) => ({ ...item })),
  };
}

function nowLabel() {
  return new Date().toLocaleTimeString();
}

function render() {
  statusChipEl.textContent = state.phase;
  taskListEl.innerHTML = state.tasks.length
    ? state.tasks.map((item) => `
      <article class="task-item ${item.done ? "done" : ""}">
        <div class="task-top">
          <strong>${escapeHTML(item.title)}</strong>
          <button type="button" data-task-id="${item.id}">${item.done ? "Undo" : "Done"}</button>
        </div>
        <div class="task-meta">id=${item.id} · updated=${item.updated_at}</div>
      </article>
    `).join("")
    : '<article class="task-item">当前没有任务。</article>';
  timelineListEl.innerHTML = state.timeline.length
    ? state.timeline.map((item) => `
      <article class="timeline-item">
        <strong>${escapeHTML(item.text)}</strong>
        <div class="timeline-meta">${item.level} · ${item.at}</div>
      </article>
    `).join("")
    : '<article class="timeline-item">等待首条时间线事件。</article>';
  taskListEl.querySelectorAll("[data-task-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      await toggleTask(button.getAttribute("data-task-id") || "");
    });
  });
}

function escapeHTML(text) {
  return String(text || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

function bumpState() {
  state.stateVersion += 1;
}

function appendTimeline(text, level = "info") {
  state.timeline.unshift({
    id: `timeline-${Date.now()}-${Math.random().toString(16).slice(2, 6)}`,
    text,
    level,
    at: nowLabel(),
  });
  state.timeline = state.timeline.slice(0, 24);
}

function commit(nextPhase = state.phase) {
  state.phase = nextPhase;
  bumpState();
  runtime.setState(snapshot(), nextPhase);
  render();
  runtime.emitStateChange(`phase.${nextPhase}`);
}

async function addTask(title) {
  const clean = String(title || "").trim();
  if (!clean) {
    throw new Error("title is required");
  }
  state.tasks.unshift({
    id: `task-${Date.now()}-${Math.random().toString(16).slice(2, 6)}`,
    title: clean,
    done: false,
    updated_at: nowLabel(),
  });
  appendTimeline(`新增任务：${clean}`);
  commit("ready");
  return { tasks: state.tasks.length };
}

async function toggleTask(taskID) {
  const item = state.tasks.find((task) => task.id === taskID);
  if (!item) {
    throw new Error("task not found");
  }
  item.done = !item.done;
  item.updated_at = nowLabel();
  appendTimeline(`${item.done ? "完成" : "恢复"}任务：${item.title}`);
  commit("ready");
  return { task_id: taskID, done: item.done };
}

const runtime = createSurfaceRuntime({
  surfaceType: "buildin",
  surfaceVersion: "1.0",
  title: "Task Surface",
  description: "任务与时间线协作面板，可被其他 surface 作为执行时间线展示面。",
  actions: [
    { name: "get_state", description: "获取任务面板完整状态", args_schema: {}, result_schema: { phase: "string" }, timeout_ms_default: 3000, side_effect: "none", streaming: false },
    { name: "task.add", description: "新增一条任务", args_schema: { title: "string" }, result_schema: { tasks: "number" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "task.toggle", description: "切换任务完成状态", args_schema: { task_id: "string" }, result_schema: { done: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "timeline.append", description: "追加一条执行时间线", args_schema: { text: "string", level: "string" }, result_schema: { count: "number" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "task.clear_done", description: "移除已完成任务", args_schema: {}, result_schema: { removed: "number" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
  ],
  initialState: snapshot(),
  getVisibleText: () => state.tasks.map((item) => item.title).slice(0, 4).join(" / "),
  getStateVersion: () => state.stateVersion,
  onReady: async ({ emitLog, setState }) => {
    setState(snapshot(), "idle");
    render();
    emitLog("info", "task surface ready");
  },
  onAction: async ({ action }) => {
    switch (action.name) {
      case "get_state":
        return snapshot();
      case "task.add":
        return addTask(action.args && action.args.title);
      case "task.toggle":
        return toggleTask(action.args && action.args.task_id);
      case "timeline.append":
        appendTimeline(
          action.args && action.args.text ? String(action.args.text) : "(empty)",
          action.args && action.args.level ? String(action.args.level) : "info",
        );
        commit("ready");
        return { count: state.timeline.length };
      case "task.clear_done": {
        const before = state.tasks.length;
        state.tasks = state.tasks.filter((item) => !item.done);
        appendTimeline("清理已完成任务");
        commit("ready");
        return { removed: before - state.tasks.length };
      }
      default:
        throw new Error(`unknown action: ${action.name}`);
    }
  },
});

document.getElementById("addTaskBtn").addEventListener("click", async () => {
  const text = taskInputEl.value;
  taskInputEl.value = "";
  await addTask(text);
});

taskInputEl.addEventListener("keydown", async (event) => {
  if (event.key !== "Enter") return;
  event.preventDefault();
  const text = taskInputEl.value;
  taskInputEl.value = "";
  await addTask(text);
});

render();
