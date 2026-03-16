#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

HUB_URL="${HUB_URL:-http://127.0.0.1:18080}"
RUN_DEPLOY="${RUN_DEPLOY:-1}"
SERVICE_ID="${SERVICE_ID:-phase2-smoke-service}"
INSTANCE_ID="${INSTANCE_ID:-phase2-smoke-1}"
TOOL_ID="${TOOL_ID:-ops.smoke.ping}"

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
  echo "[INFO] deploying services before lifecycle smoke..."
  DEPLOY_TAIL=0 ./scripts/deploy.sh
fi

echo "[INFO] checking hub internal healthz..."
health_resp="$(curl -sS "${HUB_URL}/api/internal/healthz")"
assert_contains "${health_resp}" "\"ok\":true"

echo "[INFO] prepare-start ${SERVICE_ID}/${INSTANCE_ID}"
prepare_resp="$(post_json "/api/internal/supervisor/prepare-start" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\"}")"
assert_contains "${prepare_resp}" "\"ok\":true"
assert_contains "${prepare_resp}" "\"prepared\":true"

echo "[INFO] register ${SERVICE_ID}/${INSTANCE_ID}"
register_resp="$(post_json "/api/internal/supervisor/register" "{
  \"service_id\":\"${SERVICE_ID}\",
  \"instance_id\":\"${INSTANCE_ID}\",
  \"version\":\"phase2-smoke-v1\",
  \"transport\":\"tcp\",
  \"endpoint\":{\"tcp_url\":\"http://127.0.0.1:65530\"},
  \"tools\":[{\"tool_id\":\"${TOOL_ID}\",\"timeout_ms\":1000}]
}")"
assert_contains "${register_resp}" "\"ok\":true"
assert_contains "${register_resp}" "\"service_session_token\""

echo "[INFO] heartbeat ${SERVICE_ID}/${INSTANCE_ID}"
heartbeat_resp="$(post_json "/api/internal/supervisor/heartbeat" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\",\"status\":\"ready\"}")"
assert_contains "${heartbeat_resp}" "\"ok\":true"
assert_contains "${heartbeat_resp}" "\"status\":\"active\""

echo "[INFO] drain ${SERVICE_ID}/${INSTANCE_ID}"
drain_resp="$(post_json "/api/internal/supervisor/drain" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\",\"reason\":\"phase2 smoke\"}")"
assert_contains "${drain_resp}" "\"ok\":true"
assert_contains "${drain_resp}" "\"draining\":true"

echo "[INFO] unregister ${SERVICE_ID}/${INSTANCE_ID}"
unregister_resp="$(post_json "/api/internal/supervisor/unregister" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\"}")"
assert_contains "${unregister_resp}" "\"ok\":true"
assert_contains "${unregister_resp}" "\"unregistered\":true"

echo "[INFO] heartbeat should be rejected after unregister"
heartbeat_after_unreg_resp="$(post_json "/api/internal/supervisor/heartbeat" "{\"service_id\":\"${SERVICE_ID}\",\"instance_id\":\"${INSTANCE_ID}\",\"status\":\"ready\"}")"
assert_contains "${heartbeat_after_unreg_resp}" "\"ok\":false"
assert_contains "${heartbeat_after_unreg_resp}" "\"code\":\"CONFLICT\""

echo "[PASS] phase2 lifecycle smoke completed"
