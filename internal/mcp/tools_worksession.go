package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// validWorkSessionSources is the allowlist for the source field.
// "other" is included as a fallback for callers that cannot determine the exact trigger.
var validWorkSessionSources = map[string]bool{
	"manual":       true,
	"confirm_plan": true,
	"hook":         true,
	"other":        true,
}

// maxWorkSessionListLimit mirrors outcome's maxOutcomeLimit — the hard cap
// applied by list_recent_work_sessions regardless of what the caller requests.
// worksession.StoreIface.ListRecent enforces the same cap independently
// (defence in depth), so this constant only governs the MCP-layer default.
const maxWorkSessionListLimit = worksession.MaxListRecentLimit

// maxVerificationCommandLen / maxVerificationOutputExcerptLen / maxBranchNameLen
// are server-side length guards for finish_work / start_work. mcp.MaxLength()
// on the tool schema is client-side advisory only and is not enforced by the
// mcp-go server runtime (see existing title/goal/summary guards below).
const (
	maxVerificationCommandLen       = 500
	maxVerificationOutputExcerptLen = 2000
	maxBranchNameLen                = 200
)

// maxEvidenceItems caps the number of items accepted per finish_work's
// evidence array (backend-security-design.md §2.1 — resource guard against
// an LLM-emitted unbounded array). Mirrors maxRelatedRuleIDs
// (internal/mcp/tools_outcome.go) in spirit; evidence rows carry heavier text
// payloads (up to maxVerificationOutputExcerptLen each) so the cap is smaller
// than task_ids/completed_task_ids' 50.
const maxEvidenceItems = 20

// maxEvidenceArtifactLen is the server-side length guard for each evidence
// item's artifact field (PR URL or similar reference) — matches
// maxVerificationCommandLen in spirit (short single-line reference).
const maxEvidenceArtifactLen = 500

// maxEvidenceJSONLen is the outer mcp.MaxLength() hint for the evidence JSON
// array string (client-side advisory only, matching the existing pattern —
// real enforcement is parseFinishWorkEvidence's per-item + array-length
// checks below). Sized for maxEvidenceItems items each carrying up to
// maxVerificationOutputExcerptLen + maxVerificationCommandLen +
// maxEvidenceArtifactLen chars plus JSON overhead.
const maxEvidenceJSONLen = 65536

