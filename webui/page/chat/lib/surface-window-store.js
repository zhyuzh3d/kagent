import { callTool } from "./tool-call.js";

const PAGE_ID = "chat";
const DB_NAME = "chat_page_ui.db";
const TABLE_NAME = "chat_surface_window_state";
const LAYOUT_VERSION = 1;

let schemaReadyPromise = null;

function parseStateJSON(raw) {
  if (typeof raw !== "string" || !raw.trim()) return null;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch (_) {
    return null;
  }
}

async function ensureSchema() {
  if (!schemaReadyPromise) {
    schemaReadyPromise = callTool("storage.database.execute", {
      db_name: DB_NAME,
      query: `
        CREATE TABLE IF NOT EXISTS ${TABLE_NAME} (
          page_id TEXT NOT NULL,
          surface_id TEXT NOT NULL,
          layout_version INTEGER NOT NULL,
          state_json TEXT NOT NULL,
          updated_at_ms INTEGER NOT NULL,
          PRIMARY KEY (page_id, surface_id)
        )
      `,
      args: [],
    }).catch((error) => {
      schemaReadyPromise = null;
      throw error;
    });
  }
  return schemaReadyPromise;
}

export async function loadSurfaceWindowState(surfaceID) {
  const targetSurfaceID = String(surfaceID || "").trim();
  if (!targetSurfaceID) return null;
  await ensureSchema();
  const result = await callTool("storage.database.query", {
    db_name: DB_NAME,
    query: `
      SELECT layout_version, state_json
      FROM ${TABLE_NAME}
      WHERE page_id = ? AND surface_id = ?
      LIMIT 1
    `,
    args: [PAGE_ID, targetSurfaceID],
  });
  const rows = Array.isArray(result && result.rows) ? result.rows : [];
  if (!rows.length) return null;
  const layoutVersion = Number(rows[0].layout_version || 0);
  if (layoutVersion !== LAYOUT_VERSION) return null;
  return parseStateJSON(rows[0].state_json) || null;
}

export async function saveSurfaceWindowState(surfaceID, state) {
  const targetSurfaceID = String(surfaceID || "").trim();
  if (!targetSurfaceID || !state || typeof state !== "object") return;
  await ensureSchema();
  await callTool("storage.database.execute", {
    db_name: DB_NAME,
    query: `
      INSERT INTO ${TABLE_NAME} (
        page_id,
        surface_id,
        layout_version,
        state_json,
        updated_at_ms
      ) VALUES (?, ?, ?, ?, ?)
      ON CONFLICT(page_id, surface_id) DO UPDATE SET
        layout_version = excluded.layout_version,
        state_json = excluded.state_json,
        updated_at_ms = excluded.updated_at_ms
    `,
    args: [
      PAGE_ID,
      targetSurfaceID,
      LAYOUT_VERSION,
      JSON.stringify(state),
      Date.now(),
    ],
  });
}
