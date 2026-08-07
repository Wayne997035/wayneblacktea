// Package scheduler — cognitive_jobs.go
//
// Implements the 6 Memory-7 cognitive scheduler jobs. All jobs are registered
// via WithCognitiveDeps, which must be called before sched.Start().
//
// Design principles:
//   - All jobs propose into pending_proposals; they NEVER mutate permanent state
//     directly (tasks, decisions, knowledge_items).
//   - Jobs that require a Postgres pool skip gracefully when disciplinePool is nil
//     (SQLite dev path). Each skip emits slog.Info so operators can tell it's
//     intentional.
//   - All jobs use LimitModeReschedule so a slow run is dropped rather than
//     piled up.
//   - The cognitiveDeps struct is a flat bundle; WithCognitiveDeps is a With*
//     method (not expanding scheduler.New) to keep New's cyclomatic complexity
//     below the gocyclo threshold of 15.

package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// cognitiveJobTimeout is the per-job context deadline. All 6 jobs involve DB
// queries + proposal writes; 90 s is generous for personal-OS scale.
const cognitiveJobTimeout = 90 * time.Second

// cognitiveLookbackDays is the window used by jobs that scan recent data
// (stuck tasks: 7 days; decision review: 30 days; behavior candidate: 7 days).
// Each job applies its own interval in the SQL, not this constant directly, but
// we document them together here for readability.
const (
	stuckTaskInterval       = "7 days"
	decisionOutcomeInterval = "30 days"
	proposalCleanupInterval = "30 days"
	behaviorCandidateDays   = 7 * 24 * time.Hour
)

// stuckTaskLookback / decisionOutcomeLookback / proposalCleanupLookback are
// time.Duration equivalents of the PG interval-literal constants above, used
// by the SQLite adapter path (SQLite has no `NOW() - INTERVAL` syntax — the
// cutoff is computed in Go and bound as a query parameter instead). Kept in
// sync with the PG string constants manually; a drift here would only affect
// the SQLite backend's cutoff window and is caught by the cross-backend
// same-fixture tests in cognitive_jobs_sqlite_test.go.
const (
	stuckTaskLookback       = 7 * 24 * time.Hour
	decisionOutcomeLookback = 30 * 24 * time.Hour
	proposalCleanupLookback = 30 * 24 * time.Hour
)

// knowledgeToSkillMinRecallCount is the recall_count threshold above which a
// knowledge item is considered a skill candidate (job 4). Mirrors the
// `recall_count > 3` literal in pgHighRecallKnowledgeItems's raw SQL; kept as
// a named constant so the SQLite adapter call site stays self-documenting.
const knowledgeToSkillMinRecallCount = 3

// decisionOutcomeReviewDailyCap bounds how many decisions runDecisionOutcomeReview
// processes per invocation. Incident 2026-07-19 (Railway prod log): a 613-row
// no-outcome backlog caused the job to insert 316 pending_proposals (~285 ms
// per round-trip to Aiven Postgres) before tripping cognitiveJobTimeout
// (context deadline exceeded) — the remaining ~297 decisions were silently
// dropped for that run, and the NEXT day's run re-scanned the same undeduped
// backlog from scratch, doubling up proposals for decisions already proposed.
// The cap keeps each run comfortably inside the 90 s budget; the SQL-level
// dedup (NOT EXISTS on pending_proposals.payload->>'source_entity_id') plus
// ORDER BY d.created_at ASC guarantees the backlog drains forward across
// consecutive daily runs instead of reprocessing the same head-of-queue rows.
//
// var (not const) so integration tests can shrink it to exercise the cap
// path without seeding hundreds of rows — matches the pendingProposalsPruneTimeout
// override pattern used elsewhere in this package.
var decisionOutcomeReviewDailyCap = 200

