// wbt is the one-click installer CLI for wayneblacktea.
//
// Usage:
//
//	wbt setup      — one-command install: SQLite + port reclaim + bg server + claude mcp
//	wbt status     — show server PID, port, /health (use --format json)
//	wbt stop       — stop the background server (idempotent)
//	wbt restart    — stop + setup
//	wbt init       — DEPRECATED alias for `wbt setup`
//	wbt serve      — loads .env and starts the wayneblacktea-server binary (foreground)
//	wbt mcp        — serve MCP stdio (wired into .mcp.json by `wbt init`)
//	wbt context    — Claude Code SessionStart hook (subcommand: session-start)
//	wbt hook       — Claude Code PostToolUse hook (reads JSON from stdin)
//	wbt doctor     — Claude Code Stop hook + personal-OS health snapshot
//	wbt guard      — manage guard bypass rules
//	wbt reconcile  — drain merged-PR backlog into GTD tasks (Phase 2 fuzzy)
//	wbt install-git-hook — install a global git post-merge hook that auto-closes GTD tasks on PR merge
//	wbt post-merge-local — (hook-invoked) close the GTD task for the just-merged PR
//	wbt version    — print version info (also accepts --version)
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Wayne997035/wayneblacktea/internal/buildinfo"
	"github.com/Wayne997035/wayneblacktea/internal/cli"
)

// Version metadata injected at link time via -ldflags
//
//	-X main.version=<tag> -X main.commit=<sha> -X main.date=<iso8601>
//
// **Nothing sets these any more.** This CLI is distributed with
// `go install <module>@<version>`, which injects no ldflags — the version
// arrives inside the binary's build info instead, and
// buildinfo.EffectiveVersion() reads it. The vars are kept because `commit`
// and `date` still have no build-info equivalent on the module-proxy path
// (a proxy install has no VCS revision), so they stay at their sentinels and
// printVersion prints them as such. NEVER print `version` directly.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `wbt — wayneblacktea one-click installer

Commands:
  wbt setup      One-command install: SQLite dir + port reclaim + bg server + claude mcp
  wbt status     Show server PID, port, /health (use --format json for machine-readable)
  wbt stop       Stop the background server (idempotent)
  wbt restart    Stop, then re-run setup
  wbt init       DEPRECATED alias for ` + "`wbt setup`" + `
  wbt serve      Load config and start the wayneblacktea-server in the foreground
  wbt mcp        Serve MCP stdio (wired into .mcp.json by ` + "`wbt init`" + `;
                 open Claude Code from the directory containing .mcp.json)
  wbt context    Claude Code SessionStart hook
                 (subcommand: ` + "`wbt context session-start`" + `)
  wbt hook       Claude Code PostToolUse hook (reads JSON payload from stdin)
  wbt doctor     Claude Code Stop hook + personal-OS health snapshot
  wbt guard      Manage guard bypass rules (see: wbt guard --help)
  wbt reconcile  Drain merged-PR backlog into GTD tasks (see: wbt reconcile --help)
  wbt reembed    Backfill real-provider embeddings for historical rows (see: wbt reembed --help)
  wbt install-git-hook  Install a global git post-merge hook that auto-closes
                 GTD tasks on PR merge (see: wbt install-git-hook --help)
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
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// subcommands maps subcommand names to their handlers. Keeping this as a
// table (instead of an inline switch) lets main's cyclomatic complexity
// stay constant as more commands are added.
var subcommands = map[string]func([]string) error{
	// Phase 2 lifecycle commands.
	"setup":   cli.RunSetup,
	"status":  cli.RunStatus,
	"stop":    cli.RunStop,
	"restart": cli.RunRestart,
	"init": func(rest []string) error {
		fmt.Fprintln(os.Stderr, "wbt: 'init' is deprecated; use 'wbt setup'")
		return cli.RunSetup(rest)
	},
	// Long-running / interactive commands.
	"serve": cli.RunServe,
	"mcp":   func([]string) error { return cli.RunMCP() },
	// Claude Code hook commands (Phase 2.3).
	"context": func(rest []string) error { return runHookSubcmd(rest, cli.RunContext) },
	"hook":    func(rest []string) error { return runHookSubcmd(rest, cli.RunHook) },
	"doctor":  func(rest []string) error { return runHookSubcmd(rest, cli.RunDoctor) },
	// Utility commands.
	"guard":            cli.RunGuard,
	"reconcile":        cli.RunReconcile,
	"reembed":          cli.RunReembed,
	"post-merge-local": cli.RunPostMergeLocal,
	"install-git-hook": cli.RunInstallGitHook,
}

// dispatch routes the subcommand to its handler.
func dispatch(name string, rest []string) error {
	fn, ok := subcommands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", name, usage)
		os.Exit(1)
		return nil // unreachable
	}
	if err := fn(rest); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// isVersionArg returns true for any of the conventional version flag forms.
func isVersionArg(arg string) bool {
	return arg == "version" || arg == "--version" || arg == "-v"
}

// runHookSubcmd dispatches a hook subcommand (context/hook/doctor), short-
// circuiting on `--version` / `-v` so legacy self-identification callers
// (PR #85 R3 contract) don't silently create /tmp/<binary>.log via the hook
// path. Returns nil for the version case so main exits 0.
func runHookSubcmd(args []string, run func([]string) error) error {
	if len(args) >= 1 && isVersionArg(args[0]) {
		printVersion()
		return nil
	}
	return run(args)
}

// printVersion writes "<binary> <version> (<commit>)" to stdout. Install
// scripts parse the second whitespace-separated token as the version.
//
// `go install <module>@<version>` — the way this CLI is distributed — injects
// no ldflags, so the package-level `version` var stays at its "dev" sentinel.
// The version is in the binary regardless: the toolchain bakes it into the
// build info. buildinfo.EffectiveVersion() reads that, so **NEVER print the
// raw `version` var here** — doing so made `go install …@v1.0.0` report "dev",
// which is the exact "identity-less-looking string for a build that knows its
// own identity" this project already fixed once on the MCP resource.
func printVersion() {
	fmt.Printf("%s %s (%s)\n", filepath.Base(os.Args[0]), buildinfo.EffectiveVersion(), commit)
	if date != "unknown" {
		fmt.Printf("built %s\n", date)
	}
}
