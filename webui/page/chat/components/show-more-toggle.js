export function createShowMoreController(options) {
  const {
    app,
    button,
    chatStore,
    getSessionController,
  } = options;

  function syncButton() {
    button.textContent = app.showMore ? "Show More: On" : "Show More: Off";
    button.classList.toggle("btn-primary", app.showMore);
    button.classList.toggle("btn-ghost", !app.showMore);
    chatStore.setShowMore(app.showMore);
  }

  function toggle() {
    app.showMore = !app.showMore;
    syncButton();
    chatStore.clearForJump();
    try {
      const sessionController = getSessionController();
      if (!sessionController || typeof sessionController.sendControlMessage !== "function") {
        return;
      }
      sessionController.sendControlMessage({
        type: "send_control",
        control: "fetch_history",
        extra: {
          limit: (app.pullHistorySize || 10) * 5,
          before_id: 0,
          show_more: !!app.showMore,
        },
      });
    } catch (_) {
    }
  }

  syncButton();

  return {
    toggle,
    syncButton,
  };
}
