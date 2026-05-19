package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// disciplinePruneTimeout caps the daily discipline_events DELETE. The query
// is index-supported (idx_discipline_events_observed_at) and finishes well
// under 5 s on personal-OS scale; the 60 s ceiling leaves room for a
// momentarily slow Aiven Postgres without wedging the scheduler goroutine.
const disciplinePruneTimeout = 60 * time.Second

// disciplinePruneAge is the retention window enforced by the daily prune job.
// 30 days mirrors `task discipline-prune` (build/Taskfile.yml) and
// guard_events; codified by backend-security-design.md §1.3 as the mandatory
// TTL for observability tables.
const disciplinePruneAge = "30 days"

const (
	guardPruneTimeout = 60 * time.Second
	guardPruneAge     = "30 days"
)

// pendingProposalsPruneTimeout caps the daily pending_proposals DELETE. The
// query touches resolved_at + created_at (both indexed) and finishes well
// under a second on personal-OS scale; 60 s leaves room for a momentarily
// slow Aiven Postgres without wedging the scheduler goroutine.
const pendingProposalsPruneTimeout = 60 * time.Second

// pendingProposalsResolvedRetention / pendingProposalsPendingDecisionRetention
// / pendingProposalsPendingTaskRetention document the per-status TTL on
// pending_proposals (backend-security-design.md §1.3 — observability tables
// MUST have a working retention policy in the same PR that introduces them).
//
//   - 90 days for resolved (accepted / rejected) rows: the user has already
//     acted on them; we keep ~1 quarter for retrospective audit + then drop.
//   - 180 days for pending rows of type='decision': the auto-decision
//     proposer is opt-out enabled by default and can fill the queue; old
//     pending decisions are usually obsolete (the user moved on without
//     accepting).
//   - 30 days for pending rows of type='task': the auto-task proposer (via
//     /api/activity classifier) emits high-frequency low-signal task
//     candidates; the user typically accepts within a day or two and stale
//     ones older than a month are noise. Marked as rejected (status, reason)
//     rather than deleted so the audit trail survives the 90-day resolved
//     retention. GTD 947f3f12.
//
// Other pending types (goal, project, concept, …) still require manual
// review and are NOT touched by the cleanup so we don't silently drop a
// user's intent.
const (
	pendingProposalsResolvedRetention        = "90 days"
	pendingProposalsPendingDecisionRetention = "180 days"
	pendingProposalsPendingTaskRetention     = "30 days"
)

// dailyBriefingTimeout caps each Notion morning briefing run. The aggregate
// query + Notion upsert finishes well under 10 s in practice; we pick 60 s
// to leave room for occasional Notion API slowness without letting a stuck
// HTTP call wedge the scheduler goroutine.
const dailyBriefingTimeout = 60 * time.Second

// weeklyAIReviewTimeout caps the weekly AI concept review job. Claude API
// latency plus DB updates for up to 50 concepts fits comfortably within 5 min.
const weeklyAIReviewTimeout = 5 * time.Minute

// aiReviewMinReviewCount is the minimum number of completed reviews a concept
// must have before it is eligible for AI evaluation.
const aiReviewMinReviewCount = 5

// Scheduler wraps gocron and coordinates scheduled background jobs.
type Scheduler struct {
	s              gocron.Scheduler
	learning       learning.StoreIface
	discord        DiscordSender
	notion         *notion.Client
	briefingStores notion.BriefingStores
	reviewer       ai.ConceptReviewerIface
	reflectionDeps *reflectionDeps
	consolidDeps   *consolidationDeps
	statusDeps     *statusSnapshotDeps
	pruner         *decay.Pruner
	playbookDeps   *playbookDeps
	// disciplinePool is the pgxpool used by the daily discipline_events
	// prune job. nil when running under SQLite (or when no pool is wired
	// in by the caller) — the prune job is skipped in that case because
	// SQLite is dev-local single-tenant and has no growth concern.
	disciplinePool *pgxpool.Pool
	// candidatePruner deletes resolved completion_candidates older than 30d.
	// Set via WithCandidatePruner after New(); nil skips the prune job.
	candidatePruner candidateRetentionStore
}

