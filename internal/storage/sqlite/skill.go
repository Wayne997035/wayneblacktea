package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/likeescape"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/google/uuid"
)

// SkillStore is the SQLite-backed implementation of skill.StoreIface.
type SkillStore struct {
	db *DB
}

// NewSkillStore wraps an open DB into a SkillStore.
func NewSkillStore(d *DB) *SkillStore {
	return &SkillStore{db: d}
}

// Compile-time guarantee against drift from skill.StoreIface.
var _ skill.StoreIface = (*SkillStore)(nil)

const skillSelectCols = `id, workspace_id, name, description,
	triggers, steps, failure_modes, verification_checklist, examples, source_atom_ids,
	success_count, failure_count, last_used_at, created_at, updated_at`

// skillRawRow holds raw scan variables for a skills row.
type skillRawRow struct {
	idStr            string
	wsIDNS           sql.NullString
	name             string
	description      string
	triggersStr      string
	stepsStr         string
	failureModesStr  string
	verificationStr  string
	examplesStr      string
	sourceAtomIDsStr string
	successCount     int
	failureCount     int
	lastUsedNS       sql.NullString
	createdNS        sql.NullString
	updatedNS        sql.NullString
}

func scanSkillRow(scan func(...any) error) (skill.Skill, error) {
	var r skillRawRow
	err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.name,
		&r.description,
		&r.triggersStr,
		&r.stepsStr,
		&r.failureModesStr,
		&r.verificationStr,
		&r.examplesStr,
		&r.sourceAtomIDsStr,
		&r.successCount,
		&r.failureCount,
		&r.lastUsedNS,
		&r.createdNS,
		&r.updatedNS,
	)
	if err != nil {
		return skill.Skill{}, err
	}
	return parseSkillRow(r), nil
}

func parseSkillRow(r skillRawRow) skill.Skill {
	sk := skill.Skill{
		ID:           r.idStr,
		Name:         r.name,
		Description:  r.description,
		SuccessCount: r.successCount,
		FailureCount: r.failureCount,
	}
	if r.wsIDNS.Valid && r.wsIDNS.String != "" {
		s := r.wsIDNS.String
		sk.WorkspaceID = &s
	}
	sk.LastUsedAt = parseTimestamptzToPtr(r.lastUsedNS)
	if t := parseTimestamptz(r.createdNS); t.Valid {
		sk.CreatedAt = t.Time
	}
	if t := parseTimestamptz(r.updatedNS); t.Valid {
		sk.UpdatedAt = t.Time
	}
	sk.Triggers = parseStringSliceSQLite(r.triggersStr)
	sk.Steps = parseStringSliceSQLite(r.stepsStr)
	sk.FailureModes = parseStringSliceSQLite(r.failureModesStr)
	sk.VerificationChecklist = parseStringSliceSQLite(r.verificationStr)
	sk.Examples = parseAnySliceSQLite(r.examplesStr)
	sk.SourceAtomIDs = parseStringSliceSQLite(r.sourceAtomIDsStr)
	return sk
}

func parseAnySliceSQLite(s string) []any {
	if s == "" || s == "[]" || s == jsonNullLiteral {
		return []any{}
	}
	var out []any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []any{}
	}
	if out == nil {
		return []any{}
	}
	return out
}

func encodeAny(v []any) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// Add inserts a new skill and returns the persisted record.
func (s *SkillStore) Add(ctx context.Context, p skill.AddParams) (*skill.Skill, error) {
	id := uuid.New().String()
	now := nowRFC3339()

	if p.Triggers == nil {
		p.Triggers = []string{}
	}
	if p.Steps == nil {
		p.Steps = []string{}
	}
	if p.FailureModes == nil {
		p.FailureModes = []string{}
	}
	if p.VerificationChecklist == nil {
		p.VerificationChecklist = []string{}
	}
	if p.Examples == nil {
		p.Examples = []any{}
	}
	if p.SourceAtomIDs == nil {
		p.SourceAtomIDs = []string{}
	}

	const q = `INSERT INTO skills
		(id, workspace_id, name, description,
		 triggers, steps, failure_modes, verification_checklist, examples, source_atom_ids,
		 created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?11)`

	_, err := s.db.conn.ExecContext(
		ctx, q,
		id,
		s.db.workspaceArg(),
		p.Name,
		p.Description,
		encodeStrings(p.Triggers),
		encodeStrings(p.Steps),
		encodeStrings(p.FailureModes),
		encodeStrings(p.VerificationChecklist),
		encodeAny(p.Examples),
		encodeStrings(p.SourceAtomIDs),
		now,
	)
	if err != nil {
		return nil, errWrap("SkillStore.Add", err)
	}
	return s.getByID(ctx, id)
}

// Search returns skills whose name or description match f.Query (LIKE).
func (s *SkillStore) Search(ctx context.Context, f skill.SearchFilter) ([]*skill.Skill, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 10
	}

	wsArg := stringToNullableArg(f.WorkspaceID)
	pattern := "%" + likeescape.Escape(f.Query) + "%"

	const q = `SELECT ` + skillSelectCols + ` FROM skills
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND (name LIKE ?2 ESCAPE '\' OR description LIKE ?2 ESCAPE '\')
		ORDER BY success_count DESC
		LIMIT ?3`

	rows, err := s.db.conn.QueryContext(ctx, q, wsArg, pattern, limit)
	if err != nil {
		return nil, errWrap("SkillStore.Search", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*skill.Skill, 0)
	for rows.Next() {
		sk, err := scanSkillRow(rows.Scan)
		if err != nil {
			return nil, errWrap("SkillStore.Search scan", err)
		}
		out = append(out, &sk)
	}
	return out, errWrap("SkillStore.Search iter", rows.Err())
}

