#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
project_root="$(pwd)"

trap 'echo "deploy error at line ${LINENO}: ${BASH_COMMAND}" >&2' ERR
if [[ "${KAGENT_DEBUG:-0}" == "1" ]]; then
  set -x
fi

addr="${KAGENT_ADDR:-127.0.0.1:18080}"
config="${KAGENT_CONFIG:-config/configx.json}"
model="${KAGENT_MODEL:-doubao}"
admin_token="${KAGENT_ADMIN_TOKEN:-}"

version_file="version.json"
if [[ ! -f "$version_file" ]]; then
  echo "error: missing $version_file" >&2
  exit 2
fi

backend_ver="$(
  python3 - <<'PY'
import json
with open("version.json","r",encoding="utf-8") as f:
  v=json.load(f)
print(v.get("backend","").strip())
PY
)"
if [[ -z "${backend_ver// }" ]]; then
  echo "error: version.json missing backend version" >&2
  exit 2
fi

mkdir -p bin logs run

bin_name="kagent-backend-${backend_ver}"
bin_path="bin/${bin_name}"
bin_link="bin/kagent-backend"
log_path="${project_root}/log.txt"
log_backup_path="${project_root}/log.txt.bak"
pid_file="run/kagent.pid"
tail_pid_file="run/deploy-tail.pid"
escaped_log_path="$(printf '%s' "${log_path}" | sed 's/[.[\*^$()+?{}|\\]/\\&/g')"

echo "build: ${bin_path}"
go build -buildvcs=false -o "${bin_path}" .
ln -sf "${bin_name}" "${bin_link}"

shutdown_url="http://${addr}/admin/shutdown"
version_url="http://${addr}/version"

if [[ -f "${pid_file}" ]]; then
  old_pid="$(cat "${pid_file}" 2>/dev/null || true)"
else
  old_pid=""
fi

request_shutdown() {
  # Avoid bash's surprising `set -e` behavior inside command substitutions:
  # capture curl output and always return 0 so the deploy can continue/retry.
  set +e
  local out
  if [[ -n "${admin_token}" ]]; then
    out="$(curl -sS -m 1 -X POST -H "X-Admin-Token: ${admin_token}" "${shutdown_url}" 2>&1)"
  else
    out="$(curl -sS -m 1 -X POST "${shutdown_url}" 2>&1)"
  fi
  local rc=$?
  set -e
  if (( rc != 0 )); then
    echo "shutdown request failed (rc=${rc}): ${out}" >&2
  fi
  printf "%s" "${out}"
  return 0
}

is_pid_alive() {
  local pid="$1"
  [[ -z "${pid}" ]] && return 1
  kill -0 "${pid}" 2>/dev/null
}

is_server_responding() {
  curl -sS -m 0.25 "${version_url}" >/dev/null 2>&1
}

cleanup_tail() {
  local pid=""
  if [[ -f "${tail_pid_file}" ]]; then
    pid="$(cat "${tail_pid_file}" 2>/dev/null || true)"
  fi
  if [[ -n "${pid}" ]] && is_pid_alive "${pid}"; then
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  fi
  if command -v pkill >/dev/null 2>&1; then
    # Clean both new and legacy tail commands:
    # 1) absolute log path, 2) relative log.txt in project root.
    pkill -f "(^|/|[[:space:]])tail([[:space:]].*)?-F[[:space:]]+${escaped_log_path}([[:space:]]|$)" 2>/dev/null || true
    pkill -f "(^|/|[[:space:]])tail([[:space:]].*)?-F[[:space:]]+log\\.txt([[:space:]]|$)" 2>/dev/null || true
    pkill -f "(^|/|[[:space:]])tail([[:space:]].*)?log\\.txt([[:space:]]|$)" 2>/dev/null || true
  fi
  rm -f "${tail_pid_file}"
  tail_pid=""
}

handle_follow_stop() {
  cleanup_tail
  exit 0
}

tail_pid=""
cleanup_tail