func (s *Server) registerWorkSessionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"start_work",
		mcp.WithDescription(
			"Start a new work session for a repository. "+
				"Call this when beginning focused work on a repo that doesn't already have an active session. "+
				"Links the supplied task_ids as primary tasks and sets current_task_id to the first one.",
		),
		mcp.WithString("repo_name", mcp.Description("Repository name (required)"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Short title for this work session (required)"), mcp.Required(), mcp.MaxLength(200)),
		mcp.WithString("goal", mcp.Description("One-paragraph goal for this session (required)"), mcp.Required(), mcp.MaxLength(2000)),
		mcp.WithString("task_ids",
			mcp.Description(`JSON array of task UUIDs to link as primary (e.g. ["uuid1","uuid2"]) — max 50`),
			mcp.MaxLength(8192)),
		mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
		mcp.WithString("source", mcp.Description("Source trigger: 'manual', 'confirm_plan', 'hook', or 'other'. Defaults to 'manual'.")),
		mcp.WithString("branch_name", mcp.Description("Git branch this session is working on (optional)"), mcp.MaxLength(maxBranchNameLen)),
	), s.handleStartWork)

	ms.AddTool(mcp.NewTool(
		"get_active_work",
		mcp.WithDescription(
			"Get the current in_progress work session for a repository. "+
				"Returns {active:false} when no session exists. "+
				"Check implementation_allowed before editing code.",
		),
		mcp.WithString("repo_name", mcp.Description("Repository name (required)"), mcp.Required()),
	), s.handleGetActiveWork)

	ms.AddTool(mcp.NewTool(
		"checkpoint_work",
		mcp.WithDescription(
			"Save progress on the current work session without ending it. "+
				"Sets status=checkpointed and records last_checkpoint_at. "+
				"Use when taking a break or switching context temporarily.",
		),
		mcp.WithString("session_id", mcp.Description("Work session UUID (required)"), mcp.Required()),
		mcp.WithString("summary", mcp.Description("What was accomplished since last checkpoint (required)"), mcp.Required(), mcp.MaxLength(5000)),
		mcp.WithString("completed_task_ids", mcp.Description(`JSON array of task UUIDs completed in this segment`)),
		mcp.WithString("new_task_titles", mcp.Description(`JSON array of new task titles to add`)),
		mcp.WithString("new_decisions", mcp.Description(`JSON array of decision titles to log`)),
		mcp.WithString("blockers", mcp.Description(`JSON array of blocker descriptions`)),
		mcp.WithString("next_actions", mcp.Description(`JSON array of next-action descriptions`)),
	), s.handleCheckpointWork)

	ms.AddTool(mcp.NewTool(
		"finish_work",
		mcp.WithDescription(
			"Close the current work session as completed. "+
				"Sets status=completed, records completed_at and final_summary. "+
				"Always call this when work on a session is done, even if tasks remain. "+
				"Optionally attach an evidence chain (command output, PR/CI links, Railway "+
				"deploy logs, manual notes) via the evidence parameter — get_work_session_trace "+
				"reads these rows back to reconstruct what was actually verified.",
		),
		mcp.WithString("session_id", mcp.Description("Work session UUID (required)"), mcp.Required()),
		mcp.WithString("summary", mcp.Description("Final summary of what was accomplished (required)"), mcp.Required(), mcp.MaxLength(5000)),
		mcp.WithString("completed_task_ids", mcp.Description(`JSON array of task UUIDs completed`)),
		mcp.WithString("deferred_task_ids", mcp.Description(`JSON array of task UUIDs deferred to next session`)),
		mcp.WithString("artifact", mcp.Description("PR URL or artifact reference (optional)")),
		mcp.WithString("follow_up_tasks", mcp.Description(`JSON array of new follow-up task titles`)),
		mcp.WithString("new_decisions", mcp.Description(`JSON array of decision titles to log`)),
		mcp.WithString("verification_status",
			mcp.Description("How this session's work was verified: not_run | passed | failed | unknown")),
		mcp.WithString("verification_command",
			mcp.Description("Command used to verify the work (e.g. 'cd build && task check')"),
			mcp.MaxLength(maxVerificationCommandLen)),
		mcp.WithString("verification_output_excerpt",
			mcp.Description("Excerpt of the verification command's output (redacted + capped at 2000 chars server-side)"),
			mcp.MaxLength(maxVerificationOutputExcerptLen)),
		mcp.WithString("final_result",
			mcp.Description("Terminal result of this session: success | failure | partial | unknown | regressed")),
		mcp.WithString("evidence",
			mcp.Description(fmt.Sprintf(
				`JSON array of evidence items to attach (max %d), e.g. `+
					`[{"evidence_type":"command","status":"passed","command":"cd build && task check","output_excerpt":"0 issues"},`+
					`{"evidence_type":"pr","status":"passed","artifact":"https://github.com/org/repo/pull/1"}]. `+
					`Each item: evidence_type (required) one of command|pr|ci|railway|manual_note; `+
					`status (required) one of passed|failed|unknown; `+
					`command/artifact (optional, single-line, max %d/%d chars); `+
					`output_excerpt (optional, max %d chars, redacted + capped server-side).`,
				maxEvidenceItems, maxVerificationCommandLen, maxEvidenceArtifactLen, maxVerificationOutputExcerptLen,
			)),
			mcp.MaxLength(maxEvidenceJSONLen)),
	), s.handleFinishWork)

	ms.AddTool(mcp.NewTool(
		"list_recent_work_sessions",
		mcp.WithDescription(
			"List recent work sessions ordered by created_at DESC. "+
				"Optionally filter by repo_name. Returns lightweight summaries — "+
				"call get_work_session_trace for full evidence detail on one session.",
		),
		mcp.WithString("repo_name", mcp.Description("Optional: filter to this repository")),
		mcp.WithNumber("limit",
			mcp.Description(fmt.Sprintf("Max results (default 20, max %d)", maxWorkSessionListLimit))),
	), s.handleListRecentWorkSessions)

	ms.AddTool(mcp.NewTool(
		"get_work_session_trace",
		mcp.WithDescription(
			"Get a work session plus its full evidence chain (command output, PR/CI links, "+
				"manual notes recorded at finish_work). Use to reconstruct what was actually "+
				"verified for a session.",
		),
		mcp.WithString("session_id", mcp.Description("Work session UUID (required)"), mcp.Required()),
	), s.handleGetWorkSessionTrace)
}

