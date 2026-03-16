#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
project_root="$(pwd)"

trap 'echo "deploy error at line ${LINENO}: ${BASH_COMMAND}" >&2' ERR
if [[ "${DEPLOY_DEBUG:-0}" == "1" ]]; then
  set -x
fi

hub_addr="${HUB_ADDR:-127.0.0.1:18080}"
hub_public_config="${HUB_PUBLIC_CONFIG:-config/config.json}"
hub_user_config="${HUB_USER_CONFIG:-data/users/default/user_custom_config.json}"
hub_sqlite_path="${HUB_SQLITE_PATH:-data/hub/users.db}"

chat_server_addr="${CHAT_SERVER_ADDR:-127.0.0.1:18082}"
chat_server_config="${CHAT_SERVER_CONFIG:-services/chat-server/config/configx.json}"
chat_server_model="${CHAT_SERVER_MODEL:-doubao}"
chat_server_sqlite_path="${CHAT_SERVER_SQLITE_PATH:-data/kagent.db}"

ai_doubao_addr="${AI_DOUBAO_ADDR:-127.0.0.1:18081}"
ai_doubao_config="${AI_DOUBAO_CONFIG:-services/ai-doubao/config/configx.json}"
ai_doubao_model="${AI_DOUBAO_MODEL:-doubao}"

file_service_addr="${FILE_SERVICE_ADDR:-127.0.0.1:18084}"
database_service_addr="${DATABASE_SERVICE_ADDR:-127.0.0.1:18085}"
surface_manager_addr="${SURFACE_MANAGER_ADDR:-127.0.0.1:18086}"
deploy_smoke="${DEPLOY_SMOKE:-1}"

admin_token="${HUB_ADMIN_TOKEN:-}"

version_file="version.json"
if [[ ! -f "${version_file}" ]]; then
  echo "error: missing ${version_file}" >&2
  exit 2
fi

backend_ver="$({
  python3 - <<'PY'
import json
with open('version.json','r',encoding='utf-8') as f:
  v=json.load(f)
print(v.get('backend','').strip())
PY
} )"
if [[ -z "${backend_ver// }" ]]; then
  echo "error: version.json missing backend version" >&2
  exit 2
fi

mkdir -p bin logs run data

hub_bin_name="kagent-backend-${backend_ver}"
hub_bin_path="bin/${hub_bin_name}"
hub_bin_link="bin/hub"

chat_bin_name="chat-server-${backend_ver}"
chat_bin_path="bin/${chat_bin_name}"
chat_bin_link="bin/chat-server"

ai_bin_name="ai-doubao-${backend_ver}"
ai_bin_path="bin/${ai_bin_name}"
ai_bin_link="bin/ai-doubao"

file_bin_name="file-service-${backend_ver}"
file_bin_path="bin/${file_bin_name}"
file_bin_link="bin/file-service"

database_bin_name="database-service-${backend_ver}"
database_bin_path="bin/${database_bin_name}"
database_bin_link="bin/database-service"

surface_manager_bin_name="surface-manager-${backend_ver}"
surface_manager_bin_path="bin/${surface_manager_bin_name}"
surface_manager_bin_link="bin/surface-manager"

hub_log="${project_root}/log.txt"
hub_log_backup="${project_root}/log.txt.bak"
chat_log="${project_root}/logs/chat-server.log"
ai_log="${project_root}/logs/ai-doubao.log"
file_log="${project_root}/logs/file-service.log"
database_log="${project_root}/logs/database-service.log"
surface_manager_log="${project_root}/logs/surface-manager.log"

hub_pid_file="run/hub.pid"
chat_pid_file="run/chat-server.pid"
ai_pid_file="run/ai-doubao.pid"
file_pid_file="run/file-service.pid"
database_pid_file="run/database-service.pid"
surface_manager_pid_file="run/surface-manager.pid"
tail_pid_file="run/deploy-tail.pid"

