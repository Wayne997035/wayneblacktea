package contextpack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
)

// searchLimit bounds the free-text/relevance queries fanned out to each
// domain store (knowledge, atom, procedural, skill, decision-by-*).
const searchLimit = 10

// failedOutcomeLimit and reflectionLimit bound the smaller retrospection
// sources; both surface only a handful of the most recent rows.
const (
	failedOutcomeLimit = 5
	reflectionLimit    = 5
	// reflectionLookback is the RecentWithPatterns look-back window, matching
	// the default used by the analyze_recent_patterns MCP tool.
	reflectionLookback = 7 * 24 * time.Hour
)

// recentWindow is the cutoff used to compute the Provenance["recent"] flag
// the scorer reads.
const recentWindow = 180 * 24 * time.Hour

// maxSummaryRunes caps Item.Summary before scoring/trimming ever sees it.
const maxSummaryRunes = 500

// includeTypesMap maps the assemble_context "include_types" API vocabulary
// (docs/wayneblacktea-2.0-development-prompt.md:260, tools_contextpack.go
// knownContextPackTypes) to the internal Item.Type values retrieve() emits.
// "semantic" and "episodic" are doc-level category names (Current Ground
// Truth section of the same doc) that fan out to more than one internal
// Type; "procedural"/"rules"/"skills"/"atoms"/"outcomes" map 1:1.
var includeTypesMap = map[string][]string{
	"semantic":   {TypeKnowledge, TypeDecision},
	"episodic":   {TypeSession, TypeReflection},
	"procedural": {TypeProcedure},
	"rules":      {TypeRule},
	"skills":     {TypeSkill},
	"atoms":      {TypeAtom},
	"outcomes":   {TypeOutcome},
}

// resolveIncludeTypes converts req.IncludeTypes (API vocabulary) into the set
// of internal Item.Type values retrieve() should keep. A nil/empty result
// means "no restriction" — every source runs, matching today's default
// behavior. Current task/project (and the active work session layered on
// top of them) are never filtered by this set — see
// retrieveCurrentTaskProject.
func resolveIncludeTypes(apiTypes []string) map[string]bool {
	if len(apiTypes) == 0 {
		return nil
	}
	out := make(map[string]bool)
	for _, t := range apiTypes {
		for _, internal := range includeTypesMap[t] {
			out[internal] = true
		}
	}
	return out
}

// retrieve fans out to the domain stores held by a and returns the raw
// candidate Item set for req, before scoring/capping/trimming, plus any
// non-fatal per-source Warnings collected along the way. It also owns
// stale/proposed exclusion — Assemble() does not filter by status itself.
//
// Every store call is non-fatal: a failure is logged (and recorded as a
// Warning) and the source is skipped so the rest of the pack still assembles
// from partial results. Each retrieveX helper below owns exactly one source
// (or a small cluster of related sources) and returns its own []Item slice;
// retrieve() just concatenates them and dedupes.
func (a *Assembler) retrieve(ctx context.Context, req Request) ([]Item, []Warning) {
	ws := a.workspaceID()
	wsStr := workspaceIDString(ws)
	include := resolveIncludeTypes(req.IncludeTypes)

	var items []Item
	var warnings []Warning

	// Current task/project/work-session are always retrieved, independent of
	// IncludeTypes — the caller is explicitly working on them (spec: "Always
	// include current task/project if available").
	items = append(items, a.retrieveCurrentTaskProject(ctx, req, ws, &warnings)...)

	// Every other source is gated by IncludeTypes: a nil include set means
	// "no restriction" (every source runs); otherwise only sources whose
	// emitted Type is in the set run. Table-driven (one loop, one branch)
	// instead of one if-statement per source, so retrieve() itself stays
	// under gocyclo's complexity budget.
	for _, src := range a.includeGatedSources(ctx, req, ws, wsStr, &warnings) {
		if include == nil || include[src.typ] {
			items = append(items, src.retrieve()...)
		}
	}

	return dedupeItems(items), warnings
}

// includeGatedSource pairs a retrieveX call with the single Item.Type it
// emits, so retrieve() can gate it against IncludeTypes generically.
type includeGatedSource struct {
	typ      string
	retrieve func() []Item
}