// ---- handleStartWork ----

// parseOptionalUUID parses an optional UUID field from args. Returns (nil, nil)
// when the field is absent, and (nil, errResult) when the value is present but invalid.
func parseOptionalUUID(args map[string]any, field string) (*uuid.UUID, *mcp.CallToolResult) {
	raw := stringArg(args, field)
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s UUID: %v", field, err))
	}
	return &id, nil
}

// parseTaskIDsFromField parses a JSON UUID array from the named field (max 50
// elements). Returns (nil, nil) when the field is absent or empty.
func parseTaskIDsFromField(args map[string]any, field string) ([]uuid.UUID, *mcp.CallToolResult) {
	raw := stringArg(args, field)
	if raw == "" {
		return nil, nil
	}
	var rawIDs []string
	if err := json.Unmarshal([]byte(raw), &rawIDs); err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid %s JSON: %v", field, err))
	}
	if len(rawIDs) > 50 {
		return nil, mcp.NewToolResultError(fmt.Sprintf("%s exceeds limit: got %d, max 50", field, len(rawIDs)))
	}
	ids := make([]uuid.UUID, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		id, err := uuid.Parse(rawID)
		if err != nil {
			return nil, mcp.NewToolResultError(fmt.Sprintf("invalid UUID in %s %q: %v", field, rawID, err))
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// parseTaskIDs parses the task_ids JSON array (max 50 elements) from args.
// Returns (nil, errResult) on validation error.
func parseTaskIDs(args map[string]any) ([]uuid.UUID, *mcp.CallToolResult) {
	return parseTaskIDsFromField(args, "task_ids")
}

// parseOptionalBranchName extracts and length-validates the optional
// branch_name field from start_work's arguments. Returns (nil, nil) when the
// field is absent.
func parseOptionalBranchName(args map[string]any) (*string, *mcp.CallToolResult) {
	branchName := stringArg(args, "branch_name")
	if branchName == "" {
		return nil, nil
	}
	if len(branchName) > maxBranchNameLen {
		return nil, mcp.NewToolResultError(fmt.Sprintf("branch_name exceeds %d character limit", maxBranchNameLen))
	}
	return &branchName, nil
}

// assembleStartWorkContext calls the context assembler immediately after a
// session is created so the caller gets a compact brief without a second
// round-trip. Non-fatal: a failed/unconfigured assembler must never fail
// start_work — the session is already committed by the time this runs.
// context_pack_id stays NULL always: Phase 2 does not persist Packs yet
// (contextpack.Request.Persist is not wired here), so there is nothing to
// link the column to.
func (s *Server) assembleStartWorkContext(
	ctx context.Context, sessionID uuid.UUID, goal, repoName string, taskIDs []uuid.UUID,
) *contextpack.Pack {
	if s.contextAssembler == nil {
		return nil
	}
	var taskIDPtr *uuid.UUID
	if len(taskIDs) > 0 {
		taskIDPtr = &taskIDs[0]
	}
	pack, err := s.contextAssembler.Assemble(ctx, contextpack.Request{
		Objective: goal,
		RepoName:  repoName,
		TaskID:    taskIDPtr,
	})
	if err != nil {
		slog.Warn("start_work: assemble_context failed (non-fatal)", "session_id", sessionID, "err", err)
		return nil
	}
	return pack
}

func (s *Server) handleStartWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	repoName := stringArg(args, "repo_name")
	title := stringArg(args, "title")
	goal := stringArg(args, "goal")
	if repoName == "" || title == "" || goal == "" {
		return mcp.NewToolResultError("repo_name, title, and goal are required"), nil
	}

	// Server-side length guards: mcp.MaxLength() is client-side advisory only
	// and is not enforced by the mcp-go server runtime.
	if len(title) > 200 {
		return mcp.NewToolResultError("title exceeds 200 character limit"), nil
	}
	if len(goal) > 2000 {
		return mcp.NewToolResultError("goal exceeds 2000 character limit"), nil
	}

	source := stringArg(args, "source")
	if source == "" {
		source = "manual"
	}
	if !validWorkSessionSources[source] {
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid source %q: must be one of manual, confirm_plan, hook, other", source),
		), nil
	}

	projectID, errRes := parseOptionalUUID(args, "project_id")
	if errRes != nil {
		return errRes, nil
	}

	taskIDs, errRes := parseTaskIDs(args)
	if errRes != nil {
		return errRes, nil
	}

	branchNamePtr, errRes := parseOptionalBranchName(args)
	if errRes != nil {
		return errRes, nil
	}

	sess, err := s.workSession.Create(ctx, worksession.CreateParams{
		WorkspaceID: s.workspaceUUIDVal(),
		RepoName:    repoName,
		ProjectID:   projectID,
		Title:       title,
		Goal:        goal,
		Source:      source,
		TaskIDs:     taskIDs,
		BranchName:  branchNamePtr,
	})
	if err != nil {
		if errors.Is(err, worksession.ErrAlreadyActive) {
			return mcp.NewToolResultError(
				"another session is already in_progress for this repo — call finish_work or get_active_work first",
			), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("start_work failed: %v", err)), nil
	}

	slog.Info("start_work", "session_id", sess.ID, "workspace_id", s.workspaceUUIDVal(), "repo_name", repoName)

	pack := s.assembleStartWorkContext(ctx, sess.ID, goal, repoName, taskIDs)

	return jsonText(map[string]any{
		"session_id":   sess.ID,
		"status":       sess.Status,
		"title":        sess.Title,
		"goal":         sess.Goal,
		"repo_name":    sess.RepoName,
		"started_at":   sess.StartedAt,
		"linked_tasks": len(taskIDs),
		"context_pack": pack,
	})
}

