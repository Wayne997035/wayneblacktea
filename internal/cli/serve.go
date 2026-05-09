package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/joho/godotenv"
	"github.com/pkg/browser"
)

// RunServe loads config and runs wayneblacktea-server.
// Config is loaded in priority order (later source wins unless env var already set):
//  1. ~/.config/wayneblacktea/config.yaml  (global, written by `wbt init`)
//  2. .env in CWD  (legacy / per-project override)
//  3. Existing environment variables  (Railway, CI, etc.)
//
// args is the slice of arguments after "serve" (i.e. os.Args[2:]).
//
// Flags:
//
//	--no-browser   suppress automatic browser launch (also suppressed by WBT_NO_BROWSER=1)
func RunServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	noBrowserFlag := fs.Bool("no-browser", false, "disable automatic browser launch on startup")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("serve: %w", err)
	}

	// Config priority (highest to lowest):
	//   1. Already-set environment variables (Railway, CI, shell exports)
	//   2. ~/.config/wayneblacktea/config.yaml (global, written by `wbt init`)
	//   3. .env in CWD (legacy per-project file)
	//
	// LoadGlobalConfig uses SetIfAbsent so it never overwrites already-set vars.
	if err := LoadGlobalConfig(); err != nil {
		return fmt.Errorf("loading global config: %w", err)
	}

	// Apply .env in CWD as fallback for any key not yet set.
	// godotenv.Read parses without touching the process environment; we then
	// call SetIfAbsent so that global config values take precedence over .env.
	if dotEnvMap, err := godotenv.Read(".env"); err == nil {
		for k, v := range dotEnvMap {
			SetIfAbsent(k, v)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("loading .env: %w", err)
	}

	if os.Getenv("API_KEY") == "" {
		return fmt.Errorf("API_KEY must be set (run `wbt init` first)")
	}

	port := ReadEnvPort()
	serverURL := fmt.Sprintf("http://localhost:%s", port)
	fmt.Printf("Starting wayneblacktea at %s\n", serverURL)

	serverBin, err := exec.LookPath("wayneblacktea-server")
	if err != nil {
		return fmt.Errorf("wayneblacktea-server not found in PATH: %w", err)
	}

	// Auto-open browser only after confirming the server binary exists.
	// URL is constructed from server-controlled config (no user input) — no SSRF risk.
	if !*noBrowserFlag && os.Getenv("WBT_NO_BROWSER") == "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			OpenBrowser(serverURL)
		}()
	}

	cmd := exec.CommandContext(context.Background(), serverBin) //nolint:gosec // G204: serverBin resolved from exec.LookPath, not user input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("server exited: %w", err)
	}
	return nil
}

// OpenBrowser opens url in the system default browser. Best-effort: errors are
// silently swallowed because a browser launch failure (headless env, no default
// browser configured) must not abort the server start flow.
func OpenBrowser(url string) {
	if err := browser.OpenURL(url); err != nil {
		// Non-fatal: headless environments (CI, Docker, SSH) commonly have no browser.
		fmt.Fprintf(os.Stderr, "wbt: auto-open browser: %v (suppress with --no-browser or WBT_NO_BROWSER=1)\n", err)
	}
}
