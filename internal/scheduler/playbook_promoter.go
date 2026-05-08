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

// createPlaybookProposal converts a KnowledgeProposal from the AI into a
// pending_proposals row. Tags are expected to be UUID strings for
// source_decision_ids.
func createPlaybookProposal(
	ctx context.Context,
	deps playbookDeps,
	kp ai.KnowledgeProposal,
) error {
	var srcIDs []uuid.UUID
	for _, tag := range kp.Tags {
		if id, err := uuid.Parse(tag); err == nil {
			srcIDs = append(srcIDs, id)
		}
	}

	payload, err := marshalPlaybookPayload(kp.Title, kp.Content, srcIDs)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypePlaybook,
		Payload:    payload,
		ProposedBy: "playbook-promoter-cron",
	}); cerr != nil {
		return fmt.Errorf("create playbook proposal: %w", cerr)
	}
	return nil
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

	slog.Info("playbook promoter: cron completed",
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
func processPlaybookProposals(
	ctx context.Context,
	deps playbookDeps,
	proposals []ai.KnowledgeProposal,
) int {
	created := 0
	for _, kp := range proposals {
		if kp.Title == "" || kp.Content == "" {
			continue
		}
		if len([]rune(kp.Title)) > maxTriggerLen {
			slog.Warn("playbook promoter: AI proposal trigger too long, skipping",
				"trigger_len", len([]rune(kp.Title)))
			continue
		}
		if len([]rune(kp.Content)) > maxContentLen {
			slog.Warn("playbook promoter: AI proposal content too long, skipping",
				"content_len", len([]rune(kp.Content)))
			continue
		}
		if err := sanitize.ValidateNoTagNoise(kp.Title); err != nil {
			slog.Warn("playbook promoter: tag noise in AI title, skipping", "err", err)
			continue
		}
		if err := sanitize.ValidateNoTagNoise(kp.Content); err != nil {
			slog.Warn("playbook promoter: tag noise in AI content, skipping", "err", err)
			continue
		}
		if err := createPlaybookProposal(ctx, deps, kp); err != nil {
			slog.Warn("playbook promoter: creating pending proposal failed",
				"trigger_pattern", kp.Title,
				"err", err,
			)
			continue
		}
		created++
	}
	return created
}

// buildAdaptedPlaybookPrompt builds a prompt that instructs Haiku to return
// KnowledgeProposal-compatible JSON (title/content/tags) so the existing
// Reflector.Propose parser can handle the response without a new AI interface.
// We map: title → trigger_pattern, content → action_template,
//
//	tags  → source_decision_ids (UUID strings).
func buildAdaptedPlaybookPrompt(decisionsJSON string) string {
	return fmt.Sprintf(
		"You are analyzing a person's recent architectural and technical decisions "+
			"to find recurring patterns that can be turned into procedural playbooks.\n\n"+
			"Here are the recent decisions (JSON):\n"+
			"[BEGIN ACTIVITIES]\n%s\n[END ACTIVITIES]\n\n"+
			"Produce 2–5 playbook candidates derived from 2+ related decisions.\n"+
			"Return ONLY a JSON array (no markdown) where each object has:\n"+
			"  \"title\": one-sentence description of when to apply this rule\n"+
			"  \"content\": concrete steps to take (≤300 chars)\n"+
			"  \"tags\": array of source decision UUID strings\n\n"+
			"Return [] if no patterns are worth capturing.",
		decisionsJSON,
	)
}

// PlaybookProposalPayload is the JSON shape stored in pending_proposals.payload
// when type='playbook'.
type PlaybookProposalPayload struct {
	TriggerPattern    string      `json:"trigger_pattern"`
	ActionTemplate    string      `json:"action_template"`
	SourceDecisionIDs []uuid.UUID `json:"source_decision_ids"`
}

// marshalPlaybookPayload encodes a playbook candidate into the JSONB payload
// expected by the pending_proposals table.
func marshalPlaybookPayload(triggerPattern, actionTemplate string, srcIDs []uuid.UUID) ([]byte, error) {
	if srcIDs == nil {
		srcIDs = []uuid.UUID{}
	}
	p := PlaybookProposalPayload{
		TriggerPattern:    triggerPattern,
		ActionTemplate:    actionTemplate,
		SourceDecisionIDs: srcIDs,
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal playbook payload: %w", err)
	}
	return b, nil
}
