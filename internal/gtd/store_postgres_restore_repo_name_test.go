package gtd_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestRestoreProject_RepoNameRule_Postgres is the Postgres twin of the
// SQLite TestRestoreProject_RepoNameRule ([F0925-30]): a restored repo_name
// breaking the workspace repo name rule is cleared to NULL, NULL stays NULL,
// and a compliant value is kept.
func TestRestoreProject_RepoNameRule_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	for _, tc := range []struct {
		name      string
		payload   any // planted into the tombstone's repo_name; nil = JSON null
		wantValid bool
		want      string
	}{
		{"invalid_cleared", "../x", false, ""},
		{"null_stays_null", nil, false, ""},
		{"valid_kept", "a/b", true, "a/b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wsID := uuid.New()
			store := newPgGTDStore(pool, &wsID)
			ctx := context.Background()

			proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
				Name: fmt.Sprintf("f0925-30-%s-%s", tc.name, uuid.New()), Title: "T",
			})
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
				t.Fatalf("DeleteProject: %v", err)
			}
			if _, err := pool.Exec(
				ctx,
				`UPDATE deletion_tombstones SET payload = jsonb_set(payload, '{repo_name}', COALESCE(to_jsonb($1::text), 'null'::jsonb))
				  WHERE entity_kind = 'project' AND entity_id = $2`,
				tc.payload, proj.ID,
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
			var stored *string
			if err := pool.QueryRow(ctx, `SELECT repo_name FROM projects WHERE id = $1`, proj.ID).Scan(&stored); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if (stored != nil) != tc.wantValid || (stored != nil && *stored != tc.want) {
				t.Errorf("stored repo_name = %v, want valid=%v %q", stored, tc.wantValid, tc.want)
			}
		})
	}
}