// DiscordSender is the small Discord webhook surface used by scheduled jobs.
type DiscordSender interface {
	Send(ctx context.Context, message string) error
}

// candidateRetentionStore is the narrow prune interface used by the daily
// completion-candidate cleanup job. completioncandidate.Store satisfies it.
type candidateRetentionStore interface {
	PruneResolved(ctx context.Context, olderThan time.Duration) (int64, error)
}

// statusSnapshotDeps bundles the dependencies needed by the Saturday status
// snapshot cron job. All fields are required when sDeps is non-nil.
type statusSnapshotDeps struct {
	gtd         gtd.StoreIface
	decision    decision.StoreIface
	store       snapshot.StoreIface
	generator   snapshot.GeneratorIface
	workspaceID *uuid.UUID
}

// buildSchedulerDeps initialises all optional dep bundles from the flat
// parameter list passed to New. Extracted to keep New's cyclomatic complexity
// below the gocyclo threshold of 15.
func buildSchedulerDeps(
	reflector ai.ReflectorIface,
	gtdStore gtd.StoreIface,
	decStore decision.StoreIface,
	propStore proposal.StoreIface,
	pbStore playbook.StoreIface,
	snapStore snapshot.StoreIface,
	snapGen snapshot.GeneratorIface,
	workspaceID *uuid.UUID,
) (rDeps *reflectionDeps, cDeps *consolidationDeps, pbDeps *playbookDeps, sDeps *statusSnapshotDeps) {
	if reflector != nil && gtdStore != nil && decStore != nil && propStore != nil {
		rDeps = &reflectionDeps{
			gtd:       gtdStore,
			decision:  decStore,
			proposal:  propStore,
			reflector: reflector,
		}
		cDeps = &consolidationDeps{
			gtd:       gtdStore,
			proposal:  propStore,
			reflector: reflector,
		}
	}
	if reflector != nil && decStore != nil && propStore != nil && pbStore != nil {
		pbDeps = &playbookDeps{
			decision:  decStore,
			playbook:  pbStore,
			proposal:  propStore,
			reflector: reflector,
		}
	}
	if snapStore != nil && snapGen != nil && gtdStore != nil && decStore != nil {
		sDeps = &statusSnapshotDeps{
			gtd:         gtdStore,
			decision:    decStore,
			store:       snapStore,
			generator:   snapGen,
			workspaceID: workspaceID,
		}
	}
	return rDeps, cDeps, pbDeps, sDeps
}

// New creates and configures the Scheduler with all registered jobs.
//
// notionClient and briefingStores are optional: when notionClient is nil
// (NOTION_INTEGRATION_SECRET unset) we skip registering the morning briefing
// job entirely and log a single info-level skip message at startup. This
// preserves the "no Notion configured = legacy single-user mode" behavior
// without surprising the operator with retried 401s every morning.
//
// reviewer is optional: when nil the weekly AI concept review job is skipped
// and a single info-level message is logged at startup.
//
// reflector, gtdStore, decStore, propStore are optional together: when any of
// them is nil the reflection + consolidation Saturday jobs are skipped.
//
// snapStore, snapGen are optional: when either is nil the Saturday status
// snapshot job is skipped. Both are set when CLAUDE_API_KEY is configured.
//
// pruner is optional: when nil the daily decay soft-prune job is skipped.
//
// pbStore is optional: when nil (or when reflector/decStore/propStore is also
// nil) the Sunday playbook promoter job is skipped.
func New(
	ls learning.StoreIface,
	dc DiscordSender,
	notionClient *notion.Client,
	briefingStores notion.BriefingStores,
	reviewer ai.ConceptReviewerIface,
	gtdStore gtd.StoreIface,
	decStore decision.StoreIface,
	propStore proposal.StoreIface,
	reflector ai.ReflectorIface,
	snapStore snapshot.StoreIface,
	snapGen snapshot.GeneratorIface,
	workspaceID *uuid.UUID,
	pruner *decay.Pruner,
	pbStore playbook.StoreIface,
	disciplinePool *pgxpool.Pool,
) (*Scheduler, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return nil, fmt.Errorf("loading Asia/Taipei timezone: %w", err)
	}

	s, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("creating gocron scheduler: %w", err)
	}

	rDeps, cDeps, pbDeps, sDeps := buildSchedulerDeps(
		reflector, gtdStore, decStore, propStore, pbStore,
		snapStore, snapGen, workspaceID,
	)

	sc := &Scheduler{
		s:              s,
		learning:       ls,
		discord:        dc,
		notion:         notionClient,
		briefingStores: briefingStores,
		reviewer:       reviewer,
		reflectionDeps: rDeps,
		consolidDeps:   cDeps,
		statusDeps:     sDeps,
		pruner:         pruner,
		playbookDeps:   pbDeps,
		disciplinePool: disciplinePool,
	}

	if err := sc.registerDailyJobs(s); err != nil {
		return nil, err
	}
	if err := sc.registerWeeklyJobs(s); err != nil {
		return nil, err
	}

	return sc, nil
}

