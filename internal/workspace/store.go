package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/pgconv"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store handles all database operations for the Workspace bounded context.
type Store struct {
	q           *db.Queries
	dbtx        db.DBTX // retained for hand-written queries outside of sqlc
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given DBTX scoped to the optional
// workspace. nil workspaceID = legacy unscoped mode.
func NewStore(dbtx db.DBTX, workspaceID *uuid.UUID) *Store {
	return &Store{q: db.New(dbtx), dbtx: dbtx, workspaceID: pgconv.ToUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), dbtx: tx, workspaceID: s.workspaceID}
}

// ActiveRepos returns all repos with status = 'active', ordered by last_activity.
func (s *Store) ActiveRepos(ctx context.Context) ([]db.Repo, error) {
	rows, err := s.q.ListActiveRepos(ctx, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing active repos: %w", err)
	}
	return rows, nil
}

// RepoByName returns a single repo by unique name, or ErrNotFound.
func (s *Store) RepoByName(ctx context.Context, name string) (*db.Repo, error) {
	row, err := s.q.GetRepoByName(ctx, db.GetRepoByNameParams{
		Name:        name,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying repo %q: %w", name, err)
	}
	return &row, nil
}

// RepoByID returns a single repo by primary key UUID, scoped to the configured
// workspace. Returns ErrNotFound when no row matches. Hand-written query (not
// sqlc) so the change is contained to the Go store layer.
func (s *Store) RepoByID(ctx context.Context, id uuid.UUID) (*db.Repo, error) {
	const q = `SELECT id, name, path, description, language, status,
		current_branch, known_issues, next_planned_step, last_activity,
		created_at, updated_at, workspace_id
		FROM repos
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`
	row := s.dbtx.QueryRow(ctx, q, id, s.workspaceID)
	var r db.Repo
	if err := row.Scan(
		&r.ID, &r.Name, &r.Path, &r.Description, &r.Language, &r.Status,
		&r.CurrentBranch, &r.KnownIssues, &r.NextPlannedStep, &r.LastActivity,
		&r.CreatedAt, &r.UpdatedAt, &r.WorkspaceID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying repo by id %s: %w", id, err)
	}
	return &r, nil
}

// GetModelPreference returns the workspace's stored model_preference, or
// DefaultModelPreference when no row exists (or in legacy unscoped mode).
func (s *Store) GetModelPreference(ctx context.Context) (string, error) {
	if !s.workspaceID.Valid {
		return DefaultModelPreference, nil
	}
	const q = `SELECT model_preference FROM workspace_preferences WHERE workspace_id = $1`
	var model string
	err := s.dbtx.QueryRow(ctx, q, s.workspaceID).Scan(&model)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultModelPreference, nil
	}
	if err != nil {
		return "", fmt.Errorf("querying model preference: %w", err)
	}
	return model, nil
}

// UpsertModelPreference stores the workspace's model_preference. Returns
// ErrInvalidModel when model is not allowed.
func (s *Store) UpsertModelPreference(ctx context.Context, model string) error {
	if !IsAllowedModel(model) {
		return ErrInvalidModel
	}
	if !s.workspaceID.Valid {
		return fmt.Errorf("UpsertModelPreference requires a non-nil workspaceID")
	}
	const q = `INSERT INTO workspace_preferences (workspace_id, model_preference)
		VALUES ($1, $2)
		ON CONFLICT (workspace_id) DO UPDATE SET
			model_preference = EXCLUDED.model_preference,
			updated_at = NOW()`
	if _, err := s.dbtx.Exec(ctx, q, s.workspaceID, model); err != nil {
		return fmt.Errorf("upserting model preference: %w", err)
	}
	return nil
}

// UpsertRepo creates or updates a repo entry.
// workspaceID must be non-nil: after migration 000028 the unique constraint is
// (workspace_id, name), so ON CONFLICT does not fire for NULL workspace_id.
func (s *Store) UpsertRepo(ctx context.Context, p UpsertRepoParams) (*db.Repo, error) {
	if !s.workspaceID.Valid {
		return nil, fmt.Errorf("UpsertRepo requires a non-nil workspaceID after migration 000028")
	}
	row, err := s.q.UpsertRepo(ctx, db.UpsertRepoParams{
		Name: p.Name,
		// ToTextPtr (not ToText): the UPDATE branch's CASE WHEN checks
		// whether the bound parameter itself is NULL to decide
		// preserve-vs-replace (Ω6) — ToText's "" -> NULL collapse would
		// make an explicit empty-string clear indistinguishable from
		// omission, the exact bug this fixes.
		Path:            pgconv.ToTextPtr(p.Path),
		Description:     pgconv.ToTextPtr(p.Description),
		Language:        pgconv.ToTextPtr(p.Language),
		CurrentBranch:   pgconv.ToTextPtr(p.CurrentBranch),
		KnownIssues:     p.KnownIssues,
		NextPlannedStep: pgconv.ToTextPtr(p.NextPlannedStep),
		WorkspaceID:     s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("upserting repo %q: %w", p.Name, err)
	}
	return &row, nil
}