// includeGatedSources lists every retrieveX source other than
// retrieveCurrentTaskProject (which is never IncludeTypes-gated — see
// retrieve()'s comment). Each closure captures ctx/req/ws/wsStr/warnings so
// callers only need to check src.typ against the include set.
func (a *Assembler) includeGatedSources(
	ctx context.Context, req Request, ws *uuid.UUID, wsStr *string, warnings *[]Warning,
) []includeGatedSource {
	return []includeGatedSource{
		{TypeDecision, func() []Item { return a.retrieveDecisions(ctx, req, warnings) }},
		{TypeKnowledge, func() []Item { return a.retrieveKnowledge(ctx, req, warnings) }},
		{TypeAtom, func() []Item { return a.retrieveAtoms(ctx, req, ws, warnings) }},
		{TypeProcedure, func() []Item { return a.retrieveProcedural(ctx, req, ws, warnings) }},
		{TypeSkill, func() []Item { return a.retrieveSkills(ctx, req, wsStr, warnings) }},
		{TypeOutcome, func() []Item { return a.retrieveOutcomes(ctx, ws, warnings) }},
		{TypeReflection, func() []Item { return a.retrieveReflections(ctx, ws, warnings) }},
		{TypeRule, func() []Item { return a.retrieveBehaviorRules(ctx, ws, warnings) }},
		{TypeSession, func() []Item { return a.retrieveSession(ctx, warnings) }},
	}
}

// retrieveCurrentTaskProject always includes the caller's current task and
// project, independent of any relevance filtering below — the caller is
// explicitly working on them — plus the active work session, layered on top
// of (not instead of) the task/project lookups as an additional "current
// work" signal.
func (a *Assembler) retrieveCurrentTaskProject(ctx context.Context, req Request, ws *uuid.UUID, warnings *[]Warning) []Item {
	var items []Item

	if req.TaskID != nil {
		t, err := a.gtd.GetTaskByID(ctx, *req.TaskID)
		switch {
		case err == nil:
			items = append(items, itemFromTask(*t))
		case errors.Is(err, gtd.ErrNotFound):
			// The caller passed a task_id that doesn't exist (e.g. stale) —
			// not a store failure, nothing to warn about.
		default:
			warnStoreErr(warnings, "gtd.GetTaskByID", err)
		}
	}
	if req.ProjectID != nil {
		p, err := a.gtd.GetProjectByID(ctx, *req.ProjectID)
		switch {
		case err == nil:
			items = append(items, itemFromProject(*p, req.RepoName))
		case errors.Is(err, gtd.ErrNotFound):
			// Stale project_id — not a store failure.
		default:
			warnStoreErr(warnings, "gtd.GetProjectByID", err)
		}
	} else if req.RepoName != "" {
		projects, err := a.gtd.ProjectsByRepoName(ctx, req.RepoName)
		if err != nil {
			warnStoreErr(warnings, "gtd.ProjectsByRepoName", err)
		} else {
			for _, p := range projects {
				items = append(items, itemFromProject(p, req.RepoName))
			}
		}
	}
	if req.RepoName != "" && ws != nil {
		active, err := a.workSession.GetActive(ctx, *ws, req.RepoName)
		if err != nil {
			warnStoreErr(warnings, "workSession.GetActive", err)
		} else if active.Session != nil {
			items = append(items, itemFromWorkSession(active.Session))
		}
	}

	return items
}

// retrieveDecisions returns decisions relevant to the repo/project/task in
// scope for req. When req carries no scope signal at all (no RepoName,
// ProjectID, or TaskID), it falls back to decision.All: the most recent
// decisions across every repo/project. The session-start hook (A5a) is one
// caller of this branch — it has no current-repo signal to pass — but MCP
// assemble_context reaches it too: repo_name/project_id/task_id are all
// Optional on that tool's schema (internal/mcp/tools_contextpack.go), so a
// bare objective-only call also lands here. See DecisionReadPort.All's doc
// comment for the full caller analysis and
// TestHandleAssembleContext_UnscopedRequestReachesDecisionAll
// (internal/mcp/tools_contextpack_test.go) for the test locking this
// behavior in.
func (a *Assembler) retrieveDecisions(ctx context.Context, req Request, warnings *[]Warning) []Item {
	var items []Item

	if req.RepoName == "" && req.ProjectID == nil && req.TaskID == nil {
		ds, err := a.decision.All(ctx, searchLimit)
		if err != nil {
			warnStoreErr(warnings, "decision.All", err)
		} else {
			for _, d := range ds {
				items = append(items, itemFromDecision(d))
			}
		}
		return items
	}

	if req.RepoName != "" {
		ds, err := a.decision.ByRepo(ctx, req.RepoName, searchLimit)
		if err != nil {
			warnStoreErr(warnings, "decision.ByRepo", err)
		} else {
			for _, d := range ds {
				items = append(items, itemFromDecision(d))
			}
		}
	}
	if req.ProjectID != nil {
		ds, err := a.decision.ByProject(ctx, *req.ProjectID, searchLimit)
		if err != nil {
			warnStoreErr(warnings, "decision.ByProject", err)
		} else {
			for _, d := range ds {
				items = append(items, itemFromDecision(d))
			}
		}
	}
	if req.TaskID != nil {
		ds, err := a.decision.ByTask(ctx, *req.TaskID, searchLimit)
		if err != nil {
			warnStoreErr(warnings, "decision.ByTask", err)
		} else {
			for _, d := range ds {
				items = append(items, itemFromDecision(d))
			}
		}
	}

	return items
}

