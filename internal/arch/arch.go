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
	FileMap       map[string]string `json:"file_map"`
	LastCommitSHA string            `json:"last_commit_sha"`
	UpdatedAt     time.Time         `json:"updated_at"`
	// Stale is populated at the MCP layer by comparing LastCommitSHA with the
	// live HEAD. The store always sets it false; callers add their own logic.
	Stale bool `json:"stale"`
}

// UpsertParams collects the inputs for UpsertSnapshot.
type UpsertParams struct {
	Slug          string
	Summary       string
	FileMap       map[string]string
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
