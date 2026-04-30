package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discord"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/go-co-op/gocron/v2"
)

// Scheduler wraps gocron and coordinates scheduled background jobs.
type Scheduler struct {
	s        gocron.Scheduler
	learning learning.StoreIface
	discord  *discord.Client
}

// New creates and configures the Scheduler with all registered jobs. The
// learning store is taken as the backend-agnostic StoreIface so the scheduler
// works against either pg or SQLite.
func New(ls learning.StoreIface, dc *discord.Client) (*Scheduler, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return nil, fmt.Errorf("loading Asia/Taipei timezone: %w", err)
	}

	s, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("creating gocron scheduler: %w", err)
	}

	sc := &Scheduler{
		s:        s,
		learning: ls,
		discord:  dc,
	}

	_, err = s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(8, 0, 0))),
		gocron.NewTask(sc.sendDailyReviewReminder),
		gocron.WithName("daily-review-reminder"),
	)
	if err != nil {
		return nil, fmt.Errorf("registering daily review job: %w", err)
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
