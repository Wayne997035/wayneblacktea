package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/go-co-op/gocron/v2"
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
	discord        *discord.Client
	notion         *notion.Client
	briefingStores notion.BriefingStores
	reviewer       ai.ConceptReviewerIface
	reflectionDeps *reflectionDeps
	consolidDeps   *consolidationDeps
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
func New(
	ls learning.StoreIface,
	dc *discord.Client,
	notionClient *notion.Client,
	briefingStores notion.BriefingStores,
	reviewer ai.ConceptReviewerIface,
	gtdStore gtd.StoreIface,
	decStore decision.StoreIface,
	propStore proposal.StoreIface,
	reflector ai.ReflectorIface,
) (*Scheduler, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return nil, fmt.Errorf("loading Asia/Taipei timezone: %w", err)
	}

	s, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("creating gocron scheduler: %w", err)
	}

	var rDeps *reflectionDeps
	var cDeps *consolidationDeps
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

	sc := &Scheduler{
		s:              s,
		learning:       ls,
		discord:        dc,
		notion:         notionClient,
		briefingStores: briefingStores,
		reviewer:       reviewer,
		reflectionDeps: rDeps,
		consolidDeps:   cDeps,
	}

	if err := sc.registerDailyJobs(s); err != nil {
		return nil, err
	}
	if err := sc.registerWeeklyJobs(s); err != nil {
		return nil, err
	}

	return sc, nil
}

// registerDailyJobs adds the daily 08:00 scheduled tasks.
func (sc *Scheduler) registerDailyJobs(s gocron.Scheduler) error {
	_, err := s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(8, 0, 0))),
		gocron.NewTask(sc.sendDailyReviewReminder),
		gocron.WithName("daily-review-reminder"),
	)
	if err != nil {
		return fmt.Errorf("registering daily review job: %w", err)
	}

	if sc.notion == nil || sc.briefingStores == nil {
		slog.Info("scheduler: DailyNotionBriefing skipped (Notion client not configured)")
		return nil
	}
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

	if sc.reflectionDeps == nil {
		slog.Info("scheduler: SaturdayReflection + SaturdayConsolidation skipped (reflector or stores not configured)")
		return nil
	}
	return sc.registerSaturdayJobs(s)
}

// registerSaturdayJobs adds the Saturday 23:00 reflection + consolidation pair.
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
	return nil
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
