package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/google/uuid"
)

// playbookPromoterTimeout caps the weekly playbook promoter run. AI call plus
// DB writes should finish well within 5 minutes.
const playbookPromoterTimeout = 4 * time.Minute

// playbookPromoterLookback is the trailing window scanned for decisions.
const playbookPromoterLookback = 30 * 24 * time.Hour

// playbookPromoterMaxDecisions caps the number of decisions fetched per run.
const playbookPromoterMaxDecisions = 100

// playbookPromoterMinDecisions is the minimum number of decisions required to
// proceed; fewer than this → the job skips silently.
const playbookPromoterMinDecisions = 3

// playbookDeps bundles the dependencies needed by the Sunday playbook promoter
// cron job. Each field is an interface so unit tests can inject stubs.
type playbookDeps struct {
	decision  decision.StoreIface
	playbook  playbook.StoreIface
	proposal  proposal.StoreIface
	reflector ai.ReflectorIface
}

// decisionSummary is the compact shape sent to Haiku in the prompt JSON.
type decisionSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	RepoName  string `json:"repo_name,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

// filterRecentDecisions returns decisions created after cutoff.
func filterRecentDecisions(decisions []db.Decision, cutoff time.Time) []db.Decision {
	out := decisions[:0]
	for _, d := range decisions {
		if d.CreatedAt.Valid && d.CreatedAt.Time.After(cutoff) {
			out = append(out, d)
		}
	}
	return out
}

// buildDecisionSummaries converts db.Decision slice into JSON-serialisable summaries.
func buildDecisionSummaries(decisions []db.Decision) ([]byte, error) {
	summaries := make([]decisionSummary, 0, len(decisions))
	for _, d := range decisions {
		ds := decisionSummary{
			ID:       d.ID.String(),
			Title:    d.Title,
			RepoName: d.RepoName.String,
		}
		if d.CreatedAt.Valid {
			ds.CreatedAt = d.CreatedAt.Time.Format("2006-01-02")
		}
		summaries = append(summaries, ds)
	}
	b, err := json.Marshal(summaries)
	if err != nil {
		return nil, fmt.Errorf("build decision summaries: %w", err)
	}
	return b, nil
}

// runPlaybookPromoter is the core logic of the Sunday 03:00 playbook promoter job.
//
// It:
//  1. Fetches recent decisions from the last 30 days.
//  2. If fewer than 3 decisions found, skips silently.
//  3. Calls Haiku via ai.ReflectorIface to generate 2-5 playbook candidates.
//  4. For each valid candidate, calls proposal.StoreIface.Create with Kind="playbook".
//
// All errors are logged at warn level; the function never panics.
func runPlaybookPromoter(deps playbookDeps) {
	// Independent timeout — MUST NOT inherit a request context.
	ctx, cancel := context.WithTimeout(context.Background(), playbookPromoterTimeout)
	defer cancel()

	decisions, err := deps.decision.All(ctx, playbookPromoterMaxDecisions)
	if err != nil {
		slog.Warn("playbook promoter: listing decisions failed", "err", err)
		return
	}

	cutoff := time.Now().Add(-playbookPromoterLookback)
	recent := filterRecentDecisions(decisions, cutoff)

	if len(recent) < playbookPromoterMinDecisions {
		slog.Info("playbook promoter: fewer than 3 recent decisions, skipping",
			"count", len(recent))
		return
	}

	decisionsJSON, err := buildDecisionSummaries(recent)
	if err != nil {
		slog.Warn("playbook promoter: marshaling decision summaries failed", "err", err)
		return
	}

	adaptedProposals, err := deps.reflector.Propose(
		ctx, string(decisionsJSON),
	)
	if err != nil {
		slog.Warn("playbook promoter: AI call failed", "err", err)
		return
	}

	if len(adaptedProposals) == 0 {
		slog.Info("playbook promoter: AI returned no playbook candidates")
		return
	}

	created := processPlaybookProposals(ctx, deps, adaptedProposals)

	slog.Info(
		"playbook promoter: cron completed",
		"decisions_scanned", len(recent),
		"proposals_from_ai", len(adaptedProposals),
		"proposals_created", created,
	)
}

const (
	maxTriggerLen = 500
	maxContentLen = 600
)

// processPlaybookProposals iterates KnowledgeProposals from the AI, validates,
// and creates pending_proposals rows. Returns the count of created proposals.
//
// build inlines all 4 pre-refactor validation checks (each keeping its own
// slog.Warn message, order pinned: title-empty/content-empty ->
// title-too-long -> content-too-long -> title-tag-noise -> content-tag-noise,
// per proploop spec Risk flags) plus the tag->srcIDs uuid.Parse loop and the
// srcIDs==nil -> []uuid.UUID{} nil-normalization, both formerly in
// createPlaybookProposal/marshalPlaybookPayload (both deleted, no longer needed).
// marshal-fail and Create-fail intentionally share one message/hook, matching
// createPlaybookProposal's pre-refactor single wrapped-error behaviour.
func processPlaybookProposals(
	ctx context.Context,
	deps playbookDeps,
	proposals []ai.KnowledgeProposal,
) int {
	build := func(kp ai.KnowledgeProposal) (proposal.Type, any, bool) {
		if kp.Title == "" || kp.Content == "" {
			return "", nil, false
		}
		if len([]rune(kp.Title)) > maxTriggerLen {
			slog.Warn("playbook promoter: AI proposal trigger too long, skipping",
				"trigger_len", len([]rune(kp.Title)))
			return "", nil, false
		}
		if len([]rune(kp.Content)) > maxContentLen {
			slog.Warn("playbook promoter: AI proposal content too long, skipping",
				"content_len", len([]rune(kp.Content)))
			return "", nil, false
		}
		if err := sanitize.ValidateNoTagNoise(kp.Title); err != nil {
			slog.Warn("playbook promoter: tag noise in AI title, skipping", "err", err)
			return "", nil, false
		}
		if err := sanitize.ValidateNoTagNoise(kp.Content); err != nil {
			slog.Warn("playbook promoter: tag noise in AI content, skipping", "err", err)
			return "", nil, false
		}
		var srcIDs []uuid.UUID
		for _, tag := range kp.Tags {
			if id, err := uuid.Parse(tag); err == nil {
				srcIDs = append(srcIDs, id)
			}
		}
		if srcIDs == nil {
			srcIDs = []uuid.UUID{}
		}
		return proposal.TypePlaybook, PlaybookProposalPayload{
			TriggerPattern:    kp.Title,
			ActionTemplate:    kp.Content,
			SourceDecisionIDs: srcIDs,
		}, true
	}
	logCreateFail := func(kp ai.KnowledgeProposal, err error) {
		slog.Warn(
			"playbook promoter: creating pending proposal failed",
			"trigger_pattern", kp.Title,
			"err", err,
		)
	}
	return runProposalTail(ctx, deps.proposal, nil, "playbook-promoter-cron", proposals, build, logCreateFail, logCreateFail)
}

// PlaybookProposalPayload is the JSON shape stored in pending_proposals.payload
// when type='playbook'.
type PlaybookProposalPayload struct {
	TriggerPattern    string      `json:"trigger_pattern"`
	ActionTemplate    string      `json:"action_template"`
	SourceDecisionIDs []uuid.UUID `json:"source_decision_ids"`
}