// ---- handleGetActiveWork ----

func (s *Server) handleGetActiveWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()
	repoName := stringArg(args, "repo_name")
	if repoName == "" {
		return mcp.NewToolResultError("repo_name is required"), nil
	}

	wsID := s.workspaceUUIDVal()
	result, err := s.workSession.GetActive(ctx, wsID, repoName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get_active_work failed: %v", err)), nil
	}
	return jsonText(result)
}

// ---- handleCheckpointWork ----

func (s *Server) handleCheckpointWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	rawSessID := stringArg(args, "session_id")
	summary := stringArg(args, "summary")
	if rawSessID == "" || summary == "" {
		return mcp.NewToolResultError("session_id and summary are required"), nil
	}

	// Server-side length guard: mcp.MaxLength() is client-side advisory only.
	if len(summary) > 5000 {
		return mcp.NewToolResultError("summary exceeds 5000 character limit"), nil
	}

	sessID, err := uuid.Parse(rawSessID)
	if err != nil {
		return mcp.NewToolResultError("invalid session_id UUID"), nil
	}

	slog.Info("checkpoint_work", "session_id", sessID, "workspace_id", s.workspaceUUIDVal())
	sess, err := s.workSession.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: sessID,
		Summary:   summary,
	})
	if err != nil {
		if errors.Is(err, worksession.ErrNotFound) {
			return mcp.NewToolResultError("session not found or not in checkpointable state"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("checkpoint_work failed: %v", err)), nil
	}

	return jsonText(map[string]any{
		"session_id":    sess.ID,
		"status":        sess.Status,
		"checkpoint_at": sess.LastCheckpointAt,
	})
}

// ---- handleFinishWork ----

// finishWorkEvidenceParams holds the optional P2.3 evidence-chain fields
// parsed and validated from finish_work's arguments.
type finishWorkEvidenceParams struct {
	verificationStatus        *string
	verificationCommand       *string
	verificationOutputExcerpt *string
	finalResult               *string
}

