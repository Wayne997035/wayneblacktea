package completioncandidate

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the Postgres-backed implementation of Store.
type PgStore struct {
	pool        *pgxpool.Pool
	workspaceID *uuid.UUID
}

// NewPgStore creates a Postgres-backed Store.
func NewPgStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *PgStore {
	return &PgStore{pool: pool, workspaceID: workspaceID}
}

// Compile-time guarantee.
var _ Store = (*PgStore)(nil)

// wsArg returns the effective workspace UUID for SQL queries. When workspaceID
// is nil (legacy unscoped mode), the queries use ($1 IS NULL OR workspace_id = $1)
// with a nil arg, which matches every row.
func (s *PgStore) wsArg() any {
	if s.workspaceID == nil {
		return nil
	}
	return s.workspaceID
}

// pgScanCandidate scans a Postgres row into a Candidate. The query must
// SELECT columns in the same order as pgSelectCols.
func pgScanCandidate(row pgx.Row) (*Candidate, error) {
	var (
		idStr, taskIDStr, reason, confidence, status string
		detectedAt                                   time.Time
		wsIDStr, repoName, suggestedArtifact         *string
		evidenceRefs                                 []string
		resolvedAt                                   *time.Time
	)
	err := row.Scan(
		&idStr, &wsIDStr, &taskIDStr, &repoName, &reason,
		&evidenceRefs, &confidence, &suggestedArtifact, &status,
		&detectedAt, &resolvedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("pgScanCandidate: %w", err)
	}
	c := &Candidate{
		Reason:     Reason(reason),
		Confidence: Confidence(confidence),
		Status:     Status(status),
		DetectedAt: detectedAt.UTC(),
		ResolvedAt: resolvedAt,
	}
	c.ID, _ = uuid.Parse(idStr)
	c.TaskID, _ = uuid.Parse(taskIDStr)
	if wsIDStr != nil {
		if id, e := uuid.Parse(*wsIDStr); e == nil {
			c.WorkspaceID = &id
		}
	}
	if repoName != nil {
		c.RepoName = *repoName
	}
	if suggestedArtifact != nil {
		c.SuggestedArtifact = *suggestedArtifact
	}
	if evidenceRefs != nil {
		c.EvidenceRefs = evidenceRefs
	} else {
		c.EvidenceRefs = []string{}
	}
	return c, nil
}

const pgSelectCols = `id::text, workspace_id::text, task_id::text, repo_name, reason,
	evidence_refs, confidence, suggested_artifact, status, detected_at, resolved_at`

// ListPendingCandidates returns candidates with status='pending' scoped to the
// store's workspace (or the provided workspaceID override).
func (s *PgStore) ListPendingCandidates(ctx context.Context, workspaceID *uuid.UUID) ([]Candidate, error) {
	ws := s.wsArg()
	if workspaceID != nil {
		ws = workspaceID
	}
	const q = `SELECT ` + pgSelectCols + ` FROM completion_candidates
		WHERE status = 'pending'
		  AND ($1 IS NULL OR workspace_id = $1)
		ORDER BY detected_at DESC`

	rows, err := s.pool.Query(ctx, q, ws)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate.PgStore.ListPendingCandidates: %w", err)
	}
	defer rows.Close()

	out := make([]Candidate, 0)
	for rows.Next() {
		c, sErr := pgScanCandidate(rows)
		if sErr != nil {
			return nil, fmt.Errorf("completioncandidate.PgStore.ListPendingCandidates scan: %w", sErr)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("completioncandidate.PgStore.ListPendingCandidates iter: %w", err)
	}
	return out, nil
}

