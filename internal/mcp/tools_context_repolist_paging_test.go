package mcp

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
)

// TestB9B47C5A9_ListActiveReposDescriptionSaysPageUntilHasMoreIsFalse is [GTD
// 9b47c5a9]'s acceptance criterion.
//
// [F170-01] deliberately kept the row cap out of the shared SQL, reasoning that
// "reconcile would skip repos with no error — which is a worse bug than the one
// being fixed". That protected `wbt reconcile`, which goes through the HTTP
// handler. auto-dev-loop's Step 0 drain goes through list_active_repos over
// MCP, where the cap does live — so the same failure survived on the other
// path: one call, twenty repos, every repo after that never reconciled, and
// has_more sitting in a response nobody read.
//
// Asserting on the registered description rather than on source text: the
// description is what an agent is handed, and it is the only place this
// instruction can reach the caller that broke. The assertions name the two
// operative facts — that a call is a page, and that the stop condition is
// has_more being false — not the wording around them, so ordinary edits to the
// rest of the description do not trip it.
func TestB9B47C5A9_ListActiveReposDescriptionSaysPageUntilHasMoreIsFalse(t *testing.T) {
	t.Parallel()
	ms := server.NewMCPServer("test", "0.0.0")
	(&Server{}).registerContextTools(ms)

	entry, ok := ms.ListTools()["list_active_repos"]
	if !ok {
		t.Fatal("list_active_repos is not registered")
	}
	desc := entry.Tool.Description

	if !strings.Contains(desc, "has_more is false") {
		t.Errorf("description never states the stop condition for enumerating every repo. "+
			"Naming has_more without saying when to stop is what let a single call be read "+
			"as the whole workspace: %q", desc)
	}
	for _, want := range []string{"ONE CALL RETURNS ONE PAGE", "NOT THE WORKSPACE"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description does not carry %q — an agent reading it still has no "+
				"reason to believe one call is incomplete: %q", want, desc)
		}
	}
}
