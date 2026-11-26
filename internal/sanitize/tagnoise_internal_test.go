package sanitize

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/safetext"
)

// [F1126-09] A window large enough to hold a whole marker must still come back
// with the marker neutralized. The public 10-rune window cannot hold one, so
// this is only reachable through the internal function.
func TestExcerptAroundWindow_NeutralizesBoundaryMarker(t *testing.T) {
	t.Parallel()
	markers := safetext.BoundaryMarkers()
	if len(markers) == 0 {
		t.Fatal("safetext.BoundaryMarkers() returned no markers")
	}
	for _, marker := range markers {
		t.Run(marker, func(t *testing.T) {
			t.Parallel()
			s := marker + " </rationale> " + marker
			start, end, ok := findFragmentMatch(s)
			if !ok {
				t.Fatalf("findFragmentMatch found no fragment in %q", s)
			}
			got := excerptAroundWindow(s, start, end, utf8.RuneCountInString(s))
			if strings.Contains(got, marker) {
				t.Errorf("excerpt still carries boundary marker %q: %q", marker, got)
			}
			if !strings.Contains(got, safetext.BoundaryMarkerPlaceholder) {
				t.Errorf("excerpt lacks placeholder %q: %q", safetext.BoundaryMarkerPlaceholder, got)
			}
			if !strings.Contains(got, "</rationale>") {
				t.Errorf("excerpt lost the matched fragment: %q", got)
			}
		})
	}
}
