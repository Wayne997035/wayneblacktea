// Package arch manages architecture snapshots for projects.
// Claude stores a snapshot (summary + file map) after reading 3+ internal/
// files, so subsequent sessions skip the re-read.
package arch

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when no snapshot exists for a given slug.
var ErrNotFound = errors.New("arch: snapshot not found")

// Snapshot is the stored architecture description for one project.
type Snapshot struct {
	ID            string            `json:"id"`
	Slug          string            `json:"slug"`
	Summary       string            `json:"summary"`
	FileMap       map[string]string `json:"file_map,omitempty"`
	LastCommitSHA string            `json:"last_commit_sha"`
	UpdatedAt     time.Time         `json:"updated_at"`
	// Stale is populated at the MCP layer by comparing LastCommitSHA with the
	// live HEAD. The store always sets it false; callers add their own logic.
	Stale bool `json:"stale"`
}

// UpsertParams collects the inputs for UpsertSnapshot.
type UpsertParams struct {
	Slug    string
	Summary string
	// FileMap is patch semantics, not replace semantics: nil means the
	// caller did not supply a file_map at all, and the store MUST leave
	// whatever is already stored untouched (a brand-new row still gets
	// {}, since there is nothing to preserve). A non-nil pointer — even
	// one pointing at an empty map — is an explicit value and REPLACES
	// the stored file_map outright, including clearing it to {}.
	//
	// This exists because the core MCP protocol is read-then-write
	// (get_project_arch, read changed files, upsert_project_arch) and
	// get_project_arch defaults to omitting file_map for token-diet
	// reasons (W2). An agent that follows the protocol literally and
	// omits file_map on the write-back must not be able to silently wipe
	// an existing map it never saw — see security review PR #157 M-3,
	// reproduced on both backends (3 entries -> 0 on upsert without
	// file_map, before this type existed).
	FileMap       *map[string]string
	LastCommitSHA string
}

// StoreIface is the backend-agnostic contract for the arch bounded context.
type StoreIface interface {
	// UpsertSnapshot inserts or updates the architecture snapshot for slug.
	UpsertSnapshot(ctx context.Context, p UpsertParams) (*Snapshot, error)
	// GetSnapshot returns the snapshot for the given slug.
	// Returns ErrNotFound when no snapshot exists.
	GetSnapshot(ctx context.Context, slug string) (*Snapshot, error)
}
