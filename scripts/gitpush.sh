#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

msg="${*:-}"
if [[ -z "${msg// }" ]]; then
  echo "usage: scripts/gitpush.sh <commit message>" >&2
  exit 2
fi

if [[ ! -d .git ]]; then
  echo "error: not a git repository (missing .git)" >&2
  exit 2
fi

GIT_BIN="git"
if [[ -x "/opt/homebrew/bin/git" ]]; then
  GIT_BIN="/opt/homebrew/bin/git"
fi

version_file="version.json"
if [[ ! -f "$version_file" ]]; then
  echo "error: missing $version_file" >&2
  exit 2
fi

"$GIT_BIN" add -A

if "$GIT_BIN" diff --cached --quiet; then
  echo "nothing to commit" >&2
  exit 1
fi

today="$(date +%y%m%d)"

staged_files="$("$GIT_BIN" diff --name-only --cached || true)"

has_webui=0
has_backend=0
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  # Ignore the version file itself in change classification.
  if [[ "$f" == "$version_file" ]]; then
    continue
  fi
  if [[ "$f" == webui/* ]]; then
    has_webui=1
    continue
  fi
  # Docs-only changes shouldn't force bump by default.
  if [[ "$f" == doc/* || "$f" == plan/* || "$f" == ref/* || "$f" == README.md || "$f" == AGENTS.md ]]; then
    continue
  fi
  has_backend=1
done <<< "$staged_files"

bump_component() {
  local cur="$1"
  local day="$2"

  if [[ ! "$cur" =~ ^[0-9]{8}$ ]]; then
    echo "${day}01"
    return 0
  fi
  local cur_day="${cur:0:6}"
  local cur_seq="${cur:6:2}"
  if [[ "$cur_day" != "$day" ]]; then
    echo "${day}01"
    return 0
  fi
  local n=$((10#$cur_seq + 1))
  if (( n > 99 )); then
    echo "error: version sequence overflow for day $day (>$cur_seq)" >&2
    exit 3
  fi
  printf "%s%02d" "$day" "$n"
}

get_json_field() {
  local key="$1"
  python3 - "$key" <<'PY'
import json,sys
key=sys.argv[1]
with open("version.json","r",encoding="utf-8") as f:
  data=json.load(f)
v=data.get(key,"")
if v is None:
  v=""
sys.stdout.write(str(v))
PY
}

write_version_json() {
  local backend="$1"
  local webui="$2"
  python3 - "$backend" "$webui" <<'PY'
import json,sys
backend=sys.argv[1]
webui=sys.argv[2]
with open("version.json","r",encoding="utf-8") as f:
  data=json.load(f)
data["format"]="calver-yymmddnn"
data["backend"]=backend
data["webui"]=webui
with open("version.json","w",encoding="utf-8") as f:
  json.dump(data,f,ensure_ascii=False,indent=2)
  f.write("\n")
PY
}

cur_backend="$(get_json_field backend)"
cur_webui="$(get_json_field webui)"
new_backend="$cur_backend"
new_webui="$cur_webui"

if (( has_backend == 1 )); then
  new_backend="$(bump_component "$cur_backend" "$today")"
fi
if (( has_webui == 1 )); then
  new_webui="$(bump_component "$cur_webui" "$today")"
fi

if [[ "$new_backend" != "$cur_backend" || "$new_webui" != "$cur_webui" ]]; then
  write_version_json "$new_backend" "$new_webui"
  "$GIT_BIN" add "$version_file"
  echo "bumped versions: backend=$new_backend webui=$new_webui"
else
  echo "versions unchanged: backend=$cur_backend webui=$cur_webui"
fi

"$GIT_BIN" commit -m "$msg"
"$GIT_BIN" push
