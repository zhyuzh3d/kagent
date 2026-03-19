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

shutdown_hub() {
  echo "=> stopping Hub (POST http://${HUB_ADDR}/api/tool/call)"
  curl -s -X POST "http://${HUB_ADDR}/api/tool/call" \
    -H "Content-Type: application/json" \
    -d '{"tool_id":"hub.system.shutdown","args":{}}' >/dev/null 2>&1 || true
}

echo "=> [Kagent Reset] mode=${reset_mode} starting..."
shutdown_hub

sleep 1

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
