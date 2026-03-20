#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
project_root="$(pwd)"

hub_addr="${HUB_ADDR:-127.0.0.1:18080}"
hub_log="log.txt"
hub_log_backup="log.txt.bak"
build_state_dir="run/.build-cache"
color_reset=$'\033[0m'
color_info=$'\033[36m'
color_succ=$'\033[32m'
color_warn=$'\033[33m'
color_error=$'\033[31m'

if command -v shasum >/dev/null 2>&1; then
  hash_cmd=(shasum -a 256)
elif command -v sha256sum >/dev/null 2>&1; then
  hash_cmd=(sha256sum)
else
  hash_cmd=()
fi


log_deploy() {
  local level="${1:-INFO}"
  local msg="$2"
  local line
  line="[$(date +'%Y-%m-%d %H:%M:%S')] [${level}] [DEPLOY] ${msg}"
  colorize_log_line "${line}"
  # Also append to log.txt for history preservation
  if [[ -n "${hub_log:-}" ]]; then
    printf "%s\n" "${line}" >> "${hub_log}"
  fi
}

hash_text() {
  "${hash_cmd[@]}" "$@"
}

collect_build_sources() {
  local path
  for path in "$@"; do
    if [[ -d "${path}" ]]; then
      find "${path}" -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -print
    elif [[ -f "${path}" ]]; then
      printf "%s\n" "${path}"
    fi
  done | LC_ALL=C sort -u
}

calc_build_fingerprint() {
  local file_list
  local file

  if [[ ${#hash_cmd[@]} -eq 0 ]]; then
    printf "__force_rebuild__\n"
    return 0
  fi

  file_list="$(collect_build_sources "$@")"
  if [[ -z "${file_list}" ]]; then
    printf "__force_rebuild__\n"
    return 0
  fi

  while IFS= read -r file; do
    [[ -n "${file}" ]] || continue
    printf "%s\n" "${file}"
    hash_text "${file}"
  done <<< "${file_list}" | hash_text | awk '{print $1}'
}

should_rebuild() {
  local binary_path="$1"
  local stamp_path="$2"
  local fingerprint="$3"
  local previous_fingerprint=""

  if [[ "${fingerprint}" == "__force_rebuild__" ]]; then
    return 0
  fi
  if [[ ! -x "${binary_path}" ]]; then
    return 0
  fi
  if [[ ! -f "${stamp_path}" ]]; then
    return 0
  fi

  previous_fingerprint="$(tr -d '[:space:]' < "${stamp_path}")"
  [[ "${previous_fingerprint}" != "${fingerprint}" ]]
}

colorize_log_line() {
  local line="$1"
  local color=""

  case "${line}" in
    *"[ERROR]"*)
      color="${color_error}"
      ;;
    *"[WARN]"*)
      color="${color_warn}"
      ;;
    *"[SUCC]"*)
      color="${color_succ}"
      ;;
    *"[INFO]"*)
      color="${color_info}"
      ;;
  esac

  if [[ -n "${color}" ]]; then
    printf "%b%s%b\n" "${color}" "${line}" "${color_reset}"
  else
    printf "%s\n" "${line}"
  fi
}

# 0. Environment Prep
mkdir -p logs data run "${build_state_dir}"
backend_ver=$(jq -r .backend version.json)
log_deploy "INFO" "Deploying version: ${backend_ver}"
if [[ ${#hash_cmd[@]} -eq 0 ]]; then
  log_deploy "WARN" "No SHA256 tool found. Falling back to full rebuilds."
fi

# 1. Build Phase
hub_fingerprint="$(calc_build_fingerprint "hub" "pkg" "go.mod" "go.sum")"
hub_stamp="${build_state_dir}/hub.sha256"
if should_rebuild "./kagent" "${hub_stamp}" "${hub_fingerprint}"; then
  log_deploy "INFO" "Building Hub binary..."
  go build -buildvcs=false -o "./kagent" ./hub/cmd/hub
  chmod +x ./kagent
  printf "%s\n" "${hub_fingerprint}" > "${hub_stamp}"
else
  log_deploy "INFO" "Skipping Hub build; no source changes detected."
fi

# Build all managed services defined in hub lifecycle config
service_entries=$(jq -r '.service.services[] | "\(.service_id):\(.dir)"' hub/config/services.json)
for entry in ${service_entries}; do
  sid="${entry%%:*}"
  sdir="${entry#*:}"

  mkdir -p "${sdir}/run"
  service_binary="${sdir}/run/${sid}-latest"
  service_stamp="${build_state_dir}/${sid}.sha256"
  service_fingerprint="$(calc_build_fingerprint "${sdir}" "pkg" "go.mod" "go.sum")"

  if should_rebuild "${service_binary}" "${service_stamp}" "${service_fingerprint}"; then
    log_deploy "INFO" "Building ${sid}..."
    go build -buildvcs=false -o "${service_binary}" "./${sdir}/cmd/${sid}"
    printf "%s\n" "${service_fingerprint}" > "${service_stamp}"
  else
    log_deploy "INFO" "Skipping ${sid} build; no source changes detected."
  fi
  
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
log_deploy "SUCC" "Hub launch command submitted. Hub will handle port preemption, service orchestration, and startup result logging."
log_deploy "INFO" "Entering log monitoring mode (Press Ctrl+C to stop viewing, Hub will keep running)..."
tail -n 0 -F "${hub_log}" | while IFS= read -r line; do
  colorize_log_line "${line}"
done
