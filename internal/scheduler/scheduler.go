package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGOnlyJob is a code-level (not merely a comment) capability-contract
// record for a scheduler job whose only backend implementation is Postgres.
// Queried via IsPGOnlyJob so operators/tests can assert that an SQLite skip
// is an intentional design boundary, not a silently-broken dependency.
type PGOnlyJob struct {
	// Name is the gocron job name, e.g. "daily-discipline-prune".
	Name string
	// Reason documents why this job has no SQLite implementation.
	Reason string
}

// pgOnlyJobs is the closed set of scheduler jobs with no SQLite
// implementation, per GTD decision G4 (6ea0b014): a job stays Postgres-only
// when its only externally observable effect is disk growth on an
// observability table that has NO user-facing listing surface on either
// backend — a maintainer has no way to observe discipline_events or
// guard_events/guard_bypasses except direct DB access, so leaving their
// retention job Postgres-only is genuinely invisible, not merely
// inconvenient to implement, and SQLite is dev-local single-tenant with no
// growth concern regardless.
//
// Contrast with the 4 cognitive jobs in cognitive_jobs.go
// (stuck_task_detection, decision_outcome_review, knowledge_to_skill_candidate,
// proposal_cleanup) AND daily-pending-proposals-prune (this file): all 5
// produce or prune pending_proposals rows, which ARE user-observable — via
// proposal.StoreIface on both backends AND via the proposal-review HTTP
// surface (internal/handler/proposal_handler.go:199 ListPendingProposals,
// `GET /api/proposals/pending`) — so all 5 DO have SQLite parity (the first
// 4 via CognitiveSQLiteStore; daily-pending-proposals-prune via
// PendingProposalsPruneStore, below).
var pgOnlyJobs = []PGOnlyJob{
	{
		Name: "daily-discipline-prune",
		Reason: "discipline_events retention is disk-growth-only, not user-observable " +
			"(backend-security-design.md §1.3); SQLite is dev-local single-tenant with no growth concern",
	},
	{
		Name: "daily-guard-prune",
		Reason: "guard_events/guard_bypasses retention is disk-growth-only, not user-observable; " +
			"SQLite is dev-local single-tenant with no growth concern",
	},
}

// IsPGOnlyJob reports whether name is a documented Postgres-only scheduler
// job and returns its design reason. ok=false means name is either a
// dual-backend job or not a recognised job name at all.
func IsPGOnlyJob(name string) (reason string, ok bool) {
	for _, j := range pgOnlyJobs {
		if j.Name == name {
			return j.Reason, true
		}
	}
	return "", false
}

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
//
// `var` (not `const`) so the partial-success telemetry test can shrink the
// timeout to deterministically force a context.DeadlineExceeded on the
// DELETE step after the mark step has already succeeded. Production
// callers MUST NOT mutate this — it is effectively a constant outside of
// internal/scheduler tests.
var pendingProposalsPruneTimeout = 60 * time.Second

// pendingProposalsPruneBetweenSteps is a test-only hook invoked between the
// mark step and the DELETE step of runDailyPendingProposalsPrune. Production
// code leaves this nil. The partial-success telemetry test sets it to a
// sleep long enough to exhaust the (test-shortened) context deadline,
// guaranteeing the DELETE step observes context.DeadlineExceeded after
// mark has already affected rows.
var pendingProposalsPruneBetweenSteps func(ctx context.Context)

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

// pendingProposalsResolvedRetentionDuration / *DecisionRetentionDuration /
// *TaskRetentionDuration are time.Duration equivalents of the 3 PG
// interval-literal constants above, used by the SQLite adapter path (SQLite
// has no `NOW() - INTERVAL` syntax — cutoffs are computed in Go and bound as
// query parameters instead). Kept in sync with the PG string constants
// manually, matching the existing stuckTaskLookback-style pattern in
// cognitive_jobs.go; a drift here would only affect the SQLite backend's
// cutoff window and is caught by TestPendingProposalsPrune_SQLite_*.
const (
	pendingProposalsResolvedRetentionDuration        = 90 * 24 * time.Hour
	pendingProposalsPendingDecisionRetentionDuration = 180 * 24 * time.Hour
	pendingProposalsPendingTaskRetentionDuration     = 30 * 24 * time.Hour
)

