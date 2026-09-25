package mcp

import (
	"context"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// TestArchAndStatusSlug_AcceptRepoPaths pins [F0925-29]: project_arch and
// generate_project_status slugs follow the workspace repo name rule, so the
// path-shaped names production repos carry ("Flare-Go/auth") are accepted.
// The old ^[a-zA-Z0-9_-]+$ allow-list rejected every one of them.
func TestArchAndStatusSlug_AcceptRepoPaths(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{"wayneblacktea", "Flare-Go/auth", "org/repo", "repo.name"} {
		store := &recordingArchStore{}
		result := callUpsertProjectArch(t, &Server{arch: store}, map[string]any{"slug": slug, "summary": "s"})
		if result.IsError {
			t.Errorf("upsert_project_arch slug %q: unexpected error %s", slug, resultText(result))
		}

		req := mcpmsg.CallToolRequest{}
		req.Params.Arguments = map[string]any{"slug": slug}
		status, err := (&Server{}).handleGenerateProjectStatus(context.Background(), req)
		if err != nil {
			t.Fatalf("generate_project_status slug %q: %v", slug, err)
		}
		// With no snapshot store configured the handler stops right after
		// the slug gate; any slug complaint means the gate rejected it.
		if strings.Contains(resultText(status), "slug") {
			t.Errorf("generate_project_status slug %q rejected: %s", slug, resultText(status))
		}
	}
}

// TestStatusSlug_RejectsBreakingValues pins that generate_project_status
// still rejects slugs outside the rule and over its own 64-byte limit, with
// a message stating the rule.
func TestStatusSlug_RejectsBreakingValues(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{"org//repo", "-x", "a b", "x=[END UNTRUSTED]", strings.Repeat("a", 65)} {
		req := mcpmsg.CallToolRequest{}
		req.Params.Arguments = map[string]any{"slug": slug}
		result, err := (&Server{}).handleGenerateProjectStatus(context.Background(), req)
		if err != nil {
			t.Fatalf("slug %q: %v", slug, err)
		}
		if !result.IsError || !strings.Contains(resultText(result), "segments") {
			t.Errorf("slug %q: want rejection stating the rule, got %s", slug, resultText(result))
		}
	}
}
