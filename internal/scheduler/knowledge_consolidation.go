package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// knowledgeConsolidationJobTimeout caps the full knowledge consolidation cron run.
const knowledgeConsolidationJobTimeout = 5 * time.Minute

// knowledgeConsolidationLookback is the trailing window of knowledge items inspected.
const knowledgeConsolidationLookback = 30 * 24 * time.Hour

// knowledgeConsolidationMaxItems caps the number of knowledge items fetched per run.
const knowledgeConsolidationMaxItems = 200

// knowledgeConsolidationMinClusterSize is the minimum cluster size (≥3 knowledge_items
// with ≥2 shared tags) before a consolidation proposal is generated.
const knowledgeConsolidationMinClusterSize = 3

// knowledgeConsolidationMinSharedTags is the minimum number of shared tags between
// items in a cluster for it to be considered related.
const knowledgeConsolidationMinSharedTags = 2

// knowledgeConsolidationDeps bundles dependencies for the knowledge consolidation cron job.
type knowledgeConsolidationDeps struct {
	knowledge knowledge.StoreIface
	proposal  proposal.StoreIface
	reflector ai.ReflectorIface
}

// knowledgeCluster groups knowledge_items by shared tags.
type knowledgeCluster struct {
	sharedTags []string
	items      []db.KnowledgeItem
}

// runKnowledgeConsolidation is the core logic of the Sunday knowledge consolidation job.
//
// It:
//  1. Fetches recent knowledge_items (last 30 days, limit 200).
//  2. Groups items with ≥2 shared tags into clusters of ≥3 items.
//  3. For each cluster, calls reflector.Propose to generate a consolidated knowledge proposal.
//  4. Writes each result as a pending_proposals row of type='knowledge'.
//
// All errors are logged at warn level; the function never panics.
func runKnowledgeConsolidation(deps knowledgeConsolidationDeps) {
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeConsolidationJobTimeout)
	defer cancel()

	items, err := deps.knowledge.List(ctx, knowledgeConsolidationMaxItems, 0)
	if err != nil {
		slog.Warn("knowledge consolidation: listing knowledge items failed", "err", err)
		return
	}

	// Filter to items created within the lookback window.
	cutoff := time.Now().Add(-knowledgeConsolidationLookback)
	var recent []db.KnowledgeItem
	for _, item := range items {
		if item.CreatedAt.Valid && item.CreatedAt.Time.After(cutoff) {
			recent = append(recent, item)
		}
	}

	if len(recent) == 0 {
		slog.Info("knowledge consolidation: no recent knowledge items, skipping")
		return
	}

	clusters := clusterKnowledgeByTags(recent)
	if len(clusters) == 0 {
		slog.Info("knowledge consolidation: no clusters met the minimum size and shared-tag threshold")
		return
	}

	total := 0
	for _, cl := range clusters {
		prompt := buildKnowledgeClusterPrompt(cl)

		proposals, perr := deps.reflector.Propose(ctx, prompt)
		if perr != nil {
			slog.Warn("knowledge consolidation: AI proposal failed",
				"tags", cl.sharedTags, "cluster_size", len(cl.items), "err", perr)
			continue
		}

		for _, kp := range proposals {
			if kp.Title == "" || kp.Content == "" {
				continue
			}
			payload, merr := marshalKnowledgePayload(kp)
			if merr != nil {
				slog.Warn("knowledge consolidation: marshaling payload failed",
					"tags", cl.sharedTags, "err", merr)
				continue
			}
			if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
				Type:       proposal.TypeKnowledge,
				Payload:    payload,
				ProposedBy: "knowledge-consolidation-cron",
			}); cerr != nil {
				slog.Warn("knowledge consolidation: creating pending proposal failed",
					"tags", cl.sharedTags, "title", kp.Title, "err", cerr)
				continue
			}
			total++
		}
	}

	slog.Info("knowledge consolidation: cron completed",
		"items_scanned", len(recent),
		"clusters_processed", len(clusters),
		"proposals_created", total,
	)
}