// CognitiveSQLiteStore is the narrow SQLite-only query surface backing jobs
// 2/3/4/5 (stuck_task_detection, decision_outcome_review,
// knowledge_to_skill_candidate, proposal_cleanup) when the scheduler is
// wired against a SQLite backend. nil under Postgres — disciplinePool raw
// SQL covers that path instead (unchanged from before this parity work).
//
// Deliberately NOT part of gtd/decision/knowledge/proposal.StoreIface
// (backend-security-design.md domain-ownership rule): these 4 methods are
// scheduler-local plumbing that only the jobs in this file need a symmetric
// SQLite counterpart for — mirroring the existing narrow-interface pattern
// already used in scheduler.go (PrunerStore, candidateRetentionStore, etc.)
// rather than widening a cross-domain interface every other implementer
// would have to grow a matching method for. GTD decision G4 (6ea0b014).
//
// Exported (capitalised) — not because external callers need to name it
// (they don't; internal/storage/sqlite.CognitiveJobsStore satisfies it
// structurally) but so cmd/server can declare a correctly-nil-typed local
// variable when wiring (see WithCognitiveDeps doc / main.go call site) —
// an unexported interface type cannot be named outside this package, which
// would otherwise force main.go to pass a possibly-nil *concrete* pointer
// into an interface parameter, a classic typed-nil-in-interface footgun.
type CognitiveSQLiteStore interface {
	// StuckTasks returns in_progress tasks whose updated_at is older than
	// olderThan (job 2). See internal/storage/sqlite.CognitiveJobsStore.
	StuckTasks(ctx context.Context, olderThan time.Duration) ([]db.Task, error)
	// DecisionsPendingOutcomeReview returns decisions older than olderThan
	// with no recorded outcome and no existing pending dedup proposal,
	// oldest-first, capped at limit rows (job 3).
	DecisionsPendingOutcomeReview(ctx context.Context, olderThan time.Duration, limit int) ([]db.Decision, error)
	// HighRecallKnowledgeItems returns non-archived knowledge items whose
	// recall_count exceeds minRecallCount (job 4).
	HighRecallKnowledgeItems(ctx context.Context, minRecallCount int) ([]db.KnowledgeItem, error)
	// ExpireStaleScheduledProposals marks scheduler-originated pending
	// proposals older than olderThan as rejected with reason, returning the
	// number of rows affected (job 5). SQLite-native reimplementation, NOT a
	// call-through to the Postgres code path — see
	// internal/storage/sqlite.CognitiveJobsStore.ExpireStaleScheduledProposals.
	ExpireStaleScheduledProposals(ctx context.Context, olderThan time.Duration, reason string) (int64, error)
}

// cognitiveDeps bundles dependencies for the 6 cognitive scheduler jobs.
// All fields are pointer/interface so the nil-check skip path works without
// panics.
type cognitiveDeps struct {
	reflection  reflection.StoreIface // job 6
	gtd         gtd.StoreIface        // jobs 1, 4, 6
	proposal    proposal.StoreIface   // jobs 1, 2, 3, 4, 6
	workspaceID *uuid.UUID            // jobs 1, 2, 3, 4, 6
	// sqlite is the SQLite counterpart to disciplinePool for jobs 2/3/4/5.
	// nil under Postgres (disciplinePool covers that path) and nil under
	// SQLite only if the caller failed to wire it — each job's switch falls
	// through to an explicit "no backend configured" skip in that case
	// rather than silently doing nothing.
	sqlite CognitiveSQLiteStore
}

// NewCognitiveDeps builds a cognitiveDeps bundle from the flat parameters
// passed by main.go. Any nil argument produces a valid bundle — individual
// jobs nil-check their required fields and skip gracefully. sqliteStore is
// nil when the server is Postgres-backed.
func NewCognitiveDeps(
	reflStore reflection.StoreIface,
	gtdStore gtd.StoreIface,
	propStore proposal.StoreIface,
	workspaceID *uuid.UUID,
	sqliteStore CognitiveSQLiteStore,
) *cognitiveDeps {
	return &cognitiveDeps{
		reflection:  reflStore,
		gtd:         gtdStore,
		proposal:    propStore,
		workspaceID: workspaceID,
		sqlite:      sqliteStore,
	}
}

