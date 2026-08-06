package scheduler

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// IsPGOnlyJob capability-contract lookup
// ---------------------------------------------------------------------------

// TestIsPGOnlyJob_KnownJobs asserts the code-level capability contract for
// the 3 documented Postgres-only prune jobs: each must resolve with ok=true
// and a non-empty reason. This is the "queryable in code, not just a
// comment" requirement — a test (or an operator tool) can call IsPGOnlyJob
// directly rather than grepping source comments.
func TestIsPGOnlyJob_KnownJobs(t *testing.T) {
	for _, name := range []string{
		"daily-discipline-prune",
		"daily-guard-prune",
		"daily-pending-proposals-prune",
	} {
		reason, ok := IsPGOnlyJob(name)
		if !ok {
			t.Errorf("IsPGOnlyJob(%q): ok = false, want true", name)
		}
		if reason == "" {
			t.Errorf("IsPGOnlyJob(%q): reason is empty, want a design rationale", name)
		}
	}
}

// TestIsPGOnlyJob_DualBackendJobsAreNotListed asserts the 4 cognitive jobs
// with SQLite parity (job 2/3/4/5) are NOT in the PG-only set — a
// regression here would mean a genuinely dual-backend job got miscategorised
// as unsupported.
func TestIsPGOnlyJob_DualBackendJobsAreNotListed(t *testing.T) {
	for _, name := range []string{
		"cognitive-stuck-task-detection",
		"cognitive-decision-outcome-review",
		"cognitive-knowledge-to-skill-candidate",
		"cognitive-proposal-cleanup",
		"unknown-job-name",
	} {
		if _, ok := IsPGOnlyJob(name); ok {
			t.Errorf("IsPGOnlyJob(%q): ok = true, want false (not a documented PG-only job)", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Runtime behaviour: explicit "unsupported by design" log, not silent no-op
// ---------------------------------------------------------------------------

// captureSlogWarnPlus swaps slog's default handler for a buffer-backed one
// (Info level and above, since the messages under test are Info-level) and
// returns a restore func. Mirrors the pattern already used by
// TestRunDailyPendingProposalsPrune_PartialSuccess_LogsWarn
// (pending_proposals_prune_pg_test.go) but at Info level since these are
// deliberate-skip messages, not warnings.
func captureSlogInfoPlus(t *testing.T) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return buf
}

// TestRunDailyDisciplinePrune_NilPool_LogsUnsupportedByDesign asserts that,
// with no Postgres pool wired, the job emits a distinct "unsupported on this
// backend (by design)" log line carrying the IsPGOnlyJob reason — NOT a
// generic silent return. This is the test-level proof that the capability
// contract is enforced at runtime, not just documented.
func TestRunDailyDisciplinePrune_NilPool_LogsUnsupportedByDesign(t *testing.T) {
	buf := captureSlogInfoPlus(t)
	sc := &Scheduler{disciplinePool: nil}
	sc.runDailyDisciplinePrune()

	out := buf.String()
	if !strings.Contains(out, "unsupported on this backend (by design)") {
		t.Errorf("expected explicit unsupported-by-design log, got:\n%s", out)
	}
	wantReason, _ := IsPGOnlyJob("daily-discipline-prune")
	if !strings.Contains(out, wantReason) {
		t.Errorf("expected log to carry the IsPGOnlyJob reason %q, got:\n%s", wantReason, out)
	}
}

func TestRunDailyGuardPrune_NilPool_LogsUnsupportedByDesign(t *testing.T) {
	buf := captureSlogInfoPlus(t)
	sc := &Scheduler{disciplinePool: nil}
	sc.runDailyGuardPrune()

	out := buf.String()
	if !strings.Contains(out, "unsupported on this backend (by design)") {
		t.Errorf("expected explicit unsupported-by-design log, got:\n%s", out)
	}
	wantReason, _ := IsPGOnlyJob("daily-guard-prune")
	if !strings.Contains(out, wantReason) {
		t.Errorf("expected log to carry the IsPGOnlyJob reason %q, got:\n%s", wantReason, out)
	}
}

func TestRunDailyPendingProposalsPrune_NilPool_LogsUnsupportedByDesign(t *testing.T) {
	buf := captureSlogInfoPlus(t)
	sc := &Scheduler{disciplinePool: nil}
	sc.runDailyPendingProposalsPrune()

	out := buf.String()
	if !strings.Contains(out, "unsupported on this backend (by design)") {
		t.Errorf("expected explicit unsupported-by-design log, got:\n%s", out)
	}
	wantReason, _ := IsPGOnlyJob("daily-pending-proposals-prune")
	if !strings.Contains(out, wantReason) {
		t.Errorf("expected log to carry the IsPGOnlyJob reason %q, got:\n%s", wantReason, out)
	}
}