// dailyPendingProposalsPruneJobName is the gocron job name shared by BOTH
// backend registrations of this job (registerDailyJobs's Postgres branch and
// WithPendingProposalsPruneSQLite's SQLite branch) — the two are mutually
// exclusive at runtime (see WithPendingProposalsPruneSQLite's doc comment),
// but must still resolve to the identical name so operator tooling /
// dashboards that key off gocron job names see one logical job regardless of
// backend.
const dailyPendingProposalsPruneJobName = "daily-pending-proposals-prune"

// pendingProposalsTaskTTLReason is the audit `reason` value written to a
// pending TypeTask proposal's row when the mark step rejects it for TTL
// staleness (mirrors the literal embedded in runDailyPendingProposalsPrunePG's
// markStaleTaskProposals SQL constant). Named here so the SQLite call site
// (runDailyPendingProposalsPruneSQLite) and its tests share one value instead
// of independently-drifting literals (goconst min-occurrences 3).
const pendingProposalsTaskTTLReason = "ttl-expired-30d"

// PendingProposalsPruneStore is the narrow SQLite-only query surface backing
// the daily-pending-proposals-prune job when the scheduler is wired against
// a SQLite backend. nil under Postgres (disciplinePool covers that path
// instead). Deliberately NOT part of proposal.StoreIface — mirrors
// CognitiveSQLiteStore's domain-ownership rationale documented in
// cognitive_jobs.go (backend-security-design.md): this one method is
// scheduler-local plumbing, not a cross-domain proposal operation every
// other StoreIface implementer would have to grow a matching method for.
//
// Exported (capitalised) for the same typed-nil-in-interface reason
// CognitiveSQLiteStore is exported — see its doc comment in
// cognitive_jobs.go. cmd/server/main.go declares a correctly-nil-typed local
// variable of this interface type when wiring.
type PendingProposalsPruneStore interface {
	// MarkAndDeleteStaleProposals mirrors runDailyPendingProposalsPrunePG's
	// two-step logic in SQLite dialect. See
	// storage/sqlite.ProposalStore.MarkAndDeleteStaleProposals's doc comment
	// for the full semantics.
	MarkAndDeleteStaleProposals(
		ctx context.Context, taskRetention, decisionRetention, resolvedRetention time.Duration, markReason string,
	) (markedRows, deletedRows int64, err error)
}

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
	s                     gocron.Scheduler
	learning              learning.StoreIface
	discord               DiscordSender
	notion                *notion.Client
	briefingStores        notion.BriefingStores
	reviewer              ai.ConceptReviewerIface
	reflectionDeps        *reflectionDeps
	consolidDeps          *consolidationDeps
	knowledgeConsolidDeps *knowledgeConsolidationDeps
	atomConsolidDeps      *atomConsolidDeps
	atomBridgeDeps        *atomBridgeDeps
	statusDeps            *statusSnapshotDeps
	pruner                *decay.Pruner
	playbookDeps          *playbookDeps
	// disciplinePool is the pgxpool used by the daily discipline_events
	// prune job. nil when running under SQLite (or when no pool is wired
	// in by the caller) — the prune job is skipped in that case because
	// SQLite is dev-local single-tenant and has no growth concern.
	disciplinePool *pgxpool.Pool
	// governanceDeps bundles the behavior governance weekly job dependencies.
	// Set via WithBehaviorGovernance after New(); nil skips the job.
	governanceDeps *governanceDeps
	// cognitiveDeps bundles the 6 Memory-7 cognitive job dependencies.
	// Set via WithCognitiveDeps after New(); nil skips all 6 cognitive jobs.
	cognitiveDeps *cognitiveDeps
	// pendingProposalsPruneSQLite is the SQLite counterpart to disciplinePool
	// for the daily-pending-proposals-prune job. nil under Postgres
	// (disciplinePool covers that path) and nil under SQLite only if the
	// caller failed to wire it via WithPendingProposalsPruneSQLite — in that
	// case runDailyPendingProposalsPrune's "no backend configured" default
	// branch fires rather than silently doing nothing.
	pendingProposalsPruneSQLite PendingProposalsPruneStore
}

