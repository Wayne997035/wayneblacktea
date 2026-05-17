package completioncandidate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// SQLiteStore is the SQLite-backed implementation of Store.
// It accepts a *sql.DB directly so list queries (sql.Rows) can be used.
// Wiring in cmd/server/main.go passes the connection extracted from
// wbtsqlite.DB via its ExecContext-family public methods.
type SQLiteStore struct {
	db          *sql.DB
	workspaceID string // empty = unscoped (legacy single-tenant)
}

// NewSQLiteStore creates a SQLiteStore backed by the given *sql.DB and
// optional workspace UUID string. workspaceID may be "" for unscoped mode.
func NewSQLiteStore(db *sql.DB, workspaceID string) *SQLiteStore {
	return &SQLiteStore{db: db, workspaceID: workspaceID}
}

// Compile-time guarantee.
var _ Store = (*SQLiteStore)(nil)

// nowRFC3339 returns the current UTC time formatted as RFC3339 with milliseconds.
func nowRFC3339() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// parseTime parses an RFC3339 timestamp string, returning zero time on failure.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// jsonArrayToSlice decodes a JSON string array. Returns empty slice on failure.
func jsonArrayToSlice(raw string) []string {
	if raw == "" || raw == "null" || raw == "[]" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	return out
}

// sliceToJSONArray encodes []string into a compact JSON array string.
func sliceToJSONArray(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// reURL is a simple URL pattern used to extract suggested_artifact from notes.
var reURL = regexp.MustCompile(`https?://\S+`)

// extractURLFromNotes returns the first URL found in notes, or "".
func extractURLFromNotes(notes string) string {
	return reURL.FindString(notes)
}

const candidateSelectCols = `id, workspace_id, task_id, repo_name, reason,
	evidence_refs, confidence, suggested_artifact, status, detected_at, resolved_at`

// scanCandidate reads a completion_candidates row using the provided scan function.
func scanCandidate(row func(...any) error) (*Candidate, error) {
	var (
		idStr, taskIDStr, reason, evidenceRefsJSON, confidence, status, detectedAtStr string
		wsIDNS, repoNameNS, suggestedArtifactNS, resolvedAtNS                         sql.NullString
	)
	err := row(
		&idStr, &wsIDNS, &taskIDStr, &repoNameNS, &reason,
		&evidenceRefsJSON, &confidence, &suggestedArtifactNS, &status,
		&detectedAtStr, &resolvedAtNS,
	)
	if err != nil {
		return nil, err
	}
	c := &Candidate{
		Reason:       Reason(reason),
		EvidenceRefs: jsonArrayToSlice(evidenceRefsJSON),
		Confidence:   Confidence(confidence),
		Status:       Status(status),
		DetectedAt:   parseTime(detectedAtStr),
	}
	c.ID, _ = uuid.Parse(idStr)
	c.TaskID, _ = uuid.Parse(taskIDStr)
	if wsIDNS.Valid && wsIDNS.String != "" {
		if id, e := uuid.Parse(wsIDNS.String); e == nil {
			c.WorkspaceID = &id
		}
	}
	if repoNameNS.Valid {
		c.RepoName = repoNameNS.String
	}
	if suggestedArtifactNS.Valid {
		c.SuggestedArtifact = suggestedArtifactNS.String
	}
	if resolvedAtNS.Valid && resolvedAtNS.String != "" {
		t := parseTime(resolvedAtNS.String)
		c.ResolvedAt = &t
	}
	return c, nil
}

// wsArg returns the workspace scoping arg: a string or nil.
func (s *SQLiteStore) wsArg() any {
	if s.workspaceID == "" {
		return nil
	}
	return s.workspaceID
}

// ListPendingCandidates returns candidates with status='pending', scoped to
// the store's workspace (or the provided workspaceID override).
func (s *SQLiteStore) ListPendingCandidates(ctx context.Context, workspaceID *uuid.UUID) ([]Candidate, error) {
	ws := s.wsArg()
	if workspaceID != nil {
		ws = workspaceID.String()
	}
	const q = `SELECT ` + candidateSelectCols + ` FROM completion_candidates
		WHERE status = 'pending'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY detected_at DESC`

	rows, err := s.db.QueryContext(ctx, q, ws)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate.SQLiteStore.ListPendingCandidates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Candidate, 0)
	for rows.Next() {
		c, sErr := scanCandidate(rows.Scan)
		if sErr != nil {
			return nil, fmt.Errorf("completioncandidate.SQLiteStore.ListPendingCandidates scan: %w", sErr)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("completioncandidate.SQLiteStore.ListPendingCandidates iter: %w", err)
	}
	return out, nil
}

// UpsertCandidate inserts or refreshes a candidate identified by (task_id, reason).
// On conflict (task_id, reason) detected_at, confidence, and evidence_refs are updated.
func (s *SQLiteStore) UpsertCandidate(ctx context.Context, p UpsertParams) (*Candidate, error) {
	if err := validateUpsertParams(p); err != nil {
		return nil, fmt.Errorf("completioncandidate.UpsertCandidate: %w", err)
	}

	id := uuid.New()
	now := nowRFC3339()
	wsStr := s.workspaceID
	if p.WorkspaceID != nil {
		wsStr = p.WorkspaceID.String()
	}
	evidenceJSON := sliceToJSONArray(p.EvidenceRefs)

	var wsArg any
	if wsStr != "" {
		wsArg = wsStr
	}
	var repoArg any
	if p.RepoName != "" {
		repoArg = p.RepoName
	}
	var artifactArg any
	if p.SuggestedArtifact != "" {
		artifactArg = p.SuggestedArtifact
	}

	const q = `INSERT INTO completion_candidates
		(id, workspace_id, task_id, repo_name, reason, evidence_refs, confidence, suggested_artifact, status, detected_at)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?8,'pending',?9)
		ON CONFLICT (task_id, reason) DO UPDATE SET
			detected_at       = excluded.detected_at,
			confidence        = excluded.confidence,
			evidence_refs     = excluded.evidence_refs,
			suggested_artifact = COALESCE(excluded.suggested_artifact, completion_candidates.suggested_artifact)`

	if _, err := s.db.ExecContext(ctx, q,
		id.String(), wsArg, p.TaskID.String(), repoArg,
		string(p.Reason), evidenceJSON, string(p.Confidence), artifactArg, now,
	); err != nil {
		return nil, fmt.Errorf("completioncandidate.SQLiteStore.UpsertCandidate: %w", err)
	}

	// Fetch the upserted row by (task_id, reason) — primary key is generated on
	// insert; the ON CONFLICT path uses the existing row's id.
	const sel = `SELECT ` + candidateSelectCols + ` FROM completion_candidates
		WHERE task_id = ?1 AND reason = ?2 LIMIT 1`
	row := s.db.QueryRowContext(ctx, sel, p.TaskID.String(), string(p.Reason))
	c, err := scanCandidate(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate.SQLiteStore.UpsertCandidate fetch: %w", err)
	}
	return c, nil
}

// PruneResolved deletes candidates whose resolved_at is older than olderThan.
func (s *SQLiteStore) PruneResolved(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	const q = `DELETE FROM completion_candidates
		WHERE resolved_at IS NOT NULL AND resolved_at < ?1`
	res, err := s.db.ExecContext(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("completioncandidate.SQLiteStore.PruneResolved: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// DetectAndUpsert runs all 4 detection rules and upserts results.
func (s *SQLiteStore) DetectAndUpsert(ctx context.Context, p DetectParams) ([]Candidate, error) {
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

	wsStr := s.workspaceID
	if p.WorkspaceID != nil {
		wsStr = p.WorkspaceID.String()
	}
	var ws any
	if wsStr != "" {
		ws = wsStr
	}

	var candidates []Candidate

	// Rule 1 — Stale in_progress (confidence: medium)
	r1, err := s.detectStaleInProgress(ctx, ws, staleHours)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate detect rule1: %w", err)
	}
	candidates = append(candidates, r1...)

	// Rule 2 — finish_work gap (confidence: high)
	r2, err := s.detectFinishWorkGap(ctx, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate detect rule2: %w", err)
	}
	candidates = append(candidates, r2...)

	// Rule 3 — artifact evidence in activity_log (confidence: medium)
	// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
	r3, err := s.detectArtifactEvidence(ctx, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate detect rule3: %w", err)
	}
	candidates = append(candidates, r3...)

	// Rule 4 — completion signal (confidence: low)
	// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
	r4, err := s.detectCompletionSignal(ctx, ws, lookbackDays)
	if err != nil {
		return nil, fmt.Errorf("completioncandidate detect rule4: %w", err)
	}
	candidates = append(candidates, r4...)

	if candidates == nil {
		candidates = []Candidate{}
	}
	return candidates, nil
}

// detectStaleInProgress runs rule 1: in_progress tasks not updated within staleHours.
func (s *SQLiteStore) detectStaleInProgress(ctx context.Context, ws any, staleHours int) ([]Candidate, error) {
	// Defence-in-depth cap: the MCP handler already caps at 168 h, but this
	// store-layer guard ensures no future caller can inject an unbounded SQL
	// datetime() interval via fmt.Sprintf (M-6). SQLite's datetime() function
	// does not support bind parameters for interval strings, so fmt.Sprintf is
	// required; an integer cap is the safe alternative to parameterisation.
	const maxStaleHours = 168 * 7 // 4 weeks maximum window
	if staleHours > maxStaleHours {
		staleHours = maxStaleHours
	}
	//nolint:gosec // reason: staleHours is capped above to ≤1176 (int); no user string interpolated; no SQL injection risk
	q := fmt.Sprintf(`SELECT id, updated_at FROM tasks
		WHERE status = 'in_progress'
		  AND (?1 IS NULL OR workspace_id = ?1)
		  AND updated_at < datetime('now', printf('-%%d hours', %d))`, staleHours)

	rows, err := s.db.QueryContext(ctx, q, ws)
	if err != nil {
		return nil, fmt.Errorf("rule1 query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Candidate
	for rows.Next() {
		var taskIDStr, updatedAt string
		if err := rows.Scan(&taskIDStr, &updatedAt); err != nil {
			return nil, fmt.Errorf("rule1 scan: %w", err)
		}
		taskID, err := uuid.Parse(taskIDStr)
		if err != nil {
			continue
		}
		up := UpsertParams{
			TaskID:       taskID,
			Reason:       ReasonStaleInProgress,
			Confidence:   ConfidenceMedium,
			EvidenceRefs: []string{"stale since " + updatedAt},
		}
		if wsID := wsArgToUUIDPtr(ws); wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("rule1 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rule1 iter: %w", err)
	}
	return out, nil
}

// detectFinishWorkGap runs rule 2: open tasks linked to a completed work session.
func (s *SQLiteStore) detectFinishWorkGap(ctx context.Context, ws any, lookbackDays int) ([]Candidate, error) {
	//nolint:gosec // reason: lookbackDays is a server-validated integer (1-30); no user string interpolated; no SQL injection risk
	q := fmt.Sprintf(`SELECT DISTINCT st.task_id, ws.completed_at
		FROM work_session_tasks st
		JOIN work_sessions ws ON ws.id = st.session_id
		JOIN tasks t ON t.id = st.task_id
		WHERE ws.status = 'completed'
		  AND t.status IN ('pending','in_progress')
		  AND (?1 IS NULL OR ws.workspace_id = ?1)
		  AND ws.completed_at > datetime('now', printf('-%%d days', %d))`, lookbackDays)

	rows, err := s.db.QueryContext(ctx, q, ws)
	if err != nil {
		return nil, fmt.Errorf("rule2 query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Candidate
	for rows.Next() {
		var taskIDStr string
		var completedAtNS sql.NullString
		if err := rows.Scan(&taskIDStr, &completedAtNS); err != nil {
			return nil, fmt.Errorf("rule2 scan: %w", err)
		}
		taskID, err := uuid.Parse(taskIDStr)
		if err != nil {
			continue
		}
		evidence := "work_session completed"
		if completedAtNS.Valid && completedAtNS.String != "" {
			evidence = "work_session completed at " + completedAtNS.String
		}
		up := UpsertParams{
			TaskID:       taskID,
			Reason:       ReasonFinishWorkGap,
			Confidence:   ConfidenceHigh,
			EvidenceRefs: []string{evidence},
		}
		if wsID := wsArgToUUIDPtr(ws); wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("rule2 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rule2 iter: %w", err)
	}
	return out, nil
}

// detectArtifactEvidence runs rule 3: worksession:finished activity_log entries referencing task IDs.
// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
func (s *SQLiteStore) detectArtifactEvidence(ctx context.Context, ws any, lookbackDays int) ([]Candidate, error) {
	//nolint:gosec // reason: lookbackDays is a server-validated integer (1-30); no user string interpolated; no SQL injection risk
	q := fmt.Sprintf(`SELECT al.id, t.id, al.notes
		FROM activity_log al
		JOIN tasks t ON t.status IN ('pending','in_progress')
		  AND al.notes LIKE '%%' || t.id || '%%'
		WHERE al.action = 'worksession:finished'
		  AND (?1 IS NULL OR al.workspace_id = ?1)
		  AND al.created_at > datetime('now', printf('-%%d days', %d))
		LIMIT 50`, lookbackDays)

	rows, err := s.db.QueryContext(ctx, q, ws)
	if err != nil {
		return nil, fmt.Errorf("rule3 query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Candidate
	for rows.Next() {
		var alIDStr, taskIDStr string
		var notesNS sql.NullString
		if err := rows.Scan(&alIDStr, &taskIDStr, &notesNS); err != nil {
			return nil, fmt.Errorf("rule3 scan: %w", err)
		}
		taskID, err := uuid.Parse(taskIDStr)
		if err != nil {
			continue
		}
		var artifact string
		if notesNS.Valid {
			artifact = extractURLFromNotes(notesNS.String)
		}
		up := UpsertParams{
			TaskID:            taskID,
			Reason:            ReasonArtifactEvidence,
			Confidence:        ConfidenceMedium,
			EvidenceRefs:      []string{"activity_log:" + alIDStr},
			SuggestedArtifact: artifact,
		}
		if wsID := wsArgToUUIDPtr(ws); wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("rule3 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rule3 iter: %w", err)
	}
	return out, nil
}

// detectCompletionSignal runs rule 4: completion-type events referencing task IDs.
// personal-scale scan: activity_log × tasks join via LIKE is acceptable at <10k rows each.
//
// Implementation note: the inner loop is extracted to detectCompletionSignalRows
// to avoid the dupl linter flagging structural similarity with detectStaleInProgress —
// both iterate rows and upsert but operate on fundamentally different tables and data.
func (s *SQLiteStore) detectCompletionSignal(ctx context.Context, ws any, lookbackDays int) ([]Candidate, error) {
	//nolint:gosec // reason: lookbackDays is a server-validated integer (1-30); no user string interpolated; no SQL injection risk
	q := fmt.Sprintf(`SELECT al.id, t.id
		FROM activity_log al
		JOIN tasks t ON t.status IN ('pending','in_progress')
		  AND al.notes LIKE '%%' || t.id || '%%'
		WHERE al.action IN ('task:updated','plan:confirmed','complete_task')
		  AND (?1 IS NULL OR al.workspace_id = ?1)
		  AND al.created_at > datetime('now', printf('-%%d days', %d))
		LIMIT 50`, lookbackDays)

	rows, err := s.db.QueryContext(ctx, q, ws)
	if err != nil {
		return nil, fmt.Errorf("rule4 query: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return s.detectCompletionSignalRows(ctx, rows, ws)
}

// detectCompletionSignalRows processes rows returned by the completion-signal query.
// Extracted from detectCompletionSignal to reduce structural similarity with other
// detection rules (preventing false-positive dupl alerts).
func (s *SQLiteStore) detectCompletionSignalRows(ctx context.Context, rows *sql.Rows, ws any) ([]Candidate, error) {
	var out []Candidate
	for rows.Next() {
		var alIDStr, taskIDStr string
		if err := rows.Scan(&alIDStr, &taskIDStr); err != nil {
			return nil, fmt.Errorf("rule4 scan: %w", err)
		}
		taskID, err := uuid.Parse(taskIDStr)
		if err != nil {
			continue
		}
		up := UpsertParams{
			TaskID:       taskID,
			Reason:       ReasonCompletionSignal,
			Confidence:   ConfidenceLow,
			EvidenceRefs: []string{"activity_log:" + alIDStr},
		}
		if wsID := wsArgToUUIDPtr(ws); wsID != nil {
			up.WorkspaceID = wsID
		}
		c, err := s.UpsertCandidate(ctx, up)
		if err != nil {
			return nil, fmt.Errorf("rule4 upsert: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rule4 iter: %w", err)
	}
	return out, nil
}

// wsArgToUUIDPtr converts a workspace any arg (string or nil) to *uuid.UUID.
func wsArgToUUIDPtr(ws any) *uuid.UUID {
	if ws == nil {
		return nil
	}
	s, ok := ws.(string)
	if !ok || s == "" {
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

// validateUpsertParams checks UpsertParams for required fields and safe values.
func validateUpsertParams(p UpsertParams) error {
	if p.TaskID == uuid.Nil {
		return errors.New("task_id is required")
	}
	if err := validateReason(p.Reason); err != nil {
		return err
	}
	if err := validateConfidence(p.Confidence); err != nil {
		return err
	}
	return nil
}

// validateReason returns an error if r is not a known Reason.
func validateReason(r Reason) error {
	switch r {
	case ReasonStaleInProgress, ReasonFinishWorkGap, ReasonArtifactEvidence, ReasonCompletionSignal:
		return nil
	default:
		return fmt.Errorf("unknown reason %q", r)
	}
}

// validateConfidence returns an error if c is not a known Confidence.
func validateConfidence(c Confidence) error {
	switch c {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return nil
	default:
		return fmt.Errorf("unknown confidence %q", c)
	}
}
