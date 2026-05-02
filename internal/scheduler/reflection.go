package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// reflectionJobTimeout caps the full reflection cron run. Haiku + DB writes
// should finish comfortably within 5 minutes; we pick 4 to stay under the
// 5-minute scheduler window without wedging the goroutine indefinitely.
const reflectionJobTimeout = 4 * time.Minute

// reflectionLookback is the trailing window scanned for activity + decisions.
const reflectionLookback = 7 * 24 * time.Hour

// reflectionMaxActivity is the maximum number of activity_log rows fetched per
// run to prevent unbounded memory usage on busy workspaces.
const reflectionMaxActivity = 200

// reflectionMaxDecisions caps the number of decisions included per run.
const reflectionMaxDecisions = 50

// reflectionDeps bundles the dependencies needed by the reflection cron job.
// Each field is an interface so unit tests can inject stubs.
type reflectionDeps struct {
	gtd       gtd.StoreIface
	decision  decision.StoreIface
	proposal  proposal.StoreIface
	reflector ai.ReflectorIface
}

// runReflection is the core logic of the Saturday-night reflection cron job.
//
// It:
//  1. Fetches the last 7 days of activity_log rows and recent decisions.
//  2. Builds a compact summary string and sends it to Haiku via reflector.
//  3. Writes each returned KnowledgeProposal as a pending_proposals row with
//     type='knowledge' so the user can confirm or reject it.
//
// All errors are logged at warn level; the function never panics.
// An atomic last_run_at check is enforced via the gocron singleton mode
// (LimitModeReschedule) registered in scheduler.New.
func runReflection(deps reflectionDeps) {
	// Independent timeout — MUST NOT inherit a request context.
	ctx, cancel := context.WithTimeout(context.Background(), reflectionJobTimeout)
	defer cancel()

	since := time.Now().Add(-reflectionLookback)

	activities, err := deps.gtd.ListActivityLogsSince(ctx, since, reflectionMaxActivity)
	if err != nil {
		slog.Warn("reflection: listing activity logs failed", "err", err)
		return
	}

	decisions, err := deps.decision.All(ctx, reflectionMaxDecisions)
	if err != nil {
		slog.Warn("reflection: listing decisions failed", "err", err)
		return
	}

	if len(activities) == 0 && len(decisions) == 0 {
		slog.Info("reflection: nothing to reflect on, skipping")
		return
	}

	summary := buildReflectionSummary(activities, decisions)

	proposals, err := deps.reflector.Propose(ctx, summary)
	if err != nil {
		slog.Warn("reflection: AI proposal failed", "err", err)
		return
	}

	if len(proposals) == 0 {
		slog.Info("reflection: AI returned no knowledge proposals")
		return
	}

	created := 0
	for _, kp := range proposals {
		if kp.Title == "" || kp.Content == "" {
			continue // skip malformed entries
		}
		payload, merr := marshalKnowledgePayload(kp)
		if merr != nil {
			slog.Warn("reflection: marshaling proposal payload failed", "err", merr)
			continue
		}
		if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
			Type:       proposal.TypeKnowledge,
			Payload:    payload,
			ProposedBy: "reflection-cron",
		}); cerr != nil {
			slog.Warn("reflection: creating pending proposal failed", "title", kp.Title, "err", cerr)
			continue
		}
		created++
	}

	slog.Info("reflection: cron completed",
		"activities_scanned", len(activities),
		"decisions_scanned", len(decisions),
		"proposals_from_ai", len(proposals),
		"proposals_created", created,
	)
}

// buildReflectionSummary renders activities and decisions into a compact plain-
// text string for the Haiku prompt. Activity notes are NOT included verbatim
// to minimise prompt-injection surface; only actor + action are used.
//
// SECURITY: ai.Reflector.Propose owns the [BEGIN ACTIVITIES]…[END ACTIVITIES]
// boundary wrapping (single source of truth — avoids double-wrapping the
// payload). Decision rationale is intentionally OMITTED here — title alone is
// enough signal for the reflector and rationale is a free-text field
// controlled by whoever called log_decision (security audit M-2 / OWASP LLM01).
func buildReflectionSummary(activities []db.ActivityLog, decisions []db.Decision) string {
	var sb strings.Builder

	if len(activities) > 0 {
		sb.WriteString("## Recent Activities\n")
		for _, a := range activities {
			ts := ""
			if a.CreatedAt.Valid {
				ts = a.CreatedAt.Time.Format("2006-01-02")
			}
			fmt.Fprintf(&sb, "- [%s] %s: %s\n", ts, a.Actor, a.Action)
		}
	}

	if len(decisions) > 0 {
		sb.WriteString("\n## Recent Decisions\n")
		for _, d := range decisions {
			ts := ""
			if d.CreatedAt.Valid {
				ts = d.CreatedAt.Time.Format("2006-01-02")
			}
			// Title only — rationale is excluded to prevent
			// prompt-injection via free-text decision content.
			fmt.Fprintf(&sb, "- [%s] %s\n", ts, d.Title)
		}
	}

	return sb.String()
}

// KnowledgePayload is the on-disk JSON shape stored in pending_proposals.payload
// when type='knowledge'. Mirrors ai.KnowledgeProposal but is defined here so
// the proposal package has no dependency on the ai package.
type KnowledgePayload struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

// marshalKnowledgePayload encodes a KnowledgeProposal into the JSONB payload
// expected by the pending_proposals table.
func marshalKnowledgePayload(kp ai.KnowledgeProposal) ([]byte, error) {
	p := KnowledgePayload{
		Title:   kp.Title,
		Content: kp.Content,
		Tags:    kp.Tags,
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal knowledge payload: %w", err)
	}
	return b, nil
}
