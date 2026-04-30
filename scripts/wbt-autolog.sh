#!/usr/bin/env bash
# wbt-autolog.sh — Claude Code PostToolUse hook (Bash tool)
#
# Reads the Claude Code hook JSON payload from stdin. When the bash command
# contains deployment-decision keywords (railway vars set, gh pr merge, etc.),
# it logs an activity entry to wayneblacktea via HTTP (curl).
#
# Silently exits on any failure — hooks must never block the main workflow.
# WBT_API_URL defaults to the Railway production URL; override with a local
# value (e.g. http://localhost:8080) for development.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WBT_URL="${WBT_API_URL:-https://wayneblacktea-production.up.railway.app}"
FALLBACK_LOG="$PROJECT_ROOT/.cache/wbt-pending-decisions.jsonl"

# Wrap everything so any unexpected failure is silenced.
main() {
    # --- Read hook payload ---
    local PAYLOAD
    PAYLOAD=$(cat) || return 0

    # Extract bash command from Claude Code PostToolUse JSON payload.
    local BASH_CMD
    BASH_CMD=$(printf '%s' "$PAYLOAD" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    cmd = (d.get('tool_input') or d.get('input') or {}).get('command', '')
    print(cmd)
except Exception:
    pass
" 2>/dev/null) || return 0

    [[ -z "$BASH_CMD" ]] && return 0

    # --- Detect deployment decision keywords ---
    if ! printf '%s' "$BASH_CMD" | grep -qE '(railway vars set|railway deploy|gh pr merge|gh pr create|git push.*(main|master)|docker build|docker push)'; then
        return 0
    fi

    local NOTES="${BASH_CMD:0:300}"
    local TIMESTAMP
    TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null) || TIMESTAMP="unknown"

    # Encode notes as valid JSON string using python3 (handles all control chars,
    # backslashes, and quotes correctly). If python3 is unavailable, skip the
    # log entirely rather than write corrupt JSON.
    local NOTES_JSON
    NOTES_JSON=$(printf '%s' "$NOTES" | python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))" 2>/dev/null) || return 0

    # --- Load API_KEY: parse only API_KEY= lines, never source (avoids arbitrary code exec) ---
    local _key
    if [[ -z "${API_KEY:-}" && -f "$PROJECT_ROOT/.env.local" ]]; then
        _key=$(grep -m1 '^API_KEY=' "$PROJECT_ROOT/.env.local" 2>/dev/null | cut -d= -f2-)
        [[ -n "$_key" ]] && API_KEY="$_key"
    fi
    if [[ -z "${API_KEY:-}" && -f "$PROJECT_ROOT/.env" ]]; then
        _key=$(grep -m1 '^API_KEY=' "$PROJECT_ROOT/.env" 2>/dev/null | cut -d= -f2-)
        [[ -n "$_key" ]] && API_KEY="$_key"
    fi

    # --- Try to log via HTTP ---
    if [[ -n "${API_KEY:-}" ]]; then
        curl -s -f -X POST "$WBT_URL/api/activity" \
            -H "X-API-Key: $API_KEY" \
            -H "Content-Type: application/json" \
            -d "{\"actor\":\"bash-hook\",\"action\":\"deploy:bash\",\"notes\":$NOTES_JSON}" \
            --max-time 5 >/dev/null 2>&1 && return 0
    fi

    # --- Fallback: append to project-local cache file (not world-writable /tmp) ---
    mkdir -p "$(dirname "$FALLBACK_LOG")" 2>/dev/null || return 0
    printf '{"ts":"%s","actor":"bash-hook","action":"deploy:bash","notes":%s}\n' \
        "$TIMESTAMP" "$NOTES_JSON" \
        >> "$FALLBACK_LOG" 2>/dev/null || true
}

main "$@"
exit 0
