#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
project_root="$(pwd)"

hub_addr="${HUB_ADDR:-127.0.0.1:18080}"
hub_pid_file="run/hub.pid"
hub_log="log.txt"
hub_log_backup="log.txt.bak"
hub_shutdown_url="http://${hub_addr}/admin/shutdown"

log_deploy() {
  local level="${1:-INFO}"
  local msg="$2"
  local line
  line="[$(date +'%Y-%m-%d %H:%M:%S')] [${level}] [DEPLOY] ${msg}"
  printf "%s\n" "${line}"
  # Also append to log.txt for history preservation
  if [[ -n "${hub_log:-}" ]]; then
    printf "%s\n" "${line}" >> "${hub_log}"
  fi
}

is_pid_alive() {
  local pid="${1:-}"
  [[ -n "${pid}" ]] && kill -0 "${pid}" 2>/dev/null
}

stop_hub() {
  local pid
  pid=$(cat "${hub_pid_file}" 2>/dev/null || true)
  if [[ -n "${pid}" ]] && is_pid_alive "${pid}"; then
    log_deploy "INFO" "Stopping hub (pid=${pid})..."
    curl -sS -m 2 -X POST "${hub_shutdown_url}" >/dev/null 2>&1 || true
    
    # Wait up to 3 seconds for graceful exit
    local waited=0
    while is_pid_alive "${pid}" && [ ${waited} -lt 30 ]; do
      sleep 0.1
      waited=$((waited + 1))
    done
    
    if is_pid_alive "${pid}"; then
      log_deploy "WARN" "Hub did not exit within 3s, force killing..."
      kill -9 "${pid}" 2>/dev/null || true
      # Ensure it's fully gone
      wait "${pid}" 2>/dev/null || true
      sleep 0.2
    fi
  fi
  rm -f "${hub_pid_file}"
}

# 0. Environment Prep
mkdir -p logs run data
backend_ver=$(jq -r .backend version.json)
log_deploy "INFO" "Deploying version: ${backend_ver}"

# 1. Build Phase
log_deploy "INFO" "Building Hub binary..."
go build -buildvcs=false -o "./kagent" ./hub/cmd/hub
chmod +x ./kagent

# Build all managed services defined in config.json
service_entries=$(jq -r '.service.services[] | "\(.service_id):\(.dir)"' hub/config/config.json)
for entry in ${service_entries}; do
  sid="${entry%%:*}"
  sdir="${entry#*:}"
  
  log_deploy "INFO" "Building ${sid}..."
  mkdir -p "${sdir}/run"
  go build -buildvcs=false -o "${sdir}/run/${sid}-latest" "./${sdir}/cmd/${sid}"
  
  log_deploy "INFO" "Syncing manifest for ${sid}..."
  cp "${sdir}/manifest.json" "${sdir}/run/manifest.json"
done

# 2. Stop Phase
stop_hub

# 3. Start Phase
if [[ -f "${hub_log}" && -s "${hub_log}" ]]; then
  cat "${hub_log}" >> "${hub_log_backup}"
  printf '\n' >> "${hub_log_backup}"
fi
: > "${hub_log}"

log_deploy "INFO" "Starting Hub (kagent)..."
nohup ./kagent -addr "${hub_addr}" >> "${hub_log}" 2>&1 &
echo "$!" > "${hub_pid_file}"

log_deploy "SUCC" "Hub started (PID: $!). Orchestration and self-tests are running internally."
log_deploy "INFO" "Entering log monitoring mode (Press Ctrl+C to stop viewing, Hub will keep running)..."
tail -n 0 -F "${hub_log}"

