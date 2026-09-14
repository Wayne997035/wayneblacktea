package proposal

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// [GTD 1134a216] BatchItemResult.ErrMsg is JSON-marshalled straight back to
// the caller, so whatever goes in it ships. These pin the two halves of the
// classification: the one sentinel that is the caller's own answer passes
// through, and everything else is replaced rather than trimmed.

// driverish stands in for the real store errors the finding measured — the
// SQLite text verbatim, plus a Postgres-shaped one carrying the connection
// details that make this a disclosure rather than just noise.
var driverish = []struct {
	name string
	err  error
}{
	{"sqlite", errors.New("sqlite ResolveProposal: SQL logic error: no such table: pending_proposals (1)")},
	{"postgres", errors.New(`failed to connect to host=pg-1a2b.aivencloud.com user=avnadmin database=defaultdb: SQLSTATE 28P01`)},
}

func TestBatchItemErrMsg_ReplacesStoreErrors(t *testing.T) {
	id := uuid.New()

	for _, tc := range driverish {
		t.Run(tc.name, func(t *testing.T) {
			got := BatchItemErrMsg(tc.err)

			// Substring checks on the WHOLE original, and on the individual
			// tokens that carry the disclosure. Asserting only on the full
			// string would pass an implementation that trimmed it to the
			// hostname.
			if strings.Contains(got, tc.err.Error()) {
				t.Errorf("message carries the store error verbatim: %q", got)
			}
			for _, leak := range []string{"no such table", "SQL logic error", "aivencloud.com", "avnadmin", "SQLSTATE"} {
				if strings.Contains(got, leak) {
					t.Errorf("message leaks %q: %q", leak, got)
				}
			}
			// The id belongs in BatchItemResult.ID, not in this string. An
			// agent pays for the message once per failed item, so repeating a
			// 36-character uuid that is already a sibling field is waste — and
			// an id parameter would be one more thing a caller in a loop can
			// pass the wrong value for.
			if strings.Contains(got, id.String()) {
				t.Errorf("message repeats the proposal id that BatchItemResult.ID "+
					"already carries: %q", got)
			}
		})
	}
}

// TestBatchItemErrMsg_NotFoundPassesThrough is the reverse frame. Without it,
// an implementation that replaced EVERY error with the generic text would pass
// the test above while destroying the one answer the caller can act on.
func TestBatchItemErrMsg_NotFoundPassesThrough(t *testing.T) {
	t.Run("bare", func(t *testing.T) {
		if got := BatchItemErrMsg(ErrNotFound); got != ErrNotFound.Error() {
			t.Errorf("BatchItemErrMsg(ErrNotFound) = %q, want %q", got, ErrNotFound.Error())
		}
	})

	// Stores wrap before returning; errors.Is is what makes that work, and a
	// == comparison would silently stop recognising the sentinel.
	t.Run("wrapped", func(t *testing.T) {
		wrapped := fmt.Errorf("sqlite ResolveProposal: %w", ErrNotFound)
		got := BatchItemErrMsg(wrapped)
		if got != ErrNotFound.Error() {
			t.Errorf("BatchItemErrMsg(wrapped ErrNotFound) = %q, want %q", got, ErrNotFound.Error())
		}
		if strings.Contains(got, "sqlite ResolveProposal") {
			t.Errorf("passing the sentinel through must not carry the wrapper's "+
				"store text with it: %q", got)
		}
	})
}
