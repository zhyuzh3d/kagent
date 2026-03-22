export const BOARD_SIZE = 15;

export function createInitialBoard() {
  return Array.from({ length: BOARD_SIZE }, () => Array.from({ length: BOARD_SIZE }, () => 0));
}

export function createInitialGameState() {
  const startedAtMS = Date.now();
  return {
    board_size: BOARD_SIZE,
    phase: "playing",
    current_player: "black",
    move_count: 0,
    started_at_ms: startedAtMS,
    finished_at_ms: 0,
    last_move: null,
    winner: "",
    winning_line: [],
    board: createInitialBoard(),
    state_version: 0,
    updated_at_ms: startedAtMS,
  };
}

export function cloneGameState(state) {
  return JSON.parse(JSON.stringify(state));
}

export function playerValue(player) {
  return player === "white" ? 2 : 1;
}

export function playerName(value) {
  return value === 2 ? "white" : "black";
}

export function normalizePlayerColor(color) {
  return color === "white" ? "white" : color === "black" ? "black" : "";
}

export function otherPlayer(player) {
  return player === "white" ? "black" : "white";
}

export function isInside(row, col) {
  return row >= 0 && row < BOARD_SIZE && col >= 0 && col < BOARD_SIZE;
}

export function evaluateWinningLine(board, row, col, stoneValue) {
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
    while (isInside(r, c) && board[r][c] === stoneValue) {
      line.push({ row: r, col: c });
      r += dr;
      c += dc;
    }
    r = row - dr;
    c = col - dc;
    while (isInside(r, c) && board[r][c] === stoneValue) {
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

export function validateMove(state, row, col, options = {}) {
  const source = options && typeof options === "object" ? String(options.source || "action") : "action";
  const humanSide = normalizePlayerColor(options && typeof options === "object" ? String(options.humanSide || "").trim() : "");
  const actionColor = normalizePlayerColor(options && typeof options === "object" ? String(options.color || "").trim() : "");
  if (!Number.isInteger(row) || !Number.isInteger(col)) {
    return {
      ok: false,
      reason: "invalid_coordinates",
      message: "row 和 col 必须是整数。",
    };
  }
  if (!isInside(row, col)) {
    return {
      ok: false,
      reason: "out_of_range",
      message: "落子坐标超出棋盘范围。",
    };
  }
  if (state.phase !== "playing") {
    return {
      ok: false,
      reason: "game_finished",
      message: "当前对局已结束，不能继续落子。",
    };
  }
  if (state.board[row][col] !== 0) {
    return {
      ok: false,
      reason: "occupied",
      message: "该位置已有棋子，必须重新选择空位。",
      occupied_by: playerName(state.board[row][col]),
    };
  }
  if (source === "human" && humanSide && state.current_player !== humanSide) {
    return {
      ok: false,
      reason: "not_human_turn",
      message: `当前轮到${state.current_player === "white" ? "白棋" : "黑棋"}，请等待${state.current_player === "white" ? "白棋" : "黑棋"}落子。`,
      expected_player: state.current_player,
    };
  }
  if (source === "action") {
    if (!actionColor) {
      return {
        ok: false,
        reason: "missing_action_color",
        message: "外部落子必须显式提供 color，且只能为 black 或 white。",
      };
    }
    if (humanSide && actionColor === humanSide) {
      return {
        ok: false,
        reason: "action_color_controlled_by_human",
        message: `当前用户执${humanSide === "white" ? "白棋" : "黑棋"}，外部落子只能为${humanSide === "white" ? "黑棋" : "白棋"}执行。`,
        expected_color: otherPlayer(humanSide),
      };
    }
    if (state.current_player !== actionColor) {
      return {
        ok: false,
        reason: "action_color_not_on_turn",
        message: `当前轮到${state.current_player === "white" ? "白棋" : "黑棋"}落子，不能替${actionColor === "white" ? "白棋" : "黑棋"}落子。`,
        expected_player: state.current_player,
      };
    }
  }
  return { ok: true };
}

export function performMove(state, row, col, options = {}) {
  const validation = validateMove(state, row, col, options);
  if (!validation.ok) {
    return {
      accepted: false,
      ...validation,
      state: cloneGameState(state),
    };
  }
  const nextState = cloneGameState(state);
  const source = options && typeof options === "object" ? String(options.source || "action") : "action";
  const actionColor = normalizePlayerColor(options && typeof options === "object" ? String(options.color || "").trim() : "");
  const actingPlayer = source === "action" ? actionColor : nextState.current_player;
  const stone = playerValue(actingPlayer);

  nextState.board[row][col] = stone;
  nextState.move_count += 1;
  nextState.last_move = { row, col, player: actingPlayer };
  const winningLine = evaluateWinningLine(nextState.board, row, col, stone);
  if (winningLine.length >= 5) {
    nextState.phase = "won";
    nextState.winner = actingPlayer;
    nextState.winning_line = winningLine;
    nextState.finished_at_ms = Date.now();
  } else if (nextState.move_count >= BOARD_SIZE * BOARD_SIZE) {
    nextState.phase = "draw";
    nextState.winner = "";
    nextState.winning_line = [];
    nextState.finished_at_ms = Date.now();
  } else {
    nextState.current_player = otherPlayer(actingPlayer);
    nextState.finished_at_ms = 0;
  }

  return {
    accepted: true,
    ok: true,
    reason: "",
    source,
    placed: { row, col, player: actingPlayer, source },
    phase: nextState.phase,
    winner: nextState.winner,
    next_player: nextState.phase === "playing" ? nextState.current_player : "",
    state: nextState,
  };
}
