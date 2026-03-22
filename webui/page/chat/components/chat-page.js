import { createAudioCapture } from "../lib/audio-capture.js";
import { createAudioPlayback } from "../lib/audio-playback.js";
import { createChatActionEngine } from "../lib/action-engine.js";
import { loadRuntimeConfig, loadConfigInfo, applyChatFrontendConfig, buildWorkerConfig } from "../lib/config-store.js";
import { createEventRouter } from "../lib/event-router.js";
import { createSessionController } from "../lib/session-controller.js";
import { callTool } from "../lib/tool-call.js";
import { createAppState } from "../lib/app-state.js";
import { createControlMessageReporters } from "../lib/control-reporters.js";
import { createDebugLogger } from "../lib/debug-log.js";
import { getChatPageElements } from "../lib/dom.js";
import { createChatStore } from "./chat-store.js";
import { createConfigDrawer } from "./config-drawer.js";
import { createDebugPanelController } from "./debug-panel.js";
import { renderSurfaceSelect } from "./surface-select.js";
import { createSidebarController } from "./sidebar-controller.js";
import { createShowMoreController } from "./show-more-toggle.js";
import { createStatusIndicator } from "./status-indicator.js";
import { createSurfaceBridge } from "./surface-bridge.js";
import { setupUserMenu } from "./user-menu.js";

const PAGE_NAME = "chat";

