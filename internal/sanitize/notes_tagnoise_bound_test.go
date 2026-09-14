package sanitize_test

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
)

// [GTD bc948b70] Notes' 500-rune cap is load-bearing for a guarantee that is
// not stated anywhere near it.
//
// internal/mcp/tools_outcome.go:282 and :580 run ValidateNoTagNoise on text
// that has already been through Notes, and hand the resulting error to
// inputErrorResult → inputErrorText → err.Error(), which reaches the MCP
// caller verbatim. ErrTagNoise's message embeds an excerpt of the offending
// input. So the bound on what a caller can make the server echo back on that
// path is Notes' cap — not tagNoiseDetailMaxRunes, which bounds the two
// tool_errors.go exits instead.
//
// TestNotes already pins the cap itself. What it does not say is that raising
// the cap widens an excerpt: someone lifting it for an unrelated reason would
// update that case and never learn what else moved. This test fails in that
// situation and says why.
//
// It asserts a RELATIONSHIP rather than the number 500 on purpose. Writing the
// number here would make it a second place to update in lockstep, which is the
// drift this whole family of findings is about.
func TestNotes_BoundsTagNoiseExcerptOnTheInputErrorPath(t *testing.T) {
	// A marker-free tag-noise fragment, repeated so the excerpt has far more
	// material available than the cap allows.
	const fragment = "</invoke>"
	oversized := strings.Repeat("filler "+fragment+" ", 4000)

	cleaned := sanitize.Notes(oversized)
	if len(cleaned) >= len(oversized) {
		t.Fatalf("Notes did not shorten a %d-rune input (got %d) — the cap this "+
			"test depends on is gone, and the excerpt below is no longer bounded "+
			"by anything", len([]rune(oversized)), len([]rune(cleaned)))
	}

	err := sanitize.ValidateNoTagNoise(cleaned)
	if err == nil {
		t.Fatal("cleaned text no longer trips ValidateNoTagNoise; the fixture " +
			"stopped exercising the path this test is about")
	}

	// The error text is what inputErrorText echoes. It must not be able to
	// carry more than the cleaned input it describes, plus the message's own
	// fixed wording — if it can, the excerpt is drawing on something other
	// than the capped value.
	const wordingSlack = 200
	if got, limit := len([]rune(err.Error())), len([]rune(cleaned))+wordingSlack; got > limit {
		t.Errorf("ErrTagNoise text is %d runes for a %d-rune input (limit %d): %q\n"+
			"The caller-facing excerpt on the tools_outcome path is bounded by "+
			"Notes' cap. If that cap was just raised or removed, the excerpt "+
			"grew with it and internal/mcp needs its own clip on this path.",
			got, len([]rune(cleaned)), limit, err.Error())
	}
}