// WithCognitiveDeps wires all 6 cognitive jobs and registers them with gocron.
// Must be called BEFORE sched.Start() in main.go. Returns an error if any
// gocron.NewJob call fails — caller should treat this as a startup fatal.
//
// Job time slots (Asia/Taipei):
//
//	Job 1  weekly_goal_review            Sunday  04:00
//	Job 2  stuck_task_detection          09:00 daily
//	Job 3  decision_outcome_review       10:00 daily
//	Job 4  knowledge_to_skill_candidate  Wednesday 03:30
//	Job 5  proposal_cleanup              02:00 daily
//	Job 6  behavior_rule_candidate       Saturday 03:00
func (sc *Scheduler) WithCognitiveDeps(deps *cognitiveDeps) error {
	sc.cognitiveDeps = deps

	type jobReg struct {
		def  gocron.JobDefinition
		task gocron.Task
		name string
	}

	regs := []jobReg{
		{
			def:  gocron.WeeklyJob(1, gocron.NewWeekdays(time.Sunday), gocron.NewAtTimes(gocron.NewAtTime(4, 0, 0))),
			task: gocron.NewTask(sc.runWeeklyGoalReview),
			name: "cognitive-weekly-goal-review",
		},
		{
			def:  gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(9, 0, 0))),
			task: gocron.NewTask(sc.runStuckTaskDetection),
			name: "cognitive-stuck-task-detection",
		},
		{
			def:  gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(10, 0, 0))),
			task: gocron.NewTask(sc.runDecisionOutcomeReview),
			name: "cognitive-decision-outcome-review",
		},
		{
			def:  gocron.WeeklyJob(1, gocron.NewWeekdays(time.Wednesday), gocron.NewAtTimes(gocron.NewAtTime(3, 30, 0))),
			task: gocron.NewTask(sc.runKnowledgeToSkillCandidate),
			name: "cognitive-knowledge-to-skill-candidate",
		},
		{
			def:  gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(2, 0, 0))),
			task: gocron.NewTask(sc.runProposalCleanup),
			name: "cognitive-proposal-cleanup",
		},
		{
			def:  gocron.WeeklyJob(1, gocron.NewWeekdays(time.Saturday), gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
			task: gocron.NewTask(sc.runBehaviorRuleCandidate),
			name: "cognitive-behavior-rule-candidate",
		},
	}

	for _, r := range regs {
		if _, err := sc.s.NewJob(
			r.def,
			r.task,
			gocron.WithName(r.name),
			// LimitModeReschedule: drop a run if the previous one is still
			// executing. Prevents goroutine pile-up on slow DB or AI calls.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		); err != nil {
			return fmt.Errorf("registering cognitive job %q: %w", r.name, err)
		}
		slog.Info("scheduler: cognitive job registered", "name", r.name)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared helper: detect -> propose -> log runner
// ---------------------------------------------------------------------------

// runProposalLoop is the common "detect -> propose -> log" tail shared by the
// 5 cognitive jobs that create one pending_proposal per detected item
// (weekly_goal_review, stuck_task_detection, decision_outcome_review,
// knowledge_to_skill_candidate, behavior_rule_candidate). Each job still owns
// its own detect query + empty-check; only the identical loop body — build
// payload, marshal, Create, log — is collapsed here. Go methods can't be
// generic, so this is a free function taking the scheduler's proposal store
// and workspace ID explicitly rather than a *Scheduler receiver.
//
// build(item) returns (proposal.Type, payload value, ok). ok=false skips the
// item without creating a proposal (used by behavior_rule_candidate to skip
// reflections with an empty Summary). Marshal/Create failures are logged via
// slog.Warn (with "item_id", idFunc(item) for log correlation — the
// pre-refactor per-job code logged this under a job-specific key like
// "goal_id"/"task_id"/"decision_id"; the shared runner standardises the key
// name to "item_id" but idFunc still supplies the same underlying ID value)
// and the loop continues (matches pre-refactor per-job behavior — one bad
// item never aborts the batch). job is used as the "cognitive: <job>: ..."
// log-message prefix; proposedBy is the CreateParams.ProposedBy value (the
// two differ for some jobs, e.g. job="stuck_task_detection" but
// proposedBy="scheduler:stuck_task"). countLabel is the slog key for the
// pre-loop item count in the completion log. Returns the number of proposals
// actually created so callers with a trailing gtd.LogActivity call can report
// it.
func runProposalLoop[T any](
	ctx context.Context,
	prop proposal.StoreIface,
	wsID *uuid.UUID,
	job, proposedBy, countLabel string,
	items []T,
	build func(item T) (proposal.Type, any, bool),
	idFunc func(item T) string,
) int {
	created := 0
	for _, item := range items {
		ptype, payloadVal, ok := build(item)
		if !ok {
			continue
		}
		payload, merr := json.Marshal(payloadVal)
		if merr != nil {
			slog.Warn(fmt.Sprintf("cognitive: %s: marshal payload failed", job), "item_id", idFunc(item), "err", merr)
			continue
		}
		if _, cerr := prop.Create(ctx, proposal.CreateParams{
			WorkspaceID: wsID,
			Type:        ptype,
			Payload:     payload,
			ProposedBy:  proposedBy,
		}); cerr != nil {
			slog.Warn(fmt.Sprintf("cognitive: %s: Create proposal failed", job), "item_id", idFunc(item), "err", cerr)
			continue
		}
		created++
	}
	slog.Info(fmt.Sprintf("cognitive: %s: completed", job), countLabel, len(items), "proposals_created", created)
	return created
}

// ---------------------------------------------------------------------------
// Job 1: weekly_goal_review
// ---------------------------------------------------------------------------

// runWeeklyGoalReview fires at Sunday 04:00 Asia/Taipei. It queries active
// goals via gtd.StoreIface.ActiveGoals and creates one pending_proposal per
// goal with type='knowledge' and proposed_by='scheduler:weekly_goal_review'.
//
// Skip path: if cognitiveDeps is nil or gtd/proposal store is missing.
func (sc *Scheduler) runWeeklyGoalReview() {
	deps := sc.cognitiveDeps
	if deps == nil || deps.gtd == nil || deps.proposal == nil {
		slog.Info("cognitive: weekly_goal_review skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	goals, err := deps.gtd.ActiveGoals(ctx)
	if err != nil {
		slog.Warn("cognitive: weekly_goal_review: ActiveGoals failed", "err", err)
		return
	}

	if len(goals) == 0 {
		slog.Info("cognitive: weekly_goal_review: no active goals, skipping")
		return
	}

	runProposalLoop(
		ctx, deps.proposal, deps.workspaceID,
		"weekly_goal_review", "scheduler:weekly_goal_review", "goals_scanned",
		goals,
		func(g db.Goal) (proposal.Type, any, bool) {
			return proposal.TypeKnowledge, proposal.KnowledgePayload{
				Title:   fmt.Sprintf("Weekly goal review: %s", g.Title),
				Content: fmt.Sprintf("Goal '%s' is still active. Consider reviewing progress or updating status.", g.Title),
			}, true
		},
		func(g db.Goal) string { return g.ID.String() },
	)
}

// ---------------------------------------------------------------------------
// Job 2: stuck_task_detection
// ---------------------------------------------------------------------------

// stuckTask is the minimal id+title projection job 2 needs from either
// backend. PG produces it via pgStuckTasks's raw-SQL scan; SQLite produces
// it by projecting cognitiveDeps.sqlite.StuckTasks's []db.Task rows.
type stuckTask struct {
	id    uuid.UUID
	title string
}

// runStuckTaskDetection fires at 09:00 daily. On Postgres it executes a raw
// SQL query against disciplinePool; on SQLite it calls the
// CognitiveSQLiteStore.StuckTasks adapter (GTD decision G4, 6ea0b014 — this
// job is one of the 4 with mandatory dual-backend parity because stuck-task
// proposals are user-observable). Either way it finds in_progress tasks not
// updated in 7 days and creates one TypeTask pending_proposal per stuck
// task. The TaskPayload shape matches confirm_proposal materialisation; do
// not write KnowledgePayload into TypeTask rows.
//
// Skip path: if cognitiveDeps is nil/incomplete, or neither backend is
// wired, logs info and returns without creating proposals.
func (sc *Scheduler) runStuckTaskDetection() {
	deps := sc.cognitiveDeps
	if deps == nil || deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: stuck_task_detection skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	var tasks []stuckTask
	switch {
	case sc.disciplinePool != nil:
		pgTasks, err := sc.pgStuckTasks(ctx, deps.workspaceID)
		if err != nil {
			slog.Warn("cognitive: stuck_task_detection: query failed", "err", err)
			return
		}
		tasks = pgTasks
	case deps.sqlite != nil:
		rows, err := deps.sqlite.StuckTasks(ctx, stuckTaskLookback)
		if err != nil {
			slog.Warn("cognitive: stuck_task_detection: SQLite query failed", "err", err)
			return
		}
		for _, t := range rows {
			tasks = append(tasks, stuckTask{id: t.ID, title: t.Title})
		}
	default:
		slog.Info("cognitive: stuck_task_detection skipped (no backend configured)")
		return
	}

	runProposalLoop(
		ctx, deps.proposal, deps.workspaceID,
		"stuck_task_detection", "scheduler:stuck_task", "stuck_tasks_found",
		tasks,
		func(t stuckTask) (proposal.Type, any, bool) {
			content := fmt.Sprintf(
				"Task '%s' has been in_progress for more than 7 days without an update."+
					" Consider unblocking or deprioritising.",
				t.title,
			)
			return proposal.TypeTask, proposal.TaskPayload{
				Title:          fmt.Sprintf("Unblock stuck task: %s", t.title),
				SourceTool:     "scheduler:stuck_task",
				Description:    content,
				SuggestedKind:  "general",
				SourceEntityID: t.id.String(),
			}, true
		},
		func(t stuckTask) string { return t.id.String() },
	)
}

// pgStuckTasks runs the Postgres raw-SQL stuck-task query. Behaviour is
// unchanged from the pre-parity implementation: a scan failure or
// rows.Err logs a warning and does not abort the batch (only a query-level
// error aborts, via the returned error).
//
// Dedup guard (matches job 3's shape, cognitive_jobs.go's
// pgDecisionsPendingOutcomeReview): excludes tasks that already have a
// pending, scheduler:stuck_task-originated proposal, matched via
// payload->>'source_entity_id' (see proposal.TaskPayload.SourceEntityID).
// Without this, every run re-proposed the same still-in_progress task
// indefinitely — same failure mode as the 2026-07-19 decision_outcome_review
// incident (decisionOutcomeReviewDailyCap's doc comment), just without a
// context-deadline symptom since stuck-task counts stay small at personal
// scale. Dedup only suppresses duplicate *pending* proposals — the user
// still must accept each one via confirm_proposal (Excessive Agency boundary
// unchanged).
func (sc *Scheduler) pgStuckTasks(ctx context.Context, workspaceID *uuid.UUID) ([]stuckTask, error) {
	const q = `SELECT t.id, t.title, t.updated_at FROM tasks t
WHERE t.workspace_id = $1
  AND t.status = 'in_progress'
  AND t.updated_at < NOW() - INTERVAL '` + stuckTaskInterval + `'
  AND NOT EXISTS (
      SELECT 1 FROM pending_proposals p
      WHERE p.type = 'task'
        AND p.proposed_by = 'scheduler:stuck_task'
        AND p.status = 'pending'
        AND p.payload->>'source_entity_id' = t.id::text
  )`

	rows, err := sc.disciplinePool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("querying stuck tasks: %w", err)
	}
	defer rows.Close()

	var tasks []stuckTask
	for rows.Next() {
		var t stuckTask
		var updatedAt time.Time
		if serr := rows.Scan(&t.id, &t.title, &updatedAt); serr != nil {
			slog.Warn("cognitive: stuck_task_detection: scan failed", "err", serr)
			continue
		}
		tasks = append(tasks, t)
	}
	if rerr := rows.Err(); rerr != nil {
		slog.Warn("cognitive: stuck_task_detection: rows iteration error", "err", rerr)
	}
	return tasks, nil
}

// ---------------------------------------------------------------------------
// Job 3: decision_outcome_review
// ---------------------------------------------------------------------------

// decRow is the minimal id+title projection job 3 needs from either
// backend. PG produces it via pgDecisionsPendingOutcomeReview's raw-SQL
// scan; SQLite produces it by projecting
// cognitiveDeps.sqlite.DecisionsPendingOutcomeReview's []db.Decision rows.
type decRow struct {
	id    uuid.UUID
	title string
}

// runDecisionOutcomeReview fires at 10:00 daily. On Postgres it finds
// decisions older than 30 days without any recorded outcome via a raw SQL
// query on disciplinePool; on SQLite it calls
// CognitiveSQLiteStore.DecisionsPendingOutcomeReview (GTD decision G4,
// 6ea0b014 — decision-outcome-review proposals are user-observable, so this
// job is one of the 4 with mandatory dual-backend parity). Either way it
// creates one TypeTask pending_proposal per qualifying decision so the user
// can confirm the follow-up task to record the outcome.
//
// Dedup + daily cap (fix for 2026-07-19 incident, see decisionOutcomeReviewDailyCap
// doc comment): both backends exclude decisions that already have a pending,
// scheduler-originated proposal for this job (matched via
// payload->>'source_entity_id' — see proposal.TaskPayload.SourceEntityID) and
// cap the result set to decisionOutcomeReviewDailyCap rows, ordered oldest
// decision first so consecutive daily runs drain the backlog forward. This
// does NOT bypass the human-confirm gate — dedup only prevents duplicate
// *pending* proposals; the user still must accept each one via
// confirm_proposal before it becomes a real task (Excessive Agency boundary
// unchanged).
//
// Skip path: if cognitiveDeps is nil/incomplete, or neither backend is
// wired, logs info and returns without creating proposals.
func (sc *Scheduler) runDecisionOutcomeReview() {
	deps := sc.cognitiveDeps
	if deps == nil || deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: decision_outcome_review skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	var decisions []decRow
	switch {
	case sc.disciplinePool != nil:
		pgDecisions, err := sc.pgDecisionsPendingOutcomeReview(ctx, deps.workspaceID)
		if err != nil {
			slog.Warn("cognitive: decision_outcome_review: query failed", "err", err)
			return
		}
		decisions = pgDecisions
	case deps.sqlite != nil:
		rows, err := deps.sqlite.DecisionsPendingOutcomeReview(ctx, decisionOutcomeLookback, decisionOutcomeReviewDailyCap)
		if err != nil {
			slog.Warn("cognitive: decision_outcome_review: SQLite query failed", "err", err)
			return
		}
		for _, d := range rows {
			decisions = append(decisions, decRow{id: d.ID, title: d.Title})
		}
	default:
		slog.Info("cognitive: decision_outcome_review skipped (no backend configured)")
		return
	}

	runProposalLoop(
		ctx, deps.proposal, deps.workspaceID,
		"decision_outcome_review", "scheduler:decision_outcome_review", "decisions_without_outcome",
		decisions,
		func(d decRow) (proposal.Type, any, bool) {
			content := fmt.Sprintf(
				"Decision '%s' was made more than 30 days ago but has no recorded outcome."+
					" Consider recording what happened.",
				d.title,
			)
			return proposal.TypeTask, proposal.TaskPayload{
				Title:          fmt.Sprintf("Record outcome for decision: %s", d.title),
				SourceTool:     "scheduler:decision_outcome_review",
				Description:    content,
				SuggestedKind:  "general",
				SourceEntityID: d.id.String(),
			}, true
		},
		func(d decRow) string { return d.id.String() },
	)
}

// pgDecisionsPendingOutcomeReview runs the Postgres raw-SQL query, unchanged
// from the pre-parity implementation including the 2026-07-19-incident dedup
// + daily-cap guards documented on decisionOutcomeReviewDailyCap. A scan
// failure or rows.Err logs a warning and does not abort the batch (only a
// query-level error aborts, via the returned error).
func (sc *Scheduler) pgDecisionsPendingOutcomeReview(ctx context.Context, workspaceID *uuid.UUID) ([]decRow, error) {
	const q = `SELECT d.id, d.title FROM decisions d
WHERE d.workspace_id = $1
  AND d.created_at < NOW() - INTERVAL '` + decisionOutcomeInterval + `'
  AND NOT EXISTS (
      SELECT 1 FROM outcomes o
      WHERE o.entity_type = 'decision'
        AND o.entity_id = d.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM pending_proposals p
      WHERE p.type = 'task'
        AND p.proposed_by = 'scheduler:decision_outcome_review'
        AND p.status = 'pending'
        AND p.payload->>'source_entity_id' = d.id::text
  )
ORDER BY d.created_at ASC
LIMIT $2`

	rows, err := sc.disciplinePool.Query(ctx, q, workspaceID, decisionOutcomeReviewDailyCap)
	if err != nil {
		return nil, fmt.Errorf("querying decisions pending outcome review: %w", err)
	}
	defer rows.Close()

	var decisions []decRow
	for rows.Next() {
		var d decRow
		if serr := rows.Scan(&d.id, &d.title); serr != nil {
			slog.Warn("cognitive: decision_outcome_review: scan failed", "err", serr)
			continue
		}
		decisions = append(decisions, d)
	}
	if rerr := rows.Err(); rerr != nil {
		slog.Warn("cognitive: decision_outcome_review: rows iteration error", "err", rerr)
	}
	return decisions, nil
}

// ---------------------------------------------------------------------------
// Job 4: knowledge_to_skill_candidate
// ---------------------------------------------------------------------------

// kiRow is the minimal id+title projection job 4 needs from either backend.
// PG produces it via pgHighRecallKnowledgeItems's raw-SQL scan; SQLite
// produces it by projecting
// cognitiveDeps.sqlite.HighRecallKnowledgeItems's []db.KnowledgeItem rows.
type kiRow struct {
	id    uuid.UUID
	title string
}

// runKnowledgeToSkillCandidate fires at Wednesday 03:30 Asia/Taipei. On
// Postgres it uses raw SQL on disciplinePool to find knowledge_items with
// recall_count > 3 (high-recall knowledge is a strong skill candidate); on
// SQLite it calls CognitiveSQLiteStore.HighRecallKnowledgeItems (GTD
// decision G4, 6ea0b014 — skill-candidate proposals are user-observable, so
// this job is one of the 4 with mandatory dual-backend parity). Either way
// it creates one pending_proposal per high-recall item.
//
// NOTE: The PG path bypasses knowledge.StoreIface intentionally — the store
// has no ListByRecallCount method and adding one is outside M7 scope. Raw
// SQL is an explicitly documented exception (SA risk flag). The SQLite
// adapter similarly stays outside knowledge.StoreIface — see
// CognitiveSQLiteStore's doc comment.
//
// Skip path: if cognitiveDeps is nil/incomplete, or neither backend is
// wired, logs info and returns without creating proposals.
func (sc *Scheduler) runKnowledgeToSkillCandidate() {
	deps := sc.cognitiveDeps
	if deps == nil || deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: knowledge_to_skill_candidate skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	var items []kiRow
	switch {
	case sc.disciplinePool != nil:
		pgItems, err := sc.pgHighRecallKnowledgeItems(ctx, deps.workspaceID)
		if err != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: query failed", "err", err)
			return
		}
		items = pgItems
	case deps.sqlite != nil:
		rows, err := deps.sqlite.HighRecallKnowledgeItems(ctx, knowledgeToSkillMinRecallCount)
		if err != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: SQLite query failed", "err", err)
			return
		}
		for _, ki := range rows {
			items = append(items, kiRow{id: ki.ID, title: ki.Title})
		}
	default:
		slog.Info("cognitive: knowledge_to_skill_candidate skipped (no backend configured)")
		return
	}

	created := runProposalLoop(
		ctx, deps.proposal, deps.workspaceID,
		"knowledge_to_skill_candidate", "scheduler:knowledge_to_skill", "high_recall_items",
		items,
		func(ki kiRow) (proposal.Type, any, bool) {
			return proposal.TypeKnowledge, proposal.KnowledgePayload{
				Title: fmt.Sprintf("Skill candidate: %s", ki.title),
				Content: fmt.Sprintf(
					"Knowledge item '%s' has been recalled more than 3 times. Consider promoting it to a skill or playbook.",
					ki.title,
				),
				SourceEntityID: ki.id.String(),
			}, true
		},
		func(ki kiRow) string { return ki.id.String() },
	)
	// Direction-D instrumentation: log activity so atom/outcome growth is visible
	// in the dashboard automation feed. Best-effort: failures do not abort the job.
	if deps.gtd != nil {
		if logErr := deps.gtd.LogActivity(
			ctx, "scheduler", "knowledge_to_skill_candidate",
			nil,
			fmt.Sprintf("high_recall_items=%d proposals_created=%d", len(items), created),
		); logErr != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: LogActivity failed", "err", logErr)
		}
	}
}

