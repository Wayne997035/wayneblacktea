// Command wbt-eval is the standalone, provider-backed eval CLI (Phase 6.5).
// It runs the same deterministic graders internal/evals/*_test.go already
// exercises through `go test` / `task check`, but through
// internal/evals.ProviderEvalSuite instead — kept as a separate binary.
// [F0906-24] Its unit tests (this file's _test.go, a pure validateFlags
// allowlist check) ARE wired into build/Taskfile.yml's lint and test targets: they
// never invoke run()/main() and therefore never touch a provider or the
// network. What stays deliberately NOT wired in is test-integration, and —
// regardless of which target runs — actually executing the built binary
// bin/wbt-eval, because future categories will call out to a real LLM
// provider (TODO Phase 6+ in internal/evals/provider_eval.go), and
// provider-backed evals MUST NOT run on every `task check` invocation: they
// cost money and depend on network availability.
//
// Usage:
//
//	wbt-eval [--provider=claude] [--model=claude-haiku-4-5] [--category=all]
//
// Exit codes: 0 when every graded case passes, or when ANTHROPIC_API_KEY is
// unset (see below); 1 when any case fails or a flag/fixture error occurs.
//
// stdout/stderr contract: stdout carries ONLY the JSON result array on a
// successful (configured) run — nothing else is ever written there. All
// diagnostics, including the "no API key, skipping" notice, go to stderr so
// a caller piping stdout through `jq`/`json.Decode` never has to special-case
// a non-JSON line mixed into the payload.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/evals"
)

// allowedProviders is the exhaustive client-side allowlist for --provider
// (backend-security-design.md §5.2: never delegate flag validation to a
// downstream failure). Only "claude" is wired today; a Phase 6+ provider
// gets added here alongside its llm.JSONClient construction.
var allowedProviders = []string{"claude"}

func main() {
	os.Exit(run())
}

func run() int {
	// Route slog to stderr so stdout carries only the JSON result payload
	// (backend-security-design.md §5.1: slog destination control). Go's
	// slog default handler already targets os.Stderr (verified against
	// log/slog's source: defaultHandler delegates to the standard `log`
	// package, whose default writer is os.Stderr) — this call makes that
	// explicit rather than relying on the zero-value default, and caps the
	// level at Warn so routine Info noise doesn't clutter a CLI a human is
	// watching run.
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	provider := flag.String("provider", "claude",
		"LLM provider name for provider-backed eval cases (allowed: "+strings.Join(allowedProviders, ", ")+")")
	model := flag.String("model", "claude-haiku-4-5", "model identifier passed to the provider client")
	category := flag.String("category", evals.CategoryAll,
		"eval category to run: "+evals.CategoryAll+", "+strings.Join(evals.ProviderEvalCategories, ", "))
	flag.Parse()

	if err := validateFlags(*provider, *model, *category); err != nil {
		fmt.Fprintln(os.Stderr, "wbt-eval:", err)
		return 1
	}

	return runEval(*category, os.Stdout, os.Stderr)
}

// runEval executes the configured eval category (once flags are validated)
// and reports the outcome through stdout/stderr, per the package doc's
// stdout/stderr contract. Extracted from run() so both branches — the
// no-key skip and the configured success/failure path — are unit-testable
// against injected writers without depending on flag.Parse()'s
// process-global os.Args/os.Exit side effects.
func runEval(category string, stdout, stderr io.Writer) int {
	// ANTHROPIC_API_KEY is read with LookupEnv (not Getenv) so the "is this
	// key present at all" check is explicit rather than piggybacking on a
	// string-emptiness comparison. Both an unset key (ok == false) and a
	// key explicitly set to the empty string (ok == true, value == "") are
	// treated as "not configured": an empty key could never authenticate
	// anyway, and a deploy that zeroes the var out (e.g. a secret rotator
	// clearing it before writing the real value) must fail closed to
	// skip, not silently attempt a call with a blank key. This CLI's whole
	// purpose is running provider-backed evals; with no usable key there
	// is nothing to run, and that is a deliberate operator choice, not a
	// failure (exit 0, not 1). The key itself is never logged or printed —
	// only whether it is present. The notice goes to stderr (not stdout) so
	// stdout stays empty — a caller decoding stdout as JSON on exit 0 must
	// never have to special-case a non-JSON skip line.
	if key, ok := os.LookupEnv("ANTHROPIC_API_KEY"); !ok || key == "" {
		fmt.Fprintln(stderr, "wbt-eval: ANTHROPIC_API_KEY not set; skipping provider-backed evals")
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	suite := evals.ProviderEvalSuite{Category: category}
	// client is nil: Phase 6.5 wires zero provider-backed cases (see
	// internal/evals/provider_eval.go's package doc, TODO(Phase 6+)), so no
	// llm.JSONClient implementation exists yet to construct here for
	// --provider/--model. Both flags are validated above and kept on the
	// CLI surface so a future phase only has to add client construction,
	// not a new flag.
	results := suite.Run(ctx, nil)

	return writeResults(results, stdout, stderr)
}

// writeResults marshals results as an indented JSON array to stdout and
// returns the CLI exit code: 1 if marshalling fails or any result has
// Passed == false, else 0. Split from runEval so this decision (exit-code
// semantics + the stdout-is-the-only-JSON-writer contract) is unit-testable
// against a synthetic []evals.EvalResult — the real fixture-backed suite is
// designed to always pass every case, so it alone can never deterministically
// exercise the "any failed result -> exit 1" branch.
func writeResults(results []evals.EvalResult, stdout, stderr io.Writer) int {
	out, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "wbt-eval: marshalling results:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(out))

	for _, r := range results {
		if !r.Passed {
			return 1
		}
	}
	return 0
}

// validateFlags is the exhaustive client-side validation gate
// (backend-security-design.md §5.2): every flag value is checked against an
// explicit allowlist here rather than delegated to a downstream failure
// (e.g. a provider client construction panic or a confusing grader error).
func validateFlags(provider, model, category string) error {
	if !slices.Contains(allowedProviders, provider) {
		return fmt.Errorf("--provider %q not supported (allowed: %s)", provider, strings.Join(allowedProviders, ", "))
	}
	if strings.TrimSpace(model) == "" {
		return fmt.Errorf("--model must not be empty")
	}
	if category != evals.CategoryAll && !slices.Contains(evals.ProviderEvalCategories, category) {
		return fmt.Errorf("--category %q not recognized (allowed: %s, %s)",
			category, evals.CategoryAll, strings.Join(evals.ProviderEvalCategories, ", "))
	}
	return nil
}
