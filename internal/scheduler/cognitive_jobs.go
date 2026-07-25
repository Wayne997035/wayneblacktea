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

// cognitiveDeps bundles dependencies for the 6 cognitive scheduler jobs.
// All fields are pointer/interface so the nil-check skip path works without
// panics.
type cognitiveDeps struct {
	reflection  reflection.StoreIface // job 6
	gtd         gtd.StoreIface        // jobs 1, 4, 6
	proposal    proposal.StoreIface   // jobs 1, 2, 3, 4, 6
	workspaceID *uuid.UUID            // jobs 1, 2, 3, 4, 6
}

// NewCognitiveDeps builds a cognitiveDeps bundle from the flat parameters
// passed by main.go. Any nil argument produces a valid bundle — individual
// jobs nil-check their required fields and skip gracefully.
func NewCognitiveDeps(
	reflStore reflection.StoreIface,
	gtdStore gtd.StoreIface,
	propStore proposal.StoreIface,
	workspaceID *uuid.UUID,
) *cognitiveDeps {
	return &cognitiveDeps{
		reflection:  reflStore,
		gtd:         gtdStore,
		proposal:    propStore,
		workspaceID: workspaceID,
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

// runStuckTaskDetection fires at 09:00 daily. It executes a raw SQL query
// against disciplinePool to find in_progress tasks not updated in 7 days, then
// creates one TypeTask pending_proposal per stuck task. The TaskPayload shape
// matches confirm_proposal materialisation; do not write KnowledgePayload into
// TypeTask rows.
//
// Skip path: if disciplinePool is nil, logs info (SQLite dev path).
func (sc *Scheduler) runStuckTaskDetection() {
	deps := sc.cognitiveDeps
	if sc.disciplinePool == nil {
		slog.Info("cognitive: stuck_task_detection skipped (postgres pool not configured)")
		return
	}
	if deps == nil || deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: stuck_task_detection skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	const q = `SELECT id, title, updated_at FROM tasks
WHERE workspace_id = $1
  AND status = 'in_progress'
  AND updated_at < NOW() - INTERVAL '` + stuckTaskInterval + `'`

	rows, err := sc.disciplinePool.Query(ctx, q, deps.workspaceID)
	if err != nil {
		slog.Warn("cognitive: stuck_task_detection: query failed", "err", err)
		return
	}
	defer rows.Close()

	type stuckTask struct {
		id    uuid.UUID
		title string
	}
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
				Title:         fmt.Sprintf("Unblock stuck task: %s", t.title),
				SourceTool:    "scheduler:stuck_task",
				Description:   content,
				SuggestedKind: "general",
			}, true
		},
		func(t stuckTask) string { return t.id.String() },
	)
}

// ---------------------------------------------------------------------------
// Job 3: decision_outcome_review
// ---------------------------------------------------------------------------

// runDecisionOutcomeReview fires at 10:00 daily. It finds decisions older than
// 30 days without any recorded outcome via a raw SQL query on disciplinePool,
// then creates one TypeTask pending_proposal per qualifying decision so the
// user can confirm the follow-up task to record the outcome.
//
// Dedup + daily cap (fix for 2026-07-19 incident, see decisionOutcomeReviewDailyCap
// doc comment): the query excludes decisions that already have a pending,
// scheduler-originated proposal for this job (matched via
// payload->>'source_entity_id' — see proposal.TaskPayload.SourceEntityID) and
// caps the result set to decisionOutcomeReviewDailyCap rows, ordered oldest
// decision first so consecutive daily runs drain the backlog forward. This
// does NOT bypass the human-confirm gate — dedup only prevents duplicate
// *pending* proposals; the user still must accept each one via
// confirm_proposal before it becomes a real task (Excessive Agency boundary
// unchanged).
//
// Skip path: if disciplinePool is nil, logs info.
func (sc *Scheduler) runDecisionOutcomeReview() {
	deps := sc.cognitiveDeps
	if sc.disciplinePool == nil {
		slog.Info("cognitive: decision_outcome_review skipped (postgres pool not configured)")
		return
	}
	if deps == nil || deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: decision_outcome_review skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

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

	rows, err := sc.disciplinePool.Query(ctx, q, deps.workspaceID, decisionOutcomeReviewDailyCap)
	if err != nil {
		slog.Warn("cognitive: decision_outcome_review: query failed", "err", err)
		return
	}
	defer rows.Close()

	type decRow struct {
		id    uuid.UUID
		title string
	}
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

// ---------------------------------------------------------------------------
// Job 4: knowledge_to_skill_candidate
// ---------------------------------------------------------------------------

// runKnowledgeToSkillCandidate fires at Wednesday 03:30 Asia/Taipei. It uses
// raw SQL on disciplinePool to find knowledge_items with recall_count > 3
// (high-recall knowledge is a strong skill candidate), then creates one
// pending_proposal per item.
//
// NOTE: This job bypasses knowledge.StoreIface intentionally — the store has
// no ListByRecallCount method and adding one is outside M7 scope. Raw SQL is
// an explicitly documented exception (SA risk flag).
//
// Skip path: if disciplinePool is nil, logs info.
func (sc *Scheduler) runKnowledgeToSkillCandidate() {
	deps := sc.cognitiveDeps
	if sc.disciplinePool == nil {
		slog.Info("cognitive: knowledge_to_skill_candidate skipped (postgres pool not configured)")
		return
	}
	if deps == nil || deps.proposal == nil || deps.workspaceID == nil {
		slog.Info("cognitive: knowledge_to_skill_candidate skipped (cognitive deps not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	const q = `SELECT id, title FROM knowledge_items
WHERE workspace_id = $1
  AND recall_count > 3
  AND archived_at IS NULL`

	rows, err := sc.disciplinePool.Query(ctx, q, deps.workspaceID)
	if err != nil {
		slog.Warn("cognitive: knowledge_to_skill_candidate: query failed", "err", err)
		return
	}
	defer rows.Close()

	type kiRow struct {
		id    uuid.UUID
		title string
	}
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

// ---------------------------------------------------------------------------
// Job 5: proposal_cleanup
// ---------------------------------------------------------------------------

// runProposalCleanup fires at 02:00 daily. It marks scheduler-originated
// pending proposals as 'rejected' with reason='expired by scheduler' when they
// are older than 30 days and still pending.
//
// IMPORTANT: This job ONLY touches rows WHERE proposed_by LIKE 'scheduler:%'.
// User-submitted proposals (proposed_by NOT LIKE 'scheduler:%') are NEVER
// touched. This prevents silently rejecting a proposal the user was about to
// accept via the UI.
//
// The UPDATE (not DELETE) ensures the audit trail survives until the
// runDailyPendingProposalsPrune 90-day resolved-row purge picks them up.
//
// Skip path: if disciplinePool is nil, logs info.
func (sc *Scheduler) runProposalCleanup() {
	if sc.disciplinePool == nil {
		slog.Info("cognitive: proposal_cleanup skipped (postgres pool not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

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
