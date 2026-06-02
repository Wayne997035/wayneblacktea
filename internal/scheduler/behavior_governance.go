package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// behaviorGovernanceJobTimeout caps the weekly behavior governance cron run.
// At personal scale (≤100 outcomes, ≤50 active rules) this finishes well
// within 2 minutes.
const behaviorGovernanceJobTimeout = 2 * time.Minute

// behaviorGovernanceOutcomeLimit is the max number of recent outcomes fetched
// per governance run to bound DB load.
const behaviorGovernanceOutcomeLimit = 200

// behaviorGovernanceLookbackDays is the age window (in days) for outcomes
// considered "recent" for the governance run. Outcomes older than this are
// ignored to avoid replaying historical data on every weekly run.
const behaviorGovernanceLookbackDays = 7

// behaviorGovernanceLowConfidenceThreshold is the confidence below which an
// active rule is eligible for auto-deprecation.
const behaviorGovernanceLowConfidenceThreshold = 0.10

// behaviorGovernanceMinActiveDays is the minimum age (in days) an active rule
// must have before it is eligible for auto-deprecation. Rules younger than
// this have not had enough time to accumulate outcome feedback.
//
// Note: BehaviorRule has no activated_at column; age is approximated from
// CreatedAt (proposal-date). This may overestimate the active lifetime for
// rules that were proposed early but activated later. The approximation is
// documented here and tolerated at personal scale where manual review is
// always an option.
const behaviorGovernanceMinActiveDays = 30

// behaviorGovernanceActiveRuleLimit is the max number of active rules fetched
// per governance run.
const behaviorGovernanceActiveRuleLimit = 100

// governanceDeps bundles dependencies for the weekly behavior governance job.
type governanceDeps struct {
	outcomeStore      outcome.StoreIface
	behaviorRuleStore behaviorrule.StoreIface
	workspaceID       *uuid.UUID
}

// WithBehaviorGovernance wires the behavior governance deps and registers the
// weekly Wednesday 04:15 Asia/Taipei cron job. Must be called before Start().
// 04:15 Wednesday is distinct from the daily 04:00 behavior-rule-prune job
// to avoid singleton-mode reschedule interference.
func (sc *Scheduler) WithBehaviorGovernance(
	outcomeStore outcome.StoreIface, brStore behaviorrule.StoreIface, workspaceID *uuid.UUID,
) error {
	if outcomeStore == nil || brStore == nil {
		slog.Info("scheduler: BehaviorGovernance skipped (outcome or behaviorRule store nil)")
		return nil
	}
	sc.governanceDeps = &governanceDeps{
		outcomeStore:      outcomeStore,
		behaviorRuleStore: brStore,
		workspaceID:       workspaceID,
	}
	_, err := sc.s.NewJob(
		gocron.WeeklyJob(1, gocron.NewWeekdays(time.Wednesday), gocron.NewAtTimes(gocron.NewAtTime(4, 15, 0))),
		gocron.NewTask(sc.runBehaviorGovernanceJob),
		gocron.WithName("weekly-behavior-governance"),
		// LimitModeReschedule: drop a run if the previous one is still executing.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering weekly behavior governance job: %w", err)
	}
	slog.Info("scheduler: BehaviorGovernance scheduled at Wednesday 04:15 Asia/Taipei")
	return nil
}

// runBehaviorGovernanceJob is the scheduler callback for the weekly governance job.
func (sc *Scheduler) runBehaviorGovernanceJob() {
	if sc.governanceDeps == nil {
		return
	}
	runBehaviorGovernance(*sc.governanceDeps)
}

