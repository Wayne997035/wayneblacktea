package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/redact"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
)

// defaultAtomCapacity is the target atom count per workspace. The consolidation
// cron fires only when the total atom count exceeds 80% of this value.
// M9 acceptance criterion: cron skips when CountTotal < 80% of this constant.
const defaultAtomCapacity = 500

// atomConsolidJobTimeout caps the full atom consolidation cron run. Each
// cluster triggers one Haiku call; at personal scale (≤20 clusters) this
// finishes well within 5 minutes.
const atomConsolidJobTimeout = 5 * time.Minute

// atomConsolidMaxKeywords caps the number of distinct keyword searches issued
// per cron run to bound Haiku budget and runtime (SA risk note: O(keywords ×
// Search calls) is acceptable at personal scale but must be bounded).
const atomConsolidMaxKeywords = 20

// atomConsolidSearchLimit is the per-keyword Search result count. Returns up
// to this many atoms per keyword for cluster building.
const atomConsolidSearchLimit = 20

// atomConsolidMinClusterSize is the minimum cluster size (>3 atoms sharing a
// keyword) before a consolidation Haiku call is made.
// M9 acceptance criterion 7: clusters of >3 atoms.
const atomConsolidMinClusterSize = 4

// atomConsolidDeps bundles dependencies for the M9 atom consolidation cron job.
// All fields are required when the struct is non-nil.
type atomConsolidDeps struct {
	store       atom.StoreIface
	atomizer    *ai.Atomizer
	workspaceID *uuid.UUID
}

// NewAtomConsolidDeps constructs an atomConsolidDeps bundle for use by main.go
// when wiring WithAtomConsolidator. Any nil argument is accepted — the cron job
// nil-checks each field and skips gracefully when a dep is absent.
func NewAtomConsolidDeps(store atom.StoreIface, atomizer *ai.Atomizer, workspaceID *uuid.UUID) *atomConsolidDeps {
	return &atomConsolidDeps{store: store, atomizer: atomizer, workspaceID: workspaceID}
}

// atomCluster holds a group of atoms sharing a common keyword, eligible for
// consolidation when len(atoms) > atomConsolidMinClusterSize.
type atomCluster struct {
	keyword string
	atoms   []atom.Atom
}

// WithAtomConsolidator wires the atom consolidation deps and registers the
// daily 04:30 Asia/Taipei cron job. Must be called before Start().
// Following the existing With-method pattern (WithBehaviorGovernance, etc.);
// New() signature is NOT changed (M9 acceptance criterion 12).
func (sc *Scheduler) WithAtomConsolidator(deps *atomConsolidDeps) error {
	if deps == nil {
		slog.Info("scheduler: AtomConsolidation skipped (deps nil)")
		return nil
	}
	sc.atomConsolidDeps = deps
	_, err := sc.s.NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(4, 30, 0))),
		gocron.NewTask(sc.runAtomConsolidation),
		gocron.WithName("daily-atom-consolidation"),
		// LimitModeReschedule: drop a run if the previous one is still executing
		// (M9 acceptance criterion 15 — singleton guard).
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("registering daily atom consolidation job: %w", err)
	}
	slog.Info("scheduler: AtomConsolidation scheduled at 04:30 Asia/Taipei")
	return nil
}

// runAtomConsolidation is the scheduler callback for the daily atom consolidation job.
func (s *Scheduler) runAtomConsolidation() {
	if s.atomConsolidDeps == nil {
		return
	}
	runAtomConsolidation(*s.atomConsolidDeps)
	// Direction-D instrumentation: best-effort activity log so atom growth is
	// visible in the dashboard automation feed. Uses cognitiveDeps.gtd when
	// available; skips silently when not configured.
	if s.cognitiveDeps != nil && s.cognitiveDeps.gtd != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if logErr := s.cognitiveDeps.gtd.LogActivity(ctx, "scheduler", "atom_consolidation",
			nil, "completed",
		); logErr != nil {
			slog.Warn("atom_consolidation: LogActivity failed", "err", logErr)
		}
	}
}