// parseFinishWorkEvidenceParams validates the optional evidence-chain fields
// on finish_work. Enum values are checked against worksession's allowed sets
// so invalid input returns a tool error instead of a raw DB CHECK-constraint
// violation (backend-security-design.md §5.2 — never delegate validation
// solely to the DB constraint). Length limits are enforced here because
// mcp.MaxLength() on the tool schema is client-side advisory only.
func parseFinishWorkEvidenceParams(args map[string]any) (finishWorkEvidenceParams, *mcp.CallToolResult) {
	var p finishWorkEvidenceParams

	if v := stringArg(args, "verification_status"); v != "" {
		if !worksession.AllowedVerificationStatuses[v] {
			return p, mcp.NewToolResultError(fmt.Sprintf(
				"invalid verification_status %q: must be one of not_run, passed, failed, unknown", v,
			))
		}
		p.verificationStatus = &v
	}
	if v := stringArg(args, "verification_command"); v != "" {
		if len(v) > maxVerificationCommandLen {
			return p, mcp.NewToolResultError(
				fmt.Sprintf("verification_command exceeds %d character limit", maxVerificationCommandLen),
			)
		}
		p.verificationCommand = &v
	}
	if v := stringArg(args, "verification_output_excerpt"); v != "" {
		if len(v) > maxVerificationOutputExcerptLen {
			return p, mcp.NewToolResultError(
				fmt.Sprintf("verification_output_excerpt exceeds %d character limit", maxVerificationOutputExcerptLen),
			)
		}
		p.verificationOutputExcerpt = &v
	}
	if v := stringArg(args, "final_result"); v != "" {
		if !worksession.AllowedFinalResults[v] {
			return p, mcp.NewToolResultError(fmt.Sprintf(
				"invalid final_result %q: must be one of success, failure, partial, unknown, regressed", v,
			))
		}
		p.finalResult = &v
	}
	return p, nil
}

// finishWorkEvidenceItem is the wire shape of one element of finish_work's
// evidence JSON array.
type finishWorkEvidenceItem struct {
	EvidenceType  string  `json:"evidence_type"`
	Status        string  `json:"status"`
	Command       *string `json:"command,omitempty"`
	Artifact      *string `json:"artifact,omitempty"`
	OutputExcerpt *string `json:"output_excerpt,omitempty"`
}

// parseFinishWorkEvidence parses and validates the optional evidence JSON
// array on finish_work (wbt-2.0 P2 review F1 — the schema previously had no
// input path for evidence rows even though the store layer supported them
// end to end). Each item's evidence_type/status are checked against
// worksession's allowed sets, mirroring the DB CHECK constraints
// (backend-security-design.md §5.2 — never delegate validation solely to the
// DB constraint: a raw `pq: violates check constraint` is unhelpful and,
// since AddEvidence's insertion inside Finish is intentionally best-effort/
// non-fatal, a constraint violation there would silently drop the evidence
// row instead of surfacing an error to the caller).
//
// command/artifact are single-line reference fields validated with
// worksession.CheckControlChars (backend-security-design.md §2.1 — LLM tool
// input is adversarial; a prompt-injected agent must not be able to smuggle
// a second shell instruction via an embedded newline) and length-capped.
// output_excerpt is length-capped here too (defence in depth) but is
// intentionally NOT CheckControlChars'd — it holds multi-line command output
// and is unconditionally redacted + capped again in the store layer
// (worksession.RedactAndCapOutputExcerpt), matching how
// verification_output_excerpt is already treated a few lines above.
func parseFinishWorkEvidence(args map[string]any) ([]worksession.EvidenceInput, *mcp.CallToolResult) {
	raw := stringArg(args, "evidence")
	if raw == "" {
		return nil, nil
	}
	var items []finishWorkEvidenceItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("invalid evidence JSON: %v", err))
	}
	if len(items) > maxEvidenceItems {
		return nil, mcp.NewToolResultError(
			fmt.Sprintf("evidence exceeds limit: got %d, max %d", len(items), maxEvidenceItems),
		)
	}

	out := make([]worksession.EvidenceInput, 0, len(items))
	for i, item := range items {
		if !worksession.AllowedEvidenceTypes[item.EvidenceType] {
			return nil, mcp.NewToolResultError(fmt.Sprintf(
				"evidence[%d]: invalid evidence_type %q: must be one of command, pr, ci, railway, manual_note", i, item.EvidenceType,
			))
		}
		if !worksession.AllowedEvidenceStatuses[item.Status] {
			return nil, mcp.NewToolResultError(fmt.Sprintf(
				"evidence[%d]: invalid status %q: must be one of passed, failed, unknown", i, item.Status,
			))
		}
		if item.Command != nil {
			if len(*item.Command) > maxVerificationCommandLen {
				return nil, mcp.NewToolResultError(fmt.Sprintf(
					"evidence[%d]: command exceeds %d character limit", i, maxVerificationCommandLen,
				))
			}
			if reason := worksession.CheckControlChars("command", *item.Command); reason != "" {
				return nil, mcp.NewToolResultError(fmt.Sprintf("evidence[%d]: %s", i, reason))
			}
		}
		if item.Artifact != nil {
			if len(*item.Artifact) > maxEvidenceArtifactLen {
				return nil, mcp.NewToolResultError(fmt.Sprintf(
					"evidence[%d]: artifact exceeds %d character limit", i, maxEvidenceArtifactLen,
				))
			}
			if reason := worksession.CheckControlChars("artifact", *item.Artifact); reason != "" {
				return nil, mcp.NewToolResultError(fmt.Sprintf("evidence[%d]: %s", i, reason))
			}
		}
		if item.OutputExcerpt != nil && len(*item.OutputExcerpt) > maxVerificationOutputExcerptLen {
			return nil, mcp.NewToolResultError(fmt.Sprintf(
				"evidence[%d]: output_excerpt exceeds %d character limit", i, maxVerificationOutputExcerptLen,
			))
		}
		out = append(out, worksession.EvidenceInput{
			EvidenceType:  item.EvidenceType,
			Status:        item.Status,
			Command:       item.Command,
			Artifact:      item.Artifact,
			OutputExcerpt: item.OutputExcerpt,
		})
	}
	return out, nil
}

