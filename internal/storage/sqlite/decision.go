package sqlite

import (
	"context"
	"database/sql"
	"sort"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
)

// DecisionStore is the SQLite-backed implementation of decision.StoreIface.
type DecisionStore struct {
	db *DB
}

// NewDecisionStore wraps an open DB into a DecisionStore.
func NewDecisionStore(d *DB) *DecisionStore {
	return &DecisionStore{db: d}
}

var _ decision.StoreIface = (*DecisionStore)(nil)

const sqliteMillisLayout = "2006-01-02T15:04:05.000Z"

func sqliteNowMillis() string {
	return time.Now().UTC().Format(sqliteMillisLayout)
}

// decisionsSelectCols lists all columns returned by decision read queries.
// task_id was added in migration 000048.
const decisionsSelectCols = `id, project_id, repo_name, title, context, decision,
	rationale, alternatives, created_at, workspace_id, task_id`

func scanDecision(scan func(...any) error) (db.Decision, error) {
	var (
		d                         db.Decision
		idStr                     string
		projectIDNS, repoNS, wsNS sql.NullString
		alternativesNS, createdNS sql.NullString
		taskIDNS                  sql.NullString
	)
	err := scan(&idStr, &projectIDNS, &repoNS, &d.Title, &d.Context, &d.Decision,
		&d.Rationale, &alternativesNS, &createdNS, &wsNS, &taskIDNS)
	if err != nil {
		return db.Decision{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		d.ID = id
	}
	d.ProjectID = pgtypeUUID(nsString(projectIDNS))
	d.RepoName = pgtypeText(repoNS.String, repoNS.Valid)
	d.Alternatives = pgtypeText(alternativesNS.String, alternativesNS.Valid)
	d.CreatedAt = parseTimestamptz(createdNS)
	d.WorkspaceID = pgtypeUUID(nsString(wsNS))
	d.TaskID = pgtypeUUID(nsString(taskIDNS))
	return d, nil
}

// Log records a new architectural decision.
// task_id (migration 000048) is stored as nullable TEXT UUID.
func (s *DecisionStore) Log(ctx context.Context, p decision.LogParams) (*db.Decision, error) {
	id := uuid.New()
	const q = `INSERT INTO decisions
		(id, workspace_id, project_id, repo_name, title, context, decision, rationale, alternatives, created_at, task_id)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)`
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		nullStringIfEmpty(p.RepoName), p.Title, p.Context, p.Decision, p.Rationale,
		nullStringIfEmpty(p.Alternatives), sqliteNowMillis(), nullStringFromUUID(p.TaskID))
	if err != nil {
		return nil, errWrap("LogDecision", err)
	}
	return s.decisionByID(ctx, id)
}

// LogTx is the transactional counterpart of Log. It inserts the decision row
// inside the supplied *sql.Tx so the caller (confirm_proposal accept path for
// type='decision') can atomically commit the materialised decision and the
// proposal-resolve in a single transaction. Returns the freshly-generated ID
// on success — callers that want the full row can SELECT it after Commit.
func (s *DecisionStore) LogTx(ctx context.Context, tx *sql.Tx, p decision.LogParams) (uuid.UUID, error) {
	id := uuid.New()
	const q = `INSERT INTO decisions
		(id, workspace_id, project_id, repo_name, title, context, decision, rationale, alternatives, created_at, task_id)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)`
	_, err := tx.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		nullStringIfEmpty(p.RepoName), p.Title, p.Context, p.Decision, p.Rationale,
		nullStringIfEmpty(p.Alternatives), sqliteNowMillis(), nullStringFromUUID(p.TaskID))
	if err != nil {
		return uuid.UUID{}, errWrap("LogDecisionTx", err)
	}
	return id, nil
}

// ImportDecision inserts a decision row using d's own id/created_at instead
// of generating fresh ones, so decisions that reference a task_id/project_id
// stay linked to the same rows imported by ImportProject/ImportTask. Used by
// cmd/qa-seed. Embedding columns are intentionally left NULL (all nullable) —
// pgvector recall is out of the QA-seed v1 scope; only CRUD-shaped fields
// used by the frontend are copied. Fails (no upsert) on a duplicate id —
// callers MUST import into a fresh database.
func (s *DecisionStore) ImportDecision(ctx context.Context, d db.Decision) error {
	const q = `INSERT INTO decisions
		(id, workspace_id, project_id, repo_name, title, context, decision, rationale, alternatives, created_at, task_id)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)`
	_, err := s.db.conn.ExecContext(ctx, q,
		d.ID.String(), pgUUIDToNullString(d.WorkspaceID), pgUUIDToNullString(d.ProjectID),
		pgTextToNullString(d.RepoName), d.Title, d.Context, d.Decision, d.Rationale,
		pgTextToNullString(d.Alternatives), pgTimestamptzToString(d.CreatedAt),
		pgUUIDToNullString(d.TaskID))
	if err != nil {
		return errWrap("ImportDecision", err)
	}
	return nil
}

