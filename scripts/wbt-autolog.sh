#!/usr/bin/env bash
# wbt-autolog.sh — Claude Code PostToolUse hook (Bash tool)
#
# Reads the Claude Code hook JSON payload from stdin. When the bash command
# contains deployment-decision keywords (railway vars set, gh pr merge, etc.),
# it logs an activity entry to wayneblacktea via the local MCP binary.
#
# Silently exits on any failure — hooks must never block the main workflow.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BINARY="$PROJECT_ROOT/bin/wayneblacktea-mcp"
FALLBACK_LOG="/tmp/wbt-pending-decisions.jsonl"

# --- Read hook payload ---
PAYLOAD=$(cat) || exit 0

# Extract bash command from Claude Code PostToolUse JSON payload.
# Claude Code sends: {"tool_name":"Bash","tool_input":{"command":"..."},...}
BASH_CMD=$(printf '%s' "$PAYLOAD" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    # Try multiple field names Claude Code may use
    cmd = (d.get('tool_input') or d.get('input') or {}).get('command', '')
    print(cmd)
except Exception:
    pass
" 2>/dev/null) || exit 0

[[ -z "$BASH_CMD" ]] && exit 0

# --- Detect deployment decision keywords ---
if ! printf '%s' "$BASH_CMD" | grep -qE '(railway vars set|railway deploy|gh pr merge|gh pr create|git push.*(main|master)|docker build|docker push)'; then
    exit 0
fi

NOTES="${BASH_CMD:0:300}"
TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# --- Try to log via local MCP binary ---
if [[ -x "$BINARY" ]]; then
    # Load DATABASE_URL from .env / .env.local
    if [[ -f "$PROJECT_ROOT/.env" ]]; then
        set -a
        # shellcheck source=/dev/null
        source "$PROJECT_ROOT/.env" 2>/dev/null || true
        set +a
    fi
    if [[ -f "$PROJECT_ROOT/.env.local" ]]; then
        set -a
        # shellcheck source=/dev/null
        source "$PROJECT_ROOT/.env.local" 2>/dev/null || true
        set +a
    fi

    if [[ -n "${DATABASE_URL:-}" ]]; then
        # Escape notes for JSON
        NOTES_JSON=$(printf '%s' "$NOTES" | python3 -c "import sys,json; print(json.dumps(sys.stdin.read()))" 2>/dev/null) || NOTES_JSON="\"$NOTES\""

        # MCP protocol: initialize then call tool
        {
            printf '{"jsonrpc":"2.0","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bash-hook","version":"1.0"}},"id":1}\n'
            printf '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"log_activity","arguments":{"actor":"bash-hook","action":"deploy:bash","notes":%s}},"id":2}\n' "$NOTES_JSON"
        } | timeout 8 "$BINARY" >/dev/null 2>&1 && exit 0
    fi
fi

# --- Fallback: append to local pending-decisions file ---
printf '{"ts":"%s","actor":"bash-hook","action":"deploy:bash","notes":"%s"}\n' \
    "$TIMESTAMP" \
    "$(printf '%s' "$NOTES" | sed 's/"/\\"/g' | tr '\n' ' ')" \
    >> "$FALLBACK_LOG" 2>/dev/null || true

exit 0