// registerDailyJobs adds the daily scheduled tasks (08:00 and 23:00).
func (sc *Scheduler) registerDailyJobs(s gocron.Scheduler) error {
	_, err := s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(8, 0, 0))),
		gocron.NewTask(sc.sendDailyReviewReminder),
		gocron.WithName("daily-review-reminder"),
	)
	if err != nil {
		return fmt.Errorf("registering daily review job: %w", err)
	}

	if sc.notion != nil && sc.briefingStores != nil {
		_, err = s.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(8, 0, 0))),
			gocron.NewTask(sc.sendDailyNotionBriefing),
			gocron.WithName("daily-notion-briefing"),
			// LimitModeReschedule drops a run if the previous one is still
			// executing (e.g. Notion API slow). Prevents goroutine pile-up.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering daily Notion briefing job: %w", err)
		}
		slog.Info("scheduler: DailyNotionBriefing scheduled at 08:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: DailyNotionBriefing skipped (Notion client not configured)")
	}

	if sc.pruner != nil {
		_, err = s.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(23, 0, 0))),
			gocron.NewTask(sc.runDailyDecayPrune),
			gocron.WithName("daily-decay-prune"),
			// LimitModeReschedule: drop a run if the previous one is still
			// executing. DB scan may be slow on large workspaces.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering daily decay prune job: %w", err)
		}
		slog.Info("scheduler: DailyDecayPrune scheduled at 23:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: DailyDecayPrune skipped (pruner not configured)")
	}

	if sc.disciplinePool != nil {
		_, err = s.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(23, 0, 0))),
			gocron.NewTask(sc.runDailyDisciplinePrune),
			gocron.WithName("daily-discipline-prune"),
			// LimitModeReschedule: a stuck DELETE shouldn't pile up nightly
			// triggers. Same singleton policy as decay-prune.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering daily discipline prune job: %w", err)
		}
		slog.Info("scheduler: DailyDisciplinePrune scheduled at 23:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: DailyDisciplinePrune skipped (postgres pool not configured; SQLite has no growth concern)")
	}

	if sc.disciplinePool != nil {
		_, err = s.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(23, 30, 0))),
			gocron.NewTask(sc.runDailyGuardPrune),
			gocron.WithName("daily-guard-prune"),
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering daily guard prune job: %w", err)
		}
		slog.Info("scheduler: DailyGuardPrune scheduled at 23:30 Asia/Taipei")
	} else {
		slog.Info("scheduler: DailyGuardPrune skipped (postgres pool not configured; SQLite has no growth concern)")
	}

	if sc.disciplinePool != nil {
		// Offset from the 23:00 cluster (decay + discipline prune) so the
		// pgxpool isn't slammed with three near-simultaneous DELETEs against
		// large tables. 03:00 also avoids the 02:00 Sunday AI concept review
		// and the 03:00 Sunday playbook promoter (those are weekly, not
		// daily, so the calendar overlap is at most one day per week).
		_, err = s.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
			gocron.NewTask(sc.runDailyPendingProposalsPrune),
			gocron.WithName("daily-pending-proposals-prune"),
			// LimitModeReschedule: a stuck DELETE shouldn't pile up nightly
			// triggers. Same singleton policy as decay/discipline prune.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering daily pending proposals prune job: %w", err)
		}
		slog.Info("scheduler: DailyPendingProposalsPrune scheduled at 03:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: DailyPendingProposalsPrune skipped (postgres pool not configured; SQLite has no growth concern)")
	}

	return nil
}

