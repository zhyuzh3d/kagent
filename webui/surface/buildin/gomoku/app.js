import { createSurfaceRuntime } from "../../../lib/surfaceTool.js";
import {
  BOARD_SIZE,
  cloneGameState,
  createInitialGameState,
  isInside,
  performMove,
  validateMove,
} from "./logic.mjs";

const HUMAN_SIDE_STORAGE_KEY = "kagent.surface.gomoku.human_side";
const BOARD_BASE_SIZE = 720;
const FALLBACK_BOARD_BASE_SIZE = 720;
const BOARD_GRID_OFFSET = 52;
const BOARD_GRID_STEP = 44;
const BOARD_STAR_INDEXES = [3, 7, 11];
const PLAYER_LABELS = {
  black: "黑棋",
  white: "白棋",
};
const boardStageEl = document.querySelector(".board-stage");
const boardEl = document.getElementById("board");
const boardGridEl = document.getElementById("boardGrid");
const statusTextEl = document.getElementById("statusText");
const currentPlayerEl = document.getElementById("currentPlayer");
const moveCountEl = document.getElementById("moveCount");
const winnerTextEl = document.getElementById("winnerText");
const playerSideLabelEl = document.getElementById("playerSideLabel");
const boardHintEl = document.getElementById("boardHint");
const playBlackBtn = document.getElementById("playBlackBtn");
const playWhiteBtn = document.getElementById("playWhiteBtn");
const newGameBtn = document.getElementById("newGameBtn");
const resultModalEl = document.getElementById("resultModal");
const resultTitleEl = document.getElementById("resultTitle");
const resultSummaryEl = document.getElementById("resultSummary");
const resultDurationEl = document.getElementById("resultDuration");
const resultMovesEl = document.getElementById("resultMoves");
const resultSideEl = document.getElementById("resultSide");
const resultCloseBtn = document.getElementById("resultCloseBtn");
const resultRestartBtn = document.getElementById("resultRestartBtn");

const uiState = {
  humanSide: loadHumanSide(),
  lastNotice: "",
  completedSignature: "",
};
let runtime = null;
let boardResizeObserver = null;
const state = createInitialGameState();

function loadHumanSide() {
  try {
    const saved = window.localStorage.getItem(HUMAN_SIDE_STORAGE_KEY);
    return saved === "white" ? "white" : "black";
  } catch (_) {
    return "black";
  }
}

function persistHumanSide() {
  try {
    window.localStorage.setItem(HUMAN_SIDE_STORAGE_KEY, uiState.humanSide);
  } catch (_) {
  }
}

function cloneState() {
  return cloneGameState(state);
}

function exportState() {
  return {
    ...cloneState(),
    human_side: uiState.humanSide,
    human_turn: canHumanPlaceCurrentTurn(),
  };
}

function playerName(value) {
  return value === 2 ? "white" : "black";
}

function otherPlayer(player) {
  return player === "white" ? "black" : "white";
}

function playerLabel(player) {
  return PLAYER_LABELS[player] || "未知";
}

function resultLabel() {
  if (state.phase === "draw") return "平局";
  if (state.winner === "black") return "黑胜";
  if (state.winner === "white") return "白胜";
  return "-";
}

