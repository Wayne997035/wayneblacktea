// wbt is the one-click installer CLI for wayneblacktea.
//
// Usage:
//
//	wbt init   — interactive wizard that writes .env and .mcp.json
//	wbt serve  — loads .env and starts the wayneblacktea-server binary
//	wbt mcp    — serve MCP stdio (wired into .mcp.json by `wbt init`)
//	wbt guard  — manage guard bypass rules
package main

import (
	"fmt"
	"os"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
)

const usage = `wbt — wayneblacktea one-click installer

Commands:
  wbt init   Run interactive setup wizard (writes .env and .mcp.json)
  wbt serve  Load .env and start the wayneblacktea-server (HTTP API)
  wbt mcp    Serve MCP stdio (wired into .mcp.json by ` + "`wbt init`" + `;
             open Claude Code from the directory containing .mcp.json)
  wbt guard  Manage guard bypass rules (see: wbt guard --help)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "%s", usage)
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = cli.RunInit()
	case "serve":
		err = cli.RunServe(os.Args[2:])
	case "mcp":
		err = cli.RunMCP()
	case "guard":
		err = cli.RunGuard(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