hub_version_url="http://${hub_addr}/version"
hub_shutdown_url="http://${hub_addr}/admin/shutdown"
chat_health_url="http://${chat_server_addr}/healthz"
chat_shutdown_url="http://${chat_server_addr}/admin/shutdown"
ai_health_url="http://${ai_doubao_addr}/healthz"
ai_shutdown_url="http://${ai_doubao_addr}/admin/shutdown"
file_health_url="http://${file_service_addr}/healthz"
file_shutdown_url="http://${file_service_addr}/admin/shutdown"
database_health_url="http://${database_service_addr}/healthz"
database_shutdown_url="http://${database_service_addr}/admin/shutdown"
surface_manager_health_url="http://${surface_manager_addr}/healthz"
surface_manager_shutdown_url="http://${surface_manager_addr}/admin/shutdown"
chat_register_url="http://${hub_addr}/api/service/register"
hub_prepare_start_url="http://${hub_addr}/api/service/prepare-start"
hub_chat_service_url="http://${chat_server_addr}"
hub_file_service_url="http://${file_service_addr}"
hub_database_service_url="http://${database_service_addr}"
hub_surface_manager_url="http://${surface_manager_addr}"

log_deploy() {
  local level="${1:-INFO}"
  local msg="$2"
  printf "[$(date +'%Y-%m-%d %H:%M:%S')] [%s] [DEPLOY] %s\n" "${level}" "${msg}"
}

