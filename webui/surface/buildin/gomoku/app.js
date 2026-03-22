import { createSurfaceRuntime } from "../../../lib/surfaceTool.js";

const BOARD_SIZE = 15;
const boardEl = document.getElementById("board");
const statusTextEl = document.getElementById("statusText");
const currentPlayerEl = document.getElementById("currentPlayer");
const moveCountEl = document.getElementById("moveCount");
const winnerTextEl = document.getElementById("winnerText");

const state = createInitialState();

function createInitialBoard() {
  return Array.from({ length: BOARD_SIZE }, () => Array.from({ length: BOARD_SIZE }, () => 0));
}

function createInitialState() {
  return {
    board_size: BOARD_SIZE,
    phase: "playing",
    current_player: "black",
    move_count: 0,
    last_move: null,
    winner: "",
    winning_line: [],
    board: createInitialBoard(),
    state_version: 0,
    updated_at_ms: Date.now(),
  };
}

function cloneState() {
  return JSON.parse(JSON.stringify(state));
}

function playerValue(player) {
  return player === "white" ? 2 : 1;
}

function playerName(value) {
  return value === 2 ? "white" : "black";
}

function isInside(row, col) {
  return row >= 0 && row < BOARD_SIZE && col >= 0 && col < BOARD_SIZE;
}

function evaluateWinner(row, col, stoneValue) {
  const directions = [
    [0, 1],
    [1, 0],
    [1, 1],
    [1, -1],
  ];
  for (const [dr, dc] of directions) {
    const line = [{ row, col }];
    let r = row + dr;
    let c = col + dc;
    while (isInside(r, c) && state.board[r][c] === stoneValue) {
      line.push({ row: r, col: c });
      r += dr;
      c += dc;
    }
    r = row - dr;
    c = col - dc;
    while (isInside(r, c) && state.board[r][c] === stoneValue) {
      line.unshift({ row: r, col: c });
      r -= dr;
      c -= dc;
    }
    if (line.length >= 5) {
      return line.slice(0, 5);
    }
  }
  return [];
}

function commitState() {
  state.state_version += 1;
  state.updated_at_ms = Date.now();
  runtime.setState(cloneState(), state.phase === "playing" ? "ready" : state.phase === "won" ? "busy" : "idle");
  render();
}

function placeStone(row, col) {
  if (!Number.isInteger(row) || !Number.isInteger(col) || !isInside(row, col)) {
    return { accepted: false, reason: "out_of_range", state: cloneState() };
  }
  if (state.phase !== "playing") {
    return { accepted: false, reason: "game_finished", state: cloneState() };
  }
  if (state.board[row][col] !== 0) {
    return { accepted: false, reason: "occupied", state: cloneState() };
  }
  const stone = playerValue(state.current_player);
  state.board[row][col] = stone;
  state.move_count += 1;
  state.last_move = { row, col, player: state.current_player };
  const winningLine = evaluateWinner(row, col, stone);
  if (winningLine.length >= 5) {
    state.phase = "won";
    state.winner = state.current_player;
    state.winning_line = winningLine;
  } else if (state.move_count >= BOARD_SIZE * BOARD_SIZE) {
    state.phase = "draw";
    state.winner = "";
    state.winning_line = [];
  } else {
    state.current_player = state.current_player === "black" ? "white" : "black";
  }
  commitState();
  runtime.emitStateChange("action.place_stone");
  return {
    accepted: true,
    reason: "",
    placed: { row, col, player: state.last_move.player },
    phase: state.phase,
    winner: state.winner,
    state: cloneState(),
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
  const fresh = createInitialState();
  Object.assign(state, fresh);
  commitState();
  runtime.emitStateChange("action.new_game");
  return {
    reset: true,
    state: cloneState(),
  };
}

function render() {
  statusTextEl.textContent = state.phase === "won"
    ? `${state.winner === "black" ? "Black" : "White"} wins.`
    : state.phase === "draw"
      ? "Draw game."
      : "Click a cell to place a stone.";
  currentPlayerEl.textContent = state.current_player === "black" ? "Black" : "White";
  moveCountEl.textContent = String(state.move_count);
  winnerTextEl.textContent = state.winner ? (state.winner === "black" ? "Black" : "White") : "-";
  const winningSet = new Set(state.winning_line.map((item) => `${item.row}:${item.col}`));
  boardEl.querySelectorAll(".cell").forEach((cell) => {
    const row = Number(cell.dataset.row);
    const col = Number(cell.dataset.col);
    const value = state.board[row][col];
    cell.className = "cell";
    if (value === 1) cell.classList.add("black");
    if (value === 2) cell.classList.add("white");
    if (state.last_move && state.last_move.row === row && state.last_move.col === col) {
      cell.classList.add("last-move");
    }
    if (winningSet.has(`${row}:${col}`)) {
      cell.classList.add("winning");
    }
  });
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
      button.addEventListener("click", () => {
        placeStone(row, col);
      });
      fragment.appendChild(button);
    }
  }
  boardEl.appendChild(fragment);
}

const runtime = createSurfaceRuntime({
  surfaceID: "7335fca0-223c-4a37-a49d-09b9f2244e38",
  surfaceType: "buildin",
  surfaceVersion: "1.0",
  title: "Gomoku",
  description: "15x15 本地双人五子棋。",
  actions: [
    { name: "get_state", description: "获取完整棋局状态", args_schema: {}, result_schema: { phase: "string" }, timeout_ms_default: 3000, side_effect: "none", streaming: false },
    { name: "new_game", description: "重置棋盘开始新对局", args_schema: {}, result_schema: { reset: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "place_stone", description: "在指定坐标落子", args_schema: { row: "number", col: "number" }, result_schema: { accepted: "boolean" }, timeout_ms_default: 3000, side_effect: "write", streaming: false },
    { name: "get_cell_state", description: "查询单个棋盘坐标状态", args_schema: { row: "number", col: "number" }, result_schema: { value: "number" }, timeout_ms_default: 3000, side_effect: "none", streaming: false },
  ],
  initialState: cloneState(),
  getVisibleText: () => `${state.phase}:${state.move_count}:${state.current_player}`,
  getStateVersion: () => state.state_version,
  onReady: async ({ emitLog, setState }) => {
    setState(cloneState(), "ready");
    render();
    emitLog("info", "gomoku ready");
  },
  onAction: async ({ action }) => {
    switch (action.name) {
      case "get_state":
        return cloneState();
      case "new_game":
        return newGame();
      case "place_stone":
        return placeStone(Math.trunc(action.args.row), Math.trunc(action.args.col));
      case "get_cell_state":
        return getCellState(Math.trunc(action.args.row), Math.trunc(action.args.col));
      default:
        throw new Error(`unknown action: ${action.name}`);
    }
  },
});

document.getElementById("newGameBtn").addEventListener("click", () => {
  newGame();
});

buildBoard();
render();