// runAtomConsolidation is the core logic for the M9 atom consolidation cron.
//
// It:
//  1. Checks total atom count against 80% of defaultAtomCapacity threshold.
//  2. Collects distinct keywords from recent atoms (capped at atomConsolidMaxKeywords).
//  3. For each keyword, searches for atoms; groups atoms sharing the keyword into
//     clusters of >3.
//  4. For each eligible cluster, calls Haiku to produce exactly ONE condensed atom.
//  5. Persists the condensed atom with parent_table='consolidated_cluster'.
//  6. Marks all original cluster atoms with digest_status='consolidated'.
//
// All errors are logged at warn level; the function never panics.
func runAtomConsolidation(deps atomConsolidDeps) {
	ctx, cancel := context.WithTimeout(context.Background(), atomConsolidJobTimeout)
	defer cancel()

	// Step 1: threshold check — skip when < 80% capacity.
	total, err := deps.store.CountTotal(ctx, deps.workspaceID)
	if err != nil {
		slog.Warn("atom consolidation: counting atoms failed", "err", err)
		return
	}
	threshold := int64(float64(defaultAtomCapacity) * 0.8)
	if total < threshold {
		slog.Info("atom consolidation: below 80% capacity, skipping",
			"total", total, "threshold", threshold)
		return
	}
	slog.Info("atom consolidation: threshold met, proceeding",
		"total", total, "threshold", threshold)

	// Step 2: collect distinct keywords from recent atoms by searching for
	// common high-signal terms. We use the top-N atoms to seed keyword lists.
	keywords, err := collectDistinctKeywords(ctx, deps)
	if err != nil {
		slog.Warn("atom consolidation: collecting keywords failed", "err", err)
		return
	}
	if len(keywords) == 0 {
		slog.Info("atom consolidation: no keywords found, skipping")
		return
	}

	// Step 3: build clusters per keyword.
	clusters := buildAtomClusters(ctx, deps, keywords)
	if len(clusters) == 0 {
		slog.Info("atom consolidation: no clusters of sufficient size found")
		return
	}

	// Step 4-6: consolidate each cluster.
	consolidated := 0
	for _, cl := range clusters {
		if err := consolidateCluster(ctx, deps, cl); err != nil {
			slog.Warn("atom consolidation: cluster consolidation failed",
				"keyword", cl.keyword, "cluster_size", len(cl.atoms), "err", err)
			continue
		}
		consolidated++
	}

	slog.Info("atom consolidation: cron completed",
		"total_atoms", total,
		"keywords_scanned", len(keywords),
		"clusters_processed", len(clusters),
		"clusters_consolidated", consolidated,
	)
}

// collectDistinctKeywords fetches a sample of recent active atoms and extracts
// distinct keywords to use as cluster seeds. Capped at atomConsolidMaxKeywords.
func collectDistinctKeywords(ctx context.Context, deps atomConsolidDeps) ([]string, error) {
	// Use empty query (ILIKE '%%') to get a representative sample of recent
	// atoms. We then de-duplicate their keywords.
	sample, err := deps.store.Search(ctx, deps.workspaceID, "", atomConsolidSearchLimit*atomConsolidMaxKeywords)
	if err != nil {
		// Fallback: some stores may not support empty query — use a safe common term.
		sample, err = deps.store.Search(ctx, deps.workspaceID, "the", atomConsolidSearchLimit)
		if err != nil {
			return nil, fmt.Errorf("sampling atoms: %w", err)
		}
	}

	seen := make(map[string]bool)
	var keywords []string
	for _, a := range sample {
		for _, kw := range a.Keywords {
			if kw == "" || seen[kw] {
				continue
			}
			seen[kw] = true
			keywords = append(keywords, kw)
			if len(keywords) >= atomConsolidMaxKeywords {
				return keywords, nil
			}
		}
	}
	return keywords, nil
}