// registerWeeklyJobs adds the weekly Sunday AI review and Saturday reflection jobs.
func (sc *Scheduler) registerWeeklyJobs(s gocron.Scheduler) error {
	if sc.reviewer != nil {
		_, err := s.NewJob(
			gocron.WeeklyJob(1, gocron.NewWeekdays(time.Sunday), gocron.NewAtTimes(gocron.NewAtTime(2, 0, 0))),
			gocron.NewTask(sc.weeklyAIConceptReview),
			gocron.WithName("weekly-ai-concept-review"),
			// LimitModeReschedule prevents goroutine pile-up if Claude is slow.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering weekly AI concept review job: %w", err)
		}
		slog.Info("scheduler: WeeklyAIConceptReview scheduled at Sunday 02:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: WeeklyAIConceptReview skipped (Claude API key not configured)")
	}

	if err := sc.registerSundayPlaybookPromoter(s); err != nil {
		return err
	}

	if sc.reflectionDeps == nil {
		slog.Info("scheduler: SaturdayReflection + SaturdayConsolidation + SaturdayStatusSnapshot skipped (reflector or stores not configured)")
		return nil
	}
	return sc.registerSaturdayJobs(s)
}

// registerSaturdayJobs adds the Saturday 23:00 reflection + consolidation pair,
// and optionally the status snapshot job when snapDeps is configured.
func (sc *Scheduler) registerSaturdayJobs(s gocron.Scheduler) error {
	sat := gocron.WeeklyJob(1, gocron.NewWeekdays(time.Saturday), gocron.NewAtTimes(gocron.NewAtTime(23, 0, 0)))

	_, err := s.NewJob(
		sat,
		gocron.NewTask(sc.saturdayReflection),
		gocron.WithName("saturday-reflection"),
		// LimitModeReschedule: if a previous run is still executing (Haiku slow),
		// drop the new trigger instead of piling up goroutines.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering Saturday reflection job: %w", err)
	}

	_, err = s.NewJob(
		// Same Saturday 23:00 window; gocron orders by registration within the tick.
		sat,
		gocron.NewTask(sc.saturdayConsolidation),
		gocron.WithName("saturday-consolidation"),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering Saturday consolidation job: %w", err)
	}
	slog.Info("scheduler: SaturdayReflection + SaturdayConsolidation scheduled at Saturday 23:00 Asia/Taipei")

	if sc.statusDeps != nil {
		_, err = s.NewJob(
			// Runs after reflection + consolidation; gocron executes same-tick
			// jobs in registration order.
			sat,
			gocron.NewTask(sc.saturdayStatusSnapshot),
			gocron.WithName("saturday-status-snapshot"),
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return fmt.Errorf("registering Saturday status snapshot job: %w", err)
		}
		slog.Info("scheduler: SaturdayStatusSnapshot scheduled at Saturday 23:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: SaturdayStatusSnapshot skipped (CLAUDE_API_KEY or Postgres pool not configured)")
	}

	return nil
}

// registerSundayPlaybookPromoter adds the Sunday 03:00 playbook promoter job.
func (sc *Scheduler) registerSundayPlaybookPromoter(s gocron.Scheduler) error {
	if sc.playbookDeps == nil {
		slog.Info("scheduler: SundayPlaybookPromoter skipped (reflector, decision, proposal or playbook store not configured)")
		return nil
	}
	_, err := s.NewJob(
		gocron.WeeklyJob(1, gocron.NewWeekdays(time.Sunday), gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
		gocron.NewTask(sc.sundayPlaybookPromoter),
		gocron.WithName("sunday-playbook-promoter"),
		// LimitModeReschedule: drop a run if the previous one is still executing.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering Sunday playbook promoter job: %w", err)
	}
	slog.Info("scheduler: SundayPlaybookPromoter scheduled at Sunday 03:00 Asia/Taipei")
	return nil
}

// sundayPlaybookPromoter wraps runPlaybookPromoter for the scheduler method signature.
func (s *Scheduler) sundayPlaybookPromoter() {
	if s.playbookDeps == nil {
		return
	}
	runPlaybookPromoter(*s.playbookDeps)
}

// Start begins executing scheduled jobs (non-blocking).
func (s *Scheduler) Start() {
	s.s.Start()
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	if err := s.s.Shutdown(); err != nil {
		slog.Warn("scheduler shutdown error", "err", err)
	}
}

// WithCandidatePruner wires a completion-candidate retention store and
// registers the daily 23:00 prune job. Must be called before Start().
func (sc *Scheduler) WithCandidatePruner(p candidateRetentionStore) error {
	sc.candidatePruner = p
	_, err := sc.s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(23, 0, 0))),
		gocron.NewTask(sc.runDailyCandidatePrune),
		gocron.WithName("daily-candidate-prune"),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering daily candidate prune job: %w", err)
	}
	slog.Info("scheduler: DailyCandidatePrune scheduled at 23:00 Asia/Taipei")
	return nil
}

