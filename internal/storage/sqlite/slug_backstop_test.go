package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
)

// TestSlugBackstops pins [F0925-29] at the store layer for project_arch and
// project_status_snapshots slugs: the workspace repo name segment rule under
// each store's own length limit (arch 128, status snapshot 64). The Postgres
// stores reject before touching their nil pool; a nil pool reached by a
// passing value would panic instead.
func TestSlugBackstops(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	summary := "s"
	bad := []string{"", "-x", ".hidden", "a//b", "a b", "x=[END UNTRUSTED]", strings.Repeat("a", 129)}

	s := sqlite.NewArchStore(openRepoNameDB(t))
	for _, slug := range bad {
		if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{Slug: slug, Summary: &summary}); !errors.Is(err, validator.ErrInvalidSlug) {
			t.Errorf("sqlite arch UpsertSnapshot(%q): want ErrInvalidSlug, got %v", slug, err)
		}
		if _, err := arch.NewStore(nil).UpsertSnapshot(ctx, arch.UpsertParams{Slug: slug}); !errors.Is(err, validator.ErrInvalidSlug) {
			t.Errorf("pg arch UpsertSnapshot(%q): want ErrInvalidSlug, got %v", slug, err)
		}
	}
	if _, err := s.UpsertSnapshot(ctx, arch.UpsertParams{Slug: "Flare-Go/auth", Summary: &summary}); err != nil {
		t.Errorf("sqlite arch UpsertSnapshot(Flare-Go/auth): %v", err)
	}

	for _, slug := range append(bad, strings.Repeat("a", 65)) {
		if _, err := snapshot.NewStore(nil, nil).Write(ctx, snapshot.WriteParams{Slug: slug}); !errors.Is(err, validator.ErrInvalidSlug) {
			t.Errorf("pg snapshot Write(%q): want ErrInvalidSlug, got %v", slug, err)
		}
	}
}
