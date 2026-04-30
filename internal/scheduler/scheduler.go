package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/go-co-op/gocron/v2"
)

// dailyBriefingTimeout caps each Notion morning briefing run. The aggregate
// query + Notion upsert finishes well under 10 s in practice; we pick 60 s
// to leave room for occasional Notion API slowness without letting a stuck
// HTTP call wedge the scheduler goroutine.
const dailyBriefingTimeout = 60 * time.Second

// Scheduler wraps gocron and coordinates scheduled background jobs.
type Scheduler struct {
	s              gocron.Scheduler
	learning       learning.StoreIface
	discord        *discord.Client
	notion         *notion.Client
	briefingStores notion.BriefingStores
}

// New creates and configures the Scheduler with all registered jobs.
//
// notionClient and briefingStores are optional: when notionClient is nil
// (NOTION_INTEGRATION_SECRET unset) we skip registering the morning briefing
// job entirely and log a single info-level skip message at startup. This
// preserves the "no Notion configured = legacy single-user mode" behavior
// without surprising the operator with retried 401s every morning.
func New(
	ls learning.StoreIface,
	dc *discord.Client,
	notionClient *notion.Client,
	briefingStores notion.BriefingStores,
) (*Scheduler, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return nil, fmt.Errorf("loading Asia/Taipei timezone: %w", err)
	}

	s, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("creating gocron scheduler: %w", err)
	}

	sc := &Scheduler{
		s:              s,
		learning:       ls,
		discord:        dc,
		notion:         notionClient,
		briefingStores: briefingStores,
	}

	_, err = s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(8, 0, 0))),
		gocron.NewTask(sc.sendDailyReviewReminder),
		gocron.WithName("daily-review-reminder"),
	)
	if err != nil {
		return nil, fmt.Errorf("registering daily review job: %w", err)
	}

	if notionClient != nil && briefingStores != nil {
		_, err = s.NewJob(
			gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(8, 0, 0))),
			gocron.NewTask(sc.sendDailyNotionBriefing),
			gocron.WithName("daily-notion-briefing"),
			// LimitModeReschedule drops a run if the previous one is still
			// executing (e.g. Notion API slow). Prevents goroutine pile-up.
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
		)
		if err != nil {
			return nil, fmt.Errorf("registering daily Notion briefing job: %w", err)
		}
		slog.Info("scheduler: DailyNotionBriefing scheduled at 08:00 Asia/Taipei")
	} else {
		slog.Info("scheduler: DailyNotionBriefing skipped (Notion client not configured)")
	}

	return sc, nil
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