// runDailyCandidatePrune deletes resolved completion_candidates older than
// 30 days (TTL mandated by backend-security-design.md §1.3). Errors are
// logged at warn level — the scheduler keeps running other jobs regardless.
func (s *Scheduler) runDailyCandidatePrune() {
	if s.candidatePruner == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	n, err := s.candidatePruner.PruneResolved(ctx, 30*24*time.Hour)
	if err != nil {
		slog.Warn("daily candidate prune: PruneResolved failed", "err", err)
		return
	}
	slog.Info("daily candidate prune: completed", "rows_deleted", n)
}

func (s *Scheduler) sendDailyReviewReminder() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := s.learning.CountDueReviews(ctx)
	if err != nil {
		slog.Warn("daily review reminder: counting due reviews failed", "err", err)
		return
	}

	if s.discord == nil {
		slog.Info("daily review reminder: discord client not configured, skipping notification",
			"due_count", count)
		return
	}

	msg := fmt.Sprintf("You have %d concepts due for review today", count)
	if err := s.discord.Send(ctx, msg); err != nil {
		slog.Warn("daily review reminder: sending discord notification failed", "err", err)
	}
}

// sendDailyNotionBriefing builds the morning briefing snapshot and upserts
// it to Notion using the day's ISO date as the idempotency key. Failure is
// logged at warn level only — the scheduler MUST keep running other jobs
// regardless of Notion availability (per task acceptance criterion).
func (s *Scheduler) sendDailyNotionBriefing() {
	ctx, cancel := context.WithTimeout(context.Background(), dailyBriefingTimeout)
	defer cancel()

	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		// LoadLocation fails only if the IANA tzdata is missing; the server
		// embeds time/tzdata in cmd/server/main.go so this is effectively
		// unreachable, but we still log a warn and bail rather than panic.
		slog.Warn("daily notion briefing: loading Asia/Taipei timezone failed", "err", err)
		return
	}
	now := time.Now().In(loc)

	briefing, err := notion.BuildDailyBriefing(ctx, s.briefingStores, now)
	if err != nil {
		slog.Warn("daily notion briefing: building briefing failed", "err", err, "date", now.Format("2006-01-02"))
		return
	}

	if err := s.notion.UpsertDailyPage(ctx, briefing); err != nil {
		slog.Warn("daily notion briefing: upserting page failed",
			"err", err,
			"date", now.Format("2006-01-02"),
			"in_progress_count", len(briefing.InProgressTasks),
			"recent_decision_count", len(briefing.RecentDecisions),
		)
		return
	}

	slog.Info("daily notion briefing: upsert succeeded",
		"date", now.Format("2006-01-02"),
		"in_progress_count", len(briefing.InProgressTasks),
		"recent_decision_count", len(briefing.RecentDecisions),
		"pending_proposals", len(briefing.PendingProposals),
		"due_reviews", len(briefing.DueReviews),
		"stuck_tasks", briefing.SystemHealth.StuckTaskCount,
	)
}

// saturdayReflection wraps runReflection for the scheduler method signature.
func (s *Scheduler) saturdayReflection() {
	if s.reflectionDeps == nil {
		return
	}
	runReflection(*s.reflectionDeps)
}

// saturdayConsolidation wraps runConsolidation for the scheduler method signature.
func (s *Scheduler) saturdayConsolidation() {
	if s.consolidDeps == nil {
		return
	}
	runConsolidation(*s.consolidDeps)
}