// IncrementSuccess increments success_count and sets last_used_at = now.
func (s *SkillStore) IncrementSuccess(ctx context.Context, id string, workspaceID *string) (*skill.Skill, error) {
	wsArg := stringToNullableArg(workspaceID)
	now := nowRFC3339()

	const q = `UPDATE skills
		SET success_count = success_count + 1,
		    last_used_at  = ?3,
		    updated_at    = ?3
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`

	res, err := s.db.conn.ExecContext(ctx, q, id, wsArg, now)
	if err != nil {
		return nil, errWrap("SkillStore.IncrementSuccess", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, skill.ErrNotFound
	}
	return s.getByID(ctx, id)
}

// skillExamplesMaxEntries bounds skills.examples to the most recently recorded
// entries. [F0906-11] Without this cap, examples grows unbounded across
// repeated record_outcome calls (OWASP LLM04 unbounded consumption; 1,000
// entries measured at 2,257,001 bytes returned to the LLM in one skill — GTD
// 17f08ba8). FIFO: oldest entries are dropped first.
const skillExamplesMaxEntries = 20

// UpdateFromOutcome increments the success or failure counter and appends an example entry.
func (s *SkillStore) UpdateFromOutcome(ctx context.Context, p skill.UpdateFromOutcomeParams, workspaceID *string) (*skill.Skill, error) {
	wsArg := stringToNullableArg(workspaceID)

	// Fetch current skill to append to its examples slice.
	existing, err := s.getByID(ctx, p.SkillID)
	if err != nil {
		return nil, err
	}

	newEntry := map[string]string{
		"outcome_id": p.OutcomeID,
		"notes":      p.Notes,
		"at":         time.Now().UTC().Format(time.RFC3339),
	}
	existing.Examples = append(existing.Examples, newEntry)
	// [F0906-11] Keep only the most recent skillExamplesMaxEntries entries (FIFO).
	if len(existing.Examples) > skillExamplesMaxEntries {
		existing.Examples = existing.Examples[len(existing.Examples)-skillExamplesMaxEntries:]
	}
	newExamplesJSON := encodeAny(existing.Examples)

	var q string
	if p.Success {
		q = `UPDATE skills
			SET success_count = success_count + 1,
			    examples      = ?4,
			    last_used_at  = ?3,
			    updated_at    = ?3
			WHERE id = ?1
			  AND (?2 IS NULL OR workspace_id = ?2)`
	} else {
		q = `UPDATE skills
			SET failure_count = failure_count + 1,
			    examples      = ?4,
			    last_used_at  = ?3,
			    updated_at    = ?3
			WHERE id = ?1
			  AND (?2 IS NULL OR workspace_id = ?2)`
	}

	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q, p.SkillID, wsArg, now, newExamplesJSON)
	if err != nil {
		return nil, errWrap("SkillStore.UpdateFromOutcome", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, skill.ErrNotFound
	}
	return s.getByID(ctx, p.SkillID)
}

// ListRelevant returns skills ordered by success_count DESC, last_used_at DESC.
func (s *SkillStore) ListRelevant(ctx context.Context, workspaceID *string, query string, limit int) ([]*skill.Skill, error) {
	if limit <= 0 {
		limit = 10
	}

	wsArg := stringToNullableArg(workspaceID)

	var q string
	var args []any
	if query == "" {
		q = `SELECT ` + skillSelectCols + ` FROM skills
			WHERE (?1 IS NULL OR workspace_id = ?1)
			ORDER BY success_count DESC, last_used_at DESC
			LIMIT ?2`
		args = []any{wsArg, limit}
	} else {
		pattern := "%" + likeescape.Escape(query) + "%"
		q = `SELECT ` + skillSelectCols + ` FROM skills
			WHERE (?1 IS NULL OR workspace_id = ?1)
			  AND (name LIKE ?2 ESCAPE '\' OR description LIKE ?2 ESCAPE '\')
			ORDER BY success_count DESC, last_used_at DESC
			LIMIT ?3`
		args = []any{wsArg, pattern, limit}
	}

	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("SkillStore.ListRelevant", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]*skill.Skill, 0)
	for rows.Next() {
		sk, err := scanSkillRow(rows.Scan)
		if err != nil {
			return nil, errWrap("SkillStore.ListRelevant scan", err)
		}
		out = append(out, &sk)
	}
	return out, errWrap("SkillStore.ListRelevant iter", rows.Err())
}

func (s *SkillStore) getByID(ctx context.Context, id string) (*skill.Skill, error) {
	const q = `SELECT ` + skillSelectCols + ` FROM skills
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id, s.db.workspaceArg())
	sk, err := scanSkillRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, skill.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("SkillStore.getByID", err)
	}
	return &sk, nil
}

// stringToNullableArg converts a *string to an any that SQLite treats as NULL
// when nil or empty, and as the string value otherwise. Used for optional
// workspace-scoping predicates of the form (?N IS NULL OR workspace_id = ?N).
func stringToNullableArg(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}
