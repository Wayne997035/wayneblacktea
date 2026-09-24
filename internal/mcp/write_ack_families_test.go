package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// [GTD c46025c1] The task family's measurement lives in write_ack_test.go.
// This covers the families added afterwards, and it is a measurement rather
// than a claim: every case logs the before/after byte count on every run, and
// fails if the body the caller just sent comes back.
//
// Each case names the ONE field that has to disappear. Asserting on a
// specific string is deliberate — a size-only check would pass if the payload
// shrank for some unrelated reason, which is how a "saving" survives long
// after the thing it was measuring stopped happening.

func bigBody(tag string) string {
	return strings.Repeat(tag+": the caller supplied this on the same call. ", 40)
}

func TestWriteAcks_DropTheBodyTheCallerJustSent(t *testing.T) {
	t.Parallel()
	body := bigBody("payload")
	now := time.Now()
	pgNow := pgtype.Timestamptz{Time: now, Valid: true}

	cases := []struct {
		name    string
		before  any
		after   any
		needle  string
		minSave int
	}{
		{
			name: "skill",
			before: wrapUntrustedSkill(&skill.Skill{
				ID: uuid.NewString(), Name: "s", Description: body,
				Steps: []string{body}, Examples: []any{body},
				CreatedAt: now, UpdatedAt: now,
			}),
			after: ackSkill(wrapUntrustedSkill(&skill.Skill{
				ID: uuid.NewString(), Name: "s", Description: body,
				Steps: []string{body}, Examples: []any{body},
				CreatedAt: now, UpdatedAt: now,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
		{
			name: "proposal",
			before: wrapUntrustedProposal(&db.PendingProposal{
				ID: uuid.New(), Type: "task", Status: "pending",
				Payload: []byte(`{"note":"` + body + `"}`), CreatedAt: pgNow,
			}),
			after: ackProposal(wrapUntrustedProposal(&db.PendingProposal{
				ID: uuid.New(), Type: "task", Status: "pending",
				Payload: []byte(`{"note":"` + body + `"}`), CreatedAt: pgNow,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
		{
			name: "concept",
			before: wrapUntrustedConcept(&db.Concept{
				ID: uuid.New(), Title: "c", Content: body, Status: "active", CreatedAt: pgNow,
			}),
			after: ackConcept(wrapUntrustedConcept(&db.Concept{
				ID: uuid.New(), Title: "c", Content: body, Status: "active", CreatedAt: pgNow,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
		{
			name: "project",
			before: wrapUntrustedProject(&db.Project{
				ID: uuid.New(), Name: "p", Title: "p", Status: "active",
				Description: pgtype.Text{String: body, Valid: true}, CreatedAt: pgNow,
			}),
			after: ackProject(wrapUntrustedProject(&db.Project{
				ID: uuid.New(), Name: "p", Title: "p", Status: "active",
				Description: pgtype.Text{String: body, Valid: true}, CreatedAt: pgNow,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
		{
			name: "goal",
			before: wrapUntrustedGoal(&db.Goal{
				ID: uuid.New(), Title: "g", Status: "active",
				Description: pgtype.Text{String: body, Valid: true}, CreatedAt: pgNow,
			}),
			after: ackGoal(wrapUntrustedGoal(&db.Goal{
				ID: uuid.New(), Title: "g", Status: "active",
				Description: pgtype.Text{String: body, Valid: true}, CreatedAt: pgNow,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
		{
			name: "knowledge",
			before: wrapUntrustedKnowledgeItem(&db.KnowledgeItem{
				ID: uuid.New(), Type: "til", Title: "k", Content: body, CreatedAt: pgNow,
			}),
			after: ackKnowledge(wrapUntrustedKnowledgeItem(&db.KnowledgeItem{
				ID: uuid.New(), Type: "til", Title: "k", Content: body, CreatedAt: pgNow,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
		{
			name: "vision",
			before: wrapUntrustedVisionItem(&vision.VisionItem{
				ID: uuid.New(), Title: "v", WhyBlocked: body, ContextMD: body,
				Status: vision.VisionStatusOpen, CreatedAt: now,
			}),
			after: ackVision(wrapUntrustedVisionItem(&vision.VisionItem{
				ID: uuid.New(), Title: "v", WhyBlocked: body, ContextMD: body,
				Status: vision.VisionStatusOpen, CreatedAt: now,
			})),
			needle: "the caller supplied this", minSave: 80,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := mustMarshal(t, tc.before)
			after := mustMarshal(t, tc.after)
			saved := 100 - (len(after) * 100 / len(before))
			t.Logf("%s write answer: %d bytes before, %d after (%d%% smaller)",
				tc.name, len(before), len(after), saved)

			if !strings.Contains(string(before), tc.needle) {
				t.Fatalf("%s: the fixture's body is not in the BEFORE answer, so this case "+
					"measures nothing whatever the byte counts say", tc.name)
			}
			if strings.Contains(string(after), tc.needle) {
				t.Errorf("%s: the write answer still carries the caller's own body: %s",
					tc.name, after)
			}
			if saved < tc.minSave {
				t.Errorf("%s: only %d%% smaller (%d → %d bytes), want ≥%d%%",
					tc.name, saved, len(before), len(after), tc.minSave)
			}
		})
	}
}

// TestWriteAcks_NilIn pins the boundary every wrap function honours, so an ack
// can be chained after one that returned nil for a missing row.
func TestWriteAcks_NilIn(t *testing.T) {
	t.Parallel()
	if ackSkill(nil) != nil || ackProposal(nil) != nil || ackConcept(nil) != nil ||
		ackProject(nil) != nil || ackGoal(nil) != nil || ackKnowledge(nil) != nil ||
		ackVision(nil) != nil {
		t.Error("an ack returned non-nil for a nil row")
	}
}
