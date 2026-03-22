import test from "node:test";
import assert from "node:assert/strict";

import {
  BOARD_SIZE,
  createInitialGameState,
  performMove,
  validateMove,
} from "./logic.mjs";

test("black moves first and turn switches after a valid move", () => {
  const state = createInitialGameState();
  const result = performMove(state, 7, 7, { source: "action", color: "black" });
  assert.equal(result.accepted, true);
  assert.equal(result.state.board[7][7], 1);
  assert.equal(result.state.current_player, "white");
  assert.equal(result.state.move_count, 1);
});

test("occupied positions are rejected and do not switch turns", () => {
  let state = createInitialGameState();
  state = performMove(state, 7, 7, { source: "action", color: "black" }).state;
  const duplicated = performMove(state, 7, 7, { source: "action", color: "white" });
  assert.equal(duplicated.accepted, false);
  assert.equal(duplicated.reason, "occupied");
  assert.equal(duplicated.state.current_player, "white");
  assert.equal(duplicated.state.move_count, 1);
});

test("human clicks are rejected when it is not the human-controlled side's turn", () => {
  const state = createInitialGameState();
  const result = validateMove(state, 7, 7, { source: "human", humanSide: "white" });
  assert.equal(result.ok, false);
  assert.equal(result.reason, "not_human_turn");
});

test("out of range coordinates are rejected", () => {
  const state = createInitialGameState();
  const result = performMove(state, -1, BOARD_SIZE, { source: "action", color: "black" });
  assert.equal(result.accepted, false);
  assert.equal(result.reason, "out_of_range");
});

test("finished games reject additional moves", () => {
  let state = createInitialGameState();
  state.board[7][3] = 1;
  state.board[7][4] = 1;
  state.board[7][5] = 1;
  state.board[7][6] = 1;
  state.move_count = 4;
  const win = performMove(state, 7, 7, { source: "action", color: "black" });
  assert.equal(win.accepted, true);
  assert.equal(win.state.phase, "won");

  const afterWin = performMove(win.state, 8, 8, { source: "action", color: "white" });
  assert.equal(afterWin.accepted, false);
  assert.equal(afterWin.reason, "game_finished");
});

test("winning move records finished timestamp", () => {
  let state = createInitialGameState();
  state.board[7][3] = 1;
  state.board[7][4] = 1;
  state.board[7][5] = 1;
  state.board[7][6] = 1;
  state.move_count = 4;
  const win = performMove(state, 7, 7, { source: "action", color: "black" });
  assert.equal(win.accepted, true);
  assert.equal(win.state.phase, "won");
  assert.equal(typeof win.state.finished_at_ms, "number");
  assert.ok(win.state.finished_at_ms >= win.state.started_at_ms);
});

test("action move requires an explicit color", () => {
  const state = createInitialGameState();
  const result = performMove(state, 7, 7, { source: "action" });
  assert.equal(result.accepted, false);
  assert.equal(result.reason, "missing_action_color");
  assert.equal(result.state.move_count, 0);
});

test("action cannot place the human-controlled color", () => {
  const state = createInitialGameState();
  const result = performMove(state, 7, 7, { source: "action", color: "black", humanSide: "black" });
  assert.equal(result.accepted, false);
  assert.equal(result.reason, "action_color_controlled_by_human");
  assert.equal(result.state.move_count, 0);
});

test("action color must match the current turn", () => {
  const state = createInitialGameState();
  const result = performMove(state, 7, 7, { source: "action", color: "white", humanSide: "black" });
  assert.equal(result.accepted, false);
  assert.equal(result.reason, "action_color_not_on_turn");
  assert.equal(result.state.move_count, 0);
});

test("action can only place the non-human side when that side is on turn", () => {
  let state = createInitialGameState();
  state = performMove(state, 7, 7, { source: "human", humanSide: "black" }).state;
  const result = performMove(state, 7, 8, { source: "action", color: "white", humanSide: "black" });
  assert.equal(result.accepted, true);
  assert.equal(result.placed.player, "white");
  assert.equal(result.state.board[7][8], 2);
  assert.equal(result.state.current_player, "black");
});
