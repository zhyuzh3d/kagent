export function createDebugPanelController(options) {
  const {
    debugPanel,
    debugToggle,
    resizeHandle,
    doc = document,
    windowRef = window,
  } = options;

  let savedDebugHeight = 180;
  let dragging = false;
  let startY = 0;
  let startH = 0;

  function setOpen(open) {
    if (open) {
      debugPanel.classList.add("open");
      debugPanel.style.height = savedDebugHeight + "px";
      resizeHandle.classList.add("visible");
      debugToggle.textContent = "▼ 调试日志";
      return;
    }
    savedDebugHeight = debugPanel.offsetHeight || savedDebugHeight;
    debugPanel.classList.remove("open");
    debugPanel.style.height = "0";
    resizeHandle.classList.remove("visible");
    debugToggle.textContent = "▶ 调试日志";
  }

  debugToggle.addEventListener("click", () => {
    setOpen(!debugPanel.classList.contains("open"));
  });

  resizeHandle.addEventListener("mousedown", (event) => {
    if (!debugPanel.classList.contains("open")) return;
    dragging = true;
    startY = event.clientY;
    startH = debugPanel.offsetHeight;
    resizeHandle.classList.add("active");
    event.preventDefault();
  });

  doc.addEventListener("mousemove", (event) => {
    if (!dragging) return;
    const delta = startY - event.clientY;
    const newHeight = Math.max(60, Math.min(windowRef.innerHeight * 0.7, startH + delta));
    debugPanel.style.height = newHeight + "px";
    savedDebugHeight = newHeight;
  });

  doc.addEventListener("mouseup", () => {
    if (!dragging) return;
    dragging = false;
    resizeHandle.classList.remove("active");
  });

  return {
    setOpen,
  };
}