// pgHighRecallKnowledgeItems runs the Postgres raw-SQL query. A scan failure
// or rows.Err logs a warning and does not abort the batch (only a
// query-level error aborts, via the returned error).
//
// Dedup guard (matches job 3's shape, cognitive_jobs.go's
// pgDecisionsPendingOutcomeReview): excludes knowledge items that already
// have a pending, scheduler:knowledge_to_skill-originated proposal, matched
// via payload->>'source_entity_id' (see
// proposal.KnowledgePayload.SourceEntityID). Without this, every weekly run
// re-proposed the same still-high-recall item indefinitely. Dedup only
// suppresses duplicate *pending* proposals — the user still must accept each
// one via confirm_proposal (Excessive Agency boundary unchanged).
func (sc *Scheduler) pgHighRecallKnowledgeItems(ctx context.Context, workspaceID *uuid.UUID) ([]kiRow, error) {
	const q = `SELECT ki.id, ki.title FROM knowledge_items ki
WHERE ki.workspace_id = $1
  AND ki.recall_count > 3
  AND ki.archived_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM pending_proposals p
      WHERE p.type = 'knowledge'
        AND p.proposed_by = 'scheduler:knowledge_to_skill'
        AND p.status = 'pending'
        AND p.payload->>'source_entity_id' = ki.id::text
  )`

	rows, err := sc.disciplinePool.Query(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("querying high recall knowledge items: %w", err)
	}
	defer rows.Close()

	var items []kiRow
	for rows.Next() {
		var ki kiRow
		if serr := rows.Scan(&ki.id, &ki.title); serr != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: scan failed", "err", serr)
			continue
		}
		items = append(items, ki)
	}
	if rerr := rows.Err(); rerr != nil {
		slog.Warn("cognitive: knowledge_to_skill_candidate: rows iteration error", "err", rerr)
	}
	return items, nil
}

