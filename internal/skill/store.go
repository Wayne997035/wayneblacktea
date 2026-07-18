package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/likeescape"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (single-tenant legacy mode)
}

// New returns a Store backed by the given pool, scoped to the optional workspaceID.
// nil workspaceID = legacy unscoped mode.
func New(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: toUUID(workspaceID)}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

func toNullableUUID(id *string) pgtype.UUID {
	if id == nil || *id == "" {
		return pgtype.UUID{}
	}
	parsed, err := uuid.Parse(*id)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
}

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func parseStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func parseAnySlice(raw []byte) []any {
	if len(raw) == 0 {
		return []any{}
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		return []any{}
	}
	if out == nil {
		return []any{}
	}
	return out
}

const skillSelectCols = `id, workspace_id, name, description,
	triggers, steps, failure_modes, verification_checklist, examples, source_atom_ids,
	success_count, failure_count, last_used_at, created_at, updated_at`

// scanSkillRow reads a full skills row from pgx.Rows into a Skill.
func scanSkillRow(rows pgx.Rows) (*Skill, error) {
	var (
		s                Skill
		wsID             pgtype.UUID
		triggersRaw      []byte
		stepsRaw         []byte
		failureModesRaw  []byte
		verificationRaw  []byte
		examplesRaw      []byte
		sourceAtomIDsRaw []byte
		lastUsedAt       pgtype.Timestamptz
		createdAt        pgtype.Timestamptz
		updatedAt        pgtype.Timestamptz
	)
	err := rows.Scan(
		&s.ID,
		&wsID,
		&s.Name,
		&s.Description,
		&triggersRaw,
		&stepsRaw,
		&failureModesRaw,
		&verificationRaw,
		&examplesRaw,
		&sourceAtomIDsRaw,
		&s.SuccessCount,
		&s.FailureCount,
		&lastUsedAt,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning skill: %w", err)
	}
	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes).String()
		s.WorkspaceID = &id
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		s.LastUsedAt = &t
	}
	if createdAt.Valid {
		s.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		s.UpdatedAt = updatedAt.Time
	}
	s.Triggers = parseStringSlice(triggersRaw)
	s.Steps = parseStringSlice(stepsRaw)
	s.FailureModes = parseStringSlice(failureModesRaw)
	s.VerificationChecklist = parseStringSlice(verificationRaw)
	s.Examples = parseAnySlice(examplesRaw)
	s.SourceAtomIDs = parseStringSlice(sourceAtomIDsRaw)
	return &s, nil
}

