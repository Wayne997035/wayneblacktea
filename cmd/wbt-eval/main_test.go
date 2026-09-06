package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/evals"
)

// TestValidateFlags exercises the exhaustive client-side flag allowlist
// (backend-security-design.md §5.2): every rejection path must fail closed
// with a message naming the offending flag, and every legitimate combination
// must pass. Categories are drawn from evals.ProviderEvalCategories (the real
// registry) rather than hardcoded so this test tracks the source of truth.
func TestValidateFlags(t *testing.T) {
	// Pick a genuine category from the registry for the happy path; if the
	// registry is ever emptied this guards against a vacuous "all"-only test.
	var oneCategory string
	if len(evals.ProviderEvalCategories) > 0 {
		oneCategory = evals.ProviderEvalCategories[0]
	} else {
		oneCategory = evals.CategoryAll
	}

	tests := []struct {
		name              string
		provider          string
		model             string
		category          string
		wantErr           bool
		wantErrSubstrings []string // all must appear in the error
	}{
		{
			name:     "all valid defaults",
			provider: "claude", model: "claude-haiku-4-5", category: evals.CategoryAll,
			wantErr: false,
		},
		{
			name:     "valid specific category",
			provider: "claude", model: "claude-haiku-4-5", category: oneCategory,
			wantErr: false,
		},
		{
			name:     "unsupported provider rejected with flag name",
			provider: "openai", model: "gpt-4", category: evals.CategoryAll,
			wantErr: true, wantErrSubstrings: []string{"--provider", "openai"},
		},
		{
			name:     "empty model rejected",
			provider: "claude", model: "", category: evals.CategoryAll,
			wantErr: true, wantErrSubstrings: []string{"--model"},
		},
		{
			name:     "whitespace-only model rejected",
			provider: "claude", model: "   ", category: evals.CategoryAll,
			wantErr: true, wantErrSubstrings: []string{"--model"},
		},
		{
			name:     "unknown category rejected with flag name",
			provider: "claude", model: "claude-haiku-4-5", category: "not-a-category",
			wantErr: true, wantErrSubstrings: []string{"--category", "not-a-category"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFlags(tc.provider, tc.model, tc.category)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFlags(%q,%q,%q) = nil, want error",
					tc.provider, tc.model, tc.category)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFlags(%q,%q,%q) = %v, want nil",
					tc.provider, tc.model, tc.category, err)
			}
			if err != nil {
				for _, want := range tc.wantErrSubstrings {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q missing substring %q", err.Error(), want)
					}
				}
			}
		})
	}
}

// TestRunEval_NoAPIKey exercises the "no ANTHROPIC_API_KEY" branch of the
// stdout/stderr contract documented in main.go's package doc: exit 0, stdout
// completely empty (never a non-JSON skip line mixed into what a caller
// might decode as JSON), and a stderr diagnostic naming the reason. Uses
// t.Setenv so the env var mutation is test-scoped and does not leak into
// other tests running in the same process/package.
func TestRunEval_NoAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	var stdout, stderr bytes.Buffer
	code := runEval(evals.CategoryAll, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Error("stderr = empty, want a diagnostic explaining the skip")
	}
	if !strings.Contains(stderr.String(), "ANTHROPIC_API_KEY") {
		t.Errorf("stderr = %q, want it to name ANTHROPIC_API_KEY", stderr.String())
	}
}

// TestRunEval_NoAPIKey_UnsetVsEmpty confirms both "unset" and "set to the
// empty string" are treated identically (main.go's LookupEnv doc comment):
// neither can ever authenticate a provider call, so both must fail closed to
// the same skip-with-exit-0 behavior rather than one of them falling through
// to a runtime nil-client panic.
func TestRunEval_NoAPIKey_UnsetVsEmpty(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		// t.Setenv (with a throwaway value) registers a t.Cleanup that
		// restores whatever ANTHROPIC_API_KEY held (or removes it again if
		// it was already absent) once this subtest ends — that's the only
		// way to get automatic restoration from the testing package. The
		// immediate os.Unsetenv right after then puts the var into the
		// actually-unset state this subtest exercises, robust against a
		// developer's shell happening to export a real key.
		t.Setenv("ANTHROPIC_API_KEY", "placeholder")
		_ = os.Unsetenv("ANTHROPIC_API_KEY")

		var stdout, stderr bytes.Buffer
		code := runEval(evals.CategoryAll, &stdout, &stderr)
		if code != 0 || stdout.Len() != 0 {
			t.Errorf("unset key: code=%d stdout=%q, want code=0 stdout=empty", code, stdout.String())
		}
	})

	t.Run("empty string", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")

		var stdout, stderr bytes.Buffer
		code := runEval(evals.CategoryAll, &stdout, &stderr)
		if code != 0 || stdout.Len() != 0 {
			t.Errorf("empty key: code=%d stdout=%q, want code=0 stdout=empty", code, stdout.String())
		}
	})
}

// TestWriteResults_AllPassed_DecodesAsJSON verifies the configured-success
// path: stdout MUST decode cleanly as []evals.EvalResult (the CLI's whole
// stdout contract) and the exit code MUST be 0 when every result passed.
func TestWriteResults_AllPassed_DecodesAsJSON(t *testing.T) {
	results := []evals.EvalResult{
		{CaseID: "case-1", Passed: true},
		{CaseID: "case-2", Passed: true},
	}

	var stdout, stderr bytes.Buffer
	code := writeResults(results, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty on success", stderr.String())
	}

	var decoded []evals.EvalResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout did not decode as []evals.EvalResult: %v\nstdout=%s", err, stdout.String())
	}
	if len(decoded) != len(results) {
		t.Errorf("decoded %d results, want %d", len(decoded), len(results))
	}
}

// TestWriteResults_AnyFailed_ExitsNonZero verifies a single failing result
// among otherwise-passing ones still flips the exit code to 1, and that
// stdout still carries the full, valid JSON array — a failing case must not
// suppress or corrupt the payload a caller needs to inspect which case
// failed.
func TestWriteResults_AnyFailed_ExitsNonZero(t *testing.T) {
	results := []evals.EvalResult{
		{CaseID: "case-1", Passed: true},
		{CaseID: "case-2", Passed: false, Reason: "deliberate test failure"},
		{CaseID: "case-3", Passed: true},
	}

	var stdout, stderr bytes.Buffer
	code := writeResults(results, &stdout, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1 (a result failed)", code)
	}

	var decoded []evals.EvalResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout did not decode as []evals.EvalResult: %v\nstdout=%s", err, stdout.String())
	}
	if len(decoded) != len(results) {
		t.Errorf("decoded %d results, want %d (failure must not truncate payload)", len(decoded), len(results))
	}
}

// TestWriteResults_EmptyResults_ExitsZero is the boundary case: zero results
// (e.g. a category with no wired cases) is not a failure — the loop over an
// empty slice never finds a Passed==false, so this must exit 0, not
// accidentally exit 1 from an off-by-one on an empty range.
func TestWriteResults_EmptyResults_ExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := writeResults(nil, &stdout, &stderr)

	if code != 0 {
		t.Errorf("exit code = %d, want 0 for zero results", code)
	}

	var decoded []evals.EvalResult
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout did not decode as []evals.EvalResult: %v\nstdout=%s", err, stdout.String())
	}
	if len(decoded) != 0 {
		t.Errorf("decoded %d results, want 0", len(decoded))
	}
}
