package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// repoListItemFieldExemptions maps a db.Repo field name to why repoListItem
// intentionally does not carry it. Empty today: db.Repo's 14 fields all have
// a same-named counterpart in repoListItem (plus 8 synthetic *_truncated
// fields with no db.Repo source). Adding an entry here is allowed when a
// future db.Repo field is genuinely not meant for the list view, but it must
// carry a reason, same discipline as wrapUntrustedFieldExemptions
// (u13_wrap_field_coverage_test.go). [F1003-05]
var repoListItemFieldExemptions = map[string]string{}

// fieldParityViolations is the drift gate's comparison logic: it returns the
// names of src's exported fields that have neither a same-named field in dst
// nor a valid (non-empty-reason) exemption. Factored out of
// TestRepoListItem_TracksDbRepoFields so the synthetic-probe test below
// drives the EXACT SAME logic against throwaway types instead of the real
// db.Repo/repoListItem (which can't be mutated for a test) — two separate
// implementations of the same walk could drift from each other, which is
// exactly the failure mode this gate exists to catch in the first place.
func fieldParityViolations(src, dst reflect.Type, exemptions map[string]string) []string {
	dstFields := map[string]bool{}
	for i := 0; i < dst.NumField(); i++ {
		dstFields[dst.Field(i).Name] = true
	}
	var violations []string
	for i := 0; i < src.NumField(); i++ {
		name := src.Field(i).Name
		if dstFields[name] {
			continue
		}
		if reason, exempt := exemptions[name]; exempt && strings.TrimSpace(reason) != "" {
			continue
		}
		violations = append(violations, name)
	}
	return violations
}

// TestRepoListItem_TracksDbRepoFields is [F1003-05]'s drift gate: every
// db.Repo field must have a same-named field in repoListItem, or a named,
// non-empty-reason exemption. Without this, a future db.Repo field added
// without a matching toRepoListItem projection would compile fine (Go does
// not require every source field to be consumed) and silently never reach
// list_active_repos' response.
func TestRepoListItem_TracksDbRepoFields(t *testing.T) {
	t.Parallel()
	srcType := reflect.TypeOf(db.Repo{})
	dstType := reflect.TypeOf(repoListItem{})
	for _, name := range fieldParityViolations(srcType, dstType, repoListItemFieldExemptions) {
		t.Errorf("db.Repo.%s has no matching field in repoListItem and no exemption — "+
			"either add it to repoListItem/toRepoListItem's projection, or add a named "+
			"exemption to repoListItemFieldExemptions explaining why the list view "+
			"intentionally omits it", name)
	}
	// Separately flag any exemption entry that exists but carries an empty
	// or whitespace-only reason — fieldParityViolations treats that the same
	// as "no exemption" (so the field above already fails loudly), but this
	// catches the exemption-table hygiene issue by name too.
	for name, reason := range repoListItemFieldExemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("repoListItemFieldExemptions[%q]: empty reason", name)
		}
	}
}

// probeSrc / probeDst are throwaway types for
// TestFieldParityViolations_FailsClosedOnMissingField — [F1003-05]'s proof
// that the gate actually fires, mirroring
// TestF170_11_UnforgeableFieldWithoutDispositionFailsClosed's role for the
// sanitisation gate.
type probeSrc struct {
	A string
	B string
}

type probeDst struct {
	A string
}

func TestFieldParityViolations_FailsClosedOnMissingField(t *testing.T) {
	t.Parallel()
	srcType := reflect.TypeOf(probeSrc{})
	dstType := reflect.TypeOf(probeDst{})

	// No exemption for B: exactly one violation, naming B.
	got := fieldParityViolations(srcType, dstType, map[string]string{})
	if len(got) != 1 || got[0] != "B" {
		t.Fatalf("violations = %v, want exactly [\"B\"] — the walker isn't comparing field sets", got)
	}

	// A populated-reason exemption suppresses it.
	got = fieldParityViolations(srcType, dstType, map[string]string{"B": "test fixture, not a real omission"})
	if len(got) != 0 {
		t.Errorf("violations = %v, want none once B is exempted with a reason", got)
	}

	// An empty-reason exemption must NOT suppress it — an exemption that
	// costs nothing to add is not a control.
	got = fieldParityViolations(srcType, dstType, map[string]string{"B": ""})
	if len(got) != 1 || got[0] != "B" {
		t.Errorf("violations = %v, want [\"B\"] — an empty-reason exemption must not suppress the violation", got)
	}

	// A whitespace-only reason must likewise not suppress it.
	got = fieldParityViolations(srcType, dstType, map[string]string{"B": "   "})
	if len(got) != 1 || got[0] != "B" {
		t.Errorf("violations = %v, want [\"B\"] — a whitespace-only reason must not suppress the violation", got)
	}
}