// ---------------------------------------------------------------------------
// Job 5: proposal_cleanup
// ---------------------------------------------------------------------------

// runProposalCleanup fires at 02:00 daily. It marks scheduler-originated
// pending proposals as 'rejected' with reason='expired by scheduler' when they
// are older than 30 days and still pending. On Postgres this is a raw SQL
// UPDATE against disciplinePool; on SQLite it calls
// CognitiveSQLiteStore.ExpireStaleScheduledProposals — a genuine SQLite-native
// reimplementation of the retention arithmetic (GTD decision G4, 6ea0b014),
// NOT a call-through to the Postgres code path, since SQLite has no
// `NOW() - INTERVAL` syntax.
//
// IMPORTANT: Both backends ONLY touch rows WHERE proposed_by LIKE
// 'scheduler:%'. User-submitted proposals (proposed_by NOT LIKE
// 'scheduler:%') are NEVER touched. This prevents silently rejecting a
// proposal the user was about to accept via the UI.
//
// The UPDATE (not DELETE) ensures the audit trail survives until the
// runDailyPendingProposalsPrune 90-day resolved-row purge picks them up
// (Postgres only — see the PG-only prune capability contract in
// scheduler.go's pgOnlyJobs).
//
// Skip path: if neither backend is wired, logs info.
func (sc *Scheduler) runProposalCleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	switch {
	case sc.disciplinePool != nil:
		// Only expire scheduler-originated proposals. The proposed_by LIKE
		// 'scheduler:%' guard is the critical safety boundary — it MUST NOT
		// be widened to touch user-submitted proposals.
		const q = `UPDATE pending_proposals
SET status = 'rejected', resolved_at = NOW(), reason = 'expired by scheduler'
WHERE proposed_by LIKE 'scheduler:%'
  AND status = 'pending'
  AND created_at < NOW() - INTERVAL '` + proposalCleanupInterval + `'`

		tag, err := sc.disciplinePool.Exec(ctx, q)
		if err != nil {
			slog.Warn("cognitive: proposal_cleanup: UPDATE failed", "err", err)
			return
		}
		slog.Info(
			"cognitive: proposal_cleanup: completed",
			"rows_affected", tag.RowsAffected(),
			"retention", proposalCleanupInterval,
		)
	case sc.cognitiveDeps != nil && sc.cognitiveDeps.sqlite != nil:
		n, err := sc.cognitiveDeps.sqlite.ExpireStaleScheduledProposals(ctx, proposalCleanupLookback, "expired by scheduler")
		if err != nil {
			slog.Warn("cognitive: proposal_cleanup: SQLite UPDATE failed", "err", err)
			return
		}
		slog.Info(
			"cognitive: proposal_cleanup: completed",
			"rows_affected", n,
			"retention", proposalCleanupInterval,
		)
	default:
		slog.Info("cognitive: proposal_cleanup skipped (no backend configured)")
	}
}