is_pid_alive() {
  local pid="$1"
  [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
}

read_pid() {
  local pid_file="$1"
  if [[ -f "${pid_file}" ]]; then
    cat "${pid_file}" 2>/dev/null || true
  fi
}

cleanup_tail() {
  local pid
  pid="$(read_pid "${tail_pid_file}")"
  if is_pid_alive "${pid}"; then
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  fi
  rm -f "${tail_pid_file}"
}

handle_follow_stop() {
  cleanup_tail
  exit 0
}

request_shutdown() {
  local url="$1"
  local with_admin_token="${2:-0}"
  set +e
  local out
  if [[ "${with_admin_token}" == "1" && -n "${admin_token}" ]]; then
    out="$(curl -sS -m 1 -X POST -H "X-Admin-Token: ${admin_token}" "${url}" 2>&1)"
  else
    out="$(curl -sS -m 1 -X POST "${url}" 2>&1)"
  fi
  local rc=$?
  set -e
  if (( rc != 0 )); then
    log_deploy "ERROR" "Shutdown failed (rc=${rc}) for ${url}" >&2
  fi
  printf "%s" "${out}"
  return 0
}

request_prepare_start() {
  local service_id="$1"
  local payload
  payload="$(printf '{"service_id":"%s"}' "${service_id}")"
  set +e
  local out
  out="$(curl -sS -m 3 -X POST -H "Content-Type: application/json" -d "${payload}" "${hub_prepare_start_url}" 2>&1)"
  local rc=$?
  set -e
  if (( rc != 0 )); then
    log_deploy "ERROR" "prepare-start failed (rc=${rc}) service=${service_id}: ${out}"
    return 1
  fi
  if ! grep -Fq '"ok":true' <<<"${out}"; then
    log_deploy "ERROR" "prepare-start rejected service=${service_id}: ${out}"
    return 1
  fi
  return 0
}

wait_for_pid_exit() {
  local pid="$1"
  local timeout_ms="$2"
  local waited_ms=0
  while is_pid_alive "${pid}"; do
    if (( waited_ms >= timeout_ms )); then
      return 1
    fi
    sleep 0.1
    waited_ms=$((waited_ms + 100))
  done
  return 0
}

stop_service() {
  local name="$1"
  local pid_file="$2"
  local shutdown_url="$3"
  local with_admin_token="${4:-0}"

  local pid
  pid="$(read_pid "${pid_file}")"

  # Silent shutdown request - results are handled by wait/kill logic
  request_shutdown "${shutdown_url}" "${with_admin_token}" >/dev/null 2>&1

  if [[ -z "${pid}" ]]; then
    rm -f "${pid_file}"
    return 0
  fi
  if ! is_pid_alive "${pid}"; then
    rm -f "${pid_file}"
    return 0
  fi

  if wait_for_pid_exit "${pid}" 5000; then
    log_deploy "SUCC" "Stopped ${name} (pid=${pid})"
    rm -f "${pid_file}"
    return 0
  fi

  log_deploy "WARN" "${name} graceful shutdown timeout, sending TERM"
  kill -TERM "${pid}" 2>/dev/null || true
  if ! wait_for_pid_exit "${pid}" 2000; then
    log_deploy "WARN" "${name} force killing (pid=${pid})"
    kill -KILL "${pid}" 2>/dev/null || true
    wait_for_pid_exit "${pid}" 1000 || true
  fi
  rm -f "${pid_file}"
}

ensure_private_config() {
  local target="$1"
  if [[ -f "${target}" ]]; then
    return 0
  fi
  local example="${target}.example"
  if [[ ! -f "${example}" ]]; then
    log_deploy "ERROR" "Missing private config ${target}" >&2
    return 1
  fi
  cp "${example}" "${target}"
  chmod 600 "${target}" 2>/dev/null || true
  log_deploy "INFO" "Created ${target} from example"
}

start_service() {
  local name="$1"
  local pid_file="$2"
  local log_file="$3"
  shift 3
  local cmd=("$@")

  # Try to extract port from -addr argument
  local port=""
  for ((i=0; i<${#cmd[@]}; i++)); do
    if [[ "${cmd[i]}" == "-addr" ]]; then
      port="${cmd[i+1]##*:}"
      break
    fi
  done

  nohup "${cmd[@]}" >>"${log_file}" 2>&1 &
  local pid="$!"
  printf '%s\n' "${pid}" >"${pid_file}"
  
  if [[ -n "${port}" ]]; then
    log_deploy "INFO" "Started ${name} (port=${port}, pid=${pid})"
  else
    log_deploy "INFO" "Started ${name} (pid=${pid})"
  fi
}

wait_http_contains() {
  local name="$1"
  local url="$2"
  local needle="$3"
  local attempts="${4:-80}"
  local body=""

  for _ in $(seq 1 "${attempts}"); do
    body="$(curl -sS -m 0.4 "${url}" 2>/dev/null || true)"
    if [[ -n "${body}" ]] && grep -Fq "${needle}" <<<"${body}"; then
      return 0
    fi
    sleep 0.1
  done

  log_deploy "ERROR" "Healthcheck failed: ${name} (url=${url})" >&2
  return 1
}

dump_log_tail() {
  local name="$1"
  local path="$2"
  echo "--- ${name} log tail (${path}) ---" >&2
  tail -n 80 "${path}" >&2 || true
}

abort_and_cleanup() {
  local msg="$1"
  echo "${msg}" >&2
  stop_service "surface-manager" "${surface_manager_pid_file}" "${surface_manager_shutdown_url}" 0 || true
  stop_service "database-service" "${database_pid_file}" "${database_shutdown_url}" 0 || true
  stop_service "file-service" "${file_pid_file}" "${file_shutdown_url}" 0 || true
  stop_service "ai-doubao" "${ai_pid_file}" "${ai_shutdown_url}" 0 || true
  stop_service "chat-server" "${chat_pid_file}" "${chat_shutdown_url}" 0 || true
  stop_service "hub" "${hub_pid_file}" "${hub_shutdown_url}" 1 || true
  exit 1
}

assert_http_ok_with_body() {
  local name="$1"
  local status="$2"
  local body="$3"
  if [[ "${status}" -lt 200 || "${status}" -ge 300 ]]; then
    log_deploy "ERROR" "Smoke failed: ${name} (status=${status})" >&2
    return 1
  fi
  if ! grep -Eq '"ok"[[:space:]]*:[[:space:]]*true' <<<"${body}"; then
    log_deploy "ERROR" "Smoke failed: ${name} (not ok)" >&2
    return 1
  fi
  return 0
}

run_smoke_checks() {
  local hub_base_url="http://${hub_addr}"
  local smoke_user="smoke_$(date +%s)_$RANDOM"
  local smoke_pass="${SMOKE_PASSWORD:-SmokePass123!}"
  local cookie_file
  local body_file
  local body
  local status
  cookie_file="$(mktemp /tmp/kagent-smoke-cookie.XXXXXX)"
  body_file="$(mktemp /tmp/kagent-smoke-body.XXXXXX)"

  status="$(
    curl -sS -o "${body_file}" -w '%{http_code}' -X POST \
      -H "Content-Type: application/json" \
      -c "${cookie_file}" -b "${cookie_file}" \
      -d "{\"username\":\"${smoke_user}\",\"password\":\"${smoke_pass}\"}" \
      "${hub_base_url}/api/auth/register"
  )"
  body="$(cat "${body_file}")"
  assert_http_ok_with_body "auth.register" "${status}" "${body}" || {
    rm -f "${cookie_file}" "${body_file}"
    return 1
  }

  status="$(curl -sS -o "${body_file}" -w '%{http_code}' -X GET -b "${cookie_file}" "${hub_base_url}/api/auth/me")"
  body="$(cat "${body_file}")"
  assert_http_ok_with_body "auth.me" "${status}" "${body}" || {
    rm -f "${cookie_file}" "${body_file}"
    return 1
  }

  status="$(
    curl -sS -o "${body_file}" -w '%{http_code}' -X POST \
      -H "Content-Type: application/json" \
      -b "${cookie_file}" \
      -d '{"tool_id":"app.chat.project_list","args":{}}' \
      "${hub_base_url}/api/tool/call"
  )"
  body="$(cat "${body_file}")"
  assert_http_ok_with_body "tool.app.chat.project_list" "${status}" "${body}" || {
    rm -f "${cookie_file}" "${body_file}"
    return 1
  }

  status="$(
    curl -sS -o "${body_file}" -w '%{http_code}' -X POST \
      -H "Content-Type: application/json" \
      -b "${cookie_file}" \
      -d '{"tool_id":"storage.database.schema","args":{}}' \
      "${hub_base_url}/api/tool/call"
  )"
  body="$(cat "${body_file}")"
  assert_http_ok_with_body "tool.storage.database.schema" "${status}" "${body}" || {
    rm -f "${cookie_file}" "${body_file}"
    return 1
  }

  rm -f "${cookie_file}" "${body_file}"
  log_deploy "SUCC" "Smoke checks passed (Auth + Tool Calls)"
}

cleanup_tail

log_deploy "INFO" "Building services..."
go build -buildvcs=false -o "${hub_bin_path}" ./hub/cmd/hub
ln -sf "${hub_bin_name}" "${hub_bin_link}"
go build -buildvcs=false -o "${chat_bin_path}" ./services/chat-server/cmd/chat-server
ln -sf "${chat_bin_name}" "${chat_bin_link}"
go build -buildvcs=false -o "${ai_bin_path}" ./services/ai-doubao/cmd/ai-doubao
ln -sf "${ai_bin_name}" "${ai_bin_link}"
go build -buildvcs=false -o "${file_bin_path}" ./services/file/cmd/file
ln -sf "${file_bin_name}" "${file_bin_link}"
go build -buildvcs=false -o "${database_bin_path}" ./services/database/cmd/database
ln -sf "${database_bin_name}" "${database_bin_link}"
go build -buildvcs=false -o "${surface_manager_bin_path}" ./services/surface-manager/cmd/surface-manager
ln -sf "${surface_manager_bin_name}" "${surface_manager_bin_link}"

log_deploy "INFO" "Stopping existing services..."
stop_service "hub" "${hub_pid_file}" "${hub_shutdown_url}" 1
stop_service "chat-server" "${chat_pid_file}" "${chat_shutdown_url}" 0
stop_service "ai-doubao" "${ai_pid_file}" "${ai_shutdown_url}" 0
stop_service "file-service" "${file_pid_file}" "${file_shutdown_url}" 0
stop_service "database-service" "${database_pid_file}" "${database_shutdown_url}" 0
stop_service "surface-manager" "${surface_manager_pid_file}" "${surface_manager_shutdown_url}" 0

ensure_private_config "${chat_server_config}"
ensure_private_config "${ai_doubao_config}"

if [[ -f "${hub_log}" && -s "${hub_log}" ]]; then
  cat "${hub_log}" >>"${hub_log_backup}"
  printf '\n' >>"${hub_log_backup}"
fi
: >"${hub_log}"
: >"${chat_log}"
: >"${ai_log}"
: >"${file_log}"
: >"${database_log}"
: >"${surface_manager_log}"

log_deploy "INFO" "Starting services..."
deploy_id="$(python3 - <<'PY'
import uuid
print(uuid.uuid4())
PY
)"
ts="$(date +'%Y-%m-%d %H:%M:%S')"

