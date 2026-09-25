package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestRestoreProject_RepoNameRule pins [F0925-30]: RestoreProject copies the
// tombstone payload back verbatim, and that payload may predate the
// workspace repo name rule (the cleanup migration clears table columns, not
// tombstone JSON). A restored repo_name breaking the rule is cleared to NULL
// and the restore still succeeds; NULL stays NULL (most projects never link
// a repo) and a compliant value is kept.
func TestRestoreProject_RepoNameRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		payload   any // value planted into the tombstone's $.repo_name; nil = JSON null
		wantValid bool
		want      string
	}{
		{"invalid_cleared", "../x", false, ""},
		{"null_stays_null", nil, false, ""},
		{"valid_kept", "a/b", true, "a/b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := openSoftDeleteTestDB(t)
			store := NewGTDStore(d)
			ctx := context.Background()

			proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f0925-30-" + tc.name, Title: "T"})
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
				t.Fatalf("DeleteProject: %v", err)
			}
			// Plant the legacy value the way a row deleted before the rule
			// would carry it.
			if _, err := d.conn.ExecContext(
				ctx,
				`UPDATE deletion_tombstones SET payload = json_set(payload, '$.repo_name', ?1)
				  WHERE entity_kind = 'project' AND entity_id = ?2`,
				tc.payload, proj.ID.String(),
			); err != nil {
				t.Fatalf("plant tombstone repo_name: %v", err)
			}

			restored, _, err := store.RestoreProject(ctx, proj.ID, testerActor)
			if err != nil {
				t.Fatalf("RestoreProject: %v", err)
			}
			if restored.RepoName.Valid != tc.wantValid || restored.RepoName.String != tc.want {
				t.Errorf("returned repo_name = %+v, want valid=%v %q", restored.RepoName, tc.wantValid, tc.want)
			}
			var stored sql.NullString
			if err := d.conn.QueryRowContext(ctx, `SELECT repo_name FROM projects WHERE id = ?1`,
				proj.ID.String()).Scan(&stored); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if stored.Valid != tc.wantValid || stored.String != tc.want {
				t.Errorf("stored repo_name = %+v, want valid=%v %q", stored, tc.wantValid, tc.want)
			}
		})
	}
}
