#!/usr/bin/env bash
# wbt-autolog.sh — Claude Code PostToolUse hook (Bash tool)
#
# Reads the Claude Code hook JSON payload from stdin. When the bash command
# contains deployment-decision keywords (railway vars set, gh pr merge, etc.),
# it logs an activity entry to wayneblacktea via the local MCP binary.
#
# Silently exits on any failure — hooks must never block the main workflow.
# This file is committed to the repo; it works for any contributor who has
# cloned the full repo and built the MCP binary (cd build && task build-mcp).
# Binary-only users get the same auto-log via the server-side Go middleware.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY="$PROJECT_ROOT/bin/wayneblacktea-mcp"
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

    # --- Try to log via local MCP binary ---
    if [[ -x "$BINARY" ]]; then
        # Load DATABASE_URL from .env.local (preferred) then .env
        if [[ -f "$PROJECT_ROOT/.env.local" ]]; then
            set -a
            # shellcheck source=/dev/null
            source "$PROJECT_ROOT/.env.local" 2>/dev/null || true
            set +a
        fi
        if [[ -f "$PROJECT_ROOT/.env" ]]; then
            set -a
            # shellcheck source=/dev/null
            source "$PROJECT_ROOT/.env" 2>/dev/null || true
            set +a
        fi

        # Validate DATABASE_URL looks like a postgres URI before using it
        if [[ "${DATABASE_URL:-}" =~ ^postgres(ql)?:// ]]; then
            # MCP protocol: initialize then call tool
            {
                printf '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bash-hook","version":"1.0"}},"id":1}\n'
                printf '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"log_activity","arguments":{"actor":"bash-hook","action":"deploy:bash","notes":%s}},"id":2}\n' "$NOTES_JSON"
            } | timeout 8 "$BINARY" >/dev/null 2>&1 && return 0
        fi
    fi

    # --- Fallback: append to project-local cache file (not world-writable /tmp) ---
    mkdir -p "$(dirname "$FALLBACK_LOG")" 2>/dev/null || return 0
    printf '{"ts":"%s","actor":"bash-hook","action":"deploy:bash","notes":%s}\n' \
        "$TIMESTAMP" "$NOTES_JSON" \
        >> "$FALLBACK_LOG" 2>/dev/null || true
}

main "$@"
exit 0
