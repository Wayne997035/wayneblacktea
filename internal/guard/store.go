package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// Store handles guard_events persistence.
// All write methods are fail-open: they log errors with slog.Warn but never
// return a non-nil error that would cause wbt-guard to exit non-zero.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore creates a Store backed by a pgxpool.
// OpenPool should be called first to obtain the pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// OpenPool opens a pgxpool for the guard store, reusing the existing TLS
// config logic from internal/storage so PGSSLROOTCERT / ServerName are
// handled correctly.
//
// On any error, OpenPool returns nil, nil (fail-open contract).
// The caller must check for nil before using the pool.
func OpenPool(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	if dbURL == "" {
		return nil, nil //nolint:nilnil // intentional: nil pool means DB unavailable; caller fails open
	}

	// Use the shared TLS config helper so we get the same CA-merge behaviour
	// as the main server (preserves pgx-derived ServerName per master 713ea57).
	// Read APP_ENV and PGSSLROOTCERT from the environment so production deploys
	// fail loudly if the CA cert is missing while local dev still works.
	tlsCfg, err := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		slog.Warn("guard: TLS config error — guard events will not be persisted", "err", err)
		return nil, nil //nolint:nilnil // intentional: nil pool means DB unavailable; caller fails open
	}

	pgcfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		slog.Warn("guard: invalid DB URL — guard events will not be persisted", "err", err)
		return nil, nil //nolint:nilnil // intentional: nil pool means DB unavailable; caller fails open
	}

	if tlsCfg != nil {
		if pgcfg.ConnConfig.TLSConfig == nil {
			pgcfg.ConnConfig.TLSConfig = tlsCfg
		} else {
			pgcfg.ConnConfig.TLSConfig.RootCAs = tlsCfg.RootCAs
			pgcfg.ConnConfig.TLSConfig.InsecureSkipVerify = false
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, pgcfg)
	if err != nil {
		slog.Warn("guard: DB connection failed — guard events will not be persisted", "err", err)
		return nil, nil //nolint:nilnil // intentional: nil pool means DB unavailable; caller fails open
	}
	return pool, nil
}

// Event is the data inserted into guard_events for each tool invocation.
type Event struct {
	SessionID  string
	ToolName   string
	ToolInput  json.RawMessage
	CWD        string
	RepoName   string
	RiskTier   RiskTier
	RiskReason string
	WouldDeny  bool
	Matcher    string
	BypassID   *uuid.UUID
}

const insertGuardEvent = `
INSERT INTO guard_events
    (id, created_at, session_id, tool_name, tool_input, cwd, repo_name,
     risk_tier, risk_reason, would_deny, matcher, bypass_id)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
`

// WriteEvent inserts a guard_events row.
// If the pool is nil (DB unavailable) or the INSERT fails, the error is
// logged at Warn level and nil is returned — fail-open.
func (s *Store) WriteEvent(ctx context.Context, ev Event) error {
	if s == nil || s.pool == nil {
		slog.Warn("guard: no DB pool — skipping guard_events write",
			"tool", ev.ToolName, "tier", ev.RiskTier)
		return nil
	}

	id := uuid.New()
	now := time.Now().UTC()

	// Ensure tool_input is valid JSON; fall back to null-equivalent JSON.
	toolInput := ev.ToolInput
	if len(toolInput) == 0 {
		toolInput = json.RawMessage(`{}`)
	}
	// Apply credential redaction BEFORE the INSERT. RedactToolInput
	// fail-opens (returns the original on error) so the persistence path
	// stays robust even if scrubbing breaks for an exotic payload.
	toolInput = RedactToolInput(toolInput)

	var sessionID *string
	if ev.SessionID != "" {
		sessionID = &ev.SessionID
	}
	var cwd *string
	if ev.CWD != "" {
		cwd = &ev.CWD
	}
	var repoName *string
	if ev.RepoName != "" {
		repoName = &ev.RepoName
	}
	var riskReason *string
	if ev.RiskReason != "" {
		riskReason = &ev.RiskReason
	}

	_, err := s.pool.Exec(ctx, insertGuardEvent,
		id,
		now,
		sessionID,
		ev.ToolName,
		toolInput,
		cwd,
		repoName,
		int16(ev.RiskTier),
		riskReason,
		ev.WouldDeny,
		ev.Matcher,
		ev.BypassID,
	)
	if err != nil {
		slog.Warn("guard: failed to write guard_event",
			"err", err,
			"tool", ev.ToolName,
			"tier", ev.RiskTier,
			"session", ev.SessionID,
		)
		return nil // fail-open: never propagate to caller
	}

	slog.Info("guard: event recorded",
		"id", id,
		"tool", ev.ToolName,
		"tier", ev.RiskTier,
		"would_deny", ev.WouldDeny,
		"matcher", ev.Matcher,
	)
	return nil
}

// Bypass represents a row from guard_bypasses.
type Bypass struct {
	ID        uuid.UUID
	Scope     string
	Target    string
	ToolName  *string
	Reason    string
	CreatedBy *string
	ExpiresAt *time.Time
}

