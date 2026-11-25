//go:build !integration

package mcp

import (
	"strings"
	"testing"
	"unicode/utf8"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// [F1124-12] Caller-supplied text echoed back in tool errors must go through
// clipSafe (truncate + neutralise boundary markers). Each test feeds a forged
// end-marker followed by a long tail and asserts the marker is gone, the
// placeholder is present and the echoed text is bounded; the second subtest
// pins that a short legitimate value is echoed byte-identically.

// echoClipHostile is a forged boundary marker followed by 5000 runes.
func echoClipHostile() string {
	return storedContextMarkerEnd + strings.Repeat("a", 5000)
}

// assertEchoClipped asserts the hostile-input contract on a tool error result.
func assertEchoClipped(t *testing.T, r *mcpmsg.CallToolResult) {
	t.Helper()
	if !r.IsError {
		t.Fatalf("expected IsError result, got: %s", resultText(r))
	}
	got := resultText(r)
	assertMarkerNeutralized(t, got, storedContextMarkerEnd)
	// cap (<=80) + clip marker + quotes + fixed suffix stays well under 300;
	// an unclipped echo would be >5000.
	if n := utf8.RuneCountInString(got); n > 300 {
		t.Errorf("echo not bounded: %d runes: %.120s...", n, got)
	}
}

func TestGetProject_NotFoundEchoIsClipped(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	t.Run("hostile", func(t *testing.T) {
		t.Parallel()
		r := callTool(t, s, "get_project", map[string]any{"name": echoClipHostile()}, s.handleGetProject)
		assertEchoClipped(t, r)
	})
	t.Run("legit_unchanged", func(t *testing.T) {
		t.Parallel()
		r := callTool(t, s, "get_project", map[string]any{"name": "no-such-project"}, s.handleGetProject)
		if got, want := resultText(r), `project "no-such-project" not found`; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})
}

func TestListReflections_InvalidTypeEchoIsClipped(t *testing.T) {
	t.Parallel()
	s := newReflectionServer(&stubReflectionStore{})

	t.Run("hostile", func(t *testing.T) {
		t.Parallel()
		assertEchoClipped(t, callListReflections(t, s, map[string]any{"type": echoClipHostile()}))
	})
	t.Run("legit_unchanged", func(t *testing.T) {
		t.Parallel()
		r := callListReflections(t, s, map[string]any{"type": "bogus"})
		if got, want := resultText(r), `invalid type "bogus"`; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})
}

func TestGetLatestReflection_InvalidTypeEchoIsClipped(t *testing.T) {
	t.Parallel()
	s := newReflectionServer(&stubReflectionStore{})

	t.Run("hostile", func(t *testing.T) {
		t.Parallel()
		assertEchoClipped(t, callGetLatestReflection(t, s, map[string]any{"type": echoClipHostile()}))
	})
	t.Run("legit_unchanged", func(t *testing.T) {
		t.Parallel()
		r := callGetLatestReflection(t, s, map[string]any{"type": "bogus"})
		if got, want := resultText(r), `invalid type "bogus"`; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})
}

func TestGenerateReflection_InvalidTypeEchoIsClipped(t *testing.T) {
	t.Parallel()
	s := newReflectionServer(&stubReflectionStore{})

	t.Run("hostile", func(t *testing.T) {
		t.Parallel()
		assertEchoClipped(t, callGenerateReflection(t, s, map[string]any{
			"type": echoClipHostile(), "summary": "a summary",
		}))
	})
	t.Run("legit_prefix_unchanged", func(t *testing.T) {
		t.Parallel()
		r := callGenerateReflection(t, s, map[string]any{"type": "bogus", "summary": "a summary"})
		if got := resultText(r); !strings.HasPrefix(got, `invalid type "bogus"; must be one of: `) {
			t.Errorf("text = %q, want prefix %q", got, `invalid type "bogus"; must be one of: `)
		}
	})
}

func TestConfirmProposals_InvalidIDEchoIsClipped(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)

	t.Run("hostile", func(t *testing.T) {
		t.Parallel()
		assertEchoClipped(t, callConfirmProposals(t, s, map[string]any{
			"action": "accept", "ids": []any{echoClipHostile()},
		}))
	})
	t.Run("legit_unchanged", func(t *testing.T) {
		t.Parallel()
		r := callConfirmProposals(t, s, map[string]any{"action": "accept", "ids": []any{"not-a-uuid"}})
		if got, want := resultText(r), `ids[0] "not-a-uuid" is not a valid UUID`; got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	})
}
