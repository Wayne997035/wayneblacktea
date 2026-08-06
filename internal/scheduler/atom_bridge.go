package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// atomBridgeJobTimeout caps the weekly atom-bridge cron run. Each atom
// generates one proposal.Create call; at personal scale (≤100 consolidated
// atoms) this finishes well within 2 minutes.
const atomBridgeJobTimeout = 2 * time.Minute

// atomBridgeMinConsolidated is the minimum number of atoms with
// digest_status='consolidated' that must exist before the bridge job runs.
// Below this threshold the job skips and logs at Info level.
const atomBridgeMinConsolidated = 10

// atomBridgeLimit is the maximum number of consolidated atoms fetched per
// cron run to bound DB load and proposal volume at personal scale.
const atomBridgeLimit = 50

// atomBridgeDeps bundles dependencies for the weekly atom-bridge cron job.
// All fields are required when the struct is non-nil.
type atomBridgeDeps struct {
	store       atom.StoreIface
	proposal    proposal.StoreIface
	workspaceID *uuid.UUID
}

// NewAtomBridgeDeps constructs an atomBridgeDeps bundle for use by main.go
// when wiring WithAtomBridge. Any nil argument is accepted — the cron job
// nil-checks each field and skips gracefully when a dep is absent.
func NewAtomBridgeDeps(store atom.StoreIface, prop proposal.StoreIface, workspaceID *uuid.UUID) *atomBridgeDeps {
	return &atomBridgeDeps{store: store, proposal: prop, workspaceID: workspaceID}
}

// WithAtomBridge wires the atom-bridge deps and registers the weekly Saturday
// 04:15 Asia/Taipei cron job. Must be called before Start().
// Mirrors the WithPruner/PrunerSpec registry pattern in scheduler.go.
func (sc *Scheduler) WithAtomBridge(deps *atomBridgeDeps) error {
	if deps == nil {
		slog.Info("scheduler: AtomBridge skipped (deps nil)")
		return nil
	}
	sc.atomBridgeDeps = deps
	_, err := sc.s.NewJob(
		gocron.WeeklyJob(1, gocron.NewWeekdays(time.Saturday), gocron.NewAtTimes(gocron.NewAtTime(4, 15, 0))),
		gocron.NewTask(sc.runAtomBridgeJob),
		gocron.WithName("weekly-atom-bridge"),
		// LimitModeReschedule: drop a run if the previous one is still executing.
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering weekly atom-bridge job: %w", err)
	}
	slog.Info("scheduler: AtomBridge scheduled at Saturday 04:15 Asia/Taipei")
	return nil
}

// runAtomBridgeJob is the scheduler callback for the weekly atom-bridge job.
func (sc *Scheduler) runAtomBridgeJob() {
	if sc.atomBridgeDeps == nil {
		return
	}
	runAtomBridge(*sc.atomBridgeDeps)
}

// runAtomBridge is the core logic for the weekly atom → knowledge bridge cron.
//
// It:
//  1. Counts atoms with digest_status='consolidated'; skips when < 10.
//  2. Lists up to atomBridgeLimit 'consolidated' atoms.
//  3. Applies quality gate: content length >= 80 chars AND len(tags) >= 2.
//     (atom.Atom has no confidence field; quality is approximated from richness.)
//  4. For each qualifying atom, creates a TypeKnowledge pending_proposal with
//     ProposedBy='atom_bridge'. Deduplication is delegated to AddItem's
//     built-in similarity check at confirm_proposal time.
//  5. Sets digest_status='promoted' on each successfully promoted atom.
//
// All errors are logged at warn level; the function never panics.
func runAtomBridge(deps atomBridgeDeps) {
	ctx, cancel := context.WithTimeout(context.Background(), atomBridgeJobTimeout)
	defer cancel()

	if deps.store == nil || deps.proposal == nil {
		slog.Info("atom_bridge: store or proposal dep nil, skipping")
		return
	}

	// Step 1: count 'consolidated' atoms.
	count, err := deps.store.CountByDigestStatus(ctx, deps.workspaceID, atom.DigestStatusConsolidated)
	if err != nil {
		slog.Warn("atom_bridge: counting consolidated atoms failed", "err", err)
		return
	}
	if count < atomBridgeMinConsolidated {
		slog.Info("atom_bridge: fewer than 10 consolidated atoms, skipping",
			"count", count, "min_required", atomBridgeMinConsolidated)
		return
	}

	// Step 2: list consolidated atoms.
	atoms, err := deps.store.ListByDigestStatus(ctx, deps.workspaceID, atom.DigestStatusConsolidated, atomBridgeLimit)
	if err != nil {
		slog.Warn("atom_bridge: listing consolidated atoms failed", "err", err)
		return
	}

	promoted := 0
	skipped := 0
	for _, a := range atoms {
		// Step 3: quality gate — shared with promote_atom_to_knowledge
		// (internal/mcp/tools_atom.go) via atom.CheckPromotionEligibility so
		// the manual and cron promotion paths cannot silently diverge (G6
		// decision 8f25c58d). No confidence field on atom.Atom, so gating is
		// content length + tag count only.
		if !atom.CheckPromotionEligibility(a.Content, a.Tags).Eligible() {
			skipped++
			continue
		}

		// Step 4: derive a title from content (first 80 runes or full content).
		// Rune-aware slice prevents panic and invalid UTF-8 on CJK/emoji content.
		title := a.Content
		if runes := []rune(title); len(runes) > 80 {
			title = string(runes[:80])
		}

		payload, marshalErr := json.Marshal(proposal.KnowledgePayload{
			Title:   title,
			Content: a.Content,
			Tags:    a.Tags,
		})
		if marshalErr != nil {
			slog.Warn("atom_bridge: marshaling knowledge payload failed",
				"atom_id", a.ID, "err", marshalErr)
			skipped++
			continue
		}

		_, createErr := deps.proposal.Create(ctx, proposal.CreateParams{
			WorkspaceID: deps.workspaceID,
			Type:        proposal.TypeKnowledge,
			Payload:     payload,
			ProposedBy:  "atom_bridge",
		})
		if createErr != nil {
			slog.Warn("atom_bridge: creating knowledge proposal failed",
				"atom_id", a.ID, "err", createErr)
			skipped++
			continue
		}

		// Step 5: mark atom as promoted.
		if setErr := deps.store.SetDigestStatus(ctx, a.ID, atom.DigestStatusPromoted, ""); setErr != nil {
			slog.Warn("atom_bridge: setting atom digest_status='promoted' failed",
				"atom_id", a.ID, "err", setErr)
		}
		promoted++
	}

	slog.Info(
		"atom_bridge: weekly run completed",
		"consolidated_total", count,
		"fetched", len(atoms),
		"promoted", promoted,
		"skipped", skipped,
	)
}
