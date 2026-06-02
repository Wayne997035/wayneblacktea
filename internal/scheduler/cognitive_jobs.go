// Package scheduler — cognitive_jobs.go
//
// Implements the 7 Memory-7 cognitive scheduler jobs. All jobs are registered
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

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// cognitiveJobTimeout is the per-job context deadline. All 7 jobs involve DB
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

// cognitiveDeps bundles dependencies for the 7 cognitive scheduler jobs.
// All fields are pointer/interface so the nil-check skip path works without
// panics.
type cognitiveDeps struct {
	reflection  reflection.StoreIface // jobs 1, 7
	gtd         gtd.StoreIface        // job 2
	proposal    proposal.StoreIface   // jobs 1, 2, 3, 4, 5, 7
	workspaceID *uuid.UUID            // jobs 1, 2, 3, 4, 5, 7
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

// WithCognitiveDeps wires all 7 cognitive jobs and registers them with gocron.
// Must be called BEFORE sched.Start() in main.go. Returns an error if any
// gocron.NewJob call fails — caller should treat this as a startup fatal.
//
// Job time slots (Asia/Taipei):
//
//	Job 1  daily_reflection              03:15 daily
//	Job 2  weekly_goal_review            Sunday  04:00
//	Job 3  stuck_task_detection          09:00 daily
//	Job 4  decision_outcome_review       10:00 daily
//	Job 5  knowledge_to_skill_candidate  Wednesday 03:30
//	Job 6  proposal_cleanup              02:00 daily
//	Job 7  behavior_rule_candidate       Saturday 03:00
func (sc *Scheduler) WithCognitiveDeps(deps *cognitiveDeps) error {
	sc.cognitiveDeps = deps

	type jobReg struct {
		def  gocron.JobDefinition
		task gocron.Task
		name string
	}

	regs := []jobReg{
		{
			def:  gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 15, 0))),
			task: gocron.NewTask(sc.runDailyReflectionJob),
			name: "cognitive-daily-reflection",
		},
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
// Job 1: daily_reflection
// ---------------------------------------------------------------------------

// runDailyReflectionJob fires at 03:15 Asia/Taipei. It builds a plain-text
// reflection summary from recent activity + decisions (same function used by
// the Saturday weekly reflection) and stores a 'daily' reflection record via
// reflection.StoreIface.Create — no AI call, no pending_proposal.
//
// Skip path: if cognitiveDeps.reflection is nil, logs info and returns.
func (sc *Scheduler) runDailyReflectionJob() {
	deps := sc.cognitiveDeps
	if deps == nil || deps.reflection == nil {
		slog.Info("cognitive: daily_reflection skipped (reflection store not configured)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), cognitiveJobTimeout)
	defer cancel()

	// We need recent activities and decisions for the summary. Use the existing
	// GTD store when available; if not, emit an empty summary (still records the
	// daily touchpoint in the reflections table).
	var summaryText string
	if deps.gtd != nil {
		since := time.Now().Add(-reflectionLookback)
		activities, err := deps.gtd.ListActivityLogsSince(ctx, since, reflectionMaxActivity)
		if err != nil {
			slog.Warn("cognitive: daily_reflection: listing activities failed", "err", err)
			// Fall through with empty activities — we still want a daily record.
		}
		summaryText = buildReflectionSummary(activities, nil)
	} else {
		summaryText = "daily reflection: no activity data available"
	}

	if summaryText == "" {
		summaryText = "daily reflection: no recent activity"
	}

	reflType := "daily"
	_, err := deps.reflection.Create(ctx, reflection.CreateParams{
		WorkspaceID: deps.workspaceID,
		Type:        reflType,
		Summary:     summaryText,
		Confidence:  0.8,
	})
	if err != nil {
		slog.Warn("cognitive: daily_reflection: Create failed", "err", err)
		return
	}
	slog.Info("cognitive: daily_reflection: completed", "type", reflType)
}

// ---------------------------------------------------------------------------
// Job 2: weekly_goal_review
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

	created := 0
	for _, g := range goals {
		payload, merr := json.Marshal(proposal.KnowledgePayload{
			Title:   fmt.Sprintf("Weekly goal review: %s", g.Title),
			Content: fmt.Sprintf("Goal '%s' is still active. Consider reviewing progress or updating status.", g.Title),
		})
		if merr != nil {
			slog.Warn("cognitive: weekly_goal_review: marshal payload failed", "goal_id", g.ID, "err", merr)
			continue
		}
		if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
			WorkspaceID: deps.workspaceID,
			Type:        proposal.TypeKnowledge,
			Payload:     payload,
			ProposedBy:  "scheduler:weekly_goal_review",
		}); cerr != nil {
			slog.Warn("cognitive: weekly_goal_review: Create proposal failed", "goal_id", g.ID, "err", cerr)
			continue
		}
		created++
	}
	slog.Info("cognitive: weekly_goal_review: completed",
		"goals_scanned", len(goals),
		"proposals_created", created,
	)
}

// ---------------------------------------------------------------------------
// Job 3: stuck_task_detection
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

	created := 0
	for _, t := range tasks {
		content := fmt.Sprintf(
			"Task '%s' has been in_progress for more than 7 days without an update."+
				" Consider unblocking or deprioritising.",
			t.title,
		)
		payload, merr := json.Marshal(proposal.TaskPayload{
			Title:         fmt.Sprintf("Unblock stuck task: %s", t.title),
			SourceTool:    "scheduler:stuck_task",
			Description:   content,
			SuggestedKind: "general",
		})
		if merr != nil {
			slog.Warn("cognitive: stuck_task_detection: marshal payload failed", "task_id", t.id, "err", merr)
			continue
		}
		if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
			WorkspaceID: deps.workspaceID,
			Type:        proposal.TypeTask,
			Payload:     payload,
			ProposedBy:  "scheduler:stuck_task",
		}); cerr != nil {
			slog.Warn("cognitive: stuck_task_detection: Create proposal failed", "task_id", t.id, "err", cerr)
			continue
		}
		created++
	}
	slog.Info("cognitive: stuck_task_detection: completed",
		"stuck_tasks_found", len(tasks),
		"proposals_created", created,
	)
}

