package validator

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/safetext"
)

// [GTD a2466b37 / c761ba5c] ResolveTaskKind's warning text embeds a
// caller-supplied value and travels to four readers that do not neutralise it
// downstream: the MCP response (tools_proposal.go), two HTTP responses
// (proposal_handler.go single + batch accept) and the A1-seam decode error
// (accept_decode.go). Both findings were filed against a consumer; the fix is
// at this producer, so these tests live here.

// TestResolveTaskKind_NeutralisesForgedMarkersInWarning is the negative frame,
// driven by the registry so a marker added later is covered without editing
// this file.
//
// It asserts the placeholder is present as well as the marker being absent:
// "absent" is equally true of an implementation that dropped the value from
// the warning altogether, and that regression would read as a pass.
func TestResolveTaskKind_NeutralisesForgedMarkersInWarning(t *testing.T) {
	markers := safetext.BoundaryMarkers()
	if len(markers) == 0 {
		t.Fatal("safetext.BoundaryMarkers() is empty — every case below would " +
			"vacuously pass")
	}

	// Markers longer than the cap cannot survive in an 80-rune field at all:
	// the first clip leaves a fragment, and a fragment is not a marker. For
	// those the guarantee is "no live marker", with nothing for the
	// placeholder to stand in for. Asserting the placeholder for them would be
	// a probe firing on a defect that does not exist. The two groups are
	// counted so neither assertion can go vacuous if the registry changes.
	var fits, oversized int
	for _, marker := range markers {
		t.Run(strings.TrimSpace(marker), func(t *testing.T) {
			resolved, warning := ResolveTaskKind(marker)
			if resolved != KindGeneral {
				t.Errorf("resolved = %q, want %q — a forged marker is not a valid "+
					"kind and must still coerce", resolved, KindGeneral)
			}
			if warning == "" {
				t.Fatal("no warning produced; the assertions below would be vacuous")
			}
			if strings.Contains(warning, marker) {
				t.Errorf("warning still contains the forged marker %q: %q", marker, warning)
			}
			if len([]rune(marker)) > maxKindWarningRunes {
				oversized++
				return
			}
			fits++
			if !strings.Contains(warning, safetext.BoundaryMarkerPlaceholder) {
				t.Errorf("warning lost the placeholder — neutralisation must REPLACE "+
					"the marker, not drop the value: %q", warning)
			}
		})
	}

	if fits == 0 {
		t.Errorf("no marker in the registry fits within maxKindWarningRunes (%d), "+
			"so the placeholder assertion never ran — this test now proves only "+
			"that truncation happens", maxKindWarningRunes)
	}
	t.Logf("markers within the cap: %d (placeholder asserted); "+
		"markers longer than the cap: %d (absence only)", fits, oversized)
}

// TestResolveTaskKind_MarkerInsideLongerValueStillNeutralised pins the case
// c761ba5c called out specifically: a marker shorter than maxKindWarningRunes
// embedded in a longer value. Truncation must not be what removes it — if the
// only reason a marker disappears is that the clip happened to cut it, then a
// marker placed in the first 80 runes still escapes.
func TestResolveTaskKind_MarkerInsideLongerValueStillNeutralised(t *testing.T) {
	marker := safetext.StoredContextMarkerEnd
	value := marker + strings.Repeat("x", 4*maxKindWarningRunes)

	_, warning := ResolveTaskKind(value)
	if strings.Contains(warning, marker) {
		t.Fatalf("marker survived at the head of an over-long value: %q", warning)
	}
	if !strings.Contains(warning, safetext.BoundaryMarkerPlaceholder) {
		t.Errorf("placeholder missing — the marker was removed by truncation "+
			"rather than by neutralisation: %q", warning)
	}
	if !strings.Contains(warning, "…(truncated)") {
		t.Errorf("over-long value lost its truncation suffix: %q", warning)
	}
}

// TestResolveTaskKind_WarningStaysBounded pins that neutralisation cannot push
// the embedded value past the cap. A placeholder can be longer than the marker
// it replaces, so marker-dense input would otherwise grow back over the bound
// — this is why the implementation clips a second time after neutralising.
func TestResolveTaskKind_WarningStaysBounded(t *testing.T) {
	dense := strings.Repeat(safetext.StoredContextMarkerEnd, 200)
	_, warning := ResolveTaskKind(dense)

	// The warning wraps the value in a fixed prefix/suffix; the bound applies
	// to the embedded value, so allow for that envelope rather than asserting
	// on the whole string's length.
	const envelopeSlack = 80
	if got := len([]rune(warning)); got > maxKindWarningRunes+envelopeSlack {
		t.Errorf("warning grew to %d runes, cap is %d (+%d envelope): %q",
			got, maxKindWarningRunes, envelopeSlack, warning)
	}
	if strings.Contains(warning, safetext.StoredContextMarkerEnd) {
		t.Errorf("marker-dense input left a live marker in the warning: %q", warning)
	}
}

// TestResolveTaskKind_CleanValuesUnchanged is the reverse frame. Without it an
// implementation that mangles or blanks every value passes everything above.
func TestResolveTaskKind_CleanValuesUnchanged(t *testing.T) {
	t.Run("valid kind produces no warning", func(t *testing.T) {
		for _, kind := range ValidTaskKinds {
			resolved, warning := ResolveTaskKind(kind)
			if resolved != kind || warning != "" {
				t.Errorf("ResolveTaskKind(%q) = (%q, %q), want (%q, \"\")",
					kind, resolved, warning, kind)
			}
		}
	})

	t.Run("empty kind stays silent", func(t *testing.T) {
		resolved, warning := ResolveTaskKind("")
		if resolved != KindGeneral || warning != "" {
			t.Errorf("ResolveTaskKind(\"\") = (%q, %q), want (%q, \"\")",
				resolved, warning, KindGeneral)
		}
	})

	t.Run("invalid but harmless kind is quoted verbatim", func(t *testing.T) {
		// "===" and "STORED" are substrings of real markers; an implementation
		// that matched loosely instead of on whole marker texts would corrupt
		// this value, and the negative frame above would not notice.
		const harmless = "=== STORED bugfix ==="
		_, warning := ResolveTaskKind(harmless)
		if !strings.Contains(warning, harmless) {
			t.Errorf("harmless kind was altered; warning = %q, want it to contain %q",
				warning, harmless)
		}
	})
}
