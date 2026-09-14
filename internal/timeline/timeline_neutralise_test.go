package timeline_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/safetext"
	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// [GTD 49f2ed81] GET /api/timeline renders stored, agent-authored free text
// (handoff intents, decision titles, repo names) into a surface other agents
// read back. Before this fix the fields below reached the response
// byte-for-byte, so a stored payload could forge a closing boundary marker and
// place instructions "outside" the fence a downstream reader puts around the
// span.
//
// These tests drive the marker list from safetext.BoundaryMarkers() rather
// than naming markers inline: a twelfth marker added to the registry is then
// covered here without anyone remembering to update this file. A hard-coded
// list is how coverage silently stops matching the thing it guards.

func neutraliseTestRange() (from, to, mid time.Time) {
	mid = time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	return mid.Add(-24 * time.Hour), mid.Add(24 * time.Hour), mid
}

// TestTimeline_ForgedMarkersNeutralisedInEveryFreeTextField is the negative
// frame: for EVERY marker in the registry, a handoff carrying it in intent and
// repo_name, and a decision carrying it in title and repo_name, must come out
// with the marker gone and the placeholder present.
//
// Asserting the placeholder is present matters as much as asserting the marker
// is absent: "absent" is also true when a field is dropped or blanked, and a
// regression that empties the field would otherwise read as a pass.
func TestTimeline_ForgedMarkersNeutralisedInEveryFreeTextField(t *testing.T) {
	from, to, mid := neutraliseTestRange()
	markers := safetext.BoundaryMarkers()
	if len(markers) == 0 {
		t.Fatal("safetext.BoundaryMarkers() is empty — the registry this test " +
			"drives itself from has gone missing, so every case below would " +
			"vacuously pass")
	}

	for _, marker := range markers {
		t.Run(strings.TrimSpace(marker), func(t *testing.T) {
			forged := "before " + marker + " after"
			agg := &timeline.Aggregator{
				Handoffs: &fakeHandoffSource{handoffs: []db.SessionHandoff{{
					ID:        uuid.New(),
					Intent:    forged,
					RepoName:  pgtype.Text{String: forged, Valid: true},
					CreatedAt: ts(mid),
				}}},
				Decisions: &fakeDecisionSource{decisions: []db.Decision{{
					ID:        uuid.New(),
					Title:     forged,
					RepoName:  pgtype.Text{String: forged, Valid: true},
					CreatedAt: ts(mid),
				}}},
			}

			events, err := agg.Aggregate(context.Background(), from, to)
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if len(events) == 0 {
				t.Fatal("no events produced — the fixture never reached the " +
					"code under test, so this case proves nothing")
			}

			for i, e := range events {
				for _, field := range []struct{ name, val string }{
					{"Title", e.Title},
					{"RepoName", e.RepoName},
				} {
					if strings.Contains(field.val, marker) {
						t.Errorf("events[%d].%s still contains the forged marker %q: %q",
							i, field.name, marker, field.val)
					}
					if !strings.Contains(field.val, safetext.BoundaryMarkerPlaceholder) {
						t.Errorf("events[%d].%s lost the placeholder — neutralisation "+
							"must REPLACE the marker, not drop the field: %q",
							i, field.name, field.val)
					}
				}
			}
		})
	}
}

// TestTimeline_CleanTextPassesThroughUnchanged is the reverse frame. A guard
// that only proves "the bad input is caught" is compatible with an
// implementation that mangles everything, and that implementation would ship
// green. This pins that ordinary text — including the substrings "===" and
// "STORED" that appear inside real markers — survives byte-for-byte.
func TestTimeline_CleanTextPassesThroughUnchanged(t *testing.T) {
	from, to, mid := neutraliseTestRange()
	const cleanIntent = "=== finish the STORED CONTEXT migration === and ship it"
	const cleanRepo = "wayneblacktea"

	agg := &timeline.Aggregator{
		Handoffs: &fakeHandoffSource{handoffs: []db.SessionHandoff{{
			ID:        uuid.New(),
			Intent:    cleanIntent,
			RepoName:  pgtype.Text{String: cleanRepo, Valid: true},
			CreatedAt: ts(mid),
		}}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Title != cleanIntent {
		t.Errorf("clean Title was altered:\n got %q\nwant %q", events[0].Title, cleanIntent)
	}
	if events[0].RepoName != cleanRepo {
		t.Errorf("clean RepoName was altered:\n got %q\nwant %q", events[0].RepoName, cleanRepo)
	}
}

// TestTimeline_NullRepoNameStaysEmpty pins that the NULL case does not become
// the placeholder or any other non-empty string. Event.RepoName is
// `omitempty`, so a NULL that came back non-empty would start appearing in the
// JSON response as a repo named after nothing.
func TestTimeline_NullRepoNameStaysEmpty(t *testing.T) {
	from, to, mid := neutraliseTestRange()
	agg := &timeline.Aggregator{
		Handoffs: &fakeHandoffSource{handoffs: []db.SessionHandoff{{
			ID:        uuid.New(),
			Intent:    "no repo on this one",
			RepoName:  pgtype.Text{Valid: false},
			CreatedAt: ts(mid),
		}}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].RepoName != "" {
		t.Errorf("NULL repo_name became %q, want empty", events[0].RepoName)
	}
}
