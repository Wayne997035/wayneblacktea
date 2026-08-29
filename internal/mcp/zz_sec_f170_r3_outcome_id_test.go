//go:build !integration

package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/google/uuid"
)

// TestF170SECR301_OutcomeIDIsBoundedServerSide pins the storage half of
// [F170-SEC-R3-01]. The read-time walker (zz_sec_f170_r3_skill_examples_test.go)
// makes rendering safe; this is about what gets written: examples is
// append-only (`examples || $3::jsonb` on Postgres), and outcome_id had no cap
// at any layer, so a single caller could grow a skill row without limit and
// spend a later session's context window reading it back.
//
// The assertion is on what the STORE received, not on the schema: mcp-go does
// not enforce schema constraints server-side (see hasBoolArg's comment in
// tools_skill.go), so asserting on mcp.MaxLength would be asserting on a
// request no hostile client is obliged to honour.
//
// Note what is deliberately NOT asserted: that outcome_id parses as a UUID.
// It is documented as a free reference (task ID, decision ID, no FK), so
// rejecting non-UUID input would be a breaking contract change. Bounding it
// is not.
//
// Build tag: stubSkillStore and callUpdateSkillFromOutcome live in
// tools_skill_test.go, which is `!integration`; without the same tag this file
// fails to typecheck under `-tags integration` — and a package that fails to
// typecheck degrades every other linter's view of it, which is how this was
// found.
func TestF170SECR301_OutcomeIDIsBoundedServerSide(t *testing.T) {
	oversized := strings.Repeat("A", skillOutcomeIDMaxRunes*3)

	store := &stubSkillStore{returnSkill: &skill.Skill{Name: "bounded probe"}}
	s := newSkillServer(store)

	r := callUpdateSkillFromOutcome(t, s, map[string]any{
		"skill_id":   uuid.NewString(),
		"outcome_id": oversized,
		"success":    true,
	})
	if r.IsError {
		t.Fatalf("update_skill_from_outcome errored, so nothing reached the store "+
			"and the clamp below is untested: %s", resultText(r))
	}
	if !store.updateFromOutcomeCalled {
		t.Fatal("store was never called; the assertions below would pass vacuously")
	}

	got := store.lastUpdateFromOutcomeParams.OutcomeID

	// The bound is cap + clipMarker, not cap: clipRunes appends clipMarker when
	// it truncates. Deriving it from the helpers rather than hardcoding 201
	// keeps this correct if the marker's width ever changes.
	limit := skillOutcomeIDMaxRunes + utf8.RuneCountInString(clipMarker)
	if n := utf8.RuneCountInString(got); n > limit {
		t.Errorf("outcome_id reached the store at %d runes; cap %d + marker = %d",
			n, skillOutcomeIDMaxRunes, limit)
	}
	if got == oversized {
		t.Error("outcome_id reached the store unclamped — examples is append-only, " +
			"so this is the unbounded-growth half of the finding")
	}
}
