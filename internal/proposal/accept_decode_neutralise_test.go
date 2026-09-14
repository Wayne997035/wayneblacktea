package proposal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/safetext"
)

// [GTD c761ba5c] DecodeTaskParams is the A1-seam call site of
// validator.ResolveTaskKind. The finding was filed here because the kind
// warning reached the strict-mode error text verbatim, and internal/proposal
// could not reach the neutraliser while it was package-private to internal/mcp.
//
// The fix lives at the producer (validator.kindForWarning), so this file's job
// is to pin the wiring, not the algorithm: if this seam ever stops going
// through ResolveTaskKind — the exact regression accept_decode.go's "stay in
// lockstep with the other three call sites" comment warns about — these tests
// go red even though validator's own tests stay green.

func taskPayloadJSON(t *testing.T, suggestedKind string) []byte {
	t.Helper()
	b, err := json.Marshal(TaskPayload{
		Title:         "a task title",
		SourceTool:    "test",
		SuggestedKind: suggestedKind,
		// Long enough for the vagueness check, and carrying the three tokens
		// CheckKindFields wants from a fix-pr description (branch:,
		// acceptance:, a file:line). The reverse frame below asserts a VALID
		// kind produces no kind warning, so the fixture must not trip the
		// unrelated vagueness rules and make that assertion unreachable.
		Description: "branch: fix/example\n" +
			"acceptance: the named test goes red when the guard is reverted\n" +
			"anchor: internal/proposal/accept_decode.go:243",
	})
	if err != nil {
		t.Fatalf("marshal TaskPayload fixture: %v", err)
	}
	return b
}

// TestDecodeTaskParams_StrictErrorNeutralisesForgedKind is the finding's
// acceptance verbatim: a marker shorter than maxKindWarningRunes, sent through
// the strict path, must leave the error text without the marker and with the
// placeholder.
func TestDecodeTaskParams_StrictErrorNeutralisesForgedKind(t *testing.T) {
	marker := safetext.StoredContextMarkerEnd
	if got := len([]rune(marker)); got >= 80 {
		t.Fatalf("fixture assumption broken: marker is %d runes, the finding "+
			"specified one shorter than the 80-rune warning cap so that "+
			"truncation is not what removes it", got)
	}

	_, warnings, err := DecodeTaskParams(taskPayloadJSON(t, marker), true)
	if err == nil {
		t.Fatal("strict mode accepted an invalid kind; the error text this test " +
			"inspects was never produced")
	}

	for _, subject := range []struct{ name, val string }{
		{"error text", err.Error()},
		{"warnings joined", strings.Join(warnings, " | ")},
	} {
		if strings.Contains(subject.val, marker) {
			t.Errorf("%s still contains the forged marker %q: %q",
				subject.name, marker, subject.val)
		}
		if !strings.Contains(subject.val, safetext.BoundaryMarkerPlaceholder) {
			t.Errorf("%s lost the placeholder — the seam is no longer going "+
				"through ResolveTaskKind, or neutralisation was dropped: %q",
				subject.name, subject.val)
		}
	}
}

// TestDecodeTaskParams_WarnModeNeutralisesForgedKind covers the non-strict
// path: the warning is returned to the caller instead of becoming an error,
// and it carries the same text, so it needs the same guarantee.
func TestDecodeTaskParams_WarnModeNeutralisesForgedKind(t *testing.T) {
	marker := safetext.StoredContextMarkerEnd

	params, warnings, err := DecodeTaskParams(taskPayloadJSON(t, marker), false)
	if err != nil {
		t.Fatalf("warn mode should not error: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("no warnings returned; the assertions below would be vacuous")
	}
	if params.Kind != "general" {
		t.Errorf("Kind = %q, want the coerced default", params.Kind)
	}

	joined := strings.Join(warnings, " | ")
	if strings.Contains(joined, marker) {
		t.Errorf("warning still contains the forged marker %q: %q", marker, joined)
	}
	if !strings.Contains(joined, safetext.BoundaryMarkerPlaceholder) {
		t.Errorf("warning lost the placeholder: %q", joined)
	}
}

// TestDecodeTaskParams_ValidKindStaysSilent is the reverse frame — an
// implementation that warns about everything would pass both tests above.
func TestDecodeTaskParams_ValidKindStaysSilent(t *testing.T) {
	params, warnings, err := DecodeTaskParams(taskPayloadJSON(t, "fix-pr"), true)
	if err != nil {
		t.Fatalf("a valid kind must not fail strict mode: %v", err)
	}
	if params.Kind != "fix-pr" {
		t.Errorf("Kind = %q, want fix-pr — a valid kind must pass through", params.Kind)
	}
	for _, w := range warnings {
		if strings.Contains(w, "is not a valid task kind") {
			t.Errorf("valid kind produced a kind warning: %q", w)
		}
	}
}