if is_pid_alive "${old_pid}"; then
  echo "shutdown: pid=${old_pid} url=${shutdown_url}"
  shutdown_resp="$(request_shutdown)"
  if [[ -n "${shutdown_resp}" ]]; then
    echo "shutdown response: ${shutdown_resp}"
  fi

  deadline_ms=3000
  interval_ms=100
  waited_ms=0
  last_print_ms=0
  while is_pid_alive "${old_pid}"; do
    # Print progress at a human-friendly cadence so the script never "looks stuck".
    if (( waited_ms - last_print_ms >= 500 )); then
      sr="down"
      if is_server_responding; then
        sr="up"
      fi
      echo "waiting shutdown... waited=${waited_ms}ms pid=${old_pid} server=${sr}"
      last_print_ms=$waited_ms
    fi
    if (( waited_ms >= deadline_ms )); then
      sr="down"
      if is_server_responding; then
        sr="up"
      fi
      echo "shutdown timeout after ${deadline_ms}ms (pid=${old_pid} still running, server=${sr})" >&2
      read -r -p "Force kill old process? [y/N] " ans
      if [[ "${ans}" == "y" || "${ans}" == "Y" ]]; then
        kill -TERM "${old_pid}" 2>/dev/null || true
        sleep 0.3
        if is_pid_alive "${old_pid}"; then
          kill -KILL "${old_pid}" 2>/dev/null || true
        fi
      else
        echo "abort deploy" >&2
        exit 1
      fi
      break
    fi
    sleep 0.1
    waited_ms=$((waited_ms + interval_ms))
  done

  # If the process is still alive but the server is already down, proceed so we can restart quickly.
  if is_pid_alive "${old_pid}" && ! is_server_responding; then
    echo "note: old pid=${old_pid} still alive but server is down; continuing restart"
  fi
else
  if [[ -n "${old_pid}" ]]; then
    echo "shutdown: pid file had ${old_pid} but process is not alive; continuing"
  else
    echo "shutdown: no existing pid; continuing"
  fi
fi

echo "restart: starting new server..."
echo "start: ${bin_link} -config ${config} -model ${model} -addr ${addr}"
if [[ -f "${log_path}" && -s "${log_path}" ]]; then
  cat "${log_path}" >>"${log_backup_path}"
  printf '\n' >>"${log_backup_path}"
fi
: >"${log_path}"

deploy_id="$(
  python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
ts="$(date +'%Y-%m-%d %H:%M:%S %Z')"
echo "=== DEPLOY id=${deploy_id} ts=${ts} backend=${backend_ver} pid=? ===" >>"${log_path}"

if [[ "${KAGENT_TAIL:-1}" != "0" ]]; then
  echo "logs: following ${log_path} in real time (Ctrl-C to stop following, server keeps running)..."
  tail -n +1 -F "${log_path}" &
  tail_pid="$!"
  printf '%s\n' "${tail_pid}" >"${tail_pid_file}"
  trap cleanup_tail EXIT
  trap handle_follow_stop INT TERM
fi

nohup "${bin_link}" -config "${config}" -model "${model}" -addr "${addr}" >>"${log_path}" 2>&1 &
new_pid="$!"
echo "${new_pid}" > "${pid_file}"
echo "=== DEPLOY id=${deploy_id} ts=${ts} backend=${backend_ver} pid=${new_pid} started ===" >>"${log_path}"

echo "healthcheck: ${version_url}"
ok=0
for _ in $(seq 1 50); do
  body="$(curl -sS -m 0.3 "${version_url}" 2>/dev/null || true)"
  if [[ -n "${body}" ]] && echo "${body}" | grep -Eq "\"backend\"[[:space:]]*:[[:space:]]*\"${backend_ver}\""; then
    ok=1
    break
  fi
  sleep 0.1
done

if [[ "${ok}" != "1" ]]; then
  echo "deploy failed: server did not become healthy with backend=${backend_ver}" >&2
  echo "pid=${new_pid} log=${log_path}" >&2
  echo "--- last 80 log lines ---" >&2
  tail -n 80 "${log_path}" >&2 || true
  exit 1
fi

echo "deploy ok: backend=${backend_ver} pid=${new_pid}"
echo "log: ${log_path}"

if [[ "${KAGENT_TAIL:-1}" != "0" ]]; then
  wait "${tail_pid}" || true
fi