// resolveAutoOutcomeTaskID picks the task ID to attach an auto-created
// outcome to when finish_work reports failure/partial/regressed. Priority:
// first completed task, then first deferred task, then the session's
// current_task_id. Returns (uuid.Nil, false) when none is available.
func resolveAutoOutcomeTaskID(completedTaskIDs, deferredTaskIDs []uuid.UUID, sess *worksession.Session) (uuid.UUID, bool) {
	if len(completedTaskIDs) > 0 {
		return completedTaskIDs[0], true
	}
	if len(deferredTaskIDs) > 0 {
		return deferredTaskIDs[0], true
	}
	if sess.CurrentTaskID != nil {
		return *sess.CurrentTaskID, true
	}
	return uuid.Nil, false
}

// negativeFinalResults is the subset of worksession.AllowedFinalResults that
// triggers auto-outcome creation in autoCreateOutcomeOnFailure.
var negativeFinalResults = map[string]bool{
	"failure":   true,
	"partial":   true,
	"regressed": true,
}

// autoCreateOutcomeOnFailure best-effort creates an outcome row when
// finalResult indicates failure/partial/regressed and an outcome store is
// configured, then links it back onto the session via SetOutcomeLink. Both
// steps are non-fatal: a failure here must never fail finish_work, since the
// session UPDATE has already committed by the time this runs.
//
// Returns the created outcome's ID, or nil when no outcome was created
// (nil finalResult, nil outcome store, non-matching final_result, or no task
// ID available on the session).
func (s *Server) autoCreateOutcomeOnFailure(
	ctx context.Context, sessID uuid.UUID, sess *worksession.Session,
	completedTaskIDs, deferredTaskIDs []uuid.UUID, finalResult *string, summary string,
) *uuid.UUID {
	if finalResult == nil || s.outcome == nil || !negativeFinalResults[*finalResult] {
		return nil
	}

	taskID, ok := resolveAutoOutcomeTaskID(completedTaskIDs, deferredTaskIDs, sess)
	if !ok {
		slog.Warn("finish_work: no task ID available for auto-outcome creation, skipping",
			"session_id", sessID, "final_result", *finalResult)
		return nil
	}

	o, err := s.outcome.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: s.workspaceUUID(),
		EntityType:  "task",
		EntityID:    taskID,
		Result:      *finalResult,
		Notes:       sanitize.Notes(summary),
	})
	if err != nil {
		slog.Warn("finish_work: auto-outcome creation failed (non-fatal)", "session_id", sessID, "err", err)
		return nil
	}

	if linkErr := s.workSession.SetOutcomeLink(ctx, sessID, o.ID); linkErr != nil {
		slog.Warn("finish_work: SetOutcomeLink failed (non-fatal)",
			"session_id", sessID, "outcome_id", o.ID, "err", linkErr)
	}
	return &o.ID
}

