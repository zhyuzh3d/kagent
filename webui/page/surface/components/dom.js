export function getElements() {
  return {
    // 顶部控制栏
    surfaceSelect: document.getElementById("surfaceSelect"),
    loadBtn: document.getElementById("loadBtn"),
    resetLayoutBtn: document.getElementById("resetLayoutBtn"),

    // 运行区
    surfaceFrame: document.getElementById("surfaceFrame"),
    refreshFrameBtn: document.getElementById("refreshFrameBtn"),
    previewStatus: document.getElementById("previewStatus"),

    // 分隔面板控制
    logResizer: document.getElementById("logResizer"),
    logPane: document.getElementById("logPane"),
    toggleLogBtn: document.getElementById("toggleLogBtn"),

    // 侧边概览
    surfaceMeta: document.getElementById("surfaceMeta"),
    entryMeta: document.getElementById("entryMeta"),
    sessionMeta: document.getElementById("sessionMeta"),
    capabilitiesList: document.getElementById("capabilitiesList"),

    // 动作面板
    actionSelect: document.getElementById("actionSelect"),
    actionSchema: document.getElementById("actionSchema"),
    actionsBadge: document.getElementById("actionsBadge"),
    actionEditor: document.getElementById("actionEditor"),
    dispatchBtn: document.getElementById("dispatchBtn"),
    actionTabsNav: document.getElementById("actionTabsNav"),

    // 状态与日志
    runtimeStatus: document.getElementById("runtimeStatus"),
    eventLog: document.getElementById("eventLog"),
    clearLogsBtn: document.getElementById("clearLogsBtn"),
    toastViewport: document.getElementById("toastViewport"),

    // 标签页导航
    tabsNav: document.getElementById("tabsNav"),
  };
}