// retrieveKnowledge returns knowledge items matching req.Objective; archived
// rows are excluded per the EXCLUSION contract. Uses SearchReadOnly (not
// Search) so assemble_context never bumps recall_count/last_recalled_at —
// the tool is classified read-only in internal/discipline/discipline.go and
// must stay that way.
func (a *Assembler) retrieveKnowledge(ctx context.Context, req Request, warnings *[]Warning) []Item {
	var items []Item
	ks, err := a.knowledge.SearchReadOnly(ctx, req.Objective, searchLimit)
	if err != nil {
		warnStoreErr(warnings, "knowledge.SearchReadOnly", err)
		return items
	}
	for _, k := range ks {
		if k.ArchivedAt.Valid {
			continue
		}
		items = append(items, itemFromKnowledge(k))
	}
	return items
}

// retrieveAtoms returns memory atoms matching req.Objective — a shallow
// Search only, never Traverse/BFS from retrieve().
func (a *Assembler) retrieveAtoms(ctx context.Context, req Request, ws *uuid.UUID, warnings *[]Warning) []Item {
	var items []Item
	ats, err := a.atom.Search(ctx, ws, req.Objective, searchLimit)
	if err != nil {
		warnStoreErr(warnings, "atom.Search", err)
		return items
	}
	for _, at := range ats {
		items = append(items, itemFromAtom(at))
	}
	return items
}

// retrieveProcedural returns procedural memories ("how-to" approaches)
// matching req.Objective.
func (a *Assembler) retrieveProcedural(ctx context.Context, req Request, ws *uuid.UUID, warnings *[]Warning) []Item {
	var items []Item
	pf := procedural.QueryFilter{
		WorkspaceID: ws,
		RepoName:    req.RepoName,
		Keywords:    req.Objective,
		Limit:       searchLimit,
	}
	ps, err := a.procedural.Query(ctx, pf)
	if err != nil {
		warnStoreErr(warnings, "procedural.Query", err)
		return items
	}
	for _, p := range ps {
		items = append(items, itemFromProcedural(p))
	}
	return items
}

// retrieveSkills returns skill library entries matching req.Objective; a row
// with an unparsable ID is logged and skipped rather than failing the whole
// source.
func (a *Assembler) retrieveSkills(ctx context.Context, req Request, wsStr *string, warnings *[]Warning) []Item {
	var items []Item
	sf := skill.SearchFilter{WorkspaceID: wsStr, Query: req.Objective, Limit: searchLimit}
	sks, err := a.skill.Search(ctx, sf)
	if err != nil {
		warnStoreErr(warnings, "skill.Search", err)
		return items
	}
	for _, sk := range sks {
		item, err := itemFromSkill(sk)
		if err != nil {
			slog.Warn("contextpack.retrieve: skill row has invalid id, skipping", "id", sk.ID, "error", err)
			continue
		}
		items = append(items, item)
	}
	return items
}

// retrieveOutcomes returns failed/regressed outcomes, for retrospection.
func (a *Assembler) retrieveOutcomes(ctx context.Context, ws *uuid.UUID, warnings *[]Warning) []Item {
	var items []Item
	os, err := a.outcome.ListFailedOutcomes(ctx, ws, failedOutcomeLimit)
	if err != nil {
		warnStoreErr(warnings, "outcome.ListFailedOutcomes", err)
		return items
	}
	for _, o := range os {
		items = append(items, itemFromOutcome(o))
	}
	return items
}