// DiscordSender is the small Discord webhook surface used by scheduled jobs.
type DiscordSender interface {
	Send(ctx context.Context, message string) error
}

// candidateRetentionStore is the narrow prune interface used by the daily
// completion-candidate cleanup job. completioncandidate.Store satisfies it.
// PruneResolved takes a retention duration rather than a cutoff time, so
// candidatePrunerAdapter bridges it to the unified PrunerStore contract.
type candidateRetentionStore interface {
	PruneResolved(ctx context.Context, olderThan time.Duration) (int64, error)
}

// mergedPRsRetentionStore is the narrow prune interface used by the daily
// merged_prs_observed cleanup job. mergedprs.Store satisfies it. Like
// candidateRetentionStore, PruneOlderThan here takes a duration rather than
// a cutoff time; mergedPRsPrunerAdapter bridges it to PrunerStore.
type mergedPRsRetentionStore interface {
	PruneOlderThan(ctx context.Context, olderThan time.Duration) (int64, error)
}

// PrunerStore is the unified narrow interface every daily TTL-prune job
// registered via WithPruner satisfies: delete rows older than cutoff and
// report the number of rows removed.
type PrunerStore interface {
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// PrunerSpec describes one daily TTL-prune job. Adding a new observability
// table's retention job is a single PrunerSpec value passed to WithPruner —
// no new interface, With*Pruner method, or exec block required.
type PrunerSpec struct {
	// Name identifies the table/job in gocron job names ("daily-<Name>-prune")
	// and log messages.
	Name string
	// Store performs the delete. Use one of the New*PrunerAdapter helpers
	// below to wrap a store whose native method doesn't already match
	// PrunerStore's cutoff-time signature.
	Store PrunerStore
	// Retention is the TTL window; rows with a timestamp older than
	// time.Now().Add(-Retention) are eligible for deletion.
	Retention time.Duration
	// Hour, Minute are the Asia/Taipei time-of-day the job fires daily.
	// uint (not int) matches gocron.NewAtTime's signature so no runtime
	// conversion — and no gosec G115 overflow finding — is needed.
	Hour, Minute uint
}

// prunerJobTimeout caps every registry-driven daily prune job's DELETE.
// 60 s budget — uniform across all prior individually-timed prune jobs,
// which all used this same value under different constant names.
const prunerJobTimeout = 60 * time.Second

// WithPruner registers a daily TTL-prune job driven by spec. Must be called
// before Start(). Callers are responsible for the nil-store skip check
// (mirrors the pre-refactor "if store != nil { sched.WithXxxPruner(store) }"
// pattern in cmd/server/main.go) — WithPruner itself assumes spec.Store is
// non-nil.
func (sc *Scheduler) WithPruner(spec PrunerSpec) error {
	_, err := sc.s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(spec.Hour, spec.Minute, 0))),
		gocron.NewTask(func() { runPrune(spec) }),
		gocron.WithName(fmt.Sprintf("daily-%s-prune", spec.Name)),
		// LimitModeReschedule: drop a run if the previous one is still
		// executing rather than piling up goroutines.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering daily %s prune job: %w", spec.Name, err)
	}
	slog.Info(
		"scheduler: daily prune job scheduled",
		"table", spec.Name,
		"hour", spec.Hour,
		"minute", spec.Minute,
		"retention_days", int(spec.Retention.Hours()/24),
	)
	return nil
}