// queryActiveBypasses fetches the most narrowly-scoped active bypass row.
//
//nolint:gosec // G101 false-positive: SQL query template (column "created_by" matches gosec's credential heuristic).
const queryActiveBypasses = `
SELECT id, scope, target, tool_name, reason, created_by, expires_at
FROM guard_bypasses
WHERE (expires_at IS NULL OR expires_at > NOW())
  AND (scope = $1 AND target = $2)
  AND (tool_name IS NULL OR tool_name = $3)
ORDER BY
  CASE scope
    WHEN 'file'   THEN 1
    WHEN 'dir'    THEN 2
    WHEN 'repo'   THEN 3
    WHEN 'global' THEN 4
    ELSE 5
  END
LIMIT 1
`

// FindBypass returns the most narrowly-scoped active bypass for the given
// (scope, target, toolName) tuple, or nil if no bypass applies.
// Fail-open: any DB error returns nil bypass.
func (s *Store) FindBypass(ctx context.Context, scope, target, toolName string) (*Bypass, error) {
	if s == nil || s.pool == nil {
		return nil, nil //nolint:nilnil // intentional: nil means no bypass found; caller fails open
	}

	row := s.pool.QueryRow(ctx, queryActiveBypasses, scope, target, toolName)

	var b Bypass
	var toolNameVal *string
	var createdBy *string
	var expiresAt *time.Time

	err := row.Scan(&b.ID, &b.Scope, &b.Target, &toolNameVal, &b.Reason, &createdBy, &expiresAt)
	if err != nil {
		// pgx returns pgx.ErrNoRows when no row matches — that's normal.
		return nil, nil //nolint:nilnil // intentional: nil means no bypass found; not an error
	}
	b.ToolName = toolNameVal
	b.CreatedBy = createdBy
	b.ExpiresAt = expiresAt
	return &b, nil
}

// insertBypass inserts a new guard_bypasses row.
//
//nolint:gosec // G101 false-positive: SQL template (column "created_by" matches credential heuristic).
const insertBypass = `
INSERT INTO guard_bypasses
    (id, created_at, expires_at, scope, target, tool_name, reason, created_by)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id
`

// AddBypass inserts a new bypass row.
// Returns the generated UUID on success.
func (s *Store) AddBypass(
	ctx context.Context,
	scope, target string,
	toolName *string,
	reason, createdBy string,
	expiresAt *time.Time,
) (uuid.UUID, error) {
	if s == nil || s.pool == nil {
		return uuid.UUID{}, fmt.Errorf("guard: DB pool unavailable")
	}
	if len([]rune(reason)) == 0 {
		return uuid.UUID{}, fmt.Errorf("guard: bypass reason must not be empty")
	}

	id := uuid.New()
	now := time.Now().UTC()

	var createdByVal *string
	if createdBy != "" {
		createdByVal = &createdBy
	}

	var returnedID uuid.UUID
	err := s.pool.QueryRow(ctx, insertBypass,
		id, now, expiresAt, scope, target, toolName, reason, createdByVal,
	).Scan(&returnedID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("guard: inserting bypass: %w", err)
	}
	return returnedID, nil
}

// listBypasses returns active bypasses optionally filtered by scope.
//
//nolint:gosec // G101 false-positive: SQL template (column "created_by" matches credential heuristic).
const listBypasses = `
SELECT id, scope, target, tool_name, reason, created_by, expires_at
FROM guard_bypasses
WHERE ($1 = '' OR scope = $1)
  AND (expires_at IS NULL OR expires_at > NOW())
ORDER BY created_at DESC
`

// ListBypasses returns active bypasses, optionally filtered by scope.
// Pass an empty string for scope to list all active bypasses.
func (s *Store) ListBypasses(ctx context.Context, scope string) ([]Bypass, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("guard: DB pool unavailable")
	}

	rows, err := s.pool.Query(ctx, listBypasses, scope)
	if err != nil {
		return nil, fmt.Errorf("guard: listing bypasses: %w", err)
	}
	defer rows.Close()

	var bypasses []Bypass
	for rows.Next() {
		var b Bypass
		var toolNameVal *string
		var createdBy *string
		var expiresAt *time.Time
		if err := rows.Scan(&b.ID, &b.Scope, &b.Target, &toolNameVal, &b.Reason, &createdBy, &expiresAt); err != nil {
			return nil, fmt.Errorf("guard: scanning bypass: %w", err)
		}
		b.ToolName = toolNameVal
		b.CreatedBy = createdBy
		b.ExpiresAt = expiresAt
		bypasses = append(bypasses, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("guard: rows error: %w", err)
	}
	return bypasses, nil
}

// revokeBypass deletes a guard_bypasses row by id.
//
//nolint:gosec // G101 false-positive: SQL template; const name "Bypass" matches gosec's credential heuristic.
const revokeBypass = `
DELETE FROM guard_bypasses WHERE id = $1
`

// RevokeBypass deletes a bypass by ID.
func (s *Store) RevokeBypass(ctx context.Context, id uuid.UUID) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("guard: DB pool unavailable")
	}
	ct, err := s.pool.Exec(ctx, revokeBypass, id)
	if err != nil {
		return fmt.Errorf("guard: revoking bypass %s: %w", id, err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("guard: bypass %s not found", id)
	}
	return nil
}
