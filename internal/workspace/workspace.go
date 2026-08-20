package workspace

import "errors"

var (
	ErrNotFound = errors.New("workspace: not found")
	ErrConflict = errors.New("workspace: conflict")
)

// UpsertRepoParams holds parameters for creating or updating a repo entry.
//
// Path, Description, Language, CurrentBranch, NextPlannedStep are
// presence-aware (Ω6, 2026-08-20-mcp-surface-spec.md): nil preserves the
// stored value; a non-nil pointer — even to "" — explicitly replaces it,
// matching upsert_project_arch.summary/file_map's established convention.
// Previously these were plain strings and every field a caller didn't
// re-supply on a sync_repo call was silently wiped to "" on every backend.
type UpsertRepoParams struct {
	Name            string
	Path            *string
	Description     *string
	Language        *string
	CurrentBranch   *string
	KnownIssues     []string // nil → preserve; non-nil (incl. empty slice) → replace
	NextPlannedStep *string
}
