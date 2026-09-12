package mcp

import (
	"os"
	"strings"
	"testing"
)

// TestPlanTestCommentsAreNotStale pins AC-16 / F0911-08: two doc comments
// that F0911-04 (SQLite tag-noise parity) made false.
//
// Before F0911-04, sqlite's decision.Log/LogTx did not validate tag-noise,
// so materializePlanSQLite could not fail a specific decision through the
// real SQLite store, and the Postgres-only equivalent could accurately say
// "SQLite's Log doesn't have this". After F0911-04 both claims are false —
// this test fails if either needle string still appears verbatim, so an
// edit that reintroduces the claim (instead of correcting it, as this
// round did) is caught immediately rather than silently going stale again.
//
// The needles deliberately do NOT live in either file this test reads —
// see F0911-08's own note in the spec: putting them there would make this
// test permanently red regardless of whether tools_plan_test.go /
// tools_plan_pg_test.go were actually fixed.
func TestPlanTestCommentsAreNotStale(t *testing.T) {
	checks := []struct {
		file   string
		needle string
	}{
		{"tools_plan_test.go", "is no confirm_plan-reachable input that fails a specific decision"},
		{"tools_plan_pg_test.go", "SQLite's Log doesn't have this"},
	}
	for _, c := range checks {
		body, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("reading %s: %v", c.file, err)
		}
		if strings.Contains(string(body), c.needle) {
			t.Errorf("%s still contains the stale claim %q — F0911-04 made this false; "+
				"correct the comment (don't just delete this check)", c.file, c.needle)
		}
	}
}
