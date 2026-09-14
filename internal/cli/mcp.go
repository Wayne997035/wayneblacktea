package cli

import (
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/mcprunner"
	"github.com/joho/godotenv"
)

// RunMCP serves MCP stdio by delegating to the mcprunner package. It is the
// `wbt mcp` subcommand (cmd/wbt/main.go's dispatch table) and the only caller
// of mcprunner.Run.
//
// [GTD 2fdba553] mcprunner.Run has exactly one caller — this one. The
// in-process MCP that cmd/server serves does not go through mcprunner at all,
// so "also used by …" would be wrong whatever binary it named.
//
// Reads .env from CWD if present so users do not
// need to set DATABASE_URL / CLAUDE_API_KEY in the environment that
// Claude Code launches the hook from.
func RunMCP() error {
	// Best-effort .env load — absent file is not fatal because Claude Code
	// may export the env vars itself.
	_ = godotenv.Load()
	if err := mcprunner.Run(); err != nil {
		return fmt.Errorf("running MCP stdio server: %w", err)
	}
	return nil
}
