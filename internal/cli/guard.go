package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/guard"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

//nolint:gochecknoglobals // compiled once at init; used in sanitizeReason.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

const guardUsage = `wbt guard — manage wbt-guard bypass rules

Subcommands:
  wbt guard bypass add   --scope <s> --target <t> [--tool <name>] [--ttl <duration>] --reason <text>
  wbt guard bypass list  [--scope <s>]
  wbt guard bypass revoke <id>

Scopes: global, repo, dir, file
`

// RunGuard dispatches wbt guard subcommands.
func RunGuard(args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "%s", guardUsage)
		return fmt.Errorf("guard: missing subcommand")
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(os.Stdout, "%s", guardUsage)
		return nil
	}
	if args[0] != "bypass" {
		return fmt.Errorf("guard: unknown subcommand %q", args[0])
	}
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "%s", guardUsage)
		return fmt.Errorf("guard bypass: missing action (add|list|revoke)")
	}

	// Load .env for DATABASE_URL if present — non-fatal.
	_ = godotenv.Load()

	switch args[1] {
	case "add":
		return runGuardBypassAdd(args[2:])
	case "list":
		return runGuardBypassList(args[2:])
	case "revoke":
		return runGuardBypassRevoke(args[2:])
	default:
		return fmt.Errorf("guard bypass: unknown action %q (want add|list|revoke)", args[1])
	}
}

// runGuardBypassAdd adds a bypass rule.
func runGuardBypassAdd(args []string) error {
	fs := flag.NewFlagSet("guard bypass add", flag.ContinueOnError)
	scopeFlag := fs.String("scope", "", "bypass scope: global|repo|dir|file (required)")
	targetFlag := fs.String("target", "", "bypass target value (required)")
	toolFlag := fs.String("tool", "", "tool name to bypass (empty = all tools)")
	ttlFlag := fs.String("ttl", "", "bypass TTL duration (e.g. 1h, 24h, 7d); empty = no expiry")
	reasonFlag := fs.String("reason", "", "reason for bypass (required, must not be empty)")
	dangerouslyGlobal := fs.Bool(
		"i-understand-this-is-global",
		false,
		"required confirmation when --scope=global; whitelists every repo on every machine",
	)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("guard bypass add: %w", err)
	}

	if err := ValidateGuardBypassFlags(*scopeFlag, *targetFlag, *reasonFlag, *dangerouslyGlobal); err != nil {
		return err
	}

	sanitized, err := sanitizeReason(*reasonFlag)
	if err != nil {
		return fmt.Errorf("guard bypass add: %w", err)
	}

	var expiresAt *time.Time
	if *ttlFlag != "" {
		dur, err := ParseDuration(*ttlFlag)
		if err != nil {
			return fmt.Errorf("guard bypass add: --ttl %q: %w", *ttlFlag, err)
		}
		exp := time.Now().UTC().Add(dur)
		expiresAt = &exp
	}

	var toolName *string
	if *toolFlag != "" {
		toolName = toolFlag
	}

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, _ := guard.OpenPool(ctx, dbURL)
	if pool == nil {
		return fmt.Errorf("guard bypass add: database unavailable (set DATABASE_URL)")
	}
	store := guard.NewStore(pool)

	id, err := store.AddBypass(ctx, *scopeFlag, *targetFlag, toolName, sanitized, currentUser(), expiresAt)
	if err != nil {
		return fmt.Errorf("guard bypass add: %w", err)
	}

	fmt.Printf("bypass added: %s\n", id)
	return nil
}

// validBypassScopes lists the scopes accepted by the schema CHECK constraint.
//
//nolint:gochecknoglobals // immutable enum table; equivalent to a const set.
var validBypassScopes = map[string]bool{
	"file":   true,
	"dir":    true,
	"repo":   true,
	"global": true,
}

// overlyBroadScopes lists --target values that would whitelist too broadly.
//
//nolint:gochecknoglobals // immutable allowlist.
var overlyBroadScopes = map[string]bool{
	"/":      true,
	"/home":  true,
	"/Users": true,
}

