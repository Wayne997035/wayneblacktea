package gtd

import (
	"context"
	"regexp"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
)

// CommitSHARe matches a 40-hex-character commit SHA.
// Exported so both handler and mcp packages can reference a single definition.
var CommitSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ArtifactStore is the minimal store interface required by ApplyArtifactSideEffects.
type ArtifactStore interface {
	UpdateTask(ctx context.Context, id uuid.UUID, p UpdateTaskParams) (*db.Task, error)
}

// ApplyArtifactSideEffects detects whether artifact is a GitHub PR URL or a
// 40-hex commit SHA and applies the corresponding side-effect update on the
// task. Returns the updated task on success, nil if no side-effect applied or
// the update failed (caller uses the original completed task in that case).
// SECURITY: only stores the URL string — no HTTP fetch is ever made.
func ApplyArtifactSideEffects(ctx context.Context, store ArtifactStore, id uuid.UUID, task *db.Task, artifact string) *db.Task {
	artifact = strings.TrimSpace(artifact)
	if artifact == "" {
		return nil
	}

	var up UpdateTaskParams
	if validator.GitHubPRURLRe.MatchString(artifact) {
		up.PRUrl = &artifact
	} else if CommitSHARe.MatchString(artifact) {
		newSHAs := make([]string, len(task.CommitSHAs)+1)
		copy(newSHAs, task.CommitSHAs)
		newSHAs[len(task.CommitSHAs)] = artifact
		up.CommitSHAs = newSHAs
	}
	if up.PRUrl == nil && up.CommitSHAs == nil {
		return nil
	}
	updated, err := store.UpdateTask(ctx, id, up)
	if err != nil {
		return nil
	}
	return updated
}
