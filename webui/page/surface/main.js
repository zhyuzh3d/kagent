import { GLOBAL_ACTION_DESCRIPTORS, createActionDispatcher } from "./action-dispatcher.js";
import { createLLMProtocolSession } from "./llm-protocol.js";
import { assemblePromptBundle, loadPromptOverrideText, savePromptOverrideText } from "./prompt-assembly.js";
import { createRecordStore } from "./record-store.js";
import { createSurfaceManager } from "./surface-manager.js";
import { createID, sleep } from "./utils.js";

const els = {
  globalStatus: document.getElementById("globalStatus"),
  surfaceUrlInput: document.getElementById("surfaceUrlInput"),
  loadSurfaceBtn: document.getElementById("loadSurfaceBtn"),
  loadUnsafeDemoBtn: document.getElementById("loadUnsafeDemoBtn"),
  surfaceWorkspace: document.getElementById("surfaceWorkspace"),
  llmJsonInput: document.getElementById("llmJsonInput"),
  chunkSizeInput: document.getElementById("chunkSizeInput"),
  chunkDelayInput: document.getElementById("chunkDelayInput"),
  simulateStreamBtn: document.getElementById("simulateStreamBtn"),
  submitOnceBtn: document.getElementById("submitOnceBtn"),
  clearHistoryBtn: document.getElementById("clearHistoryBtn"),
  streamingPreview: document.getElementById("streamingPreview"),
  messageHistoryList: document.getElementById("messageHistoryList"),
  allowedActionsList: document.getElementById("allowedActionsList"),
  actionRecordsList: document.getElementById("actionRecordsList"),
  promptOverrideInput: document.getElementById("promptOverrideInput"),
  assemblePromptBtn: document.getElementById("assemblePromptBtn"),
  promptMetaText: document.getElementById("promptMetaText"),
  promptOutput: document.getElementById("promptOutput"),
};

const state = {
  surfaces: [],
  history: [],
  allowedActions: [],
  running: false,
  statusTimer: null,
};

function setGlobalStatus(text, isError) {
  els.globalStatus.textContent = text;
  els.globalStatus.style.color = isError ? "#ef4444" : "";
}

function notify(text, isError = false) {
  setGlobalStatus(text, isError);
  if (state.statusTimer) {
    clearTimeout(state.statusTimer);
    state.statusTimer = null;
  }
  state.statusTimer = setTimeout(() => {
    setGlobalStatus("Idle", false);
  }, 2800);
}

function addHistory(role, text) {
  state.history.push({
    role,
    text: String(text == null ? "" : text),
    ts: Date.now(),
  });
  if (state.history.length > 120) {
    state.history.splice(0, state.history.length - 120);
  }
  renderHistory();
}

function renderHistory() {
  if (state.history.length === 0) {
    els.messageHistoryList.textContent = "(empty)";
    return;
  }
  const lines = state.history.map((item) => {
    const stamp = new Date(item.ts).toLocaleTimeString();
    return `[${stamp}] ${item.role}: ${item.text}`;
  });
  els.messageHistoryList.textContent = lines.join("\n");
  els.messageHistoryList.scrollTop = els.messageHistoryList.scrollHeight;
}

function renderAllowedActions() {
  if (state.allowedActions.length === 0) {
    els.allowedActionsList.textContent = "(none)";
    return;
  }
  const lines = state.allowedActions.map((item) => `${item.name} :: ${item.description || ""}`);
  els.allowedActionsList.textContent = lines.join("\n");
}

function renderRecords() {
  const rows = recordStore.list().slice(0, 20);
  if (rows.length === 0) {
    els.actionRecordsList.textContent = "(empty)";
    return;
  }
  const lines = rows.map((row) => {
    const tail = row.status === "ok" ? JSON.stringify(row.result_json || {}) : row.error || "error";
    return `${row.action_name} | ${row.status} | ${row.duration_ms}ms | ${tail}`;
  });
  els.actionRecordsList.textContent = lines.join("\n");
}

function buildAllowedActions() {
  const dynamic = surfaceManager.listDeclaredActions();
  return [...GLOBAL_ACTION_DESCRIPTORS, ...dynamic];
}

function refreshAllowedActions() {
  state.allowedActions = buildAllowedActions();
  renderAllowedActions();
}

function getAllowedActionNames() {
  const set = new Set();
  for (const item of state.allowedActions) {
    set.add(item.name);
  }
  return set;
}

function renderPrompt() {
  const bundle = assemblePromptBundle({
    overrideText: els.promptOverrideInput.value,
    surfaces: state.surfaces,
    allowedActions: state.allowedActions,
    history: state.history,
  });
  if (!bundle.ok) {
    els.promptOutput.textContent = bundle.error;
    els.promptMetaText.textContent = "render failed";
    return;
  }
  els.promptOutput.textContent = bundle.promptText;
  els.promptMetaText.textContent = `prompt_hash=${bundle.promptHash} config_hash=${bundle.configHash}`;
}

