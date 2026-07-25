package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// See import_test.go for the shared rationale: ImportX methods must preserve
// every field verbatim (id/status/timestamps/relationships), unlike
// Log/Create which always mint fresh ones. openDecisionDB / openProposalStore
// are defined in decision_test.go / proposal_test.go respectively (same
// sqlite_test package).

func TestDecisionStore_ImportDecision(t *testing.T) {
	fixed := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	projectID := uuid.New()
	taskID := uuid.New()

	tests := []struct {
		name     string
		decision db.Decision
	}{
		{
			name: "decision linked to project and task",
			decision: db.Decision{
				ID: uuid.New(), ProjectID: pgUUIDVal(projectID), TaskID: pgUUIDVal(taskID),
				RepoName: pgTextVal("wayneblacktea"), Title: "Use pgvector",
				Context: "ctx", Decision: "decided", Rationale: "because",
				Alternatives: pgTextVal("alt A, alt B"), CreatedAt: pgTimeVal(fixed),
			},
		},
		{
			name: "standalone decision — no project/task link",
			decision: db.Decision{
				ID: uuid.New(), Title: "No FK constraints", Context: "ctx2",
				Decision: "decided2", Rationale: "because2", CreatedAt: pgTimeVal(fixed),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, s := openDecisionDB(t, ":memory:", "")
			ctx := context.Background()
			if err := s.ImportDecision(ctx, tt.decision); err != nil {
				t.Fatalf("ImportDecision: %v", err)
			}

			var title string
			var pid, tid sql.NullString
			row := d.QueryRowContext(ctx, `SELECT title, project_id, task_id FROM decisions WHERE id = ?1`, tt.decision.ID.String())
			if err := row.Scan(&title, &pid, &tid); err != nil {
				t.Fatalf("verify scan: %v", err)
			}
			if title != tt.decision.Title {
				t.Errorf("title = %q, want %q", title, tt.decision.Title)
			}
			if pid.Valid != tt.decision.ProjectID.Valid {
				t.Errorf("project_id valid = %v, want %v", pid.Valid, tt.decision.ProjectID.Valid)
			}
			if tid.Valid != tt.decision.TaskID.Valid {
				t.Errorf("task_id valid = %v, want %v", tid.Valid, tt.decision.TaskID.Valid)
			}
			if tt.decision.TaskID.Valid && tid.String != taskID.String() {
				t.Errorf("task_id = %q, want %q (relationship must survive import verbatim)", tid.String, taskID.String())
			}
		})
	}
}

func TestDecisionStore_ImportDecision_DuplicateIDFails(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()
	d := db.Decision{
		ID: uuid.New(), Title: "dup", Context: "c", Decision: "d", Rationale: "r",
		CreatedAt: pgTimeVal(time.Now()),
	}

	if err := s.ImportDecision(ctx, d); err != nil {
		t.Fatalf("first ImportDecision: %v", err)
	}
	if err := s.ImportDecision(ctx, d); err == nil {
		t.Fatal("re-importing a duplicate id: expected error, got nil")
	}
}

func TestProposalStore_ImportProposal(t *testing.T) {
	fixed := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	resolved := time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		proposal db.PendingProposal
	}{
		{
			name: "accepted proposal with reason and resolved_at",
			proposal: db.PendingProposal{
				ID: uuid.New(), Type: "goal", Payload: []byte(`{"title":"x"}`),
				Status: "accepted", ProposedBy: pgTextVal("claude"),
				CreatedAt: pgTimeVal(fixed), ResolvedAt: pgTimeVal(resolved), Reason: pgTextVal("looked good"),
			},
		},
		{
			name: "still-pending proposal — resolved_at/reason null",
			proposal: db.PendingProposal{
				ID: uuid.New(), Type: "task", Payload: []byte(`{"title":"y"}`),
				Status: "pending", CreatedAt: pgTimeVal(fixed),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openProposalStore(t, ":memory:", "")
			ctx := context.Background()
			if err := s.ImportProposal(ctx, tt.proposal); err != nil {
				t.Fatalf("ImportProposal: %v", err)
			}

			got, err := s.Get(ctx, tt.proposal.ID)
			if err != nil {
				t.Fatalf("Get after import: %v", err)
			}
			if got.Status != tt.proposal.Status {
				t.Errorf("status = %q, want %q (Import must preserve resolution status, not default to 'pending')", got.Status, tt.proposal.Status)
			}
			if got.ResolvedAt.Valid != tt.proposal.ResolvedAt.Valid {
				t.Errorf("resolved_at valid = %v, want %v", got.ResolvedAt.Valid, tt.proposal.ResolvedAt.Valid)
			}
			if got.Reason.Valid != tt.proposal.Reason.Valid {
				t.Errorf("reason valid = %v, want %v", got.Reason.Valid, tt.proposal.Reason.Valid)
			}
			if string(got.Payload) != string(tt.proposal.Payload) {
				t.Errorf("payload = %q, want %q", got.Payload, tt.proposal.Payload)
			}
		})
	}
}

func TestProposalStore_ImportProposal_DuplicateIDFails(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	ctx := context.Background()
	p := db.PendingProposal{ID: uuid.New(), Type: "goal", Payload: []byte(`{}`), Status: "pending", CreatedAt: pgTimeVal(time.Now())}

	if err := s.ImportProposal(ctx, p); err != nil {
		t.Fatalf("first ImportProposal: %v", err)
	}
	if err := s.ImportProposal(ctx, p); err == nil {
		t.Fatal("re-importing a duplicate id: expected error, got nil")
	}
}