echo "[${ts}] [INFO] [DEPLOY] New Deployment (ID: ${deploy_id:0:8}, Version: ${backend_ver})" >>"${hub_log}"
start_service "hub" "${hub_pid_file}" "${hub_log}" \
  "${hub_bin_link}" \
  -public-config "${hub_public_config}" \
  -user-config "${hub_user_config}" \
  -sqlite-path "${hub_sqlite_path}" \
  -addr "${hub_addr}" \
  -chat-service-url "${hub_chat_service_url}" \
  -file-service-url "${hub_file_service_url}" \
  -database-service-url "${hub_database_service_url}" \
  -surface-manager-url "${hub_surface_manager_url}"

if ! wait_http_contains "hub" "${hub_version_url}" "\"backend\":\"${backend_ver}\"" 100; then
  dump_log_tail "hub" "${hub_log}"
  abort_and_cleanup "deploy failed: hub healthcheck did not pass"
fi

echo "[${ts}] [INFO] [DEPLOY] Starting ai-doubao" >>"${ai_log}"
request_prepare_start "ai-doubao"
start_service "ai-doubao" "${ai_pid_file}" "${ai_log}" \
  "${ai_bin_link}" \
  -addr "${ai_doubao_addr}" \
  -config "${ai_doubao_config}" \
  -model "${ai_doubao_model}" \
  -hub-register-url "${chat_register_url}"

