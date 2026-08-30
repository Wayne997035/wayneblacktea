//go:build !integration

package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/google/uuid"
)

// [SEC171-08] TestSEC171_08_AllFiveCSVArgumentsScreenControlChars pins the
// write half of the SourceAtomIDs finding (r1's numbering: SEC171-08). It
// used to be named after SEC171-01, which is a DIFFERENT, still-OPEN finding
// (the unbounded examples array, deferred to GTD 17f08ba8 — see
// skillOutcomeIDMaxRunes' own comment in tools_skill.go) — grepping
// SEC171-01 returned two green tests for a finding this commit does not
// close. See tools_skill.go:91 and :356 for the matching [SEC171-08]
// anchors this test's assertions are pinning.
//
// extract_skill takes five comma-separated arguments and ran
// validateSkillCSVField on four of them. Measured before the fix: a newline
// was rejected in triggers, steps, failure_modes and verification_checklist,
// and accepted in source_atom_ids — making it the one argument where a forged
// boundary marker could occupy a line of its own in the rendered response.
//
// The test is written as a table over all five rather than as a single case
// for the field that was broken, because the defect was an asymmetry. A test
// that only exercised source_atom_ids would pass just as happily if someone
// later removed the check from a different one.
//
// Mutation proof: delete the source_atom_ids validation from
// handleExtractSkill and the source_atom_ids row goes red while the other four
// stay green.
func TestSEC171_08_AllFiveCSVArgumentsScreenControlChars(t *testing.T) {
	for _, field := range []string{
		"triggers", "steps", "failure_modes", "verification_checklist", "source_atom_ids",
	} {
		t.Run(field, func(t *testing.T) {
			store := &stubSkillStore{returnSkill: &skill.Skill{Name: "csv probe"}}
			s := newSkillServer(store)

			args := map[string]any{
				"name":        "csv probe",
				"description": "checks that every comma-separated argument screens control characters",
				field:         "alpha\nbeta",
			}
			r := callExtractSkill(t, s, args)

			if !r.IsError {
				t.Fatalf("%s accepted a newline; the other CSV arguments reject it, and this is "+
					"the field where that difference let a forged fence own a line", field)
			}
			if !strings.Contains(resultText(r), field) {
				t.Errorf("the rejection should name the offending field, got: %s", resultText(r))
			}
			if store.returnSkill != nil && len(store.lastAddParams.Name) > 0 {
				t.Errorf("%s reached the store despite being rejected", field)
			}
		})
	}
}

// TestF170SECR301_OutcomeIDIsBoundedServerSide pins the storage half of
// [F170-SEC-R3-01]. The read-time walker (zz_sec_f170_r3_skill_examples_test.go)
// makes rendering safe; this is about what gets written: examples is
// append-only (`examples || $3::jsonb` on Postgres), and outcome_id had no cap
// at any layer, so a single caller could grow a skill row without limit and
// spend a later session's context window reading it back.
//
// [F171-06] This bounds the PER-VALUE half only, the same distinction
// skillOutcomeIDMaxRunes' own comment (tools_skill.go) makes in bold: the
// array half — examples' unbounded entry count — is a SEPARATE, still-OPEN
// gap tracked under GTD 17f08ba8, not something this test closes. An earlier
// version of this test's failure message claimed otherwise.
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
		t.Error("outcome_id reached the store unclamped")
	}
}
