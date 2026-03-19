#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
project_root="$(pwd)"

hub_addr="${HUB_ADDR:-127.0.0.1:18080}"
hub_log="log.txt"
hub_log_backup="log.txt.bak"


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

# 0. Environment Prep
mkdir -p logs data
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

# 2. Start Phase
if [[ -f "${hub_log}" && -s "${hub_log}" ]]; then
  cat "${hub_log}" >> "${hub_log_backup}"
  printf '\n' >> "${hub_log_backup}"
fi
: > "${hub_log}"

log_deploy "INFO" "Starting Hub (kagent)..."
nohup ./kagent -addr "${hub_addr}" >> "${hub_log}" 2>&1 &
log_deploy "SUCC" "Hub started. Orchestration and self-tests are running internally."
log_deploy "INFO" "Entering log monitoring mode (Press Ctrl+C to stop viewing, Hub will keep running)..."
tail -n 0 -F "${hub_log}"
