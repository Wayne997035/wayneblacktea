package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// consolidationJobTimeout caps the full consolidation cron run.
const consolidationJobTimeout = 4 * time.Minute

// consolidationLookback is the trailing window inspected for clustering.
const consolidationLookback = 30 * 24 * time.Hour

// consolidationMaxActivity caps activity rows fetched per run.
const consolidationMaxActivity = 500

// consolidationMinClusterSize is the minimum number of activities in a
// (repo_name, project_id) cluster to warrant a consolidation proposal.
const consolidationMinClusterSize = 5

// consolidationDeps bundles dependencies for the consolidation cron job.
type consolidationDeps struct {
	gtd       gtd.StoreIface
	proposal  proposal.StoreIface
	reflector ai.ReflectorIface
}

// activityCluster groups activity log rows by a (repo+project) key derived
// from the actor field. We treat distinct actor prefixes as a lightweight
// proxy for the originating project context.
type activityCluster struct {
	key        string
	activities []db.ActivityLog
}

// runConsolidation is the core logic of the Saturday-night consolidation job.
//
// It:
//  1. Fetches the last 30 days of activity_log rows.
//  2. Clusters them by actor prefix (proxy for repo/project context).
//  3. For each cluster with ≥ 5 rows, asks Haiku to merge them into one
//     consolidated knowledge entry.
//  4. Writes each result as a pending_proposals row type='knowledge'.
//
// All errors are logged at warn level; the function never panics.
func runConsolidation(deps consolidationDeps) {
	ctx, cancel := context.WithTimeout(context.Background(), consolidationJobTimeout)
	defer cancel()

	since := time.Now().Add(-consolidationLookback)

	activities, err := deps.gtd.ListActivityLogsSince(ctx, since, consolidationMaxActivity)
	if err != nil {
		slog.Warn("consolidation: listing activity logs failed", "err", err)
		return
	}

	if len(activities) == 0 {
		slog.Info("consolidation: no activity in the past 30 days, skipping")
		return
	}

	clusters := clusterActivities(activities)
	if len(clusters) == 0 {
		slog.Info("consolidation: no clusters met the minimum size threshold")
		return
	}

	total := 0
	for _, cl := range clusters {
		summary := buildConsolidationSummary(cl)
		prompt := fmt.Sprintf(
			"These %d activities are all associated with context '%s'. "+
				"Merge into 1 knowledge entry capturing the consolidated takeaway. "+
				"Output a JSON array with exactly 1 object: {\"title\": ..., \"content\": ..., \"tags\": [...]}.\n\n%s",
			len(cl.activities), cl.key, summary,
		)

		proposals, perr := deps.reflector.Propose(ctx, prompt)
		if perr != nil {
			slog.Warn("consolidation: AI proposal failed", "cluster", cl.key, "err", perr)
			continue
		}

		for _, kp := range proposals {
			if kp.Title == "" || kp.Content == "" {
				continue
			}
			payload, merr := marshalKnowledgePayload(kp)
			if merr != nil {
				slog.Warn("consolidation: marshaling payload failed", "cluster", cl.key, "err", merr)
				continue
			}
			if _, cerr := deps.proposal.Create(ctx, proposal.CreateParams{
				Type:       proposal.TypeKnowledge,
				Payload:    payload,
				ProposedBy: "consolidation-cron",
			}); cerr != nil {
				slog.Warn("consolidation: creating pending proposal failed",
					"cluster", cl.key, "title", kp.Title, "err", cerr)
				continue
			}
			total++
		}
	}

	slog.Info("consolidation: cron completed",
		"activities_scanned", len(activities),
		"clusters_processed", len(clusters),
		"proposals_created", total,
	)
}

// clusterActivities groups activity_log rows by a context key derived from the
// actor field prefix (everything before the first '/') as a lightweight proxy
// for repo or project context. Only clusters with ≥ consolidationMinClusterSize
// rows are returned.
func clusterActivities(activities []db.ActivityLog) []activityCluster {
	byKey := make(map[string][]db.ActivityLog)
	for _, a := range activities {
		key := actorKey(a.Actor)
		byKey[key] = append(byKey[key], a)
	}

	var out []activityCluster
	for k, rows := range byKey {
		if len(rows) >= consolidationMinClusterSize {
			out = append(out, activityCluster{key: k, activities: rows})
		}
	}
	return out
}

// actorKey extracts the first segment of the actor string (split on '/')
// to use as the cluster key. Falls back to the full actor string.
func actorKey(actor string) string {
	if i := strings.IndexByte(actor, '/'); i > 0 {
		return actor[:i]
	}
	return actor
}

// buildConsolidationSummary renders an activity cluster into a compact summary
// for the Haiku prompt. Notes are excluded to reduce prompt-injection surface.
func buildConsolidationSummary(cl activityCluster) string {
	var sb strings.Builder
	for _, a := range cl.activities {
		ts := ""
		if a.CreatedAt.Valid {
			ts = a.CreatedAt.Time.Format("2006-01-02")
		}
		fmt.Fprintf(&sb, "- [%s] %s: %s\n", ts, a.Actor, a.Action)
	}
	return sb.String()
}