function formatDuration(durationMS) {
  const totalSeconds = Math.max(0, Math.round(durationMS / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
}

function currentGameDurationMS() {
  const startedAt = Number(state.started_at_ms || 0);
  const finishedAt = Number(state.finished_at_ms || 0);
  if (!startedAt) return 0;
  if (finishedAt > startedAt) return finishedAt - startedAt;
  return Date.now() - startedAt;
}

function canHumanPlaceCurrentTurn() {
  return state.phase === "playing" && state.current_player === uiState.humanSide;
}

function currentLifecycleStatus() {
  return state.phase === "playing" ? "ready" : "idle";
}

function syncRuntimeState(eventType = "") {
  if (!runtime) return;
  runtime.setState(exportState(), currentLifecycleStatus());
  if (eventType) {
    runtime.emitStateChange(eventType);
  }
}

function replaceState(nextState) {
  const snapshot = cloneGameState(nextState);
  for (const key of Object.keys(state)) {
    delete state[key];
  }
  Object.assign(state, snapshot);
}

function commitState(eventType = "") {
  state.state_version += 1;
  state.updated_at_ms = Date.now();
  syncRuntimeState(eventType);
  render();
}

function placeStone(row, col, options = {}) {
  const result = performMove(state, row, col, {
    ...(options && typeof options === "object" ? options : {}),
    humanSide: uiState.humanSide,
  });
  if (!result.accepted) {
    uiState.lastNotice = result.message || "本次落子无效。";
    return {
      ...result,
      state: exportState(),
    };
  }
  uiState.lastNotice = "";
  replaceState(result.state);
  commitState("action.place_stone");
  return {
    ...result,
    state: exportState(),
  };
}

function getCellState(row, col) {
  if (!Number.isInteger(row) || !Number.isInteger(col) || !isInside(row, col)) {
    throw new Error("row/col out of range");
  }
  const value = state.board[row][col];
  const lastMove = state.last_move;
  return {
    row,
    col,
    value,
    player: value === 0 ? "" : playerName(value),
    is_last_move: !!(lastMove && lastMove.row === row && lastMove.col === col),
  };
}

function newGame() {
  uiState.lastNotice = "";
  uiState.completedSignature = "";
  closeResultModal();
  replaceState(createInitialGameState());
  commitState("action.new_game");
  return {
    reset: true,
    next_player: state.current_player,
    state: exportState(),
  };
}

function actionHintText() {
  if (uiState.lastNotice) {
    return uiState.lastNotice;
  }
  if (state.phase === "won") {
    return `${playerLabel(state.winner)}已完成五连。可点击 “New Game” 重新开始。`;
  }
  if (state.phase === "draw") {
    return "棋盘已满，当前为平局。可点击 “New Game” 重新开始。";
  }
  if (canHumanPlaceCurrentTurn()) {
    return `现在由你控制的${playerLabel(uiState.humanSide)}行动，点击棋盘任意空位即可落子。`;
  }
  return `当前轮到${playerLabel(state.current_player)}。请等待${playerLabel(state.current_player)}落子。`;
}

function completionSignature() {
  if (state.phase !== "won" && state.phase !== "draw") return "";
  return `${state.phase}:${state.winner}:${state.move_count}:${state.finished_at_ms}`;
}

function openResultModal() {
  if (!resultModalEl) return;
  resultTitleEl.textContent = resultLabel();
  resultSummaryEl.textContent = state.phase === "draw"
    ? "本局以平局结束，棋盘已被全部下满。"
    : `${playerLabel(state.winner)}完成五连，拿下本局。`;
  resultDurationEl.textContent = formatDuration(currentGameDurationMS());
  resultMovesEl.textContent = String(state.move_count);
  resultSideEl.textContent = playerLabel(uiState.humanSide);
  resultModalEl.hidden = false;
}

function closeResultModal() {
  if (!resultModalEl) return;
  resultModalEl.hidden = true;
}

function syncResultModal() {
  const signature = completionSignature();
  if (!signature) {
    closeResultModal();
    return;
  }
  if (uiState.completedSignature === signature) {
    return;
  }
  uiState.completedSignature = signature;
  openResultModal();
}

function render() {
  const humanTurn = canHumanPlaceCurrentTurn();
  statusTextEl.textContent = state.phase === "won"
    ? `${playerLabel(state.winner)}获胜。最后一手已高亮，可重新开始下一局。`
    : state.phase === "draw"
      ? "本局平手，棋盘已经下满。"
      : humanTurn
        ? `现在轮到你执${playerLabel(uiState.humanSide)}落子。`
        : `你当前执${playerLabel(uiState.humanSide)}，请等待${playerLabel(state.current_player)}落子。`;
  currentPlayerEl.textContent = playerLabel(state.current_player);
  moveCountEl.textContent = String(state.move_count);
  winnerTextEl.textContent = resultLabel();
  playerSideLabelEl.textContent = playerLabel(uiState.humanSide);
  boardHintEl.textContent = actionHintText();
  playBlackBtn.classList.toggle("is-active", uiState.humanSide === "black");
  playWhiteBtn.classList.toggle("is-active", uiState.humanSide === "white");
  playBlackBtn.setAttribute("aria-pressed", String(uiState.humanSide === "black"));
  playWhiteBtn.setAttribute("aria-pressed", String(uiState.humanSide === "white"));
  boardEl.dataset.humanTurn = humanTurn ? "true" : "false";
  boardEl.dataset.nextPlayer = state.current_player;
  boardEl.dataset.phase = state.phase;
  const winningSet = new Set(state.winning_line.map((item) => `${item.row}:${item.col}`));
  boardEl.querySelectorAll(".cell").forEach((cell) => {
    const row = Number(cell.dataset.row);
    const col = Number(cell.dataset.col);
    const value = state.board[row][col];
    cell.className = "cell";
    if (value === 1) cell.classList.add("black");
    if (value === 2) cell.classList.add("white");
    const canPlace = humanTurn && value === 0;
    if (canPlace) {
      cell.classList.add("can-place");
    }
    cell.disabled = !canPlace;
    cell.setAttribute("aria-label", value === 0
      ? `第 ${row + 1} 行，第 ${col + 1} 列，空位`
      : `第 ${row + 1} 行，第 ${col + 1} 列，${playerLabel(playerName(value))}`);
    if (state.last_move && state.last_move.row === row && state.last_move.col === col) {
      cell.classList.add("last-move");
    }
    if (winningSet.has(`${row}:${col}`)) {
      cell.classList.add("winning");
    }
  });
  syncResultModal();
}

function getBoardBaseSize() {
  const raw = window.getComputedStyle(boardEl).getPropertyValue("--board-base-size");
  const value = Number.parseFloat(raw);
  return Number.isFinite(value) && value > 0 ? value : FALLBACK_BOARD_BASE_SIZE;
}

function syncBoardScale() {
  if (!boardStageEl || !boardEl) return;
  const rect = boardStageEl.getBoundingClientRect();
  const baseSize = getBoardBaseSize();
  if (!rect.width || !baseSize) return;
  const scale = Math.min(rect.width / baseSize, rect.height / baseSize);
  boardEl.style.setProperty("--board-scale", String(scale > 0 ? scale : 1));
}

function buildBoardGrid() {
  if (!boardGridEl) return;
  const ns = "http://www.w3.org/2000/svg";
  const fragment = document.createDocumentFragment();
  for (let index = 0; index < BOARD_SIZE; index += 1) {
    const axis = BOARD_GRID_OFFSET + BOARD_GRID_STEP * index;
    const vertical = document.createElementNS(ns, "line");
    vertical.setAttribute("x1", String(axis));
    vertical.setAttribute("y1", String(BOARD_GRID_OFFSET));
    vertical.setAttribute("x2", String(axis));
    vertical.setAttribute("y2", String(BOARD_BASE_SIZE - BOARD_GRID_OFFSET));
    vertical.setAttribute("class", "grid-line");
    fragment.appendChild(vertical);

    const horizontal = document.createElementNS(ns, "line");
    horizontal.setAttribute("x1", String(BOARD_GRID_OFFSET));
    horizontal.setAttribute("y1", String(axis));
    horizontal.setAttribute("x2", String(BOARD_BASE_SIZE - BOARD_GRID_OFFSET));
    horizontal.setAttribute("y2", String(axis));
    horizontal.setAttribute("class", "grid-line");
    fragment.appendChild(horizontal);
  }

  for (const rowIndex of BOARD_STAR_INDEXES) {
    for (const colIndex of BOARD_STAR_INDEXES) {
      const star = document.createElementNS(ns, "circle");
      star.setAttribute("cx", String(BOARD_GRID_OFFSET + BOARD_GRID_STEP * colIndex));
      star.setAttribute("cy", String(BOARD_GRID_OFFSET + BOARD_GRID_STEP * rowIndex));
      star.setAttribute("r", "4");
      star.setAttribute("class", "star-point");
      fragment.appendChild(star);
    }
  }
  boardGridEl.replaceChildren(fragment);
}

function bindBoardScale() {
  syncBoardScale();
  if (typeof ResizeObserver === "function" && boardStageEl) {
    boardResizeObserver = new ResizeObserver(() => {
      syncBoardScale();
    });
    boardResizeObserver.observe(boardStageEl);
    return;
  }
  window.addEventListener("resize", syncBoardScale);
}

function setHumanSide(side) {
  const nextSide = side === "white" ? "white" : "black";
  if (uiState.humanSide === nextSide) return;
  uiState.humanSide = nextSide;
  uiState.lastNotice = "";
  persistHumanSide();
  syncRuntimeState("ui.side_change");
  render();
}

function handleHumanMove(row, col) {
  const validation = validateMove(state, row, col, { source: "human", humanSide: uiState.humanSide });
  if (!validation.ok) {
    uiState.lastNotice = validation.message || "本次落子无效。";
    render();
    return {
      accepted: false,
      ...validation,
      state: exportState(),
    };
  }
  const result = placeStone(row, col, { source: "human" });
  if (!result.accepted) {
    render();
  } else {
    void requestAIReplyAfterHumanMove(result);
  }
  return result;
}

function executeActionMove(row, col, color) {
  const result = placeStone(row, col, { source: "action", color });
  if (!result.accepted) {
    throw new Error(result.message || "非法落子");
  }
  return result;
}

async function requestAIReplyAfterHumanMove(result) {
  if (!runtime || !result || result.accepted !== true) return;
  try {
    await runtime.callHostAction("call_ai_reply", {
      reason: "gomoku_human_move",
      move: result.move && typeof result.move === "object" ? result.move : {},
      phase: state.phase,
      current_player: state.current_player,
      move_count: state.move_count,
    });
  } catch (_) {
  }
}

function buildBoard() {
  const fragment = document.createDocumentFragment();
  for (let row = 0; row < BOARD_SIZE; row += 1) {
    for (let col = 0; col < BOARD_SIZE; col += 1) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "cell";
      button.dataset.row = String(row);
      button.dataset.col = String(col);
      button.style.setProperty("--row", String(row));
      button.style.setProperty("--col", String(col));
      button.setAttribute("aria-label", `第 ${row + 1} 行，第 ${col + 1} 列，空位`);
      button.addEventListener("click", () => {
        handleHumanMove(row, col);
      });
      fragment.appendChild(button);
    }
  }
  boardEl.appendChild(fragment);
}

runtime = createSurfaceRuntime({
  surfaceID: "7335fca0-223c-4a37-a49d-09b9f2244e38",
  surfaceType: "buildin",
  surfaceVersion: "1.0",
  title: "Gomoku",
  description: "15x15 本地双人五子棋。",
  actions: [
    { name: "get_state", description: "获取完整棋局状态", args_schema: {}, result_schema: { phase: "string" }, timeout_ms_default: 3000, side_effect: "none", streaming: false },
    { name: "new_game", description: "重置棋盘开始新对局", args_schema: {}, result_schema: { reset: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "place_stone", description: "在指定坐标为指定颜色落子", args_schema: { row: "number", col: "number", color: "string" }, result_schema: { accepted: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "get_cell_state", description: "查询单个棋盘坐标状态", args_schema: { row: "number", col: "number" }, result_schema: { value: "number" }, timeout_ms_default: 3000, side_effect: "none", streaming: false },
  ],
  initialState: exportState(),
  getVisibleText: () => `${state.phase}:${state.move_count}:${state.current_player}:${uiState.humanSide}`,
  getStateVersion: () => state.state_version,
  onReady: async ({ emitLog, setState }) => {
    setState(exportState(), currentLifecycleStatus());
    render();
    emitLog("info", "gomoku ready");
  },
  onAction: async ({ action }) => {
    switch (action.name) {
      case "get_state":
        return exportState();
      case "new_game":
        return newGame();
      case "place_stone":
        return executeActionMove(Math.trunc(action.args.row), Math.trunc(action.args.col), action.args.color);
      case "get_cell_state":
        return getCellState(Math.trunc(action.args.row), Math.trunc(action.args.col));
      default:
        throw new Error(`unknown action: ${action.name}`);
    }
  },
});

newGameBtn.addEventListener("click", () => {
  newGame();
});
resultCloseBtn?.addEventListener("click", () => {
  closeResultModal();
});
resultRestartBtn?.addEventListener("click", () => {
  newGame();
});
playBlackBtn.addEventListener("click", () => {
  setHumanSide("black");
});
playWhiteBtn.addEventListener("click", () => {
  setHumanSide("white");
});

buildBoard();
buildBoardGrid();
bindBoardScale();
render();