func (s *Server) handleFinishWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	rawSessID := stringArg(args, "session_id")
	summary := stringArg(args, "summary")
	if rawSessID == "" || summary == "" {
		return mcp.NewToolResultError("session_id and summary are required"), nil
	}

	// Server-side length guard: mcp.MaxLength() is client-side advisory only.
	if len(summary) > 5000 {
		return mcp.NewToolResultError("summary exceeds 5000 character limit"), nil
	}

	sessID, err := uuid.Parse(rawSessID)
	if err != nil {
		return mcp.NewToolResultError("invalid session_id UUID"), nil
	}

	completedTaskIDs, errRes := parseTaskIDsFromField(args, "completed_task_ids")
	if errRes != nil {
		return errRes, nil
	}
	deferredTaskIDs, errRes := parseTaskIDsFromField(args, "deferred_task_ids")
	if errRes != nil {
		return errRes, nil
	}

	evidenceParams, errRes := parseFinishWorkEvidenceParams(args)
	if errRes != nil {
		return errRes, nil
	}

	evidenceItems, errRes := parseFinishWorkEvidence(args)
	if errRes != nil {
		return errRes, nil
	}

	var artifact *string
	if raw := stringArg(args, "artifact"); raw != "" {
		artifact = &raw
	}

	// Log new_decisions before finishing the session (best-effort, non-fatal).
	s.logFinishWorkDecisions(ctx, sessID, stringArg(args, "new_decisions"), stringArg(args, "repo_name"))

	slog.Info("finish_work", "session_id", sessID, "workspace_id", s.workspaceUUIDVal())
	sess, err := s.workSession.Finish(ctx, worksession.FinishParams{
		SessionID:                 sessID,
		Summary:                   summary,
		CompletedTaskIDs:          completedTaskIDs,
		DeferredTaskIDs:           deferredTaskIDs,
		Artifact:                  artifact,
		VerificationStatus:        evidenceParams.verificationStatus,
		VerificationCommand:       evidenceParams.verificationCommand,
		VerificationOutputExcerpt: evidenceParams.verificationOutputExcerpt,
		FinalResult:               evidenceParams.finalResult,
		Evidence:                  evidenceItems,
	})
	if err != nil {
		if errors.Is(err, worksession.ErrNotFound) {
			return mcp.NewToolResultError("session not found or already completed/cancelled"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("finish_work failed: %v", err)), nil
	}

	outcomeID := s.autoCreateOutcomeOnFailure(
		ctx, sessID, sess, completedTaskIDs, deferredTaskIDs, evidenceParams.finalResult, summary,
	)

	return jsonText(map[string]any{
		"session_id":   sess.ID,
		"status":       sess.Status,
		"completed_at": sess.CompletedAt,
		"final_report": sess.FinalSummary,
		"outcome_id":   outcomeID,
	})
}

// ---- handleListRecentWorkSessions ----

// workSessionSummary is the list_recent_work_sessions response shape.
// verification_output_excerpt is intentionally excluded — it is a
// potentially large/sensitive field only exposed via get_work_session_trace.
type workSessionSummary struct {
	ID                 uuid.UUID `json:"id"`
	Title              string    `json:"title"`
	Goal               string    `json:"goal"`
	Status             string    `json:"status"`
	FinalResult        *string   `json:"final_result,omitempty"`
	VerificationStatus *string   `json:"verification_status,omitempty"`
	CreatedAt          string    `json:"created_at"`
	CompletedAt        *string   `json:"completed_at,omitempty"`
}