// ValidateGuardBypassFlags exhaustively validates the (scope, target, reason)
// triple plus the global confirmation.
//
// Validation rules:
//   - scope MUST be in {file, dir, repo, global}.
//   - When scope=global, target MUST be the literal "global" AND the
//     --i-understand-this-is-global flag MUST be set.
//   - When scope in {file, dir}, target MUST be an absolute path AND
//     MUST NOT be one of the overly-broad system roots ("/", "/home", "/Users").
//   - reason MUST be non-empty (non-whitespace).
func ValidateGuardBypassFlags(scope, target, reason string, iUnderstandGlobal bool) error {
	if scope == "" {
		return fmt.Errorf("guard bypass add: --scope is required (one of: file, dir, repo, global)")
	}
	if !validBypassScopes[scope] {
		return fmt.Errorf("guard bypass add: --scope %q invalid; want one of: file, dir, repo, global", scope)
	}
	if target == "" {
		return fmt.Errorf("guard bypass add: --target is required")
	}
	if guard.IsWhitespacesOnly(reason) {
		return fmt.Errorf("guard bypass add: --reason is required and must not be empty or whitespace-only")
	}

	switch scope {
	case "global":
		if target != "global" {
			return fmt.Errorf(
				"guard bypass add: --scope global requires --target=global literal (got %q)",
				target,
			)
		}
		if !iUnderstandGlobal {
			return fmt.Errorf(
				"guard bypass add: --scope global requires --i-understand-this-is-global " +
					"(this whitelists every repo on every machine)",
			)
		}
	case "dir", "file":
		if !filepath.IsAbs(target) {
			return fmt.Errorf("guard bypass add: --scope %s requires an absolute --target path (got %q)", scope, target)
		}
		if overlyBroadScopes[target] {
			return fmt.Errorf(
				"guard bypass add: --scope %s --target %q would whitelist too broadly; pick a deeper directory",
				scope, target,
			)
		}
	}
	return nil
}

// runGuardBypassList lists active bypass rules.
func runGuardBypassList(args []string) error {
	fs := flag.NewFlagSet("guard bypass list", flag.ContinueOnError)
	scopeFlag := fs.String("scope", "", "filter by scope (optional)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("guard bypass list: %w", err)
	}

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, _ := guard.OpenPool(ctx, dbURL)
	if pool == nil {
		return fmt.Errorf("guard bypass list: database unavailable (set DATABASE_URL)")
	}
	store := guard.NewStore(pool)

	bypasses, err := store.ListBypasses(ctx, *scopeFlag)
	if err != nil {
		return fmt.Errorf("guard bypass list: %w", err)
	}

	if len(bypasses) == 0 {
		fmt.Println("no active bypasses")
		return nil
	}

	fmt.Printf("%-36s  %-6s  %-30s  %-10s  %s\n", "ID", "SCOPE", "TARGET", "TOOL", "REASON")
	fmt.Println(strings.Repeat("-", 110))
	for _, b := range bypasses {
		toolDisplay := "(all)"
		if b.ToolName != nil {
			toolDisplay = *b.ToolName
		}
		fmt.Printf("%-36s  %-6s  %-30s  %-10s  %s\n",
			b.ID.String(), b.Scope, b.Target, toolDisplay, b.Reason)
	}
	return nil
}

// runGuardBypassRevoke revokes a bypass by ID.
func runGuardBypassRevoke(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("guard bypass revoke: missing bypass ID")
	}
	idStr := args[0]
	id, err := uuid.Parse(idStr)
	if err != nil {
		return fmt.Errorf("guard bypass revoke: invalid UUID %q: %w", idStr, err)
	}

	ctx := context.Background()
	dbURL := os.Getenv("DATABASE_URL")
	pool, _ := guard.OpenPool(ctx, dbURL)
	if pool == nil {
		return fmt.Errorf("guard bypass revoke: database unavailable (set DATABASE_URL)")
	}
	store := guard.NewStore(pool)

	if err := store.RevokeBypass(ctx, id); err != nil {
		return fmt.Errorf("guard bypass revoke: %w", err)
	}

	fmt.Printf("bypass %s revoked\n", id)
	return nil
}

// ParseDuration extends time.ParseDuration to accept "d" for days.
func ParseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
		d, err := time.ParseDuration(days + "h")
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q", s)
		}
		return d * 24, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parsing duration %q: %w", s, err)
	}
	return d, nil
}

// sanitizeReason rejects reasons with control characters or ANSI sequences and
// caps length at 500 runes. Returns the sanitized string or an error.
// This prevents terminal escape injection in `wbt guard bypass list` output.
func sanitizeReason(r string) (string, error) {
	if len([]rune(r)) > 500 {
		return "", fmt.Errorf("--reason exceeds 500 rune limit")
	}
	for _, c := range r {
		if c < 0x20 && c != '\t' {
			return "", fmt.Errorf("--reason contains disallowed control character 0x%02x", c)
		}
	}
	cleaned := ansiEscapeRe.ReplaceAllString(r, "")
	if strings.TrimSpace(cleaned) == "" {
		return "", fmt.Errorf("--reason is empty after stripping control characters")
	}
	return cleaned, nil
}

// currentUser returns the current OS user name.
// Uses os/user.Current() for accuracy; falls back to env vars only if that fails.
func currentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "unknown"
}
