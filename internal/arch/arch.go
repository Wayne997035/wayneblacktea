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
//
// Summary, FileMap and LastCommitSHA all share one patch-semantics contract,
// unified in security review PR #157 round 3 (Summary/LastCommitSHA
// previously had NO such protection and were unconditionally overwritten —
// see store.go's SQL comment for why that was itself a latent staleness bug,
// not just a data-loss one): nil means the caller did not supply the field
// at all, and the store MUST leave whatever is already stored untouched (a
// brand-new row still gets the column's zero value — "" or {} — since there
// is nothing to preserve). A non-nil pointer — even one pointing at an empty
// string/map — is an explicit value and REPLACES the stored value outright,
// including clearing it.
//
// This exists because the core MCP protocol is read-then-write
// (get_project_arch, read changed files, upsert_project_arch) and
// get_project_arch defaults to omitting file_map for token-diet reasons
// (W2). An agent that follows the protocol literally and omits a field on
// the write-back must not be able to silently wipe a value it never saw —
// see security review PR #157 M-3 (file_map: 3 entries -> 0 on upsert
// without file_map, reproduced on both backends before this type existed)
// and PR #157 round 3 (last_commit_sha / summary: same collapse, but via
// stringArg folding "absent" and "present but empty" into "").
type UpsertParams struct {
	Slug string
	// Summary is patch semantics — see the type doc comment above.
	Summary *string
	// FileMap is patch semantics — see the type doc comment above.
	FileMap *map[string]string
	// LastCommitSHA is patch semantics — see the type doc comment above.
	// This one matters beyond plain data loss: the core protocol compares
	// last_commit_sha against `git rev-parse HEAD` to decide whether a
	// snapshot is stale (mcpInstructions, server.go). Wiping it to "" on
	// every upsert that omits it would not just lose data, it would make
	// that staleness check permanently wrong in whichever direction ""
	// compares (e.g. always "stale", forcing a full re-read every time).
	LastCommitSHA *string
}

// StoreIface is the backend-agnostic contract for the arch bounded context.
type StoreIface interface {
	// UpsertSnapshot inserts or updates the architecture snapshot for slug.
	UpsertSnapshot(ctx context.Context, p UpsertParams) (*Snapshot, error)
	// GetSnapshot returns the snapshot for the given slug.
	// Returns ErrNotFound when no snapshot exists.
	GetSnapshot(ctx context.Context, slug string) (*Snapshot, error)
}
