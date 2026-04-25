package workspace

import "errors"

var (
	ErrNotFound = errors.New("workspace: not found")
	ErrConflict = errors.New("workspace: conflict")
)

// UpsertRepoParams holds parameters for creating or updating a repo entry.
type UpsertRepoParams struct {
	Name            string
	Path            string
	Description     string
	Language        string
	CurrentBranch   string
	KnownIssues     []string
	NextPlannedStep string
}
