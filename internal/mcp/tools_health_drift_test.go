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

func TestDetectCompletionDrift_StatusFiltering(t *testing.T) {
	makeTaskWithDesc := func(status, title, desc string) db.Task {
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

	root := t.TempDir()
	full := filepath.Join(root, "internal", "handler", "timeline_handler.go")
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cases := []struct {
		name   string
		status string
		want   int
	}{
		{
			name:   "pending status included",
			status: "pending",
			want:   1,
		},
		{
			name:   "in_progress status included",
			status: "in_progress",
			want:   1,
		},
		{
			name:   "blocked status ignored",
			status: "blocked",
			want:   0,
		},
		{
			name:   "done status ignored",
			status: "done",
			want:   0,
		},
		{
			name:   "cancelled status ignored",
			status: "cancelled",
			want:   0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tasks := []db.Task{
				makeTaskWithDesc(tc.status, "Some task", "see internal/handler/timeline_handler.go"),
			}
			got := detectCompletionDrift(tasks, root)
			if len(got) != tc.want {
				t.Errorf("status %q: got %d candidates, want %d", tc.status, len(got), tc.want)
			}
		})
	}
}

// ---- keywordExistsOnDisk: adversarial inputs ----

func TestKeywordExistsOnDisk_PathTraversal(t *testing.T) {
	root := t.TempDir()

	// Sanity: create a real nested file inside repoRoot that the positive
	// case can resolve.
	realPath := filepath.Join(root, "internal", "handler", "timeline_handler.go")
	if err := os.MkdirAll(filepath.Dir(realPath), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(realPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cases := []struct {
		name string
		kw   string
		want bool
	}{
		{
			name: "dotdot escape rejected",
			kw:   "internal/../../../etc/passwd",
			want: false,
		},
		{
			name: "embedded dotdot rejected even when join would still be inside",
			kw:   "internal/handler/../../etc/hosts",
			want: false,
		},
		{
			name: "null byte rejected",
			kw:   "internal/foo\x00.go",
			want: false,
		},
		{
			name: "newline rejected",
			kw:   "internal/foo\nbar",
			want: false,
		},
		{
			name: "carriage return rejected",
			kw:   "internal/foo\rbar",
			want: false,
		},
		{
			name: "valid existing path returns true",
			kw:   "internal/handler/timeline_handler.go",
			want: true,
		},
		{
			name: "non-numeric bare keyword (no slash) rejected",
			kw:   "passwd",
			want: false,
		},
		{
			name: "valid migration glob with nonexistent number returns false",
			kw:   "999999",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keywordExistsOnDisk(tc.kw, root)
			if got != tc.want {
				t.Errorf("keywordExistsOnDisk(%q) = %v, want %v", tc.kw, got, tc.want)
			}
		})
	}
}

// ---- detectCompletionDrift: cap, in_progress, title sanitisation ----

func TestDetectCompletionDrift_InProgressIncluded(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "internal", "handler", "wip.go")
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tasks := []db.Task{
		{
			ID:     uuid.New(),
			Title:  "WIP handler",
			Status: "in_progress",
			Description: pgtype.Text{
				String: "Implement internal/handler/wip.go for new endpoint",
				Valid:  true,
			},
		},
	}

	got := detectCompletionDrift(tasks, root)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate for in_progress task, got %d", len(got))
	}
	if got[0].Title != "WIP handler" {
		t.Errorf("title = %q, want %q", got[0].Title, "WIP handler")
	}
}

func TestDetectCompletionDrift_CapAt50(t *testing.T) {
	root := t.TempDir()

	// Create 100 distinct files and 100 pending tasks each referencing one.
	const total = 100
	tasks := make([]db.Task, 0, total)
	for i := 0; i < total; i++ {
		rel := filepath.Join("internal", "pkg", "f"+itoa(i)+".go")
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(""), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		tasks = append(tasks, db.Task{
			ID:     uuid.New(),
			Title:  "task " + itoa(i),
			Status: "pending",
			Description: pgtype.Text{
				String: "see " + rel,
				Valid:  true,
			},
		})
	}

	got := detectCompletionDrift(tasks, root)
	if len(got) > maxDriftCandidates {
		t.Fatalf("expected at most %d candidates, got %d", maxDriftCandidates, len(got))
	}
	if len(got) != maxDriftCandidates {
		t.Errorf("expected exactly %d candidates (cap hit), got %d", maxDriftCandidates, len(got))
	}
}

func TestDetectCompletionDrift_TitleSanitization(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "internal", "handler", "evil.go")
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tasks := []db.Task{
		{
			ID:     uuid.New(),
			Title:  "\x1b[31mEvil\x1b[0m\tkeep-tab\nDROP",
			Status: "pending",
			Description: pgtype.Text{
				String: "Touches internal/handler/evil.go",
				Valid:  true,
			},
		},
	}

	got := detectCompletionDrift(tasks, root)
	if len(got) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(got))
	}

	for _, r := range got[0].Title {
		if r < 0x20 && r != '\t' {
			t.Errorf("sanitised title still contains control rune %q (full title: %q)", r, got[0].Title)
		}
	}

	// Tab is explicitly preserved.
	if !containsRune(got[0].Title, '\t') {
		t.Errorf("tab should be preserved, got %q", got[0].Title)
	}

	// ESC byte must be removed.
	if containsRune(got[0].Title, 0x1b) {
		t.Errorf("ESC byte should be stripped, got %q", got[0].Title)
	}

	// Newline must be removed.
	if containsRune(got[0].Title, '\n') {
		t.Errorf("newline should be stripped, got %q", got[0].Title)
	}
}

// itoa is a tiny base-10 conversion helper to avoid pulling in strconv just
// for test scaffolding (keeps the test file's import block tight).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func containsRune(s string, target rune) bool {
	for _, r := range s {
		if r == target {
			return true
		}
	}
	return false
}