// runPrune executes spec's PruneOlderThan against a fresh timeout-bounded
// context and logs the outcome. A free function (not a Scheduler method) so
// it's directly unit-testable without constructing a full Scheduler. Errors
// are logged at warn level — the scheduler MUST keep running other jobs
// regardless of a single table's DB hiccup.
func runPrune(spec PrunerSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), prunerJobTimeout)
	defer cancel()

	cutoff := time.Now().Add(-spec.Retention)
	n, err := spec.Store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		slog.Warn(fmt.Sprintf("daily %s prune: PruneOlderThan failed", spec.Name), "err", err)
		return
	}
	slog.Info(
		fmt.Sprintf("daily %s prune: completed", spec.Name),
		"rows_deleted", n,
		"retention_days", int(spec.Retention.Hours()/24),
	)
}

// candidatePrunerAdapter adapts candidateRetentionStore's
// PruneResolved(ctx, olderThan time.Duration) to the PrunerStore contract.
// cutoff was computed by runPrune as time.Now().Add(-spec.Retention)
// immediately before this call, so time.Since(cutoff) recovers that same
// retention duration (sub-millisecond of computation drift, immaterial for a
// day-granularity TTL).
type candidatePrunerAdapter struct{ store candidateRetentionStore }

func (a candidatePrunerAdapter) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := a.store.PruneResolved(ctx, time.Since(cutoff))
	if err != nil {
		return 0, fmt.Errorf("candidate PruneResolved: %w", err)
	}
	return n, nil
}

// NewCandidatePrunerAdapter wraps a completion-candidate store (whose
// PruneResolved takes a retention duration) so it satisfies PrunerStore for
// use in a PrunerSpec.
func NewCandidatePrunerAdapter(store candidateRetentionStore) PrunerStore {
	return candidatePrunerAdapter{store: store}
}

// mergedPRsPrunerAdapter adapts mergedPRsRetentionStore's
// PruneOlderThan(ctx, olderThan time.Duration) to the PrunerStore contract.
// Same cutoff-to-duration derivation rationale as candidatePrunerAdapter.
type mergedPRsPrunerAdapter struct{ store mergedPRsRetentionStore }

func (a mergedPRsPrunerAdapter) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := a.store.PruneOlderThan(ctx, time.Since(cutoff))
	if err != nil {
		return 0, fmt.Errorf("merged_prs_observed PruneOlderThan: %w", err)
	}
	return n, nil
}

// NewMergedPRsPrunerAdapter wraps a merged_prs_observed store (whose
// PruneOlderThan takes a retention duration, not a cutoff) so it satisfies
// PrunerStore for use in a PrunerSpec.
func NewMergedPRsPrunerAdapter(store mergedPRsRetentionStore) PrunerStore {
	return mergedPRsPrunerAdapter{store: store}
}

// aiCostLedgerPrunerAdapter adapts a raw *pgxpool.Pool to the PrunerStore
// contract for ai_cost_ledger, which has no dedicated Store type. Uses a
// parameterized cutoff instead of the pre-refactor `NOW() - INTERVAL '30
// days'` literal — same 30-day retention, computed from the app server's
// clock like every other registry-driven prune job instead of Postgres's.
type aiCostLedgerPrunerAdapter struct{ pool *pgxpool.Pool }