// UpsertCandidate inserts or refreshes a candidate identified by (task_id, reason).
func (s *PgStore) UpsertCandidate(ctx context.Context, p UpsertParams) (*Candidate, error) {
	if err := validateUpsertParams(p); err != nil {
		return nil, fmt.Errorf("completioncandidate.UpsertCandidate: %w", err)
	}

	ws := s.wsArg()
	if p.WorkspaceID != nil {
		ws = p.WorkspaceID
	}

	var repoArg *string
	if p.RepoName != "" {
		repoArg = &p.RepoName
	}
	var artifactArg *string
	if p.SuggestedArtifact != "" {
		artifactArg = &p.SuggestedArtifact
	}
	refs := p.EvidenceRefs
	if refs == nil {
		refs = []string{}
	}

	const q = `INSERT INTO completion_candidates
		(workspace_id, task_id, repo_name, reason, evidence_refs, confidence, suggested_artifact, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'pending')
		ON CONFLICT (task_id, reason) DO UPDATE SET
			detected_at        = now(),
			confidence         = excluded.confidence,
			evidence_refs      = excluded.evidence_refs,
			suggested_artifact = COALESCE(excluded.suggested_artifact, completion_candidates.suggested_artifact)
		RETURNING ` + pgSelectCols

	row := s.pool.QueryRow(ctx, q,
		ws, p.TaskID, repoArg,
		string(p.Reason), refs, string(p.Confidence), artifactArg,
	)
	c, err := pgScanCandidate(row)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate.PgStore.UpsertCandidate: %w", err)
	}
	return c, nil
}

// PruneResolved deletes candidates whose resolved_at is older than olderThan.
func (s *PgStore) PruneResolved(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	const q = `DELETE FROM completion_candidates
		WHERE resolved_at IS NOT NULL AND resolved_at < $1`
	res, err := s.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("completioncandidate.PgStore.PruneResolved: %w", err)
	}
	return res.RowsAffected(), nil
}

// DetectAndUpsert runs all 4 detection rules and upserts results.
func (s *PgStore) DetectAndUpsert(ctx context.Context, p DetectParams) ([]Candidate, error) {
	staleHours := int(p.StaleThreshold.Hours())
	if staleHours <= 0 {
		staleHours = 24
	}
	lookbackDays := p.LookbackDays
	if lookbackDays <= 0 {
		lookbackDays = 7
	}
	if lookbackDays > 30 {
		lookbackDays = 30
	}

	ws := s.wsArg()
	if p.WorkspaceID != nil {
		ws = p.WorkspaceID
	}

	var candidates []Candidate

	// Rule 1 — Stale in_progress (confidence: medium)
	r1, err := s.pgDetectStaleInProgress(ctx, ws, staleHours)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate PG detect rule1: %w", err)
	}
	candidates = append(candidates, r1...)

	// Rule 2 — finish_work gap (confidence: high)
	r2, err := s.pgDetectFinishWorkGap(ctx, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate PG detect rule2: %w", err)
	}
	candidates = append(candidates, r2...)

	// Rule 3 — artifact evidence in activity_log (confidence: medium)
	// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
	r3, err := s.pgDetectArtifactEvidence(ctx, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate PG detect rule3: %w", err)
	}
	candidates = append(candidates, r3...)

	// Rule 4 — completion signal (confidence: low)
	// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
	r4, err := s.pgDetectCompletionSignal(ctx, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate PG detect rule4: %w", err)
	}
	candidates = append(candidates, r4...)

	if candidates == nil {
		candidates = []Candidate{}
	}
	return candidates, nil
}

