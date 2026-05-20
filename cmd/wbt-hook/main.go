// wbt-hook is a thin shim that exec's `wbt hook <args...>`.
//
// Phase 2.3 of the install simplification consolidated all hook binaries into
// `wbt` subcommands; this binary remains only so that existing
// ~/.claude/settings.json PostToolUse entries that hard-code the absolute
// path to wbt-hook continue to work without operator intervention. The
// shared shim logic (PATH lookup, sibling fallback, exit-code propagation,
// stdio wiring) lives in internal/shim.
package main

import (
	"os"

	"github.com/Wayne997035/wayneblacktea/internal/shim"
)

func main() {
	os.Exit(shim.ExecWbt("hook", os.Args[1:]))
}
