#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

reset_mode="${1:-all}"
if [[ "${reset_mode}" != "all" && "${reset_mode}" != "users" ]]; then
  echo "usage: $0 [all|users]" >&2
  echo "  all   - clear all runtime data under ./data (default)" >&2
  echo "  users - clear user-scoped data under ./data/users, keep ./data/hub" >&2
  exit 2
fi

HUB_ADDR="${HUB_ADDR:-127.0.0.1:18080}"
CHAT_SERVER_ADDR="${CHAT_SERVER_ADDR:-127.0.0.1:18082}"
AI_DOUBAO_ADDR="${AI_DOUBAO_ADDR:-127.0.0.1:18081}"
FILE_SERVICE_ADDR="${FILE_SERVICE_ADDR:-127.0.0.1:18084}"
DATABASE_SERVICE_ADDR="${DATABASE_SERVICE_ADDR:-127.0.0.1:18085}"
SURFACE_MANAGER_ADDR="${SURFACE_MANAGER_ADDR:-127.0.0.1:18086}"

shutdown_service() {
  local name="$1"
  local addr="$2"
  echo "=> stopping ${name} (POST http://${addr}/admin/shutdown)"
  curl -s -X POST "http://${addr}/admin/shutdown" >/dev/null 2>&1 || true
}

echo "=> [Kagent Reset] mode=${reset_mode} starting..."
shutdown_service "Hub" "${HUB_ADDR}"
shutdown_service "Chat Server" "${CHAT_SERVER_ADDR}"
shutdown_service "AI Doubao" "${AI_DOUBAO_ADDR}"
shutdown_service "File Service" "${FILE_SERVICE_ADDR}"
shutdown_service "Database Service" "${DATABASE_SERVICE_ADDR}"
shutdown_service "Surface Manager" "${SURFACE_MANAGER_ADDR}"

sleep 1

echo "=> removing runtime pid files"
rm -f run/hub.pid run/chat-server.pid run/ai-doubao.pid run/file-service.pid run/database-service.pid run/surface-manager.pid run/deploy-tail.pid

if [[ "${reset_mode}" == "all" ]]; then
  echo "=> delete targets:"
  echo "   - data/*"
else
  echo "=> delete targets:"
  echo "   - data/users/*"
  echo "=> keep targets:"
  echo "   - data/hub/*"
fi
echo "=> deleting in 3 seconds..."
sleep 1
echo "=> deleting in 2 seconds..."
sleep 1
echo "=> deleting in 1 second..."
sleep 1

if [[ "${reset_mode}" == "all" ]]; then
  rm -rf data/*
else
  rm -rf data/users/*
fi

mkdir -p data/users/default

echo "=> reset completed"
if [[ "${reset_mode}" == "all" ]]; then
  echo "=> next deploy will recreate sqlite schema/secrets from scratch"
else
  echo "=> users data reset done (hub auth db and secrets are preserved)"
fi