// pgDetectStaleInProgress implements rule 1 for Postgres.
func (s *PgStore) pgDetectStaleInProgress(ctx context.Context, ws any, staleHours int) ([]Candidate, error) {
	const q = `SELECT id, updated_at FROM tasks
		WHERE status = 'in_progress'
		  AND ($1 IS NULL OR workspace_id = $1)
		  AND updated_at < now() - ($2 * INTERVAL '1 hour')`

	rows, err := s.pool.Query(ctx, q, ws, staleHours)
	if err != nil {
		return nil, fmt.Errorf("pg rule1 query: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var taskID uuid.UUID
		var updatedAt time.Time
		if err := rows.Scan(&taskID, &updatedAt); err != nil {
			return nil, fmt.Errorf("pg rule1 scan: %w", err)
		}
		up := UpsertParams{
			TaskID:       taskID,
			Reason:       ReasonStaleInProgress,
			Confidence:   ConfidenceMedium,
			EvidenceRefs: []string{"stale since " + updatedAt.UTC().Format(time.RFC3339)},
		}
		if wsID, ok := ws.(*uuid.UUID); ok && wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("pg rule1 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg rule1 iter: %w", err)
	}
	return out, nil
}

// pgDetectFinishWorkGap implements rule 2 for Postgres.
func (s *PgStore) pgDetectFinishWorkGap(ctx context.Context, ws any, lookbackDays int) ([]Candidate, error) {
	const q = `SELECT DISTINCT st.task_id, ws.completed_at::text AS evidence
		FROM work_session_tasks st
		JOIN work_sessions ws ON ws.id = st.session_id
		JOIN tasks t ON t.id = st.task_id
		WHERE ws.status = 'completed'
		  AND t.status IN ('pending','in_progress')
		  AND ($1 IS NULL OR ws.workspace_id = $1)
		  AND ws.completed_at > now() - ($2 * INTERVAL '1 day')`

	rows, err := s.pool.Query(ctx, q, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("pg rule2 query: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var taskID uuid.UUID
		var completedAtStr *string
		if err := rows.Scan(&taskID, &completedAtStr); err != nil {
			return nil, fmt.Errorf("pg rule2 scan: %w", err)
		}
		evidence := "work_session completed"
		if completedAtStr != nil {
			evidence = "work_session completed at " + *completedAtStr
		}
		up := UpsertParams{
			TaskID:       taskID,
			Reason:       ReasonFinishWorkGap,
			Confidence:   ConfidenceHigh,
			EvidenceRefs: []string{evidence},
		}
		if wsID, ok := ws.(*uuid.UUID); ok && wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("pg rule2 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg rule2 iter: %w", err)
	}
	return out, nil
}

// pgDetectArtifactEvidence implements rule 3 for Postgres.
// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
func (s *PgStore) pgDetectArtifactEvidence(ctx context.Context, ws any, lookbackDays int) ([]Candidate, error) {
	const q = `SELECT al.id::text AS al_id, t.id AS task_id, al.notes
		FROM activity_log al
		JOIN tasks t ON t.status IN ('pending','in_progress')
		  AND al.notes LIKE '%' || t.id::text || '%'
		WHERE al.action = 'worksession:finished'
		  AND ($1 IS NULL OR al.workspace_id = $1)
		  AND al.created_at > now() - ($2 * INTERVAL '1 day')
		LIMIT 50`

	rows, err := s.pool.Query(ctx, q, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("pg rule3 query: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var alIDStr string
		var taskID uuid.UUID
		var notesPtr *string
		if err := rows.Scan(&alIDStr, &taskID, &notesPtr); err != nil {
			return nil, fmt.Errorf("pg rule3 scan: %w", err)
		}
		var artifact string
		if notesPtr != nil {
			artifact = extractURLFromNotes(*notesPtr)
		}
		up := UpsertParams{
			TaskID:            taskID,
			Reason:            ReasonArtifactEvidence,
			Confidence:        ConfidenceMedium,
			EvidenceRefs:      []string{"activity_log:" + alIDStr},
			SuggestedArtifact: artifact,
		}
		if wsID, ok := ws.(*uuid.UUID); ok && wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("pg rule3 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg rule3 iter: %w", err)
	}
	return out, nil
}

// pgDetectCompletionSignal implements rule 4 for Postgres.
// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
func (s *PgStore) pgDetectCompletionSignal(ctx context.Context, ws any, lookbackDays int) ([]Candidate, error) {
	const q = `SELECT al.id::text, t.id AS task_id
		FROM activity_log al
		JOIN tasks t ON t.status IN ('pending','in_progress')
		  AND al.notes LIKE '%' || t.id::text || '%'
		WHERE al.action IN ('task:updated','plan:confirmed','complete_task')
		  AND ($1 IS NULL OR al.workspace_id = $1)
		  AND al.created_at > now() - ($2 * INTERVAL '1 day')
		LIMIT 50`

	rows, err := s.pool.Query(ctx, q, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("pg rule4 query: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var alIDStr string
		var taskID uuid.UUID
		if err := rows.Scan(&alIDStr, &taskID); err != nil {
			return nil, fmt.Errorf("pg rule4 scan: %w", err)
		}
		up := UpsertParams{
			TaskID:       taskID,
			Reason:       ReasonCompletionSignal,
			Confidence:   ConfidenceLow,
			EvidenceRefs: []string{"activity_log:" + alIDStr},
		}
		if wsID, ok := ws.(*uuid.UUID); ok && wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("pg rule4 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pg rule4 iter: %w", err)
	}
	return out, nil
}
