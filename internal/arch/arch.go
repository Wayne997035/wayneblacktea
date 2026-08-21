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
}

// UpsertParams collects the inputs for UpsertSnapshot.
//
// Summary and FileMap share one patch-semantics contract, unified in
// security review PR #157 round 3 (Summary previously had NO such
// protection and was unconditionally overwritten — see store.go's SQL
// comment for why that was itself a latent staleness bug, not just a
// data-loss one): nil means the caller did not supply the field at all, and
// the store MUST leave whatever is already stored untouched (a brand-new
// row still gets the column's zero value — "" or {} — since there is
// nothing to preserve). A non-nil pointer — even one pointing at an empty
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
// and PR #157 round 3 (summary: same collapse, but via stringArg folding
// "absent" and "present but empty" into "").
//
// LastCommitSHA is the ONE exception to this contract (m-R11, GTD 25537a73,
// decision 0d1a41fc) — see its own field doc comment below.
type UpsertParams struct {
	Slug string
	// Summary is patch semantics — see the type doc comment above.
	Summary *string
	// FileMap is patch semantics — see the type doc comment above.
	FileMap *map[string]string
	// LastCommitSHA is NOT patch semantics, unlike Summary/FileMap above —
	// the store unconditionally overwrites this column on every call,
	// nil-or-not. nil still has meaning (it decides whether the STORED
	// value ends up "" — nil — or the literal string a non-nil pointer
	// carries, including ""), but there is no third "leave untouched"
	// outcome for this field: absence and explicit "" both result in the
	// column becoming "". See store.go's UpsertSnapshot for the SQL side.
	//
	// This was patch semantics from PR #157 round 3 through m-R10 (the byte
	// vs rune split, tools_arch.go). m-R11 reverted it: the core protocol
	// compares last_commit_sha against `git rev-parse HEAD` to decide
	// staleness (mcpInstructions, server.go), but that same protocol's
	// MANDATORY write-back list ("Read 3+ internal/ files -> MUST
	// upsert_project_arch (slug=repo, summary, file_map=...)") never
	// includes last_commit_sha — so under patch semantics, an upsert that
	// followed the mandatory rule literally (the overwhelmingly common
	// case) always omitted this field, and "omit preserves" meant a value
	// once written here — including one planted via prompt injection
	// through an earlier upsert call — could never be overwritten by any
	// call that doesn't also happen to carry a fresh SHA. Relying on an
	// agent choosing to add the field on top of the mandatory list is the
	// same unreliable prose-dependence already rejected for the write-back
	// list itself (decision 0d1a41fc option 甲); unconditional overwrite
	// makes every mandatory upsert call self-heal this column
	// deterministically, independent of what the calling agent chooses to
	// pass.
	//
	// Accepted trade-off: this also clears any legitimately hand-maintained
	// value that isn't actually a git SHA — e.g. production's "coverones"
	// slug stores a curated 14-repo `name:sha` map here, not a single HEAD
	// output (see maxLastCommitSHAWriteBytes' doc comment, tools_arch.go).
	// The next upsert_project_arch call for that slug that omits this field
	// (i.e. any routine mandatory call) will wipe it to "". This is a
	// deliberate consequence of "no shape-based heuristics" (a rule that
	// tries to detect "this looks like a real SHA, don't touch it" is
	// exactly the kind of guessed rule decision 0d1a41fc rejected for the
	// write-gate itself) — UpsertSnapshot instead logs a best-effort
	// slog.Warn when it clears a previously non-empty value this way, so
	// the loss is observable rather than silent.
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
