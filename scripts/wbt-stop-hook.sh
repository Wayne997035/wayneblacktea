#!/usr/bin/env bash
# wbt-stop-hook.sh — Claude Code Stop hook
#
# Called when Claude Code session ends. Creates a session handoff record via
# HTTP so the next session can pick up context (in-progress tasks + recent
# decisions) automatically.
#
# Silently exits on any failure — hooks must never block the main workflow.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WBT_URL="${WBT_API_URL:-https://wayneblacktea-production.up.railway.app}"

main() {
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

    [[ -z "${API_KEY:-}" ]] && return 0

    curl -s -X POST "$WBT_URL/api/auto-handoff" \
        -H "X-API-Key: $API_KEY" \
        --max-time 8 >/dev/null 2>&1 || true
}

main "$@"
exit 0
