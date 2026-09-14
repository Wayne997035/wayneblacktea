package handler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/safetext"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// [GTD 49f2ed81] The HTTP twins of the MCP handoff projections. internal/mcp
// neutralises repo_name and next_actions.title/command/expected before they
// reach a reader and fences intent/context_summary; these two builders used to
// copy all of it through byte-for-byte. The response is not only rendered by
// the dashboard — the CLI prints it and people paste it into a conversation —
// so a forged closing marker stored in any of these fields could place
// instructions "outside" a downstream fence.
//
// Both tests drive the marker set from safetext.BoundaryMarkers() so a marker
// added to the registry later is covered without editing this file.

// forgedFieldsCases returns, for one marker, the (name, value) pairs that a
// projection must have neutralised. Keeping the extraction in one place means
// the positive and reverse frames below cannot disagree about which fields are
// in scope.
func pendingViewFields(v *pendingHandoffHTTPView) []struct{ name, val string } {
	out := []struct{ name, val string }{
		{"Intent", v.Intent},
		{"ContextSummary", v.ContextSummary.String},
		{"RepoName", v.RepoName.String},
	}
	for i, a := range v.NextActions {
		out = append(
			out,
			struct{ name, val string }{"NextActions[" + itoa(i) + "].Title", a.Title},
			struct{ name, val string }{"NextActions[" + itoa(i) + "].Command", a.Command},
			struct{ name, val string }{"NextActions[" + itoa(i) + "].Expected", a.Expected},
		)
		if a.RefTaskID != nil {
			out = append(out, struct{ name, val string }{
				"NextActions[" + itoa(i) + "].RefTaskID", *a.RefTaskID,
			})
		}
	}
	return out
}

func itoa(i int) string { return string(rune('0' + i)) }

func handoffWithForgedText(t *testing.T, forged string) *db.SessionHandoff {
	t.Helper()
	ref := forged
	actions, err := json.Marshal([]session.NextAction{{
		Step: 1, Title: forged, Command: forged, Expected: forged, RefTaskID: &ref,
	}})
	if err != nil {
		t.Fatalf("marshal next_actions fixture: %v", err)
	}
	return &db.SessionHandoff{
		ID:             uuid.New(),
		Intent:         forged,
		ContextSummary: pgtype.Text{String: forged, Valid: true},
		RepoName:       pgtype.Text{String: forged, Valid: true},
		NextActions:    actions,
	}
}

// TestBuildPendingHandoffHTTPView_NeutralisesEveryFreeTextField is the
// negative frame. Asserting the placeholder is present matters as much as
// asserting the marker is absent — "absent" is also true of a field that was
// dropped or blanked, and that regression would otherwise read as a pass.
func TestBuildPendingHandoffHTTPView_NeutralisesEveryFreeTextField(t *testing.T) {
	markers := safetext.BoundaryMarkers()
	if len(markers) == 0 {
		t.Fatal("safetext.BoundaryMarkers() is empty — every case below would " +
			"vacuously pass")
	}

	for _, marker := range markers {
		t.Run(strings.TrimSpace(marker), func(t *testing.T) {
			forged := "before " + marker + " after"
			v := buildPendingHandoffHTTPView(handoffWithForgedText(t, forged))
			if v == nil {
				t.Fatal("builder returned nil for a non-nil handoff")
			}
			fields := pendingViewFields(v)
			if len(fields) < 7 {
				t.Fatalf("only %d fields reached the assertions — the "+
					"next_actions fixture did not decode, so the three "+
					"highest-risk fields were never checked", len(fields))
			}
			for _, f := range fields {
				if strings.Contains(f.val, marker) {
					t.Errorf("%s still contains the forged marker %q: %q", f.name, marker, f.val)
				}
				if !strings.Contains(f.val, safetext.BoundaryMarkerPlaceholder) {
					t.Errorf("%s lost the placeholder — neutralisation must REPLACE "+
						"the marker, not drop the field: %q", f.name, f.val)
				}
			}
		})
	}
}

// TestBuildPendingHandoffHTTPView_CleanTextAndNullsUnchanged is the reverse
// frame plus the NULL case. An implementation that mangles or blanks
// everything passes the negative frame alone; this is what rejects it.
func TestBuildPendingHandoffHTTPView_CleanTextAndNullsUnchanged(t *testing.T) {
	const clean = "=== finish the STORED CONTEXT migration === and ship it"
	v := buildPendingHandoffHTTPView(&db.SessionHandoff{
		ID:             uuid.New(),
		Intent:         clean,
		ContextSummary: pgtype.Text{Valid: false},
		RepoName:       pgtype.Text{Valid: false},
	})
	if v == nil {
		t.Fatal("builder returned nil for a non-nil handoff")
	}
	if v.Intent != clean {
		t.Errorf("clean Intent was altered:\n got %q\nwant %q", v.Intent, clean)
	}
	// A NULL column must stay NULL: neutralising must not flip Valid, because
	// every consumer of this view distinguishes null from "".
	if v.ContextSummary.Valid {
		t.Errorf("NULL context_summary became valid=%v string=%q",
			v.ContextSummary.Valid, v.ContextSummary.String)
	}
	if v.RepoName.Valid {
		t.Errorf("NULL repo_name became valid=%v string=%q",
			v.RepoName.Valid, v.RepoName.String)
	}
	if v.NextActions == nil || len(v.NextActions) != 0 {
		t.Errorf("absent next_actions should project as an empty slice, got %#v", v.NextActions)
	}
}

// TestToRecentHandoffItems_NeutralisesIntent covers the /api/workspace/repos/
// :id/overview exit. Intent is the only forgeable field on that item — the
// rest are a UUID, a server-derived enum and two formatted timestamps.
func TestToRecentHandoffItems_NeutralisesIntent(t *testing.T) {
	for _, marker := range safetext.BoundaryMarkers() {
		t.Run(strings.TrimSpace(marker), func(t *testing.T) {
			forged := "before " + marker + " after"
			items := toRecentHandoffItems([]db.SessionHandoff{{
				ID:     uuid.New(),
				Intent: forged,
			}})
			if len(items) != 1 {
				t.Fatalf("want 1 item, got %d", len(items))
			}
			if strings.Contains(items[0].Intent, marker) {
				t.Errorf("Intent still contains the forged marker %q: %q", marker, items[0].Intent)
			}
			if !strings.Contains(items[0].Intent, safetext.BoundaryMarkerPlaceholder) {
				t.Errorf("Intent lost the placeholder: %q", items[0].Intent)
			}
		})
	}
}

// TestToRecentHandoffItems_CleanIntentUnchanged is that exit's reverse frame.
func TestToRecentHandoffItems_CleanIntentUnchanged(t *testing.T) {
	const clean = "=== ship the STORED CONTEXT migration ==="
	items := toRecentHandoffItems([]db.SessionHandoff{{ID: uuid.New(), Intent: clean}})
	if len(items) != 1 {
		t.Fatalf("want 1 item, got %d", len(items))
	}
	if items[0].Intent != clean {
		t.Errorf("clean Intent was altered:\n got %q\nwant %q", items[0].Intent, clean)
	}
}