// buildAtomClusters searches for atoms matching each keyword and groups them
// into clusters. Only clusters with >atomConsolidMinClusterSize atoms qualify.
func buildAtomClusters(ctx context.Context, deps atomConsolidDeps, keywords []string) []atomCluster {
	// De-duplicate by atom ID across clusters to avoid processing the same
	// atom twice when it matches multiple keywords.
	usedAtomIDs := make(map[uuid.UUID]bool)
	var clusters []atomCluster

	for _, kw := range keywords {
		atoms, err := deps.store.Search(ctx, deps.workspaceID, kw, atomConsolidSearchLimit)
		if err != nil {
			slog.Warn("atom consolidation: search failed for keyword",
				"keyword", kw, "err", err)
			continue
		}

		// Filter to atoms not already consumed by a previous cluster and that
		// have not already been consolidated (digest_status != 'consolidated').
		var eligible []atom.Atom
		for _, a := range atoms {
			if usedAtomIDs[a.ID] {
				continue
			}
			if a.DigestStatus != nil && *a.DigestStatus == atom.DigestStatusConsolidated {
				continue
			}
			eligible = append(eligible, a)
		}

		if len(eligible) <= atomConsolidMinClusterSize-1 {
			// Need strictly >3 (i.e. at least 4) to qualify.
			continue
		}

		// Mark these atom IDs as consumed.
		for _, a := range eligible {
			usedAtomIDs[a.ID] = true
		}
		clusters = append(clusters, atomCluster{keyword: kw, atoms: eligible})
	}
	return clusters
}

// consolidateCluster calls Haiku to merge a cluster of atoms into one condensed
// atom, persists the result, and marks the originals as 'consolidated'.
func consolidateCluster(ctx context.Context, deps atomConsolidDeps, cl atomCluster) error {
	if deps.atomizer == nil {
		return fmt.Errorf("atomizer not configured")
	}

	// Build the consolidation prompt.
	prompt := buildConsolidationPrompt(cl)

	// Call Haiku with the consolidation system prompt (not atomizeSystemPrompt).
	result, err := deps.atomizer.Consolidate(ctx, prompt)
	if err != nil {
		return fmt.Errorf("haiku consolidation call: %w", err)
	}
	if result == nil {
		return fmt.Errorf("haiku returned nil consolidation result")
	}

	// Persist the condensed atom.
	// parent_table='consolidated_cluster' per M9 acceptance criterion 9.
	condensed, err := deps.store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: deps.workspaceID,
		ParentTable: "consolidated_cluster",
		ParentID:    uuid.Nil, // no single parent; represents the cluster
		Content:     ai.RedactCredentials(result.Content),
		Keywords:    result.Keywords,
		Tags:        result.Tags,
	})
	if err != nil {
		return fmt.Errorf("persisting condensed atom: %w", err)
	}
	if err := deps.store.SetDigestStatus(ctx, condensed.ID, atom.DigestStatusDone, ""); err != nil {
		slog.Warn("atom consolidation: setting condensed atom digest status failed",
			"atom_id", condensed.ID, "err", err)
	}

	// Mark all original cluster atoms as 'consolidated' (criterion 10).
	// atom IDs are already workspace-scoped via Search (criterion 10 safety note).
	for _, a := range cl.atoms {
		if err := deps.store.SetDigestStatus(ctx, a.ID, atom.DigestStatusConsolidated, ""); err != nil {
			slog.Warn("atom consolidation: marking original atom consolidated failed",
				"atom_id", a.ID, "err", err)
		}
	}

	slog.Info("atom consolidation: cluster consolidated",
		"keyword", cl.keyword,
		"original_count", len(cl.atoms),
		"condensed_atom_id", condensed.ID,
	)
	return nil
}

// buildConsolidationPrompt renders cluster atoms into a prompt for the Haiku
// consolidation call. Content, keywords, and tags are credential-scrubbed before
// sending (backend-security-design.md §3.1).
func buildConsolidationPrompt(cl atomCluster) string {
	summary := ""
	for _, a := range cl.atoms {
		// Scrub credentials from atom content, keywords, and tags before sending to LLM.
		safe := redact.ForLLM(a.Content)
		redactedKeywords := make([]string, len(a.Keywords))
		for i, kw := range a.Keywords {
			redactedKeywords[i] = redact.ForLLM(kw)
		}
		redactedTags := make([]string, len(a.Tags))
		for i, tag := range a.Tags {
			redactedTags[i] = redact.ForLLM(tag)
		}
		summary += fmt.Sprintf("- %s (keywords: %v, tags: %v)\n", safe, redactedKeywords, redactedTags)
	}
	return fmt.Sprintf(
		"These %d memory atoms all relate to the keyword '%s'. "+
			"Merge into one concise summary atom.\n\n"+
			"[BEGIN ATOMS]\n%s[END ATOMS]",
		len(cl.atoms), cl.keyword, summary,
	)
}