// saturdayStatusSnapshot generates a fresh status snapshot for all known slugs.
func (s *Scheduler) saturdayStatusSnapshot() {
	if s.statusDeps == nil {
		return
	}
	runStatusSnapshot(*s.statusDeps)
}

// runDailyDecayPrune delegates to the Pruner.Run which applies the Ebbinghaus
// soft-delete policy across knowledge_items and concepts. Decisions are never
// touched. Runs at 23:00 Asia/Taipei.
func (s *Scheduler) runDailyDecayPrune() {
	if s.pruner == nil {
		return
	}
	s.pruner.Run()
}

// runDailyDisciplinePrune deletes discipline_events rows older than 30 days
// (TTL codified by backend-security-design.md §1.3). Mirrors the existing
// `task discipline-prune` Taskfile target so operators don't need to wire a
// separate cron in production. Runs at 23:00 Asia/Taipei alongside the decay
// prune. Errors are logged at warn level — the scheduler MUST keep running
// other jobs regardless of a single DB hiccup.
func (s *Scheduler) runDailyDisciplinePrune() {
	if s.disciplinePool == nil {
		return
	}
	// Independent timeout — MUST NOT inherit any request context (none
	// available here anyway, but explicit is safer than relying on caller).
	ctx, cancel := context.WithTimeout(context.Background(), disciplinePruneTimeout)
	defer cancel()

	// 30-day TTL. Parameter binding is unnecessary because the interval
	// literal is a code constant, not user input.
	const q = `DELETE FROM discipline_events WHERE observed_at < NOW() - INTERVAL '` + disciplinePruneAge + `'`
	tag, err := s.disciplinePool.Exec(ctx, q)
	if err != nil {
		slog.Warn("daily discipline prune: DELETE failed", "err", err)
		return
	}
	slog.Info("daily discipline prune: completed",
		"rows_deleted", tag.RowsAffected(),
		"retention", disciplinePruneAge,
	)
}

func (s *Scheduler) runDailyGuardPrune() {
	if s.disciplinePool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), guardPruneTimeout)
	defer cancel()

	const q = `
DELETE FROM guard_events WHERE created_at < NOW() - INTERVAL '` + guardPruneAge + `';
DELETE FROM guard_bypasses WHERE created_at < NOW() - INTERVAL '` + guardPruneAge + `';`
	tag, err := s.disciplinePool.Exec(ctx, q)
	if err != nil {
		slog.Warn("daily guard prune: DELETE failed", "err", err)
		return
	}
	slog.Info("daily guard prune: completed",
		"rows_deleted", tag.RowsAffected(),
		"retention", guardPruneAge,
	)
}

