package mcp

import (
	"context"
	"testing"
	"time"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// TestDisciplineMiddleware_RepoNameStoredEmptyWhenInvalid pins [F0925-29]
// for the discipline audit writer: it records every tool call before the
// tool's own validation runs, so a repo_name argument breaking the workspace
// repo name rule is audited with repo_name empty (the row is still written),
// while a compliant one is kept.
func TestDisciplineMiddleware_RepoNameStoredEmptyWhenInvalid(t *testing.T) {
	t.Parallel()
	for arg, want := range map[string]string{"../x": "", "_project/.claude": "", "Flare-Go/auth": "Flare-Go/auth"} {
		disc := &captureDisciplineStore{}
		srv := &Server{discipline: disc, sessionID: "test-session-repo"}
		handler := srv.disciplineMiddleware()(func(context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			return textResult("ok", false), nil
		})
		req := mcpmsg.CallToolRequest{}
		req.Params.Name = testTool
		req.Params.Arguments = map[string]any{"repo_name": arg}
		if _, err := handler(context.Background(), req); err != nil {
			t.Fatalf("handler: %v", err)
		}
		deadline := time.Now().Add(time.Second)
		for len(disc.snapshot()) == 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		got := disc.snapshot()
		if len(got) != 1 {
			t.Fatalf("repo_name %q: want 1 audit row, got %d", arg, len(got))
		}
		if got[0].RepoName != want {
			t.Errorf("repo_name %q: audited %q, want %q", arg, got[0].RepoName, want)
		}
	}
}