// ---------------------------------------------------------------------------
// Job 4: decision_outcome_review
// ---------------------------------------------------------------------------

// runDecisionOutcomeReview fires at 10:00 daily. It finds decisions older than
// 30 days without any recorded outcome via a raw LEFT JOIN SQL query on
// disciplinePool, then creates one TypeTask pending_proposal per qualifying
// decision so the user can confirm the follow-up task to record the outcome.
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
  )`

	rows, err := sc.disciplinePool.Query(ctx, q, deps.workspaceID)
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

	created := 0
	for _, d := range decisions {
		content := fmt.Sprintf(
			"Decision '%s' was made more than 30 days ago but has no recorded outcome."+
				" Consider recording what happened.",
			d.title,
		)
		payload, merr := json.Marshal(proposal.TaskPayload{
			Title:         fmt.Sprintf("Record outcome for decision: %s", d.title),
			SourceTool:    "scheduler:decision_outcome_review",
			Description:   content,
			SuggestedKind: "general",
		})
		if merr != nil {
			slog.Warn("cognitive: decision_outcome_review: marshal payload failed", "decision_id", d.id, "err", merr)
			continue
		}
		if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
			WorkspaceID: deps.workspaceID,
			Type:        proposal.TypeTask,
			Payload:     payload,
			ProposedBy:  "scheduler:decision_outcome_review",
		}); cerr != nil {
			slog.Warn("cognitive: decision_outcome_review: Create proposal failed", "decision_id", d.id, "err", cerr)
			continue
		}
		created++
	}
	slog.Info("cognitive: decision_outcome_review: completed",
		"decisions_without_outcome", len(decisions),
		"proposals_created", created,
	)
}

// ---------------------------------------------------------------------------
// Job 5: knowledge_to_skill_candidate
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

	created := 0
	for _, ki := range items {
		payload, merr := json.Marshal(proposal.KnowledgePayload{
			Title:   fmt.Sprintf("Skill candidate: %s", ki.title),
			Content: fmt.Sprintf("Knowledge item '%s' has been recalled more than 3 times. Consider promoting it to a skill or playbook.", ki.title),
		})
		if merr != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: marshal payload failed", "item_id", ki.id, "err", merr)
			continue
		}
		if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
			WorkspaceID: deps.workspaceID,
			Type:        proposal.TypeKnowledge,
			Payload:     payload,
			ProposedBy:  "scheduler:knowledge_to_skill",
		}); cerr != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: Create proposal failed", "item_id", ki.id, "err", cerr)
			continue
		}
		created++
	}
	slog.Info("cognitive: knowledge_to_skill_candidate: completed",
		"high_recall_items", len(items),
		"proposals_created", created,
	)
	// Direction-D instrumentation: log activity so atom/outcome growth is visible
	// in the dashboard automation feed. Best-effort: failures do not abort the job.
	if deps.gtd != nil {
		if logErr := deps.gtd.LogActivity(ctx, "scheduler", "knowledge_to_skill_candidate",
			nil,
			fmt.Sprintf("high_recall_items=%d proposals_created=%d", len(items), created),
		); logErr != nil {
			slog.Warn("cognitive: knowledge_to_skill_candidate: LogActivity failed", "err", logErr)
		}
	}
}

// ---------------------------------------------------------------------------
// Job 6: proposal_cleanup
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
	slog.Info("cognitive: proposal_cleanup: completed",
		"rows_affected", tag.RowsAffected(),
		"retention", proposalCleanupInterval,
	)
}

// ---------------------------------------------------------------------------
// Job 7: behavior_rule_candidate_generation
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

	created := 0
	for _, r := range reflections {
		if r.Summary == "" {
			continue
		}
		payload, merr := json.Marshal(proposal.KnowledgePayload{
			Title:   fmt.Sprintf("Behavior rule candidate from %s reflection", r.Type),
			Content: fmt.Sprintf("Pattern detected in reflection (type=%s, created=%s): %s", r.Type, r.CreatedAt.Format("2006-01-02"), r.Summary),
		})
		if merr != nil {
			slog.Warn("cognitive: behavior_rule_candidate: marshal payload failed", "reflection_id", r.ID, "err", merr)
			continue
		}
		if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
			WorkspaceID: deps.workspaceID,
			Type:        proposal.TypeKnowledge,
			Payload:     payload,
			ProposedBy:  "scheduler:behavior_rule_candidate",
		}); cerr != nil {
			slog.Warn("cognitive: behavior_rule_candidate: Create proposal failed", "reflection_id", r.ID, "err", cerr)
			continue
		}
		created++
	}
	slog.Info("cognitive: behavior_rule_candidate: completed",
		"reflections_with_patterns", len(reflections),
		"proposals_created", created,
	)
	// Direction-D instrumentation: best-effort activity log for dashboard feed.
	if deps.gtd != nil {
		if logErr := deps.gtd.LogActivity(ctx, "scheduler", "behavior_rule_candidate",
			nil,
			fmt.Sprintf("reflections_with_patterns=%d proposals_created=%d", len(reflections), created),
		); logErr != nil {
			slog.Warn("cognitive: behavior_rule_candidate: LogActivity failed", "err", logErr)
		}
	}
}