// retrieveReflections returns recent reflections with detected patterns.
func (a *Assembler) retrieveReflections(ctx context.Context, ws *uuid.UUID, warnings *[]Warning) []Item {
	var items []Item
	since := time.Now().Add(-reflectionLookback)
	rs, err := a.reflection.RecentWithPatterns(ctx, ws, since, reflectionLimit)
	if err != nil {
		warnStoreErr(warnings, "reflection.RecentWithPatterns", err)
		return items
	}
	for _, r := range rs {
		items = append(items, itemFromReflection(r))
	}
	return items
}

// retrieveBehaviorRules returns behavior rules with status="active" only;
// proposed/rejected/deprecated rules are never surfaced as candidates.
func (a *Assembler) retrieveBehaviorRules(ctx context.Context, ws *uuid.UUID, warnings *[]Warning) []Item {
	var items []Item
	activeStatus := "active"
	rf := behaviorrule.ListParams{WorkspaceID: ws, Status: &activeStatus, Limit: searchLimit}
	rules, err := a.behaviorRule.List(ctx, rf)
	if err != nil {
		warnStoreErr(warnings, "behaviorRule.List", err)
		return items
	}
	for _, r := range rules {
		items = append(items, itemFromBehaviorRule(r))
	}
	return items
}

// retrieveSession returns the latest unresolved session handoff, if any.
// session.ErrNotFound (no unresolved handoff — the common case) is not a
// store failure and is not surfaced as a Warning.
func (a *Assembler) retrieveSession(ctx context.Context, warnings *[]Warning) []Item {
	var items []Item
	h, err := a.session.LatestHandoff(ctx)
	if err != nil {
		if !errors.Is(err, session.ErrNotFound) {
			warnStoreErr(warnings, "session.LatestHandoff", err)
		}
		return items
	}
	return append(items, itemFromSessionHandoff(h))
}

// warnStoreErr logs a non-fatal single-source retrieval failure and records
// it as a Pack-visible Warning. Callers continue with whatever other sources
// succeeded.
//
// [F170-SEC-R3-02] The Warning names the SOURCE and nothing else. Warnings
// is a json-tagged field of Pack, and Pack is marshalled whole into the
// assemble_context and start_work tool responses, so putting the raw store
// error here delivered the pgx / modernc-sqlite text into an LLM's context:
// PoC-verified to carry the Aiven host, port, database name, DB user, the
// violated constraint's name and the SQLSTATE. Bounding and neutralising the
// string downstream (tools_worksession.go) cannot fix that — by then the
// error object is gone and only redaction at the source is possible.
//
// The "<op> failed" shape is deliberately byte-identical to storeErrorText
// (internal/mcp/tool_errors.go), so the two redaction policies stay ONE
// policy. If a caller-facing sentinel ever needs to survive this boundary,
// mirror that function's errors.Is loop over callerFacingSentinels rather
// than inventing a second rule here. The full error is not lost: it stays in
// the slog.Warn line below, which never crosses the wire.
func warnStoreErr(warnings *[]Warning, source string, err error) {
	slog.Warn("contextpack.retrieve: store call failed, skipping source", "source", source, "error", err)
	*warnings = append(*warnings, Warning{
		Type:    "store_error",
		Summary: source + " failed",
	})
}

// dedupeItems drops repeat (Type, ID) pairs that can occur when the same row
// is reachable through more than one query (e.g. a decision returned by both
// ByRepo and ByProject), preserving first-seen order.
func dedupeItems(items []Item) []Item {
	seen := make(map[string]bool, len(items))
	out := make([]Item, 0, len(items))
	for _, it := range items {
		key := it.Type + ":" + it.ID.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, it)
	}
	return out
}

// workspaceID returns the configured workspace scope as *uuid.UUID (nil
// when unscoped), derived from the gtd store — every domain store in an
// Assembler shares the same workspace.
func (a *Assembler) workspaceID() *uuid.UUID {
	ws := a.gtd.WorkspaceID()
	if !ws.Valid {
		return nil
	}
	id := uuid.UUID(ws.Bytes)
	return &id
}

// workspaceIDString mirrors workspaceID for the skill store, which keys on
// a string workspace ID instead of uuid.UUID.
func workspaceIDString(ws *uuid.UUID) *string {
	if ws == nil {
		return nil
	}
	s := ws.String()
	return &s
}

