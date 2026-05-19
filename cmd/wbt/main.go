// wbt is the one-click installer CLI for wayneblacktea.
//
// Usage:
//
//	wbt init       — interactive wizard that writes .env and .mcp.json
//	wbt serve      — loads .env and starts the wayneblacktea-server binary
//	wbt mcp        — serve MCP stdio (wired into .mcp.json by `wbt init`)
//	wbt guard      — manage guard bypass rules
//	wbt reconcile  — drain merged-PR backlog into GTD tasks (Phase 2 fuzzy)
//	wbt version    — print version info (also accepts --version)
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wayne997035/wayneblacktea/internal/cli"
)

// Version metadata injected at link time via -ldflags
//
//	-X main.version=<tag> -X main.commit=<sha> -X main.date=<iso8601>
//
// goreleaser populates these on tagged release builds; local `go build`
// without ldflags produces "dev" / "none" / "unknown" sentinel values.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `wbt — wayneblacktea one-click installer

Commands:
  wbt init       Run interactive setup wizard (writes .env and .mcp.json)
  wbt serve      Load .env and start the wayneblacktea-server (HTTP API)
  wbt mcp        Serve MCP stdio (wired into .mcp.json by ` + "`wbt init`" + `;
                 open Claude Code from the directory containing .mcp.json)
  wbt guard      Manage guard bypass rules (see: wbt guard --help)
  wbt reconcile  Drain merged-PR backlog into GTD tasks (see: wbt reconcile --help)
  wbt version    Print version info (also accepts --version)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "%s", usage)
		os.Exit(1)
	}
	// Version subcommand handled before generic dispatch so install scripts
	// can parse `wbt version` output without depending on cli sub-init.
	if isVersionArg(os.Args[1]) {
		printVersion()
		return
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
	case "reconcile":
		err = cli.RunReconcile(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// isVersionArg returns true for any of the conventional version flag forms.
func isVersionArg(arg string) bool {
	return arg == "version" || arg == "--version" || arg == "-v"
}

// printVersion writes "<binary> <version> (<commit>)" to stdout. Install
// scripts parse the second whitespace-separated token as the version.
func printVersion() {
	fmt.Printf("%s %s (%s)\n", filepath.Base(os.Args[0]), version, commit)
	if date != "unknown" {
		fmt.Printf("built %s\n", date)
	}
}
