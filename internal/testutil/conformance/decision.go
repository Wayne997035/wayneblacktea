package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// RunDecisionSmoke runs the 2 shared decision behaviours (Log+ByRepo happy
// path plus the invalid-Source rejection, and the SearchByCosine capability
// divergence) against store, as t.Run subtests named backend+"/"+<op>.
func RunDecisionSmoke(t *testing.T, store decision.StoreIface, backend string) {
	ctx := context.Background()

	t.Run(backend+"/Log_HappyPath_ThenByRepo", func(t *testing.T) {
		if _, err := store.Log(ctx, decision.LogParams{
			RepoName: "smoke-repo",
			Title:    "t",
			Decision: "d",
			Source:   decision.SourceManual,
		}); err != nil {
			t.Fatalf("Log: %v", err)
		}
		rows, err := store.ByRepo(ctx, "smoke-repo", 10)
		if err != nil {
			t.Fatalf("ByRepo: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("ByRepo returned %d rows, want exactly 1", len(rows))
		}
		if rows[0].Title != "t" {
			t.Errorf("Title = %q, want %q", rows[0].Title, "t")
		}
	})

	t.Run(backend+"/Log_InvalidSource_Rejected", func(t *testing.T) {
		if _, err := store.Log(ctx, decision.LogParams{
			RepoName: "smoke-repo-invalid-source",
			Title:    "t",
			Decision: "d",
			// Source left at its zero value — neither SourceManual nor
			// SourceAuto — must be rejected, not silently accepted.
		}); !errors.Is(err, decision.ErrInvalidSource) {
			t.Fatalf("Log with zero-value Source: err = %v, want errors.Is(err, decision.ErrInvalidSource)", err)
		}
	})

	t.Run(backend+"/SearchByCosine_CapabilityDivergence", func(t *testing.T) {
		rows, err := store.SearchByCosine(ctx, []float32{0.1, 0.2, 0.3}, 5)
		switch backend {
		case "postgres":
			if err != nil {
				t.Fatalf("SearchByCosine (postgres, no embedded decisions): err = %v, want nil", err)
			}
			if len(rows) != 0 {
				t.Errorf("SearchByCosine (postgres, no embedded decisions) = %v, want empty", rows)
			}
		case "sqlite":
			if !errors.Is(err, decision.ErrCosineUnsupported) {
				t.Fatalf("SearchByCosine (sqlite): err = %v, want errors.Is(err, decision.ErrCosineUnsupported)", err)
			}
		default:
			t.Fatalf("unknown backend %q — conformance.RunDecisionSmoke only knows postgres/sqlite", backend)
		}
	})
}
