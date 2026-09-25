package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// countingWorkspaceStore records UpsertRepo calls so a test can prove the
// handler rejected a name before reaching the store.
type countingWorkspaceStore struct {
	upserts int
}

var _ workspace.StoreIface = (*countingWorkspaceStore)(nil)

func (c *countingWorkspaceStore) ActiveRepos(context.Context) ([]db.Repo, error) { return nil, nil }
func (c *countingWorkspaceStore) RepoByName(context.Context, string) (*db.Repo, error) {
	return nil, workspace.ErrNotFound
}

func (c *countingWorkspaceStore) RepoByID(context.Context, uuid.UUID) (*db.Repo, error) {
	return nil, workspace.ErrNotFound
}

func (c *countingWorkspaceStore) UpsertRepo(_ context.Context, p workspace.UpsertRepoParams) (*db.Repo, error) {
	c.upserts++
	return &db.Repo{Name: p.Name}, nil
}

func (c *countingWorkspaceStore) GetModelPreference(context.Context) (string, error)  { return "", nil }
func (c *countingWorkspaceStore) UpsertModelPreference(context.Context, string) error { return nil }

func syncRepoRequest(name string) mcpmsg.CallToolRequest {
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{"name": name}
	return req
}

// TestHandleSyncRepo_RejectsInvalidName pins that sync_repo applies the
// workspace repo name rule before the store is called.
func TestHandleSyncRepo_RejectsInvalidName(t *testing.T) {
	t.Parallel() // [F0925-29]
	for _, name := range []string{"../x", "-x/y", "a//b", "a b", ".claude"} {
		store := &countingWorkspaceStore{}
		s := &Server{workspace: store}
		result, err := s.handleSyncRepo(context.Background(), syncRepoRequest(name))
		if err != nil {
			t.Fatalf("handleSyncRepo(%q): %v", name, err)
		}
		if !result.IsError {
			t.Errorf("handleSyncRepo(%q): want tool error, got %s", name, resultText(result))
		}
		if !strings.Contains(resultText(result), "repo_name must be") {
			t.Errorf("handleSyncRepo(%q): error %q does not state the rule", name, resultText(result))
		}
		if store.upserts != 0 {
			t.Errorf("handleSyncRepo(%q): store called %d times, want 0", name, store.upserts)
		}
	}
}

// TestHandleSyncRepo_AcceptsRepoPaths pins that path-shaped production names
// still reach the store.
func TestHandleSyncRepo_AcceptsRepoPaths(t *testing.T) {
	t.Parallel() // [F0925-29]
	for _, name := range []string{"wayneblacktea", "Flare-Go/auth", "_project"} {
		store := &countingWorkspaceStore{}
		s := &Server{workspace: store}
		result, err := s.handleSyncRepo(context.Background(), syncRepoRequest(name))
		if err != nil {
			t.Fatalf("handleSyncRepo(%q): %v", name, err)
		}
		if result.IsError {
			t.Errorf("handleSyncRepo(%q): unexpected tool error %s", name, resultText(result))
		}
		if store.upserts != 1 {
			t.Errorf("handleSyncRepo(%q): store called %d times, want 1", name, store.upserts)
		}
	}
}
