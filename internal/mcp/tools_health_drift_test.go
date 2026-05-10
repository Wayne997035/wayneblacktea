package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- extractKeywords ----

func TestExtractKeywords(t *testing.T) {
	cases := []struct {
		name  string
		desc  string
		want  []string // expected subset (all must be present); nil means empty result
		count int      // exact count check; 0 = skip
	}{
		{
			name:  "file path extracted",
			desc:  "Implement internal/handler/timeline_handler.go for new endpoint",
			want:  []string{"internal/handler/timeline_handler.go"},
			count: 1,
		},
		{
			name:  "migration number extracted",
			desc:  "Apply migration 000025 to add index on created_at",
			want:  []string{"000025"},
			count: 1,
		},
		{
			// Migration number that appears standalone (not only inside a file path)
			// plus a file path → both classes present, 3 keywords total.
			name:  "both classes extracted",
			desc:  "Apply migration 000071 see migrations/000071_add_tags.up.sql and internal/db/tags.go done",
			want:  []string{"000071", "migrations/000071_add_tags.up.sql", "internal/db/tags.go"},
			count: 3,
		},
		{
			name:  "deduplication: same keyword appears twice",
			desc:  "internal/handler/auth.go conflicts with internal/handler/auth.go",
			want:  []string{"internal/handler/auth.go"},
			count: 1,
		},
		{
			name:  "empty description returns nil",
			desc:  "",
			want:  nil,
			count: 0,
		},
		{
			name:  "description with no matching patterns returns nil",
			desc:  "Refactor the payment service to improve clarity",
			want:  nil,
			count: 0,
		},
		{
			name: "cap at 20 keywords",
			// Build a description with 25 distinct file paths.
			desc: func() string {
				s := ""
				for i := 0; i < 25; i++ {
					s += "internal/pkg/file" + string(rune('a'+i)) + ".go "
				}
				return s
			}(),
			count: 20,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractKeywords(tc.desc)

			if tc.count > 0 && len(got) != tc.count {
				t.Errorf("extractKeywords count = %d, want %d; result: %v", len(got), tc.count, got)
			}
			if tc.count == 0 && len(got) != 0 {
				t.Errorf("extractKeywords = %v, want nil/empty", got)
			}

			// Verify required keywords are present.
			for _, w := range tc.want {
				found := false
				for _, g := range got {
					if g == w {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected keyword %q not found in %v", w, got)
				}
			}
		})
	}
}

// ---- detectCompletionDrift ----

func TestDetectCompletionDrift(t *testing.T) {
	// helper: create a db.Task with the given status, title, description.
	makeTask := func(status, title, desc string) db.Task {
		return db.Task{
			ID:     uuid.New(),
			Title:  title,
			Status: status,
			Description: pgtype.Text{
				String: desc,
				Valid:  desc != "",
			},
		}
	}

	// helper: set up a temp dir with fake repo structure.
	setupRepo := func(t *testing.T, files ...string) string {
		t.Helper()
		root := t.TempDir()
		for _, f := range files {
			full := filepath.Join(root, f)
			if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(full, []byte(""), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		}
		return root
	}

	t.Run("pending task with matching file returns candidate", func(t *testing.T) {
		root := setupRepo(t, "internal/handler/timeline_handler.go")
		tasks := []db.Task{
			makeTask("pending", "Add timeline handler", "Implement internal/handler/timeline_handler.go"),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 1 {
			t.Fatalf("expected 1 candidate, got %d: %v", len(got), got)
		}
		if got[0].Title != "Add timeline handler" {
			t.Errorf("candidate title = %q, want %q", got[0].Title, "Add timeline handler")
		}
		if len(got[0].Evidence) == 0 {
			t.Error("expected at least 1 evidence item")
		}
	})

	t.Run("in_progress task is ignored even if file exists", func(t *testing.T) {
		root := setupRepo(t, "internal/handler/timeline_handler.go")
		tasks := []db.Task{
			makeTask("in_progress", "Work in progress", "Implement internal/handler/timeline_handler.go"),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 0 {
			t.Errorf("expected 0 candidates for in_progress task, got %d", len(got))
		}
	})

	t.Run("pending task with no matching file on disk is not returned", func(t *testing.T) {
		root := t.TempDir() // empty — no files
		tasks := []db.Task{
			makeTask("pending", "Missing file task", "Implement internal/handler/missing_handler.go"),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 0 {
			t.Errorf("expected 0 candidates when file absent, got %d", len(got))
		}
	})

	t.Run("empty repoRoot returns nil", func(t *testing.T) {
		tasks := []db.Task{
			makeTask("pending", "Some task", "internal/handler/foo.go"),
		}
		got := detectCompletionDrift(tasks, "")
		if got != nil {
			t.Errorf("expected nil for empty repoRoot, got %v", got)
		}
	})

	t.Run("pending task with matching migration number returns candidate", func(t *testing.T) {
		root := setupRepo(t, "migrations/000025_add_index.up.sql")
		tasks := []db.Task{
			makeTask("pending", "Apply migration 000025", "Apply migration 000025 to add index"),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 1 {
			t.Fatalf("expected 1 candidate for migration number, got %d", len(got))
		}
	})

	t.Run("completed task is ignored", func(t *testing.T) {
		root := setupRepo(t, "internal/handler/timeline_handler.go")
		tasks := []db.Task{
			makeTask("done", "Already done", "Implement internal/handler/timeline_handler.go"),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 0 {
			t.Errorf("expected 0 candidates for done task, got %d", len(got))
		}
	})

	t.Run("pending task with empty description is not returned", func(t *testing.T) {
		root := t.TempDir()
		tasks := []db.Task{
			makeTask("pending", "No description task", ""),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 0 {
			t.Errorf("expected 0 candidates for task with empty description, got %d", len(got))
		}
	})

	t.Run("multiple pending tasks: only matching ones returned", func(t *testing.T) {
		root := setupRepo(t, "internal/handler/auth.go")
		tasks := []db.Task{
			makeTask("pending", "Auth handler", "Add internal/handler/auth.go login endpoint"),
			makeTask("pending", "No evidence", "Refactor the payment service"),
		}
		got := detectCompletionDrift(tasks, root)
		if len(got) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(got))
		}
		if got[0].Title != "Auth handler" {
			t.Errorf("wrong candidate returned: %q", got[0].Title)
		}
	})
}