// truncateRunes caps s at n runes, matching the Item.Summary budget.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// isRecent reports whether t falls within recentWindow of now, formatted as
// the Provenance["recent"] flag the scorer reads. This is the only place
// retrieve() reads the wall clock; retrieval_test.go builds fixture
// timestamps relative to time.Now() so assertions stay deterministic.
func isRecent(t time.Time) string {
	if time.Since(t) < recentWindow {
		return provTrue
	}
	return "false"
}

func itemFromTask(t db.Task) Item {
	// task_id is self-tagged (not just derivable from the request) so
	// matchesRepoProjectTask (scorer.go) can award the current-task bonus and
	// trimToBudget can recognize this as a pinned "always include current
	// task" item regardless of req.TaskID's exact identity.
	prov := map[string]string{
		"source_table": "tasks",
		"recent":       isRecent(t.CreatedAt.Time),
		"task_id":      t.ID.String(),
	}
	if t.ProjectID.Valid {
		prov["project_id"] = uuid.UUID(t.ProjectID.Bytes).String()
	}
	summary := t.Title
	if t.Description.Valid && t.Description.String != "" {
		summary += " — " + t.Description.String
	}
	return Item{
		Type:        TypeTask,
		ID:          t.ID,
		SourceTable: "tasks",
		Summary:     truncateRunes(summary, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromProject(p db.Project, repoName string) Item {
	// project_id is self-tagged for the same reason as itemFromTask's
	// task_id — see that comment.
	prov := map[string]string{
		"source_table": "projects",
		"recent":       isRecent(p.CreatedAt.Time),
		"project_id":   p.ID.String(),
	}
	if repoName != "" {
		prov["repo_name"] = repoName
	}
	summary := p.Title
	if p.Description.Valid && p.Description.String != "" {
		summary += " — " + p.Description.String
	}
	return Item{
		Type:        TypeProject,
		ID:          p.ID,
		SourceTable: "projects",
		Summary:     truncateRunes(summary, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromWorkSession(s *worksession.Session) Item {
	prov := map[string]string{"source_table": "work_sessions"}
	if t, err := time.Parse(time.RFC3339, s.CreatedAt); err == nil {
		prov["recent"] = isRecent(t)
	} else {
		slog.Warn("itemFromWorkSession: parsing CreatedAt failed, omitting recent flag",
			"session_id", s.ID, "created_at", s.CreatedAt, "err", err)
	}
	if s.RepoName != "" {
		prov["repo_name"] = s.RepoName
	}
	if s.ProjectID != nil {
		prov["project_id"] = s.ProjectID.String()
	}
	if s.CurrentTaskID != nil {
		prov["task_id"] = s.CurrentTaskID.String()
	}
	summary := s.Title
	if s.Goal != "" {
		summary += " — " + s.Goal
	}
	return Item{
		Type:        TypeSession,
		ID:          s.ID,
		SourceTable: "work_sessions",
		Summary:     truncateRunes(summary, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromDecision(d db.Decision) Item {
	prov := map[string]string{"source_table": "decisions", "recent": isRecent(d.CreatedAt.Time)}
	if d.RepoName.Valid && d.RepoName.String != "" {
		prov["repo_name"] = d.RepoName.String
	}
	if d.ProjectID.Valid {
		prov["project_id"] = uuid.UUID(d.ProjectID.Bytes).String()
	}
	if d.TaskID.Valid {
		prov["task_id"] = uuid.UUID(d.TaskID.Bytes).String()
	}
	return Item{
		Type:        TypeDecision,
		ID:          d.ID,
		SourceTable: "decisions",
		Summary:     truncateRunes(d.Title+" — "+d.Decision, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromKnowledge(k db.KnowledgeItem) Item {
	prov := map[string]string{"source_table": "knowledge_items", "recent": isRecent(k.CreatedAt.Time)}
	if k.ProjectID.Valid {
		prov["project_id"] = uuid.UUID(k.ProjectID.Bytes).String()
	}
	if k.TaskID.Valid {
		prov["task_id"] = uuid.UUID(k.TaskID.Bytes).String()
	}
	return Item{
		Type:        TypeKnowledge,
		ID:          k.ID,
		SourceTable: "knowledge_items",
		Summary:     truncateRunes(k.Title+" — "+k.Content, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromAtom(at atom.Atom) Item {
	prov := map[string]string{"source_table": "memory_atoms", "recent": isRecent(at.CreatedAt)}
	return Item{
		Type:        TypeAtom,
		ID:          at.ID,
		SourceTable: "memory_atoms",
		Summary:     truncateRunes(at.Content, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromProcedural(p procedural.ProceduralMemory) Item {
	prov := map[string]string{
		"source_table":  "procedural_memories",
		"recent":        isRecent(p.CreatedAt),
		"success_count": strconv.Itoa(p.SuccessCount),
	}
	if p.RepoName != "" {
		prov["repo_name"] = p.RepoName
	}
	if p.ProjectID != nil {
		prov["project_id"] = p.ProjectID.String()
	}
	if len(p.FilesTouched) > 0 {
		prov["files"] = strings.Join(p.FilesTouched, ",")
	}
	return Item{
		Type:        TypeProcedure,
		ID:          p.ID,
		SourceTable: "procedural_memories",
		Summary:     truncateRunes(p.Title+" — "+p.WhenToUse, maxSummaryRunes),
		Provenance:  prov,
	}
}

// itemFromSkill converts a Skill row to an Item. Skill.ID is a UUID stored
// as TEXT (shared Go type across the Postgres/SQLite backends); a parse
// failure is returned to the caller rather than silently zeroed so the
// caller can log and skip the row instead of emitting a bogus zero-UUID Item.
func itemFromSkill(s *skill.Skill) (Item, error) {
	id, err := uuid.Parse(s.ID)
	if err != nil {
		return Item{}, fmt.Errorf("parse skill id: %w", err)
	}
	prov := map[string]string{
		"source_table":  "skills",
		"recent":        isRecent(s.CreatedAt),
		"success_count": strconv.Itoa(s.SuccessCount),
	}
	if total := s.SuccessCount + s.FailureCount; total > 0 {
		prov["confidence"] = fmt.Sprintf("%.2f", float64(s.SuccessCount)/float64(total))
	}
	return Item{
		Type:        TypeSkill,
		ID:          id,
		SourceTable: "skills",
		Summary:     truncateRunes(s.Name+" — "+s.Description, maxSummaryRunes),
		Provenance:  prov,
	}, nil
}

func itemFromOutcome(o outcome.Outcome) Item {
	prov := map[string]string{
		"source_table": "outcomes",
		"recent":       isRecent(o.CreatedAt),
		"result":       o.Result,
	}
	summary := fmt.Sprintf("%s %s: %s", o.EntityType, o.Result, o.Notes)
	return Item{
		Type:        TypeOutcome,
		ID:          o.ID,
		SourceTable: "outcomes",
		Summary:     truncateRunes(summary, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromReflection(r *reflection.Reflection) Item {
	prov := map[string]string{"source_table": "reflections", "recent": isRecent(r.CreatedAt)}
	if r.RelatedEntityType != nil && r.RelatedEntityID != nil && *r.RelatedEntityType == "task" {
		prov["task_id"] = r.RelatedEntityID.String()
	}
	return Item{
		Type:        TypeReflection,
		ID:          r.ID,
		SourceTable: "reflections",
		Summary:     truncateRunes(r.Summary, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromBehaviorRule(r *behaviorrule.BehaviorRule) Item {
	prov := map[string]string{
		"source_table": "behavior_rules",
		"recent":       isRecent(r.CreatedAt),
		"confidence":   fmt.Sprintf("%.2f", r.Confidence),
	}
	summary := fmt.Sprintf("IF %s THEN %s", r.Condition, r.Action)
	return Item{
		Type:        TypeRule,
		ID:          r.ID,
		SourceTable: "behavior_rules",
		Summary:     truncateRunes(summary, maxSummaryRunes),
		Provenance:  prov,
	}
}

func itemFromSessionHandoff(h *db.SessionHandoff) Item {
	prov := map[string]string{"source_table": "session_handoffs", "recent": isRecent(h.CreatedAt.Time)}
	if h.ProjectID.Valid {
		prov["project_id"] = uuid.UUID(h.ProjectID.Bytes).String()
	}
	if h.RepoName.Valid && h.RepoName.String != "" {
		prov["repo_name"] = h.RepoName.String
	}
	summary := h.Intent
	if h.ContextSummary.Valid && h.ContextSummary.String != "" {
		summary += " — " + h.ContextSummary.String
	}
	return Item{
		Type:        TypeSession,
		ID:          h.ID,
		SourceTable: "session_handoffs",
		Summary:     truncateRunes(summary, maxSummaryRunes),
		Provenance:  prov,
	}
}