// ByRepo returns the most recent decisions for a given repo name.
func (s *DecisionStore) ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error) {
	const q = `SELECT ` + decisionsSelectCols + ` FROM decisions
		WHERE repo_name = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, id DESC
		LIMIT ?3`
	return s.list(ctx, "ByRepo", q, repoName, s.db.workspaceArg(), limit)
}

// All returns the most recent decisions across all repos and projects.
func (s *DecisionStore) All(ctx context.Context, limit int32) ([]db.Decision, error) {
	const q = `SELECT ` + decisionsSelectCols + ` FROM decisions
		WHERE (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC, id DESC
		LIMIT ?2`
	return s.list(ctx, "AllDecisions", q, s.db.workspaceArg(), limit)
}

// ByProject returns the most recent decisions for a given project ID.
func (s *DecisionStore) ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error) {
	const q = `SELECT ` + decisionsSelectCols + ` FROM decisions
		WHERE project_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, id DESC
		LIMIT ?3`
	return s.list(ctx, "ByProject", q, projectID.String(), s.db.workspaceArg(), limit)
}

// ByTask returns the most recent decisions for a given task ID.
// Migration 000048 added task_id to the decisions table.
// SECURITY: workspace-scoped.
func (s *DecisionStore) ByTask(ctx context.Context, taskID uuid.UUID, limit int32) ([]db.Decision, error) {
	const q = `SELECT ` + decisionsSelectCols + ` FROM decisions
		WHERE task_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, id DESC
		LIMIT ?3`
	return s.list(ctx, "ByTask", q, taskID.String(), s.db.workspaceArg(), limit)
}

// SearchByCosine returns the top-limit decisions most similar to queryEmbedding.
// SQLite has no pgvector — brute-force Go-side cosine scan.
//
// SECURITY: filtered by workspace_id — no cross-workspace data returned.
func (s *DecisionStore) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.Decision, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}
	const q = `SELECT ` + decisionsSelectCols + `, embedding FROM decisions
		WHERE embedding IS NOT NULL
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC
		LIMIT 200`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("SearchByCosine", err)
	}
	defer func() { _ = rows.Close() }()

	type scored struct {
		d   db.Decision
		sim float64
	}
	var candidates []scored
	for rows.Next() {
		var d db.Decision
		var idStr string
		var projectIDNS, repoNS, wsNS sql.NullString
		var alternativesNS, createdNS sql.NullString
		var taskIDNS sql.NullString
		var rawEmbed []byte
		if err := rows.Scan(&idStr, &projectIDNS, &repoNS, &d.Title, &d.Context,
			&d.Decision, &d.Rationale, &alternativesNS, &createdNS, &wsNS, &taskIDNS, &rawEmbed); err != nil {
			continue
		}
		if id, parseErr := uuid.Parse(idStr); parseErr == nil {
			d.ID = id
		}
		d.ProjectID = pgtypeUUID(nsString(projectIDNS))
		d.RepoName = pgtypeText(repoNS.String, repoNS.Valid)
		d.Alternatives = pgtypeText(alternativesNS.String, alternativesNS.Valid)
		d.CreatedAt = parseTimestamptz(createdNS)
		d.WorkspaceID = pgtypeUUID(nsString(wsNS))
		d.TaskID = pgtypeUUID(nsString(taskIDNS))

		vec := localai.DeserializeEmbedding(rawEmbed)
		if vec == nil {
			continue
		}
		candidates = append(candidates, scored{d: d, sim: localai.CosineSimilarity(queryEmbedding, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("SearchByCosine iter", err)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sim > candidates[j].sim
	})
	if limit < len(candidates) {
		candidates = candidates[:limit]
	}
	result := make([]db.Decision, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, c.d)
	}
	return result, nil
}

func (s *DecisionStore) list(ctx context.Context, op, q string, args ...any) ([]db.Decision, error) {
	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap(op, err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Decision
	for rows.Next() {
		d, err := scanDecision(rows.Scan)
		if err != nil {
			return nil, errWrap(op+" scan", err)
		}
		out = append(out, d)
	}
	return out, errWrap(op+" iter", rows.Err())
}

func (s *DecisionStore) decisionByID(ctx context.Context, id uuid.UUID) (*db.Decision, error) {
	const q = `SELECT ` + decisionsSelectCols + ` FROM decisions WHERE id = ?1 LIMIT 1`
	d, err := scanDecision(s.db.conn.QueryRowContext(ctx, q, id.String()).Scan)
	if err != nil {
		return nil, errWrap("decisionByID", err)
	}
	return &d, nil
}
