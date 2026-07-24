package validator

import (
	"os"
	"strconv"
)

// CheckTaskInput is the single source of truth for "which quality checks run
// against a task's description" — CheckVagueness (field fixed at
// "description", matching every call site's existing warning-message text)
// composed with CheckKindFields. Every entry point that creates or accepts a
// task (HTTP CreateTask, MCP add_task, HTTP/MCP proposal-accept single +
// batch) MUST route through this function instead of inlining the same two
// calls, so a future entry point can't silently drift from what the others
// check.
//
// Pure function — no I/O, no env reads. Returns a slice of warning strings;
// empty means no issues detected.
func CheckTaskInput(description, kind string) []string {
	var warnings []string
	warnings = append(warnings, CheckVagueness("description", description, kind)...)
	warnings = append(warnings, CheckKindFields(kind, description)...)
	return warnings
}

// StrictModeEnabled reports whether WBT_STRICT_VAGUENESS is set to a truthy
// value in the server environment, as understood by strconv.ParseBool ("1",
// "t", "T", "true", "True", "TRUE"). Never sourced from tool arguments or
// request bodies (user-controlled) — this is the single, server-side read of
// the env var; every HTTP handler and MCP tool that gates task
// creation/acceptance on CheckTaskInput's warnings reads the gate decision
// through this function instead of duplicating the os.Getenv + ParseBool
// pair.
func StrictModeEnabled() bool {
	b, _ := strconv.ParseBool(os.Getenv("WBT_STRICT_VAGUENESS"))
	return b
}
