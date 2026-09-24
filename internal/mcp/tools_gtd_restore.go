package mcp

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/mark3labs/mcp-go/mcp"
)

// handleRestoreProject implements restore_project [F191-07], the sole undo
// path for delete_project.
//
// Unlike delete_task/delete_project this is single-step: the tool only
// writes rows back from deletion_tombstones, it never deletes anything, so
// the two-step confirmation flow those tools share (deletion_confirm.go)
// does not apply — a restore has no destructive side effect for that flow
// to gate.
//
// actor is always s.auditSessionID(ctx), the same provenance contract
// handleDeleteTask/handleDeleteProject use below — NEVER a caller-supplied
// tool argument. LLM tool input is hostile, and actor is exactly the field
// a forged call would want to control, to make a restore look like it came
// from someone else. RestoreProjectArgs (tools_gtd_args.go) has no field
// that could carry one.
//
// Error translation: gtd.ErrNotFound and gtd.ErrConflict get caller-facing,
// non-echoing messages; every other error — including the contract-stage
// gtd.ErrNotImplemented stub (see RestoreProject's doc comment in
// internal/gtd/iface.go, cleared once the storage-layer PR in this same
// fan-out lands) — falls through to storeErrorResult, which is still an
// error result, never translated into success or into the not-found
// message above it.
func (s *Server) handleRestoreProject(ctx context.Context, args RestoreProjectArgs) (*mcp.CallToolResult, error) {
	id := args.ProjectID
	actor := s.auditSessionID(ctx)

	project, tasksRestored, err := s.gtd.RestoreProject(ctx, id, actor)
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return mcp.NewToolResultError("no deleted project with that id within the retention window"), nil
		}
		if errors.Is(err, gtd.ErrConflict) {
			return mcp.NewToolResultError("a project with that id or name already exists; nothing was restored"), nil
		}
		slog.Warn("restore_project: RestoreProject failed", "project_id", id, "err", err)
		return storeErrorResult("restoring project", err), nil
	}

	return jsonText(map[string]any{
		"restored_project_id": project.ID.String(),
		// clipSafe: project.Name is caller-authored stored text (U13) — same
		// cap and helper handleDeleteProject's confirmation_required preview
		// already uses for this field (tools_gtd.go).
		"name":           clipSafe(project.Name, gtdTitleMaxRunes),
		"tasks_restored": tasksRestored,
	})
}