export async function initChatPage() {
  const App = createAppState();
  const el = getChatPageElements();
  const debugPanelController = createDebugPanelController({
    debugPanel: el.debugPanel,
    debugToggle: el.debugToggle,
    resizeHandle: el.resizeHandle,
  });
  const { appendDebug, reportClientLog } = createDebugLogger({
    debugEl: el.debug,
    callTool,
    pageName: PAGE_NAME,
  });

  setupUserMenu({
    user: window.__kagentUser || {},
    avatarBtn: el.userAvatarBtn,
    menu: el.userMenu,
    menuName: el.userMenuName,
    logoutBtn: el.logoutBtn,
    reportClientLog,
    pageName: PAGE_NAME,
  });

  reportClientLog({
    level: "INFO",
    module: "System",
    content: "Script module start",
    page: PAGE_NAME,
  });

  const statusIndicator = createStatusIndicator({
    statusText: el.statusText,
    statusDot: el.statusDot,
    initialState: App.backendState,
  });

  let sessionController = null;
  const getSessionController = () => sessionController;
  const getCurrentTurnId = () => App.activeTurnId || App.currentTurn || 0;
  const {
    reportActionRecord,
    reportStateChange,
    reportConfigChange,
    reportSurfaceContext,
  } = createControlMessageReporters({
    getSessionController,
    getTurnId: getCurrentTurnId,
  });

  function syncWorkerConfig() {
    if (!sessionController) return;
    sessionController.syncWorkerConfig();
  }

  function applyUIConfig(config) {
    const showDebugPanel = !!((((config || {}).app || {}).ui || {}).showDebugPanelByDefault);
    debugPanelController.setOpen(showDebugPanel);
  }

  function applyPublicConfig(config) {
    applyChatFrontendConfig(App, config);
    applyUIConfig(App.publicConfig);
    syncWorkerConfig();
  }

  function setupConfigDrawer() {
    if (App.configDrawer) {
      App.configDrawer.setConfigInfo(App.configInfo);
      App.configDrawer.setConfig(App.publicConfig);
      return;
    }
    App.configDrawer = createConfigDrawer({
      mount: el.configShell,
      initialConfig: App.publicConfig,
      configInfo: App.configInfo,
      onConfigApplied: (config) => {
        applyPublicConfig(config);
      },
      onConfigSaved: (payload) => {
        reportConfigChange(payload);
      },
      onDebug: appendDebug,
    });
  }

  async function loadClientConfig() {
    const [publicConfig, configInfo] = await Promise.all([
      loadRuntimeConfig(),
      loadConfigInfo(),
    ]);
    App.configInfo = configInfo || { tabs: [], fields: {} };
    applyPublicConfig(publicConfig);
    setupConfigDrawer();
    if (App.configDrawer) {
      App.configDrawer.setConfig(App.publicConfig);
    }
    appendDebug("INFO", "System", null, null, "runtime config loaded");
  }

  async function loadVersion() {
    try {
      const version = await callTool("hub.system.version.get", {});
      const backend = version.backend || "unknown";
      const webui = version.webui || "unknown";
      const value = `b${backend} ui${webui}`;
      el.versionText.textContent = value;
      appendDebug("INFO", "System", null, null, `version ${value}`);
    } catch (error) {
      el.versionText.textContent = "version unknown";
      appendDebug("ERROR", "System", null, null, `version load failed: ${error.message || error}`);
    }
  }

  const chatStore = createChatStore({
    app: App,
    chatArea: el.chatArea,
  });

  const showMoreController = createShowMoreController({
    app: App,
    button: el.showMoreBtn,
    chatStore,
    getSessionController,
  });

  const surfaceBridge = createSurfaceBridge({
    root: el.surfaceFloatRoot,
    dockHost: el.surfaceDockHost,
    dockShell: el.surfaceDockShell,
    dockResizeHandle: el.surfaceDockResize,
    appendDebug: (...args) => appendDebug(...args, "SURF"),
    appendSystem: (text) => chatStore.addChatMsg("system", text, 0),
    reportActionRecord,
    onRequestAIReply: ({ surfaceID, reason }) => {
      const currentSessionController = getSessionController();
      if (!currentSessionController || typeof currentSessionController.requestAIReply !== "function") {
        return { requested: false, reason: "session_controller_not_ready" };
      }
      return currentSessionController.requestAIReply(reason || `surface:${surfaceID || "unknown"}:call_ai_reply`);
    },
    onStateChange: (payload) => {
      renderSurfaceSelect(el.surfaceSelect, payload);
      reportSurfaceContext(payload);
    },
    onSurfaceEvent: (evt) => {
      const turnId = getCurrentTurnId();
      actionEngine.handleSurfaceEffect(turnId, evt);
    },
  });

  const actionEngine = createChatActionEngine({
    chatStore,
    surfaceBridge,
    appendDebug,
    appendSystem: (text) => chatStore.addChatMsg("system", text, 0),
    reportActionRecord,
    reportStateChange,
  });

  const audioPlayback = createAudioPlayback({
    app: App,
    appendDebug,
    flashIndicator: statusIndicator.flashIndicator,
  });

  const eventRouter = createEventRouter({
    app: App,
    chatStore,
    audioPlayback,
    setStatus: statusIndicator.setStatus,
    flashIndicator: statusIndicator.flashIndicator,
    appendDebug,
    onLLMDelta: ({ turnId, text }) => actionEngine.handleAssistantDelta(turnId, text || ""),
    onLLMFinal: ({ turnId, text }) => {
      actionEngine.handleAssistantFinal(turnId, text || "").catch((error) => {
        appendDebug("ERROR", "ActionEngine", turnId, null, `handleAssistantFinal failed: ${error.message || error}`);
      });
    },
    onInterrupt: (turnId) => {
      const currentSessionController = getSessionController();
      if (currentSessionController && currentSessionController.sendControlMessage) {
        currentSessionController.sendControlMessage({
          type: "send_control",
          control: "interrupt",
          reason: "commit_stop",
          turn_id: turnId || getCurrentTurnId(),
        });
      }
    },
  });

  sessionController = createSessionController({
    app: App,
    workerURL: "./io-worker.js",
    audioPlayback,
    chatStore,
    eventRouter,
    setStatus: statusIndicator.setStatus,
    setButtons,
    appendDebug,
    flashIndicator: statusIndicator.flashIndicator,
    getWorkerConfig: () => buildWorkerConfig(App.publicConfig),
    getReplayControlMessages: () => {
      const turnId = getCurrentTurnId();
      return [
        ...surfaceBridge.buildSurfaceContextControls(turnId),
        ...surfaceBridge.buildStateReplayControls(turnId),
      ];
    },
  });
  App.workerSend = sessionController.workerSend;

  const audioCapture = createAudioCapture({
    app: App,
    audioPlayback,
    workerSend: sessionController.workerSend,
    setStatus: statusIndicator.setStatus,
    appendDebug,
    onBargeIn: (interruptedTurnId) => {
      sessionController.finalizeUtterance(interruptedTurnId);
    },
  });

  sessionController.bindAudioCapture(audioCapture);

  const sidebarController = createSidebarController({
    app: App,
    el,
    chatStore,
    sessionController,
    appendDebug,
  });

  function setButtons(running) {
    el.startBtn.disabled = running;
    el.stopBtn.disabled = !running;
  }

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && App.running && App.audioCtx) {
      App.audioCtx.resume().then(() => appendDebug("INFO", "AudioContext", null, null, "AudioContext resumed on tab focus"));
    }
  });

  el.startBtn.addEventListener("click", () => {
    const ctx = sidebarController.getCurrentContext();
    sessionController.startAll(ctx.projectId, ctx.threadId);
  });
  el.stopBtn.addEventListener("click", () => sessionController.stopAll("手动停止"));
  el.configBtn.addEventListener("click", () => {
    if (App.configDrawer) App.configDrawer.toggle();
  });
  el.surfaceSelect.addEventListener("change", () => {
    surfaceBridge.selectSurface(el.surfaceSelect.value).catch((error) => {
      appendDebug("ERROR", "SurfaceBridge", null, null, `surface select failed: ${error.message || error}`);
    });
  });
  el.showMoreBtn.addEventListener("click", () => {
    showMoreController.toggle();
  });
  el.sidebarToggle.addEventListener("click", () => {
    el.sidebar.classList.toggle("collapsed");
  });
  window.addEventListener("beforeunload", () => sessionController.stopAll(""));

  try {
    await loadClientConfig();
    await loadVersion();
    await surfaceBridge.refreshRegistry().catch((error) => {
      appendDebug("WARN", "SurfaceBridge", null, null, `surface catalog preload failed: ${error.message || error}`);
    });
    await sidebarController.init();
    const ctx = sidebarController.getCurrentContext();
    await sessionController.initWorkerConnection(ctx.projectId, ctx.threadId);
  } catch (error) {
    appendDebug("ERROR", "System", null, null, `init failed: ${error.message || error}`);
    await loadVersion();
  }
}
