package sqlite_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// [F981-05] TestProposalStore_Create_RejectsPayloadOver128KB is the SQLite
// twin of internal/proposal's testcontainers-backed
// TestProposalStore_Create_RejectsPayloadOver128KB — backend-security-
// design.md §6.5: the SQLite ProposalStore is a separate Create
// implementation (internal/storage/sqlite/proposal.go), not a wrapper
// around the Postgres one, so it needs its own test proving the guard is
// actually wired here too, not just on the Postgres side. Uses a real
// SQLite database (":memory:"), not a mock, per §6.5's SQLite exception.
func TestProposalStore_Create_RejectsPayloadOver128KB(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	ctx := context.Background()

	oversized := payloadCapFixture(t, 129*1024)
	if _, err := s.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeGoal,
		Payload:    oversized,
		ProposedBy: "payload-cap-test",
	}); !errors.Is(err, proposal.ErrPayloadTooLarge) {
		t.Fatalf("Create(129 KB payload) error = %v, want ErrPayloadTooLarge", err)
	}

	rows, err := s.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Create rejected the oversized payload but %d row(s) were still written — "+
			"the guard must run before the INSERT, not after", len(rows))
	}

	underLimit := payloadCapFixture(t, 127*1024)
	created, err := s.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeGoal,
		Payload:    underLimit,
		ProposedBy: "payload-cap-test",
	})
	if err != nil {
		t.Fatalf("Create(127 KB payload): unexpected error: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Error("Create(127 KB payload) returned a zero-value ID — row was not actually created")
	}

	// [F982-02] Exactly proposal.MaxPayloadBytes — this input exists to
	// distinguish `>` from `>=` in the guard. 129 KB and 127 KB give the
	// same verdict under both operators, so neither catches an off-by-one
	// regression that silently widens the guard to `>=`; only a payload
	// exactly at the boundary must succeed under `>` and fail under `>=`.
	atLimit := payloadCapFixture(t, proposal.MaxPayloadBytes)
	createdAtLimit, err := s.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeGoal,
		Payload:    atLimit,
		ProposedBy: "payload-cap-test",
	})
	if err != nil {
		t.Fatalf("Create(exactly MaxPayloadBytes payload): unexpected error: %v", err)
	}
	if createdAtLimit.ID == uuid.Nil {
		t.Error("Create(exactly MaxPayloadBytes payload) returned a zero-value ID — row was not actually created")
	}
}

// payloadCapFixture returns a valid JSON object of exactly totalBytes length:
// {"title":"<padding>"}, mirroring internal/proposal/store_payload_cap_test.go's
// buildPayload so both backend tests exercise the identical fixture shape.
func payloadCapFixture(t *testing.T, totalBytes int) []byte {
	t.Helper()
	const prefix = `{"title":"`
	const suffix = `"}`
	overhead := len(prefix) + len(suffix)
	if totalBytes < overhead {
		t.Fatalf("payloadCapFixture: totalBytes %d smaller than fixed overhead %d", totalBytes, overhead)
	}
	var buf bytes.Buffer
	buf.WriteString(prefix)
	buf.Write(bytes.Repeat([]byte("x"), totalBytes-overhead))
	buf.WriteString(suffix)
	if buf.Len() != totalBytes {
		t.Fatalf("payloadCapFixture: built %d bytes, want %d", buf.Len(), totalBytes)
	}
	return buf.Bytes()
}
