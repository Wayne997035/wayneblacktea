#!/usr/bin/env bash
# Starts the wayneblacktea MCP server (stdio transport).
# Sources .env.local from the project root for DATABASE_URL.
#
# HTTP transport alternative: when the wayneblacktea server is running
# (e.g. via `wbt serve` or `go run ./cmd/server`), Claude Code can also
# connect via the HTTP MCP endpoint without this script:
#
#   claude mcp add --transport http wayneblacktea http://localhost:8080/mcp
#
# This stdio script remains the recommended approach for offline / no-server use.
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
