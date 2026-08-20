package gtd

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// mockArtifactStore is a minimal ArtifactStore for unit tests.
type mockArtifactStore struct {
	updated   *UpdateTaskParams
	returnErr error
}

func (m *mockArtifactStore) UpdateTask(_ context.Context, _ uuid.UUID, p UpdateTaskParams) (*db.Task, error) {
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	m.updated = &p
	if p.AppendCommitSHA != nil {
		return &db.Task{CommitSHAs: []string{*p.AppendCommitSHA}}, nil
	}
	return &db.Task{}, nil
}

func TestApplyArtifactSideEffects_PRUrl(t *testing.T) {
	store := &mockArtifactStore{}
	id := uuid.New()
	task := &db.Task{}
	artifact := "https://github.com/org/repo/pull/42"

	result := ApplyArtifactSideEffects(context.Background(), store, id, task, artifact)
	if result == nil {
		t.Fatal("expected non-nil result for PR URL artifact")
	}
	if store.updated == nil || store.updated.PRUrl == nil {
		t.Fatal("expected PRUrl to be set in UpdateTask params")
	}
	if *store.updated.PRUrl != artifact {
		t.Errorf("PRUrl = %q, want %q", *store.updated.PRUrl, artifact)
	}
}

// TestApplyArtifactSideEffects_CommitSHA pins P7: the side effect now passes
// a single AppendCommitSHA value for the store to append atomically at the
// SQL layer, rather than reading task.CommitSHAs and building a merged array
// in Go (the TOCTOU-prone pattern this fix replaces — see
// ApplyArtifactSideEffects' doc comment). The passed-in task's existing
// CommitSHAs is deliberately irrelevant to this function's output now: it is
// no longer read at all, which is exactly what closes the race.
func TestApplyArtifactSideEffects_CommitSHA(t *testing.T) {
	store := &mockArtifactStore{}
	id := uuid.New()
	sha := "a3f1c2e4b5d6789012345678901234567890abcd"
	task := &db.Task{CommitSHAs: []string{"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}}

	result := ApplyArtifactSideEffects(context.Background(), store, id, task, sha)
	if result == nil {
		t.Fatal("expected non-nil result for commit SHA artifact")
	}
	if store.updated == nil || store.updated.AppendCommitSHA == nil {
		t.Fatal("expected AppendCommitSHA to be set in UpdateTask params")
	}
	if *store.updated.AppendCommitSHA != sha {
		t.Errorf("AppendCommitSHA = %q, want %q", *store.updated.AppendCommitSHA, sha)
	}
}

func TestApplyArtifactSideEffects_EmptyArtifact(t *testing.T) {
	store := &mockArtifactStore{}
	result := ApplyArtifactSideEffects(context.Background(), store, uuid.New(), &db.Task{}, "")
	if result != nil {
		t.Errorf("expected nil for empty artifact, got non-nil")
	}
	if store.updated != nil {
		t.Error("expected UpdateTask not to be called for empty artifact")
	}
}

func TestApplyArtifactSideEffects_NonMatchingArtifact(t *testing.T) {
	store := &mockArtifactStore{}
	result := ApplyArtifactSideEffects(context.Background(), store, uuid.New(), &db.Task{}, "commit SHA on master via PR #42")
	if result != nil {
		t.Errorf("expected nil for non-matching artifact, got non-nil")
	}
	if store.updated != nil {
		t.Error("expected UpdateTask not to be called for non-matching artifact")
	}
}

func TestApplyArtifactSideEffects_StoreError(t *testing.T) {
	store := &mockArtifactStore{returnErr: errors.New("db error")}
	artifact := "https://github.com/org/repo/pull/99"
	result := ApplyArtifactSideEffects(context.Background(), store, uuid.New(), &db.Task{}, artifact)
	if result != nil {
		t.Errorf("expected nil when store returns error, got non-nil")
	}
}
