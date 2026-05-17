#!/usr/bin/env bash
# wbt-stop-hook.sh — Claude Code Stop hook
#
# Called when Claude Code session ends. Creates a session handoff record via
# HTTP so the next session can pick up context (in-progress tasks + recent
# decisions) automatically.
#
# When a session JSONL exists, the last 50 user/assistant messages are sent
# as a transcript so the server can generate an AI-enriched summary via
# claude-haiku-4-5.
#
# Silently exits on any failure — hooks must never block the main workflow.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WBT_URL="${WBT_API_URL:-http://localhost:8080}"

# Claude project directory where session JSONL files live.
# Set CLAUDE_PROJECTS_DIR to your own path, e.g.:
#   export CLAUDE_PROJECTS_DIR="${HOME}/.claude/projects/$(pwd | tr '/' '-')"
CLAUDE_PROJECTS_DIR="${CLAUDE_PROJECTS_DIR:-}"

warn_if_insecure_env_file() {
    local path="$1"
    [[ -f "$path" ]] || return 0

    local mode
    mode=$(stat -f '%Lp' "$path" 2>/dev/null || stat -c '%a' "$path" 2>/dev/null || true)
    [[ -z "$mode" ]] && return 0

    local last_two="${mode: -2}"
    if (( 10#$last_two != 0 )); then
        printf 'WARNING: %s is group/world readable; chmod 600 is recommended\n' "$path" >&2
    fi
}

resolve_path() {
    local path="$1"
    readlink -f "$path" 2>/dev/null || python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$path" 2>/dev/null
}

main() {
    # --- Load API_KEY: parse only API_KEY= lines, never source (avoids arbitrary code exec) ---
    local _key
    if [[ -z "${API_KEY:-}" && -f "$PROJECT_ROOT/.env.local" ]]; then
        warn_if_insecure_env_file "$PROJECT_ROOT/.env.local"
        _key=$(grep -m1 '^API_KEY=' "$PROJECT_ROOT/.env.local" 2>/dev/null | cut -d= -f2-)
        [[ -n "$_key" ]] && API_KEY="$_key"
    fi
    if [[ -z "${API_KEY:-}" && -f "$PROJECT_ROOT/.env" ]]; then
        warn_if_insecure_env_file "$PROJECT_ROOT/.env"
        _key=$(grep -m1 '^API_KEY=' "$PROJECT_ROOT/.env" 2>/dev/null | cut -d= -f2-)
        [[ -n "$_key" ]] && API_KEY="$_key"
    fi

    [[ -z "${API_KEY:-}" ]] && return 0

    # --- Read session_id from stdin JSON ---
    local stdin_json
    stdin_json=$(cat)
    local session_id
    session_id=$(python3 -c "
import json, sys
try:
    data = json.loads(sys.stdin.read())
    print(data.get('session_id', ''))
except Exception:
    print('')
" <<< "$stdin_json" 2>/dev/null || true)

    # --- Build transcript JSON (last 50 user/assistant messages, capped at 64KB) ---
    local transcript_json="[]"
    if [[ -n "$session_id" && -n "$CLAUDE_PROJECTS_DIR" ]]; then
        printf '%s' "$session_id" | grep -qE '^[0-9a-fA-F-]{36}$' || return 0
        local jsonl_path="${CLAUDE_PROJECTS_DIR}/${session_id}.jsonl"
        local resolved_projects_dir resolved_jsonl_path
        resolved_projects_dir=$(resolve_path "$CLAUDE_PROJECTS_DIR") || return 0
        resolved_jsonl_path=$(resolve_path "$jsonl_path") || return 0
        case "$resolved_jsonl_path" in
            "$resolved_projects_dir"/*) ;;
            *) return 0 ;;
        esac
        if [[ -f "$resolved_jsonl_path" ]]; then
            transcript_json=$(python3 - "$resolved_jsonl_path" <<'PYEOF' 2>/dev/null || echo "[]"
import json, sys

path = sys.argv[1]
messages = []

with open(path, 'r', errors='replace') as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if obj.get('type') != 'message':
            continue
        role = obj.get('role', '')
        if role not in ('user', 'assistant'):
            continue
        msg = obj.get('message', {})
        content = msg.get('content', '')
        if isinstance(content, list):
            parts = []
            for block in content:
                if isinstance(block, dict) and block.get('type') == 'text':
                    parts.append(block.get('text', ''))
            content = ' '.join(parts)
        if not isinstance(content, str):
            content = str(content)
        messages.append({'role': role, 'content': content})

# Take last 50 messages.
messages = messages[-50:]

# Cap total size at 64KB.
cap = 64 * 1024
total = 0
result = []
for m in messages:
    encoded = json.dumps(m)
    if total + len(encoded) > cap:
        break
    result.append(m)
    total += len(encoded)

print(json.dumps(result))
PYEOF
)
        fi
    fi

    # --- POST to /api/auto-handoff ---
    local body="{}"
    if [[ "$transcript_json" != "[]" && -n "$transcript_json" ]]; then
        body=$(python3 -c "
import json, sys
t = json.loads(sys.stdin.read())
print(json.dumps({'transcript': t}))
" <<< "$transcript_json" 2>/dev/null || echo "{}")
    fi

    curl -s -X POST "$WBT_URL/api/auto-handoff" \
        -H "X-API-Key: $API_KEY" \
        -H "Content-Type: application/json" \
        --data-raw "$body" \
        --max-time 8 >/dev/null 2>&1 || true
}

main "$@"
exit 0