// runDailyPendingProposalsPrune marks stale TypeTask pending proposals as
// rejected (with reason='ttl-expired-30d'), then deletes long-resolved rows
// according to the per-status retention policy:
//
//   - mark step: pending TypeTask older than 30 days → status='rejected',
//     reason='ttl-expired-30d'. Stays in the table until it ages out via
//     the 90-day resolved retention so the audit trail survives.
//   - delete: resolved (status IN ('accepted','rejected')) older than 90
//     days: user has acted on them; keep ~1 quarter for retrospective audit.
//   - delete: pending decision proposals (type='decision') older than 180
//     days: auto-decision proposer can fill the queue and stale ones are
//     noise.
//
// Other pending types (goal/project/concept/knowledge/playbook) are NOT
// touched — they represent unresolved user intent and silent deletion or
// auto-reject would be hostile.
//
// Backend-security-design.md §1.3 mandates a working retention policy in the
// same PR that introduces the auto-proposer (which can write unbounded
// pending rows); this job is that policy. Runs at 03:00 Asia/Taipei,
// offset from the 23:00 decay/discipline cluster to spread DB load.
//
// Errors are logged at warn level — the scheduler MUST keep running other
// jobs regardless of a single DB hiccup. The mark step's outcome does NOT
// gate the delete step: a transient mark failure shouldn't block the 90-day
// resolved-row cleanup.
func (s *Scheduler) runDailyPendingProposalsPrune() {
	if s.disciplinePool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), pendingProposalsPruneTimeout)
	defer cancel()

	// Mark stale TypeTask pending proposals as rejected with an audit
	// reason. Run BEFORE the DELETE so newly-marked rows enter the resolved
	// retention window (90d) instead of being silently dropped.
	const markStaleTaskProposals = `UPDATE pending_proposals
SET status = 'rejected', resolved_at = NOW(), reason = 'ttl-expired-30d'
WHERE status = 'pending' AND type = 'task'
  AND created_at < NOW() - INTERVAL '` + pendingProposalsPendingTaskRetention + `'`
	markTag, err := s.disciplinePool.Exec(ctx, markStaleTaskProposals)
	if err != nil {
		slog.Warn("daily pending_proposals prune: mark TTL-stale task proposals failed", "err", err)
	} else {
		slog.Info("daily pending_proposals prune: marked TTL-stale task proposals",
			"rows_updated", markTag.RowsAffected(),
			"pending_task_retention", pendingProposalsPendingTaskRetention,
		)
	}

	// Both interval literals are code constants — parameter binding is
	// unnecessary and would also confuse pg's planner about the predicate.
	// The OR keeps it a single statement so we don't pay round-trip latency
	// twice. resolved_at is NULL on still-pending rows, so the first arm
	// can never match a pending row even if the WHERE were re-ordered.
	const q = `DELETE FROM pending_proposals
WHERE (status IN ('accepted', 'rejected') AND resolved_at < NOW() - INTERVAL '` + pendingProposalsResolvedRetention + `')
   OR (status = 'pending' AND created_at < NOW() - INTERVAL '` + pendingProposalsPendingDecisionRetention + `' AND type = 'decision')`
	tag, err := s.disciplinePool.Exec(ctx, q)
	if err != nil {
		slog.Warn("daily pending_proposals prune: DELETE failed", "err", err)
		return
	}
	slog.Info("daily pending_proposals prune: completed",
		"rows_deleted", tag.RowsAffected(),
		"resolved_retention", pendingProposalsResolvedRetention,
		"pending_decision_retention", pendingProposalsPendingDecisionRetention,
		"pending_task_retention", pendingProposalsPendingTaskRetention,
	)
}

// weeklyAIConceptReview fetches active concepts with sufficient review history,
// asks Claude to evaluate them, and updates the status of any concept that has
// been mastered or found to be not helpful. All errors are logged at warn level
// so the scheduler keeps running other jobs.
func (s *Scheduler) weeklyAIConceptReview() {
	// Independent timeout — MUST NOT inherit a request context.
	ctx, cancel := context.WithTimeout(context.Background(), weeklyAIReviewTimeout)
	defer cancel()

	concepts, err := s.learning.ListForAIReview(ctx, aiReviewMinReviewCount)
	if err != nil {
		slog.Warn("weekly AI concept review: listing concepts failed", "err", err)
		return
	}

	if len(concepts) == 0 {
		slog.Info("weekly AI concept review: no concepts eligible for review")
		return
	}

	inputs := make([]ai.ReviewInput, 0, len(concepts))
	for _, c := range concepts {
		inputs = append(inputs, ai.ReviewInput{
			ID:          c.ID,
			Title:       c.Title,
			Content:     c.Content,
			ReviewCount: c.ReviewCount,
			Stability:   c.Stability,
		})
	}

	results := s.reviewer.ReviewConcepts(ctx, inputs)
	if len(results) == 0 {
		slog.Info("weekly AI concept review: no status changes recommended")
		return
	}

	updated := 0
	for _, res := range results {
		if res.NewStatus == "active" {
			continue
		}
		if err := s.learning.UpdateConceptStatus(ctx, res.ID, res.NewStatus); err != nil {
			slog.Warn("weekly AI concept review: updating concept status failed",
				"concept_id", res.ID,
				"new_status", res.NewStatus,
				"err", err,
			)
			continue
		}
		updated++
		slog.Info("weekly AI concept review: concept status updated",
			"concept_id", res.ID,
			"new_status", res.NewStatus,
		)
	}

	slog.Info("weekly AI concept review: completed",
		"eligible_concepts", len(concepts),
		"ai_results", len(results),
		"updated", updated,
	)
}
