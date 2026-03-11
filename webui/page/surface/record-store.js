const STORAGE_KEY = "kagent.surface.action_records.v1";
const MAX_RECORDS = 300;

function loadInitialRecords() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item) => item && typeof item === "object");
  } catch (_) {
    return [];
  }
}

export function createRecordStore() {
  const records = loadInitialRecords();

  function persist() {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(records));
    } catch (_) {
    }
  }

  function add(record) {
    records.unshift(record);
    if (records.length > MAX_RECORDS) {
      records.length = MAX_RECORDS;
    }
    persist();
    return record;
  }

  function list() {
    return records.slice();
  }

  function clear() {
    records.length = 0;
    persist();
  }

  function query(options) {
    const opts = options && typeof options === "object" ? options : {};
    const status = typeof opts.status === "string" ? opts.status : "";
    const actionName = typeof opts.action_name === "string" ? opts.action_name : "";
    const limit = Number.isFinite(opts.limit) ? Math.max(1, Math.floor(opts.limit)) : 20;
    const output = [];
    for (const record of records) {
      if (status && record.status !== status) continue;
      if (actionName && record.action_name !== actionName) continue;
      output.push(record);
      if (output.length >= limit) break;
    }
    return output;
  }

  return {
    add,
    list,
    clear,
    query,
  };
}
