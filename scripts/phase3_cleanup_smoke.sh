#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

HUB_URL="${HUB_URL:-http://127.0.0.1:18080}"
RUN_DEPLOY="${RUN_DEPLOY:-1}"
SERVICE_ID="${SERVICE_ID:-phase3-cleanup-smoke}"
INSTANCE_ID="${INSTANCE_ID:-phase3-cleanup-1}"
TOOL_ID="${TOOL_ID:-ops.cleanup.ping}"

if [[ "${1:-}" == "--skip-deploy" ]]; then
  RUN_DEPLOY="0"
fi

assert_contains() {
  local payload="$1"
  local expected="$2"
  if [[ "${payload}" != *"${expected}"* ]]; then
    echo "[FAIL] expected response to contain: ${expected}" >&2
    echo "[FAIL] actual: ${payload}" >&2
    exit 1
  fi
}

assert_status_404() {
  local method="$1"
  local path="$2"
  local status
  status="$(curl -sS -o /tmp/phase3-smoke.body -w "%{http_code}" -X "${method}" "${HUB_URL}${path}")"
  if [[ "${status}" != "404" ]]; then
    echo "[FAIL] expected removed endpoint ${path} to return 404, got ${status}" >&2
    cat /tmp/phase3-smoke.body >&2 || true
    exit 1
  fi
}

post_json() {
  local path="$1"
  local body="$2"
  curl -sS -X POST "${HUB_URL}${path}" \
    -H "Content-Type: application/json" \
    -d "${body}"
}

cleanup() {
  curl -sS -X POST "${HUB_URL}/api/internal/supervisor/unregister" \
    -H "Content-Type: application/json" \
    -d "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\"}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [[ "${RUN_DEPLOY}" == "1" ]]; then
  echo "[INFO] deploying services before phase3 smoke..."
  DEPLOY_TAIL=0 ./scripts/deploy.sh
fi

echo "[INFO] checking hub internal healthz..."
health_resp="$(curl -sS "${HUB_URL}/api/internal/healthz")"
assert_contains "${health_resp}" "\"ok\":true"

echo "[INFO] verifying removed legacy endpoints..."
assert_status_404 "GET" "/api/projects"
assert_status_404 "GET" "/api/threads/demo"
assert_status_404 "GET" "/ws"
assert_status_404 "POST" "/api/admin/services/prepare-start"
assert_status_404 "POST" "/api/admin/services/register"
assert_status_404 "POST" "/api/admin/services/heartbeat"

echo "[INFO] verifying internal register path still works..."
register_resp="$(post_json "/api/internal/supervisor/register" "{
  \"service_id\":\"${SERVICE_ID}\",
  \"instance_id\":\"${INSTANCE_ID}\",
  \"version\":\"phase3-cleanup-v1\",
  \"transport\":\"tcp\",
  \"endpoint\":{\"tcp_url\":\"http://127.0.0.1:65531\"},
  \"tools\":[{\"tool_id\":\"${TOOL_ID}\",\"timeout_ms\":1000}]
}")"
assert_contains "${register_resp}" "\"ok\":true"
assert_contains "${register_resp}" "\"service_session_token\""

echo "[INFO] heartbeat/unregister on internal protocol..."
hb_resp="$(post_json "/api/internal/supervisor/heartbeat" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\",\"status\":\"ready\"}")"
assert_contains "${hb_resp}" "\"ok\":true"
unreg_resp="$(post_json "/api/internal/supervisor/unregister" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\"}")"
assert_contains "${unreg_resp}" "\"ok\":true"

echo "[PASS] phase3 cleanup smoke completed"