// clusterKnowledgeByTags groups knowledge items by pairs of shared tags.
// Returns only clusters with ≥ knowledgeConsolidationMinClusterSize items and
// ≥ knowledgeConsolidationMinSharedTags shared tags between all items in the cluster.
//
// Strategy: build a tag → items index, then for each tag pair find items
// sharing both tags. Clusters are de-duplicated by canonical item-set key
// so the same group doesn't generate multiple proposals.
func clusterKnowledgeByTags(items []db.KnowledgeItem) []knowledgeCluster {
	// Build inverted index: tag → item indices
	byTag := make(map[string][]int)
	for i, item := range items {
		for _, tag := range item.Tags {
			byTag[tag] = append(byTag[tag], i)
		}
	}

	// For each item, count how many shared tags it has with each other item.
	// sharedCount[i][j] = number of tags shared between items[i] and items[j].
	sharedCount := make(map[int]map[int]int)
	for _, idxList := range byTag {
		for a := 0; a < len(idxList); a++ {
			for b := a + 1; b < len(idxList); b++ {
				i, j := idxList[a], idxList[b]
				if sharedCount[i] == nil {
					sharedCount[i] = make(map[int]int)
				}
				if sharedCount[j] == nil {
					sharedCount[j] = make(map[int]int)
				}
				sharedCount[i][j]++
				sharedCount[j][i]++
			}
		}
	}

	// Find the dominant shared-tag pair for each item (the tag pair with most support).
	// Use a simple greedy approach: seed clusters from items that share ≥2 tags with many others.
	// To avoid O(n^3) complexity at personal scale (≤200 items) this is fine.
	seenCluster := make(map[string]bool) // canonical key to dedup clusters
	var clusters []knowledgeCluster

	for seed := 0; seed < len(items); seed++ {
		// Find all items sharing ≥ knowledgeConsolidationMinSharedTags tags with the seed.
		var clusterIdxs []int
		clusterIdxs = append(clusterIdxs, seed)
		for other, count := range sharedCount[seed] {
			if count >= knowledgeConsolidationMinSharedTags {
				clusterIdxs = append(clusterIdxs, other)
			}
		}
		if len(clusterIdxs) < knowledgeConsolidationMinClusterSize {
			continue
		}

		// Compute canonical key: sorted item IDs joined.
		key := canonicalClusterKey(clusterIdxs, items)
		if seenCluster[key] {
			continue
		}
		seenCluster[key] = true

		// Compute the actual shared tags across all items in the cluster.
		sharedTags := sharedTagsForCluster(clusterIdxs, items)
		if len(sharedTags) < knowledgeConsolidationMinSharedTags {
			continue
		}

		var clusterItems []db.KnowledgeItem
		for _, idx := range clusterIdxs {
			clusterItems = append(clusterItems, items[idx])
		}
		clusters = append(clusters, knowledgeCluster{
			sharedTags: sharedTags,
			items:      clusterItems,
		})
	}
	return clusters
}

// canonicalClusterKey builds a deterministic string key for a cluster to
// enable deduplication. IDs are sorted so the same set of items produces the
// same key regardless of the seed item that initiated the cluster.
func canonicalClusterKey(idxs []int, items []db.KnowledgeItem) string {
	ids := make([]string, 0, len(idxs))
	for _, i := range idxs {
		ids = append(ids, items[i].ID.String())
	}
	sort.Strings(ids)
	b, _ := json.Marshal(ids)
	return string(b)
}

// sharedTagsForCluster returns the intersection of tags across all items in the cluster.
func sharedTagsForCluster(idxs []int, items []db.KnowledgeItem) []string {
	if len(idxs) == 0 {
		return nil
	}
	// Start with seed item's tags.
	counts := make(map[string]int)
	for _, idx := range idxs {
		seen := make(map[string]bool)
		for _, tag := range items[idx].Tags {
			if !seen[tag] {
				seen[tag] = true
				counts[tag]++
			}
		}
	}
	// Tags present in ALL items.
	var shared []string
	for tag, cnt := range counts {
		if cnt == len(idxs) {
			shared = append(shared, tag)
		}
	}
	return shared
}

// buildKnowledgeClusterPrompt renders a knowledge cluster into a prompt for Haiku.
func buildKnowledgeClusterPrompt(cl knowledgeCluster) string {
	return fmt.Sprintf(
		"These %d knowledge items all share the tags %v. "+
			"Merge into 1 consolidated knowledge entry capturing the key takeaway. "+
			"Output a JSON array with exactly 1 object: {\"title\": ..., \"content\": ..., \"tags\": [...]}.\n\n"+
			"[BEGIN ACTIVITIES]\n%s[END ACTIVITIES]",
		len(cl.items), cl.sharedTags,
		buildKnowledgeSummary(cl.items),
	)
}

// buildKnowledgeSummary renders items into a compact summary for the Haiku prompt.
// Full content is NOT included to reduce prompt-injection surface — title + tags only.
func buildKnowledgeSummary(items []db.KnowledgeItem) string {
	result := ""
	for _, item := range items {
		ts := ""
		if item.CreatedAt.Valid {
			ts = item.CreatedAt.Time.Format("2006-01-02")
		}
		result += fmt.Sprintf("- [%s] %s (tags: %v)\n", ts, item.Title, item.Tags)
	}
	return result
}
