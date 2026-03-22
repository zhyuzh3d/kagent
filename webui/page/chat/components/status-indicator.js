export function createStatusIndicator(options) {
  const {
    statusText,
    statusDot,
    initialState = "Idle",
  } = options;

  let backendState = initialState;
  let flashTimeout = null;
  let lastFlashTime = 0;

  function setStatus(state) {
    const nextState = state || backendState;
    backendState = nextState;
    statusText.textContent = nextState;
  }

  function flashIndicator(type) {
    const now = Date.now();
    if (now - lastFlashTime < 100) return;
    lastFlashTime = now;

    statusDot.className = "status-dot";
    void statusDot.offsetWidth;

    let cls = "";
    if (type === "send") cls = "flash-send";
    else if (type === "receive") cls = "flash-receive";
    else if (type === "error") cls = "flash-error";

    if (cls) statusDot.classList.add(cls);

    if (flashTimeout) clearTimeout(flashTimeout);
    flashTimeout = setTimeout(() => {
      statusDot.className = "status-dot";
    }, 300);
  }

  setStatus(initialState);

  return {
    setStatus,
    flashIndicator,
    getState() {
      return backendState;
    },
  };
}
