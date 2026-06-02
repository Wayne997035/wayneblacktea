package handler

import (
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestFreshDashboardHandoff is the regression guard for the production bug where
// the homepage "下次工作" card showed a 3-week-old, never-resolved session
// handoff. The dashboard must hide handoffs older than dashboardHandoffFreshness.
func TestFreshDashboardHandoff(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	withAge := func(age time.Duration) *db.SessionHandoff {
		return &db.SessionHandoff{
			CreatedAt: pgtype.Timestamptz{Time: now.Add(-age), Valid: true},
		}
	}

	tests := []struct {
		name     string
		handoff  *db.SessionHandoff
		wantShow bool
	}{
		{"nil handoff hidden", nil, false},
		{"no created_at hidden", &db.SessionHandoff{CreatedAt: pgtype.Timestamptz{Valid: false}}, false},
		{"1h old shown", withAge(1 * time.Hour), true},
		{"71h old (within window) shown", withAge(71 * time.Hour), true},
		{"73h old (past window) hidden", withAge(73 * time.Hour), false},
		{"3-week-old reported bug hidden", withAge(21 * 24 * time.Hour), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := freshDashboardHandoff(tc.handoff, now)
			if tc.wantShow && got == nil {
				t.Errorf("freshDashboardHandoff() = nil, want handoff shown")
			}
			if !tc.wantShow && got != nil {
				t.Errorf("freshDashboardHandoff() = handoff, want hidden (stale/nil)")
			}
		})
	}
}