// Add inserts a new skill and returns the persisted record.
func (s *Store) Add(ctx context.Context, p AddParams) (*Skill, error) {
	const q = `
		INSERT INTO skills
			(workspace_id, name, description,
			 triggers, steps, failure_modes, verification_checklist, examples, source_atom_ids)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb)
		RETURNING ` + skillSelectCols

	rows, err := s.pool.Query(
		ctx, q,
		s.workspaceID,
		p.Name,
		p.Description,
		jsonStr(p.Triggers),
		jsonStr(p.Steps),
		jsonStr(p.FailureModes),
		jsonStr(p.VerificationChecklist),
		jsonStr(p.Examples),
		jsonStr(p.SourceAtomIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("inserting skill: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("inserting skill: no row returned")
	}
	sk, err := scanSkillRow(rows)
	if err != nil {
		return nil, fmt.Errorf("inserting skill scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inserting skill rows.Err: %w", err)
	}
	return sk, nil
}

// Search returns skills whose name or description match f.Query (ILIKE), ordered
// by success_count DESC.
func (s *Store) Search(ctx context.Context, f SearchFilter) ([]*Skill, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 10
	}

	pattern := "%" + likeescape.Escape(f.Query) + "%"

	const q = `SELECT ` + skillSelectCols + ` FROM skills
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND (name ILIKE $2 ESCAPE '\' OR description ILIKE $2 ESCAPE '\')
		ORDER BY success_count DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, toNullableUUID(f.WorkspaceID), pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("searching skills: %w", err)
	}
	defer rows.Close()

	out := make([]*Skill, 0)
	for rows.Next() {
		sk, err := scanSkillRow(rows)
		if err != nil {
			return nil, fmt.Errorf("searching skills scan: %w", err)
		}
		out = append(out, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searching skills rows.Err: %w", err)
	}
	return out, nil
}

// IncrementSuccess increments success_count and sets last_used_at = NOW().
func (s *Store) IncrementSuccess(ctx context.Context, id string, workspaceID *string) (*Skill, error) {
	const q = `UPDATE skills
		SET success_count = success_count + 1,
		    last_used_at  = NOW(),
		    updated_at    = NOW()
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		RETURNING ` + skillSelectCols

	rows, err := s.pool.Query(ctx, q, id, toNullableUUID(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("incrementing skill success: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	sk, err := scanSkillRow(rows)
	if err != nil {
		return nil, fmt.Errorf("incrementing skill success scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("incrementing skill success rows.Err: %w", err)
	}
	return sk, nil
}

// UpdateFromOutcome increments the success or failure counter, appends an example
// entry, and refreshes last_used_at / updated_at.
func (s *Store) UpdateFromOutcome(ctx context.Context, p UpdateFromOutcomeParams, workspaceID *string) (*Skill, error) {
	example := map[string]string{
		"outcome_id": p.OutcomeID,
		"notes":      p.Notes,
		"at":         time.Now().UTC().Format(time.RFC3339),
	}
	exampleJSON := jsonStr(example)

	var q string
	if p.Success {
		q = `UPDATE skills
			SET success_count = success_count + 1,
			    examples      = examples || $3::jsonb,
			    last_used_at  = NOW(),
			    updated_at    = NOW()
			WHERE id = $1
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			RETURNING ` + skillSelectCols
	} else {
		q = `UPDATE skills
			SET failure_count = failure_count + 1,
			    examples      = examples || $3::jsonb,
			    last_used_at  = NOW(),
			    updated_at    = NOW()
			WHERE id = $1
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			RETURNING ` + skillSelectCols
	}

	rows, err := s.pool.Query(ctx, q, p.SkillID, toNullableUUID(workspaceID), "["+exampleJSON+"]")
	if err != nil {
		return nil, fmt.Errorf("updating skill from outcome: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	sk, err := scanSkillRow(rows)
	if err != nil {
		return nil, fmt.Errorf("updating skill from outcome scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("updating skill from outcome rows.Err: %w", err)
	}
	return sk, nil
}

// ListRelevant returns skills ordered by success_count DESC, last_used_at DESC,
// optionally filtered by query (name/description ILIKE).
func (s *Store) ListRelevant(ctx context.Context, workspaceID *string, query string, limit int) ([]*Skill, error) {
	if limit <= 0 {
		limit = 10
	}

	var q string
	var args []any
	if query == "" {
		q = `SELECT ` + skillSelectCols + ` FROM skills
			WHERE ($1::uuid IS NULL OR workspace_id = $1)
			ORDER BY success_count DESC, last_used_at DESC NULLS LAST
			LIMIT $2`
		args = []any{toNullableUUID(workspaceID), limit}
	} else {
		pattern := "%" + likeescape.Escape(query) + "%"
		q = `SELECT ` + skillSelectCols + ` FROM skills
			WHERE ($1::uuid IS NULL OR workspace_id = $1)
			  AND (name ILIKE $2 ESCAPE '\' OR description ILIKE $2 ESCAPE '\')
			ORDER BY success_count DESC, last_used_at DESC NULLS LAST
			LIMIT $3`
		args = []any{toNullableUUID(workspaceID), pattern, limit}
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing relevant skills: %w", err)
	}
	defer rows.Close()

	out := make([]*Skill, 0)
	for rows.Next() {
		sk, err := scanSkillRow(rows)
		if err != nil {
			return nil, fmt.Errorf("listing relevant skills scan: %w", err)
		}
		out = append(out, sk)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing relevant skills rows.Err: %w", err)
	}
	return out, nil
}
