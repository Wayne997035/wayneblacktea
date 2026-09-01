package proposal_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// [F981-05] TestProposalStore_Create_RejectsPayloadOverLimit pins the
// fail-closed payload size guard at Store.Create's write path
// (store.go:MaxPayloadBytes) — backend-security-design.md §2.1 ("LLM tool
// input is hostile"): a prompt-injected agent controls
// pending_proposals.payload via propose_goal/propose_project, and before
// this guard nothing checked len(p.Payload) before the row was written.
// Uses the testcontainers Postgres pool shared with this package's other PG
// tests (backend-security-design.md §6.5), not a mock or shared dev DB.
//
// Sizes are expressed relative to proposal.MaxPayloadBytes (not hardcoded
// KB literals) so a future cap change — like [F983-01]'s 128 KB -> 2 MiB
// widening — doesn't also require re-deriving these boundary values.
func TestProposalStore_Create_RejectsPayloadOverLimit(t *testing.T) {
	pool := openBatchTestPgPool(t)
	wsID := uuid.New() // fresh workspace: isolated from other tests' rows
	store := proposal.NewStore(pool, &wsID)
	ctx := context.Background()

	// MaxPayloadBytes+1024 — comfortably over the boundary, using a minimal
	// valid-JSON envelope so a would-be decode failure never masks the size
	// check.
	oversized := buildPayload(t, proposal.MaxPayloadBytes+1024)
	if _, err := store.Create(ctx, proposal.CreateParams{
		WorkspaceID: &wsID,
		Type:        proposal.TypeGoal,
		Payload:     oversized,
		ProposedBy:  "payload-cap-test",
	}); !errors.Is(err, proposal.ErrPayloadTooLarge) {
		t.Fatalf("Create(MaxPayloadBytes+1024 payload) error = %v, want ErrPayloadTooLarge", err)
	}

	rows, err := store.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Create rejected the oversized payload but %d row(s) were still written — "+
			"the guard must run before CreatePendingProposal, not after", len(rows))
	}

	// MaxPayloadBytes-1024 — comfortably under the boundary — must succeed
	// exactly as before this guard existed. Not asserting stored payload
	// byte-length here deliberately: the payload column is JSONB
	// (migrations/000010_pending_proposals.up.sql), and Postgres
	// reconstructs JSONB text from its parsed binary form rather than
	// preserving the original bytes verbatim — a round-trip length
	// difference there is a JSONB storage characteristic, not something
	// Store.Create's size guard controls or this ticket is scoped to
	// change.
	underLimit := buildPayload(t, proposal.MaxPayloadBytes-1024)
	created, err := store.Create(ctx, proposal.CreateParams{
		WorkspaceID: &wsID,
		Type:        proposal.TypeGoal,
		Payload:     underLimit,
		ProposedBy:  "payload-cap-test",
	})
	if err != nil {
		t.Fatalf("Create(MaxPayloadBytes-1024 payload): unexpected error: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", created.ID)
		if cleanErr != nil {
			t.Logf("cleanup proposal: %v", cleanErr)
		}
	})
	if created.ID == uuid.Nil {
		t.Error("Create(MaxPayloadBytes-1024 payload) returned a zero-value ID — row was not actually created")
	}

	// [F982-01] Exactly proposal.MaxPayloadBytes — this input exists to
	// distinguish `>` from `>=` in the guard. MaxPayloadBytes+1024 and
	// MaxPayloadBytes-1024 give the same verdict under both operators, so
	// neither catches an off-by-one
	// regression that silently widens the guard to `>=`; only a payload
	// exactly at the boundary must succeed under `>` and fail under `>=`.
	atLimit := buildPayload(t, proposal.MaxPayloadBytes)
	createdAtLimit, err := store.Create(ctx, proposal.CreateParams{
		WorkspaceID: &wsID,
		Type:        proposal.TypeGoal,
		Payload:     atLimit,
		ProposedBy:  "payload-cap-test",
	})
	if err != nil {
		t.Fatalf("Create(exactly MaxPayloadBytes payload): unexpected error: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", createdAtLimit.ID)
		if cleanErr != nil {
			t.Logf("cleanup proposal: %v", cleanErr)
		}
	})
	if createdAtLimit.ID == uuid.Nil {
		t.Error("Create(exactly MaxPayloadBytes payload) returned a zero-value ID — row was not actually created")
	}
}

// buildPayload returns a valid JSON object of exactly totalBytes length:
// {"title":"<padding>"} with "x" repeated in the title so the fixture stays
// well-formed JSON at any size, letting the size guard be tested in
// isolation from JSON-decode failures.
func buildPayload(t *testing.T, totalBytes int) []byte {
	t.Helper()
	const prefix = `{"title":"`
	const suffix = `"}`
	overhead := len(prefix) + len(suffix)
	if totalBytes < overhead {
		t.Fatalf("buildPayload: totalBytes %d smaller than fixed overhead %d", totalBytes, overhead)
	}
	var buf bytes.Buffer
	buf.WriteString(prefix)
	buf.Write(bytes.Repeat([]byte("x"), totalBytes-overhead))
	buf.WriteString(suffix)
	if buf.Len() != totalBytes {
		t.Fatalf("buildPayload: built %d bytes, want %d", buf.Len(), totalBytes)
	}
	return buf.Bytes()
}