func (s *Server) handleListRecentWorkSessions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	repoName := stringArg(args, "repo_name")
	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 20
	}
	if limit > maxWorkSessionListLimit {
		limit = maxWorkSessionListLimit
	}

	sessions, err := s.workSession.ListRecent(ctx, s.workspaceUUIDVal(), repoName, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("list_recent_work_sessions failed: %v", err)), nil
	}

	summaries := make([]workSessionSummary, 0, len(sessions))
	for _, sess := range sessions {
		summaries = append(summaries, workSessionSummary{
			ID:                 sess.ID,
			Title:              sess.Title,
			Goal:               sess.Goal,
			Status:             sess.Status,
			FinalResult:        sess.FinalResult,
			VerificationStatus: sess.VerificationStatus,
			CreatedAt:          sess.CreatedAt,
			CompletedAt:        sess.CompletedAt,
		})
	}
	return jsonText(summaries)
}

// ---- handleGetWorkSessionTrace ----

// workSessionTrace is the get_work_session_trace response shape. Unlike
// workSessionSummary, output_excerpt IS included on each evidence row here —
// it is already capped/redacted at write time (worksession.AddEvidence /
// Finish), so exposing it via this dedicated trace tool is safe.
type workSessionTrace struct {
	Session  *worksession.Session   `json:"session"`
	Evidence []worksession.Evidence `json:"evidence"`
}

func (s *Server) handleGetWorkSessionTrace(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	sessID, errResult := requireUUIDArg(args, "session_id", "invalid session_id UUID")
	if errResult != nil {
		return errResult, nil
	}

	sess, err := s.workSession.GetByID(ctx, s.workspaceUUIDVal(), sessID)
	if err != nil {
		if errors.Is(err, worksession.ErrNotFound) {
			return mcp.NewToolResultError(fmt.Sprintf("session %s not found", sessID)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("get_work_session_trace failed: %v", err)), nil
	}

	evidence, err := s.workSession.GetEvidence(ctx, sessID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get_work_session_trace failed: %v", err)), nil
	}
	if evidence == nil {
		evidence = []worksession.Evidence{}
	}

	return jsonText(workSessionTrace{Session: sess, Evidence: evidence})
}

// workspaceUUIDVal returns the workspace UUID, or uuid.Nil if not configured.
func (s *Server) workspaceUUIDVal() uuid.UUID {
	if s.workspaceID == nil {
		return uuid.Nil
	}
	return *s.workspaceID
}

// logFinishWorkDecisions parses a JSON array of decision title strings from
// rawDecisions and logs each one via s.decision.Log. All errors are logged as
// Warn so the finish_work call is never aborted by a failed decision write.
func (s *Server) logFinishWorkDecisions(ctx context.Context, sessID uuid.UUID, rawDecisions, repoName string) {
	if rawDecisions == "" {
		return
	}
	var titles []string
	if err := json.Unmarshal([]byte(rawDecisions), &titles); err != nil {
		slog.Warn("finish_work: invalid new_decisions JSON, skipping", "session_id", sessID, "err", err)
		return
	}
	const maxFinishWorkDecisions = 50
	if len(titles) > maxFinishWorkDecisions {
		slog.Warn(
			"finish_work: new_decisions exceeds cap, truncating",
			"session_id", sessID,
			"count", len(titles),
			"cap", maxFinishWorkDecisions,
		)
		titles = titles[:maxFinishWorkDecisions]
	}
	for _, title := range titles {
		if title == "" {
			continue
		}
		if reason := checkField("title", title); reason != "" {
			slog.Warn(
				"finish_work: skipping noisy decision title",
				"session_id", sessID,
				"reason", reason,
			)
			continue
		}
		if reason := checkCommandField("title", title); reason != "" {
			slog.Warn(
				"finish_work: skipping decision title with control chars",
				"session_id", sessID,
				"reason", reason,
			)
			continue
		}
		if _, logErr := s.decision.Log(ctx, decision.LogParams{
			Title:    title,
			RepoName: repoName,
		}); logErr != nil {
			logTitle := title
			if runes := []rune(logTitle); len(runes) > 80 {
				logTitle = string(runes[:80]) + "..."
			}
			slog.Warn(
				"finish_work: failed to log decision",
				"session_id", sessID,
				"title", logTitle,
				"err", logErr,
			)
		}
	}
}