// ---------------------------------------------------------------------------
// Job 6: behavior_rule_candidate_generation
// ---------------------------------------------------------------------------

// runBehaviorRuleCandidate fires at Saturday 03:00 Asia/Taipei (free slot:
// Saturday 23:00 is already taken by Saturday reflection/consolidation).
// It queries reflections from the last 7 days that have non-NULL
// patterns_detected, then creates one pending_proposal per pattern-bearing
// reflection summary.
//
// Skip path: if cognitiveDeps.reflection is nil, logs info.
func (sc *Scheduler) runBehaviorRuleCandidate() {
	deps := sc.cognitiveDeps
	if deps == nil || deps.reflection == nil {
		slog.Info("cognitive: behavior_rule_candidate skipped (reflection store not configured)")
		return
	}
	if deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: behavior_rule_candidate skipped (proposal store or workspaceID not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	since := time.Now().Add(-behaviorCandidateDays)
	reflections, err := deps.reflection.RecentWithPatterns(ctx, deps.workspaceID, since, 50)
	if err != nil {
		slog.Warn("cognitive: behavior_rule_candidate: RecentWithPatterns failed", "err", err)
		return
	}

	if len(reflections) == 0 {
		slog.Info("cognitive: behavior_rule_candidate: no pattern-bearing reflections found, skipping")
		return
	}

	created := runProposalLoop(
		ctx, deps.proposal, deps.workspaceID,
		"behavior_rule_candidate", "scheduler:behavior_rule_candidate", "reflections_with_patterns",
		reflections,
		func(r *reflection.Reflection) (proposal.Type, any, bool) {
			if r.Summary == "" {
				return "", nil, false
			}
			return proposal.TypeKnowledge, proposal.KnowledgePayload{
				Title:   fmt.Sprintf("Behavior rule candidate from %s reflection", r.Type),
				Content: fmt.Sprintf("Pattern detected in reflection (type=%s, created=%s): %s", r.Type, r.CreatedAt.Format("2006-01-02"), r.Summary),
			}, true
		},
		func(r *reflection.Reflection) string { return r.ID.String() },
	)
	// Direction-D instrumentation: best-effort activity log for dashboard feed.
	if deps.gtd != nil {
		if logErr := deps.gtd.LogActivity(
			ctx, "scheduler", "behavior_rule_candidate",
			nil,
			fmt.Sprintf("reflections_with_patterns=%d proposals_created=%d", len(reflections), created),
		); logErr != nil {
			slog.Warn("cognitive: behavior_rule_candidate: LogActivity failed", "err", logErr)
		}
	}
}
