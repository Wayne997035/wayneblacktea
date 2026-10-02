package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestListTasks_Q_LengthBoundaries covers handleListTasks' hand-enforced
// 2-char minimum (MinLength is schema-advisory only — registerToolSpec never
// reads it) and the seam's own MaxLength(200) enforcement, both edges
// [F0930-20]. This is additive to the 3 dispatch-named store-level tests
// (TestListTasksQ_*_PG/_SQLite) — those prove the SQL predicate; this proves
// the MCP-layer validation wrapped around it.
func TestListTasks_Q_LengthBoundaries(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	t.Run("whitespace-only is treated as empty, not too-short", func(t *testing.T) {
		t.Parallel()
		r := callListTasks(t, s, map[string]any{"q": "   "})
		if r.IsError {
			t.Fatalf("whitespace-only q should be treated as no filter, got: %s", resultText(r))
		}
	})

	t.Run("1 char errors as too short", func(t *testing.T) {
		t.Parallel()
		r := callListTasks(t, s, map[string]any{"q": "a"})
		if !r.IsError || resultText(r) != "q must be at least 2 characters" {
			t.Errorf("got %q, want %q", resultText(r), "q must be at least 2 characters")
		}
	})

	t.Run("exactly 2 chars succeeds (boundary)", func(t *testing.T) {
		t.Parallel()
		r := callListTasks(t, s, map[string]any{"q": "ab"})
		if r.IsError {
			t.Errorf("2-char q should succeed, got: %s", resultText(r))
		}
	})

	t.Run("exactly 200 chars succeeds (boundary)", func(t *testing.T) {
		t.Parallel()
		r := callListTasks(t, s, map[string]any{"q": strings.Repeat("a", 200)})
		if r.IsError {
			t.Errorf("200-char q should succeed, got: %s", resultText(r))
		}
	})

	t.Run("201 chars is rejected by the seam's MaxLength(200)", func(t *testing.T) {
		t.Parallel()
		r := callListTasks(t, s, map[string]any{"q": strings.Repeat("a", 201)})
		if !r.IsError || !strings.Contains(resultText(r), "exceeds 200 characters") {
			t.Errorf("got %q, want a message mentioning \"exceeds 200 characters\"", resultText(r))
		}
	})
}

// TestListTasks_Q_TagNoiseRejected covers the sanitize.ValidateNoTagNoise
// screen on q [F0930-20]. Positive control in the same test: a clean q must
// still find a title that itself legitimately contains angle brackets — the
// noise filter screens the QUERY text, not the stored title.
func TestListTasks_Q_TagNoiseRejected(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	r := callListTasks(t, s, map[string]any{"q": "A </invoke> B"})
	if !r.IsError {
		t.Fatalf("tag-noise q must be rejected, got success: %s", resultText(r))
	}

	if _, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{Title: "Fix <script> issue"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	r = callListTasks(t, s, map[string]any{"q": "script"})
	if r.IsError {
		t.Fatalf("clean q should succeed even though the matched TITLE has angle brackets, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "Fix <script> issue") && !strings.Contains(resultText(r), `"returned":1`) {
		t.Errorf("expected the angle-bracket title to be found, got: %s", resultText(r))
	}
}

// TestListTasks_Q_CombinesWithAreaAndStatus covers q composing as an AND
// predicate alongside the existing area/status filters [F0930-20] — a row
// matching q alone but failing area must not appear.
func TestListTasks_Q_CombinesWithAreaAndStatus(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	matchBoth, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{Title: "shared-substring alpha", Area: "unsorted"})
	if err != nil {
		t.Fatalf("CreateTask matchBoth: %v", err)
	}
	_, err = s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{Title: "shared-substring beta", Area: "wbt"})
	if err != nil {
		t.Fatalf("CreateTask matchQOnly: %v", err)
	}

	r := callListTasks(t, s, map[string]any{"q": "shared-substring", "area": "unsorted"})
	if r.IsError {
		t.Fatalf("q+area combo should succeed, got: %s", resultText(r))
	}
	var page struct {
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &page); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, resultText(r))
	}
	if len(page.Tasks) != 1 || page.Tasks[0].ID != matchBoth.ID.String() {
		t.Errorf("expected exactly the area=unsorted match, got: %+v", page.Tasks)
	}
}