func (a aiCostLedgerPrunerAdapter) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := a.pool.Exec(ctx, `DELETE FROM ai_cost_ledger WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("ai_cost_ledger DELETE: %w", err)
	}
	return tag.RowsAffected(), nil
}

// NewAICostLedgerPrunerAdapter wraps a Postgres pool so it satisfies
// PrunerStore for use in a PrunerSpec.
func NewAICostLedgerPrunerAdapter(pool *pgxpool.Pool) PrunerStore {
	return aiCostLedgerPrunerAdapter{pool: pool}
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

// buildKnowledgeConsolidDeps initialises the knowledge consolidation dep bundle.
// Extracted from buildSchedulerDeps to keep its cyclomatic complexity below
// the gocyclo threshold of 15.
func buildKnowledgeConsolidDeps(
	reflector ai.ReflectorIface,
	propStore proposal.StoreIface,
	knowledgeStore knowledge.StoreIface,
) *knowledgeConsolidationDeps {
	if reflector == nil || propStore == nil || knowledgeStore == nil {
		return nil
	}
	return &knowledgeConsolidationDeps{
		knowledge: knowledgeStore,
		proposal:  propStore,
		reflector: reflector,
	}
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
	knowledgeStore knowledge.StoreIface,
	workspaceID *uuid.UUID,
) (rDeps *reflectionDeps, cDeps *consolidationDeps, kcDeps *knowledgeConsolidationDeps, pbDeps *playbookDeps, sDeps *statusSnapshotDeps) {
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
	kcDeps = buildKnowledgeConsolidDeps(reflector, propStore, knowledgeStore)
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
	return rDeps, cDeps, kcDeps, pbDeps, sDeps
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
//
// knowledgeStore is optional: when nil (or when reflector/propStore is nil)
// the Sunday knowledge consolidation job is skipped.
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
	knowledgeStore knowledge.StoreIface,
) (*Scheduler, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return nil, fmt.Errorf("loading Asia/Taipei timezone: %w", err)
	}

	s, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("creating gocron scheduler: %w", err)
	}

	rDeps, cDeps, kcDeps, pbDeps, sDeps := buildSchedulerDeps(
		reflector, gtdStore, decStore, propStore, pbStore,
		snapStore, snapGen, knowledgeStore, workspaceID,
	)

	sc := &Scheduler{
		s:                     s,
		learning:              ls,
		discord:               dc,
		notion:                notionClient,
		briefingStores:        briefingStores,
		reviewer:              reviewer,
		reflectionDeps:        rDeps,
		consolidDeps:          cDeps,
		knowledgeConsolidDeps: kcDeps,
		statusDeps:            sDeps,
		pruner:                pruner,
		playbookDeps:          pbDeps,
		disciplinePool:        disciplinePool,
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
			gocron.WithName(dailyPendingProposalsPruneJobName),
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
	if err := sc.registerSundayKnowledgeConsolidation(s); err != nil {
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

// registerSundayKnowledgeConsolidation adds the Sunday 01:00 knowledge consolidation job.
func (sc *Scheduler) registerSundayKnowledgeConsolidation(s gocron.Scheduler) error {
	if sc.knowledgeConsolidDeps == nil {
		slog.Info("scheduler: SundayKnowledgeConsolidation skipped (reflector, proposal or knowledge store not configured)")
		return nil
	}
	_, err := s.NewJob(
		gocron.WeeklyJob(1, gocron.NewWeekdays(time.Sunday), gocron.NewAtTimes(gocron.NewAtTime(1, 0, 0))),
		gocron.NewTask(sc.sundayKnowledgeConsolidation),
		gocron.WithName("sunday-knowledge-consolidation"),
		// LimitModeReschedule: drop a run if the previous one is still executing.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering Sunday knowledge consolidation job: %w", err)
	}
	slog.Info("scheduler: SundayKnowledgeConsolidation scheduled at Sunday 01:00 Asia/Taipei")
	return nil
}

// sundayKnowledgeConsolidation wraps runKnowledgeConsolidation for the scheduler method signature.
func (s *Scheduler) sundayKnowledgeConsolidation() {
	if s.knowledgeConsolidDeps == nil {
		return
	}
	runKnowledgeConsolidation(*s.knowledgeConsolidDeps)
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
		slog.Warn(
			"daily notion briefing: upserting page failed",
			"err", err,
			"date", now.Format("2006-01-02"),
			"in_progress_count", len(briefing.InProgressTasks),
			"recent_decision_count", len(briefing.RecentDecisions),
		)
		return
	}

	slog.Info(
		"daily notion briefing: upsert succeeded",
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
		reason, _ := IsPGOnlyJob("daily-discipline-prune")
		slog.Info("daily discipline prune: unsupported on this backend (by design)", "reason", reason)
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
	slog.Info(
		"daily discipline prune: completed",
		"rows_deleted", tag.RowsAffected(),
		"retention", disciplinePruneAge,
	)
}

func (s *Scheduler) runDailyGuardPrune() {
	if s.disciplinePool == nil {
		reason, _ := IsPGOnlyJob("daily-guard-prune")
		slog.Info("daily guard prune: unsupported on this backend (by design)", "reason", reason)
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
	slog.Info(
		"daily guard prune: completed",
		"rows_deleted", tag.RowsAffected(),
		"retention", guardPruneAge,
	)
}

// WithPendingProposalsPruneSQLite wires the SQLite-only counterpart to the
// daily-pending-proposals-prune job and registers its cron entry. Call after
// New() (so disciplinePool has already been resolved) and before Start().
//
// A no-op when disciplinePool is already set: registerDailyJobs already owns
// that job's Postgres registration (New() calls it before any With* method
// runs), and a Scheduler is only ever backed by ONE storage backend at a
// time (storage.ResolveFromEnv) — this guard exists purely as a caller-bug
// safety net, not an expected runtime path.
func (sc *Scheduler) WithPendingProposalsPruneSQLite(store PendingProposalsPruneStore) error {
	if store == nil {
		slog.Info("scheduler: DailyPendingProposalsPrune (SQLite) skipped (store not configured)")
		return nil
	}
	if sc.disciplinePool != nil {
		return nil
	}
	sc.pendingProposalsPruneSQLite = store
	_, err := sc.s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
		gocron.NewTask(sc.runDailyPendingProposalsPrune),
		gocron.WithName(dailyPendingProposalsPruneJobName),
		// LimitModeReschedule: a stuck DELETE shouldn't pile up nightly
		// triggers. Same singleton policy as the Postgres registration.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering daily pending proposals prune job (SQLite): %w", err)
	}
	slog.Info("scheduler: DailyPendingProposalsPrune (SQLite) scheduled at 03:00 Asia/Taipei")
	return nil
}

// runDailyPendingProposalsPrune dispatches to the Postgres or SQLite
// implementation depending on which backend is wired, or logs a skip when
// neither is configured. See runDailyPendingProposalsPrunePG and
// runDailyPendingProposalsPruneSQLite for the per-backend retention logic —
// both apply the identical per-status policy documented on the PG function.
func (s *Scheduler) runDailyPendingProposalsPrune() {
	switch {
	case s.disciplinePool != nil:
		ctx, cancel := context.WithTimeout(context.Background(), pendingProposalsPruneTimeout)
		defer cancel()
		s.runDailyPendingProposalsPrunePG(ctx)
	case s.pendingProposalsPruneSQLite != nil:
		ctx, cancel := context.WithTimeout(context.Background(), pendingProposalsPruneTimeout)
		defer cancel()
		s.runDailyPendingProposalsPruneSQLite(ctx)
	default:
		slog.Info("daily pending_proposals prune: skipped (no backend configured)")
	}
}

// runDailyPendingProposalsPrunePG marks stale TypeTask pending proposals as
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
func (s *Scheduler) runDailyPendingProposalsPrunePG(ctx context.Context) {
	// Mark stale TypeTask pending proposals as rejected with an audit
	// reason. Run BEFORE the DELETE so newly-marked rows enter the resolved
	// retention window (90d) instead of being silently dropped.
	const markStaleTaskProposals = `UPDATE pending_proposals
SET status = 'rejected', resolved_at = NOW(), reason = 'ttl-expired-30d'
WHERE status = 'pending' AND type = 'task'
  AND created_at < NOW() - INTERVAL '` + pendingProposalsPendingTaskRetention + `'`
	markTag, markErr := s.disciplinePool.Exec(ctx, markStaleTaskProposals)
	markedRows := int64(0)
	if markErr != nil {
		slog.Warn("daily pending_proposals prune: mark TTL-stale task proposals failed", "err", markErr)
	} else {
		markedRows = markTag.RowsAffected()
		slog.Info(
			"daily pending_proposals prune: marked TTL-stale task proposals",
			"rows_updated", markedRows,
			"pending_task_retention", pendingProposalsPendingTaskRetention,
		)
	}

	// Test-only hook between mark and delete (nil in prod). Used by the
	// partial-success telemetry test to deterministically blow the ctx
	// deadline after mark has already affected rows.
	if pendingProposalsPruneBetweenSteps != nil {
		pendingProposalsPruneBetweenSteps(ctx)
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
		// Partial-success telemetry: if mark succeeded with rows-affected
		// but the ctx deadline tripped DURING the DELETE, the table is in
		// a consistent intermediate state (rows marked rejected but not
		// yet purged). Functionally fine — the 90 d resolved retention
		// will pick them up on the next run — but operators need a
		// distinct warn-level signal so they can monitor for chronic
		// deadline pressure (which would mean the timeout needs tuning
		// or the table needs more aggressive indexing). GTD 0ba91291.
		if errors.Is(err, context.DeadlineExceeded) && markErr == nil && markedRows > 0 {
			slog.Warn(
				"daily pending_proposals prune: mark succeeded but delete timed out — will retry next run",
				"marked_rows", markedRows,
				"task_retention", pendingProposalsPendingTaskRetention,
				"decision_retention", pendingProposalsPendingDecisionRetention,
				"resolved_retention", pendingProposalsResolvedRetention,
			)
			return
		}
		slog.Warn("daily pending_proposals prune: DELETE failed", "err", err)
		return
	}
	slog.Info(
		"daily pending_proposals prune: completed",
		"rows_deleted", tag.RowsAffected(),
		"resolved_retention", pendingProposalsResolvedRetention,
		"pending_decision_retention", pendingProposalsPendingDecisionRetention,
		"pending_task_retention", pendingProposalsPendingTaskRetention,
	)
}

// runDailyPendingProposalsPruneSQLite is the SQLite-native counterpart to
// runDailyPendingProposalsPrunePG — a genuine reimplementation of the same
// mark+delete retention policy in SQLite dialect (GTD decision G4,
// 6ea0b014), NOT a call-through to the Postgres code path. See
// PendingProposalsPruneStore and storage/sqlite.ProposalStore.
// MarkAndDeleteStaleProposals for why the cutoffs are Go-computed durations
// instead of PG interval literals.
//
// No partial-success ctx-deadline telemetry here (contrast with the PG
// function's DeadlineExceeded branch): SQLite is a local single-connection
// file/:memory: DB with no network round-trip, so the mark+delete pair
// finishes in low single-digit milliseconds even at personal-OS scale —
// there is no realistic timeout-pressure scenario for this branch to detect.
func (s *Scheduler) runDailyPendingProposalsPruneSQLite(ctx context.Context) {
	markedRows, deletedRows, err := s.pendingProposalsPruneSQLite.MarkAndDeleteStaleProposals(
		ctx,
		pendingProposalsPendingTaskRetentionDuration,
		pendingProposalsPendingDecisionRetentionDuration,
		pendingProposalsResolvedRetentionDuration,
		pendingProposalsTaskTTLReason,
	)
	if err != nil {
		slog.Warn("daily pending_proposals prune (SQLite): mark/delete failed", "err", err, "marked_rows", markedRows)
		return
	}
	slog.Info(
		"daily pending_proposals prune (SQLite): completed",
		"rows_marked", markedRows,
		"rows_deleted", deletedRows,
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
			slog.Warn(
				"weekly AI concept review: updating concept status failed",
				"concept_id", res.ID,
				"new_status", res.NewStatus,
				"err", err,
			)
			continue
		}
		updated++
		slog.Info(
			"weekly AI concept review: concept status updated",
			"concept_id", res.ID,
			"new_status", res.NewStatus,
		)
	}

	slog.Info(
		"weekly AI concept review: completed",
		"eligible_concepts", len(concepts),
		"ai_results", len(results),
		"updated", updated,
	)
}
