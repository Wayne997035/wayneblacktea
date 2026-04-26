#!/usr/bin/env bash
# Starts the wayneblacktea MCP server.
# Sources .env.local from the project root for DATABASE_URL.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

if [[ -f "$PROJECT_ROOT/.env.local" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$PROJECT_ROOT/.env.local"
    set +a
fi

BINARY="$PROJECT_ROOT/bin/wayneblacktea-mcp"
if [[ ! -x "$BINARY" ]]; then
    echo "Binary not found. Run: cd build && task build-mcp" >&2
    exit 1
fi

exec "$BINARY"