// runBehaviorGovernance is the core logic for the weekly behavior governance cron.
//
// Phase 1 responsibilities:
//  1. Fetch recent outcomes (created_at >= now-7d) that have RelatedRuleIDs.
//  2. For each linked rule, map the outcome result to 'success'|'failure' and
//     call behaviorrule.ApplyOutcome. Partial and unknown results are SKIPPED
//     (ApplyOutcome only accepts 'success' or 'failure').
//     Tolerates ErrNotFound for stale rule IDs.
//  3. List all active rules; auto-deprecate those with Confidence < 0.10 that
//     have been active >= 30 days (approximated from CreatedAt — see comment on
//     behaviorGovernanceMinActiveDays).
//
// All errors are logged at warn level; the function never panics.
func runBehaviorGovernance(deps governanceDeps) {
	ctx, cancel := context.WithTimeout(context.Background(), behaviorGovernanceJobTimeout)
	defer cancel()

	// ----------------------------------------------------------------
	// Step 1: fetch recent outcomes and apply confidence updates.
	// ----------------------------------------------------------------
	outcomes, err := deps.outcomeStore.ListRecentOutcomes(ctx, deps.workspaceID, "", behaviorGovernanceOutcomeLimit)
	if err != nil {
		slog.Warn("behavior_governance: listing recent outcomes failed", "err", err)
		return
	}

	cutoff := time.Now().AddDate(0, 0, -behaviorGovernanceLookbackDays)
	applyCount := 0
	for _, o := range outcomes {
		if o.CreatedAt.Before(cutoff) {
			continue
		}
		if len(o.RelatedRuleIDs) == 0 {
			continue
		}

		// Map outcome result to behaviorrule outcome string.
		// success → success, failure → failure, regressed → failure,
		// partial + unknown → SKIP (ApplyOutcome errors on other values).
		var brOutcome string
		switch o.Result {
		case "success":
			brOutcome = "success"
		case "failure", "regressed":
			brOutcome = "failure"
		default:
			// partial, unknown: not enough signal to adjust confidence.
			continue
		}

		for _, ruleID := range o.RelatedRuleIDs {
			_, applyErr := deps.behaviorRuleStore.ApplyOutcome(ctx, ruleID, brOutcome)
			if applyErr != nil {
				if errors.Is(applyErr, behaviorrule.ErrNotFound) {
					slog.Warn("behavior_governance: rule not found (stale link), skipping",
						"rule_id", ruleID, "outcome_id", o.ID)
					continue
				}
				slog.Warn("behavior_governance: ApplyOutcome failed",
					"rule_id", ruleID, "outcome_id", o.ID, "err", applyErr)
				continue
			}
			applyCount++
		}
	}

	slog.Info("behavior_governance: outcome→rule confidence updates applied",
		"outcomes_scanned", len(outcomes),
		"apply_calls", applyCount,
	)

	// ----------------------------------------------------------------
	// Step 2: auto-deprecate low-confidence stale active rules.
	// ----------------------------------------------------------------
	autoDeprecateStaleLowConfidenceRules(ctx, deps)
}

// autoDeprecateStaleLowConfidenceRules lists active rules and deprecates those
// with Confidence < 0.10 that have been active >= 30 days (from CreatedAt —
// see behaviorGovernanceMinActiveDays). Extracted to keep runBehaviorGovernance
// below the gocyclo threshold of 15.
func autoDeprecateStaleLowConfidenceRules(ctx context.Context, deps governanceDeps) {
	activeStatus := "active"
	activeRules, listErr := deps.behaviorRuleStore.List(ctx, behaviorrule.ListParams{
		WorkspaceID: deps.workspaceID,
		Status:      &activeStatus,
		Limit:       behaviorGovernanceActiveRuleLimit,
	})
	if listErr != nil {
		slog.Warn("behavior_governance: listing active rules failed", "err", listErr)
		return
	}

	deprecateThreshold := time.Now().AddDate(0, 0, -behaviorGovernanceMinActiveDays)
	deprecated := 0
	for _, rule := range activeRules {
		if rule.Confidence >= behaviorGovernanceLowConfidenceThreshold {
			continue
		}
		if rule.CreatedAt.After(deprecateThreshold) {
			continue
		}
		if _, depErr := deps.behaviorRuleStore.Deprecate(ctx, rule.ID); depErr != nil {
			if errors.Is(depErr, behaviorrule.ErrNotFound) {
				continue
			}
			slog.Warn("behavior_governance: Deprecate failed",
				"rule_id", rule.ID, "confidence", rule.Confidence, "err", depErr)
			continue
		}
		deprecated++
		slog.Info("behavior_governance: deprecated low-confidence stale rule",
			"rule_id", rule.ID, "confidence", rule.Confidence,
			"created_at", rule.CreatedAt.Format(time.RFC3339),
		)
	}

	slog.Info("behavior_governance: auto-deprecation completed",
		"active_rules_scanned", len(activeRules),
		"deprecated", deprecated,
	)
}