if ! wait_http_contains "ai-doubao" "${ai_health_url}" "\"ok\":true" 100; then
  dump_log_tail "ai-doubao" "${ai_log}"
  abort_and_cleanup "deploy failed: ai-doubao healthcheck did not pass"
fi

echo "[${ts}] [INFO] [DEPLOY] Starting chat-server" >>"${chat_log}"
request_prepare_start "chat-server"
start_service "chat-server" "${chat_pid_file}" "${chat_log}" \
  "${chat_bin_link}" \
  -addr "${chat_server_addr}" \
  -config "${chat_server_config}" \
  -model "${chat_server_model}" \
  -sqlite-path "${chat_server_sqlite_path}" \
  -hub-register-url "${chat_register_url}"

if ! wait_http_contains "chat-server" "${chat_health_url}" "\"ok\":true" 100; then
  dump_log_tail "chat-server" "${chat_log}"
  abort_and_cleanup "deploy failed: chat-server healthcheck did not pass"
fi

echo "[${ts}] [INFO] [DEPLOY] Starting file-service" >>"${file_log}"
request_prepare_start "file"
start_service "file-service" "${file_pid_file}" "${file_log}" \
  "${file_bin_link}" \
  -addr "${file_service_addr}" \
  -hub-register-url "${chat_register_url}"

if ! wait_http_contains "file-service" "${file_health_url}" "\"ok\":true" 100; then
  dump_log_tail "file-service" "${file_log}"
  abort_and_cleanup "deploy failed: file-service healthcheck did not pass"
fi

echo "[${ts}] [INFO] [DEPLOY] Starting database-service" >>"${database_log}"
request_prepare_start "database"
start_service "database-service" "${database_pid_file}" "${database_log}" \
  "${database_bin_link}" \
  -addr "${database_service_addr}" \
  -hub-register-url "${chat_register_url}"

if ! wait_http_contains "database-service" "${database_health_url}" "\"ok\":true" 100; then
  dump_log_tail "database-service" "${database_log}"
  abort_and_cleanup "deploy failed: database-service healthcheck did not pass"
fi

echo "[${ts}] [INFO] [DEPLOY] Starting surface-manager" >>"${surface_manager_log}"
request_prepare_start "surface-manager"
start_service "surface-manager" "${surface_manager_pid_file}" "${surface_manager_log}" \
  "${surface_manager_bin_link}" \
  -addr "${surface_manager_addr}" \
  -hub-register-url "${chat_register_url}"

if ! wait_http_contains "surface-manager" "${surface_manager_health_url}" "\"ok\":true" 100; then
  dump_log_tail "surface-manager" "${surface_manager_log}"
  abort_and_cleanup "deploy failed: surface-manager healthcheck did not pass"
fi

if [[ "${deploy_smoke}" != "0" ]]; then
  log_deploy "INFO" "Running smoke checks..."
  if ! run_smoke_checks; then
    dump_log_tail "hub" "${hub_log}"
    dump_log_tail "chat-server" "${chat_log}"
    dump_log_tail "database-service" "${database_log}"
    abort_and_cleanup "deploy failed: smoke checks did not pass"
  fi
fi

log_deploy "SUCC" "Deployment successful (Version: ${backend_ver})"

if [[ "${DEPLOY_TAIL:-1}" != "0" ]]; then
  log_deploy "INFO" "Following Hub logs (Ctrl-C to stop following; services keep running)"
  tail -n +1 -F "${hub_log}" &
  tail_pid="$!"
  printf '%s\n' "${tail_pid}" >"${tail_pid_file}"
  trap cleanup_tail EXIT
  trap handle_follow_stop INT TERM
  wait "${tail_pid}" || true
fi
