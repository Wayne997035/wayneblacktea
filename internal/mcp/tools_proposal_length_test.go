package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// [GTD 795efe59] propose_goal / propose_project took a description of any size:
// the handler marshalled it and the first refusal came from Store.Create's
// payload backstop, after the whole string had been materialised. Worse, the
// accept-time decoders refuse >65,536 — so an oversized description became a
// pending proposal that could never be accepted, and the caller learned that
// later, from a different error, in a different session.
//
// Every assertion below is on what the HANDLER does, and one of them is on
// what the store never sees. mcp.MaxLength on the schema is advisory only
// (mcp-go does not enforce it, and these two tools are registered with raw
// ms.AddTool rather than the typed seam), so a test that only checked the
// schema would be checking a hint.

func callProposeTool(
	t *testing.T,
	h func(context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error),
	args map[string]any,
) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return r
}

func TestProposeGoal_RejectsOversizedFieldsBeforeTheStore(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		args  map[string]any
		field string
	}{
		"description over the text limit": {
			args: map[string]any{
				"title": "ship it", "area": "engineering",
				"description": strings.Repeat("a", proposeTextMaxBytes+1),
			},
			field: "description",
		},
		"title over the short limit": {
			args:  map[string]any{"title": strings.Repeat("a", proposeShortMaxBytes+1), "area": "engineering"},
			field: "title",
		},
		"area over the short limit": {
			args:  map[string]any{"title": "ship it", "area": strings.Repeat("a", proposeShortMaxBytes+1)},
			field: "area",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := &stubProposalStore{}
			res := callProposeTool(t, (&Server{proposal: store}).handleProposeGoal, tc.args)

			if !res.IsError {
				t.Fatal("oversized input was accepted")
			}
			body := resultText(res)
			if !strings.Contains(body, tc.field) {
				t.Errorf("message does not name the offending field %q: %q", tc.field, body)
			}
			// An agent told only "too large" has to guess which field and by
			// how much, and its most likely next move is to re-send.
			if !strings.Contains(body, "byte") {
				t.Errorf("message does not state a byte limit, leaving nothing actionable: %q", body)
			}
			if n := len(store.created); n != 0 {
				t.Errorf("store.Create was called %d time(s); the guard must refuse before the "+
					"payload is marshalled and written", n)
			}
		})
	}
}

func TestProposeProject_RejectsOversizedFieldsBeforeTheStore(t *testing.T) {
	t.Parallel()

	store := &stubProposalStore{}
	res := callProposeTool(t, (&Server{proposal: store}).handleProposeProject, map[string]any{
		"name": "wbt", "title": "ship it", "area": "engineering",
		"description": strings.Repeat("a", proposeTextMaxBytes+1),
	})
	if !res.IsError {
		t.Fatal("oversized description was accepted")
	}
	if n := len(store.created); n != 0 {
		t.Errorf("store.Create was called %d time(s) despite the refusal", n)
	}

	// name is propose_project's own field and has its own cap; propose_goal
	// has no name, so this arm exists only here.
	store2 := &stubProposalStore{}
	res2 := callProposeTool(t, (&Server{proposal: store2}).handleProposeProject, map[string]any{
		"name": strings.Repeat("a", proposeShortMaxBytes+1), "title": "ship it", "area": "engineering",
	})
	if !res2.IsError || !strings.Contains(resultText(res2), "name") {
		t.Errorf("oversized name was not refused by name: IsError=%v %q", res2.IsError, resultText(res2))
	}
}

// TestProposeGoal_ExactlyAtTheLimitIsAccepted distinguishes `>` from `>=`.
// One-over and one-under give the same verdict under both operators, so
// without this case the comparison could be tightened by one byte and nothing
// would notice — the same gap [F982-01] closed for the payload cap.
func TestProposeGoal_ExactlyAtTheLimitIsAccepted(t *testing.T) {
	t.Parallel()

	store := &stubProposalStore{}
	res := callProposeTool(t, (&Server{proposal: store}).handleProposeGoal, map[string]any{
		"title": "ship it", "area": "engineering",
		"description": strings.Repeat("a", proposeTextMaxBytes),
	})
	if res.IsError {
		t.Fatalf("a description of exactly %d bytes was refused: %q",
			proposeTextMaxBytes, resultText(res))
	}
	if len(store.created) != 1 {
		t.Fatalf("store.Create was called %d time(s), want 1", len(store.created))
	}
}

// TestProposeSchemasDeclareTheSameLimits keeps the advisory half honest. The
// hint is what a client reads before sending; if it drifts from the constants
// the handler enforces, the tool advertises a limit it does not apply.
//
// ⚠ The hint counts code points and the handler counts bytes, so for multibyte
// text the schema is the looser of the two. Same number on both sides is what
// keeps the hint from ever rejecting something the server would have accepted.
func TestProposeSchemasDeclareTheSameLimits(t *testing.T) {
	t.Parallel()

	ms := server.NewMCPServer("test", "0.0.0")
	(&Server{}).registerProposalTools(ms)
	tools := ms.ListTools()

	for tool, fields := range map[string]map[string]int{
		"propose_goal": {
			"title": proposeShortMaxBytes, "area": proposeShortMaxBytes,
			"description": proposeTextMaxBytes,
		},
		"propose_project": {
			"name": proposeShortMaxBytes, "title": proposeShortMaxBytes,
			"area": proposeShortMaxBytes, "description": proposeTextMaxBytes,
		},
	} {
		entry, ok := tools[tool]
		if !ok {
			t.Fatalf("%s is not registered", tool)
		}
		for field, want := range fields {
			prop, ok := entry.Tool.InputSchema.Properties[field].(map[string]any)
			if !ok {
				t.Errorf("%s.%s missing from the schema", tool, field)
				continue
			}
			// Compared as `any`, not through a float64 assertion. mcp-go
			// stores maxLength as an int while mcp.Max's "maximum" arrives as
			// a float64, and a type assertion that guesses wrong reports
			// "declares no maxLength" — naming a defect that is not the one
			// present. TestAddTaskSchema_AssigneeMaxLength compares the same
			// way for the same reason.
			got, ok := prop["maxLength"]
			if !ok {
				t.Errorf("%s.%s declares no maxLength; a caller cannot see the limit until it "+
					"is rejected", tool, field)
				continue
			}
			if got != want {
				t.Errorf("%s.%s maxLength = %v (%T), want %d", tool, field, got, got, want)
			}
		}
	}
}