const recordStore = createRecordStore();

const surfaceManager = createSurfaceManager({
  workspaceEl: els.surfaceWorkspace,
  notify,
  onSurfaceEvent: (evt) => {
    if (!evt || !evt.message) return;
    const msg = evt.message;
    if (msg.type === "surface_ready") {
      addHistory("surface", `${evt.surface_id}: ready`);
      return;
    }
    if (msg.type === "surface_event") {
      const detail = msg.event ? `event=${msg.event}` : "event";
      addHistory("surface", `${evt.surface_id}: ${detail} ${JSON.stringify(msg.payload || {})}`);
      return;
    }
    if (msg.type === "surface_log") {
      addHistory("surface", `${evt.surface_id}: ${msg.message || ""}`);
      return;
    }
    if (msg.type === "surface_heartbeat") {
      return;
    }
    addHistory("surface", `${evt.surface_id}: ${JSON.stringify(msg)}`);
  },
  onStateChange: (surfaces) => {
    state.surfaces = surfaces;
    refreshAllowedActions();
    renderPrompt();
  },
});

const dispatcher = createActionDispatcher({
  surfaceManager,
  recordStore,
  notify,
  getAllowedActionNames,
});

function createProtocolSession() {
  return createLLMProtocolSession({
    onContentDelta: (payload) => {
      const mark = payload.complete ? "(content done)" : "(streaming)";
      els.streamingPreview.textContent = `${mark}\n${payload.content}`;
    },
    onParseError: (payload) => {
      notify(`JSON parse 失败: ${payload.error.message}`, true);
      addHistory("system", `parse_error: ${payload.error.message}`);
    },
    onMessageFinal: (payload) => {
      const content = payload.content || "";
      if (content) {
        addHistory("assistant", content);
      } else if (payload.action) {
        addHistory("system", "action-only message (content empty)");
      }
      els.streamingPreview.textContent = content || "(empty content)";
    },
    onActionCall: async (action, meta) => {
      const result = await dispatcher.execute(action, { messageId: meta.messageId || createID("msg") });
      addHistory("observation", result.observation);
      renderRecords();
      renderPrompt();
      return result;
    },
  });
}

async function runLLMInput(streaming) {
  if (state.running) return;
  const raw = els.llmJsonInput.value || "";
  if (!raw.trim()) {
    notify("LLM JSON 输入为空", true);
    return;
  }
  state.running = true;
  try {
    const session = createProtocolSession();
    if (streaming) {
      const chunkSize = Math.max(1, Math.min(64, Number.parseInt(els.chunkSizeInput.value, 10) || 4));
      const chunkDelay = Math.max(0, Math.min(500, Number.parseInt(els.chunkDelayInput.value, 10) || 25));
      for (let i = 0; i < raw.length; i += chunkSize) {
        session.ingest(raw.slice(i, i + chunkSize));
        if (chunkDelay > 0) {
          await sleep(chunkDelay);
        }
      }
    } else {
      session.ingest(raw);
    }
    await session.finish({ messageId: createID("msg") });
  } finally {
    state.running = false;
  }
}

async function handleLoadSurface(url) {
  try {
    const id = await surfaceManager.loadSurface(url);
    notify(`Surface loaded: ${id}`);
    addHistory("system", `surface_loaded: ${id}`);
  } catch (err) {
    notify(err.message || String(err), true);
    addHistory("system", `surface_load_failed: ${err.message || String(err)}`);
  }
}

function bindEvents() {
  els.loadSurfaceBtn.addEventListener("click", () => {
    handleLoadSurface(els.surfaceUrlInput.value);
  });
  els.loadUnsafeDemoBtn.addEventListener("click", () => {
    handleLoadSurface("/surface/demo-unsafe.html");
  });
  els.simulateStreamBtn.addEventListener("click", () => {
    runLLMInput(true);
  });
  els.submitOnceBtn.addEventListener("click", () => {
    runLLMInput(false);
  });
  els.clearHistoryBtn.addEventListener("click", () => {
    state.history = [];
    els.streamingPreview.textContent = "";
    recordStore.clear();
    renderHistory();
    renderRecords();
    renderPrompt();
    addHistory("system", "history and records cleared");
  });
  els.assemblePromptBtn.addEventListener("click", () => {
    savePromptOverrideText(els.promptOverrideInput.value || "");
    renderPrompt();
    notify("Prompt 已渲染");
  });
}

function initDefaults() {
  els.llmJsonInput.value = JSON.stringify({
    content: "准备执行一个主界面提示动作",
    action: {
      id: "demo-action-1",
      name: "ui.toast",
      args: { message: "hello from action dispatcher" },
      timeout_s: 3,
    },
  });
  els.promptOverrideInput.value = loadPromptOverrideText();
}

function main() {
  initDefaults();
  bindEvents();
  refreshAllowedActions();
  renderHistory();
  renderRecords();
  renderPrompt();
  notify("Surface Main 已就绪");
}

main();
