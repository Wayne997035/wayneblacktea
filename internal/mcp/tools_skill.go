package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Read-time bounds for skill.Skill's free-text fields, applied by
// wrapUntrustedSkill before jsonText — U13 (2026-08-20-mcp-surface-spec.md).
// Name/Description mirror their write-time caps
// (validateSkillName/validateSkillDescription: 200/5000 runes).
// Triggers/Steps/FailureModes/VerificationChecklist have no write-time
// per-item cap (handleExtractSkill only validates the raw comma-separated
// string as a whole via validateSkillCSVField, which screens control chars,
// not length) — skillItemMaxRunes is a read-time backstop against
// marker-stuffing / pathological growth only, same rationale as
// decisionTitleMaxRunes/decisionBodyMaxRunes (tools_decision.go).
// skillNotesMaxRunes mirrors validateNotes' write-time cap and bounds every
// key and string leaf inside Examples entries (see neutralizeSkillExamples —
// it is the whole structure's bound, not just the "notes" field's).
const (
	skillNameMaxRunes  = 200
	skillBodyMaxRunes  = 5000
	skillItemMaxRunes  = 2000
	skillNotesMaxRunes = 2000

	// [F170-SEC-R3-01] outcome_id had no bound at any layer: no MaxLength on
	// the schema and no server-side check, so a single value could be
	// arbitrarily long. 200 runes is the same cap validateSkillName uses for
	// the other id-shaped-by-convention field. It deliberately does NOT reject
	// non-UUID input, because outcome_id is documented as a free reference
	// (task ID, decision ID, no FK) and rejecting would break callers.
	//
	// ⚠ This bounds ONE VALUE, not the array. examples is append-only and has
	// no entry-count limit at any layer, so a caller can still grow a skill
	// row without bound one entry at a time — measured at 1000 appends =
	// 2,257,001 bytes in a single tool response, paid by every later session
	// that reads that skill. An earlier version of this comment claimed this
	// cap closed that; it does not, and saying so here is the same false
	// assurance the rest of this file exists to remove. Bounding the entry
	// count belongs at the write path where the count is known
	// (UpdateFromOutcome, both stores) and is a data-retention decision, not a
	// bug fix — tracked as GTD 17f08ba8.
	skillOutcomeIDMaxRunes = 200
)

// wrapUntrustedSkill returns a copy of sk with every free-text field
// clipSafe'd (bounded + boundary-marker-neutralised) — U13. Mirrors
// wrapUntrustedTask/wrapUntrustedDecision's copy-not-mutate contract: the
// caller's *skill.Skill (and any cache holding it) must not end up with
// fence/neutralisation baked into its stored text. nil in, nil out.
//
// Steps/FailureModes/VerificationChecklist are literally step-by-step
// instructions an assistant wrote — exactly the shape a forged marker plus
// injected instruction would want to hide inside (backend-security-
// design.md §2.1: treat LLM-authored text as adversarial regardless of
// which model wrote it, not exempt because "it's our own model's output").
//
// [SEC171-08] SourceAtomIDs is treated exactly like the other four comma-separated
// fields, because it IS one: extract_skill takes source_atom_ids as a plain
// string argument and splitCSV's the caller's text straight into it. The
// paragraph that used to stand here said "ID, WorkspaceID, SourceAtomIDs …
// are left untouched — none is free text an LLM authored", and for
// SourceAtomIDs that was false. It was the sentence, not the field, that made
// this look covered: knownUnprotectedFields
// (u13_wrap_field_coverage_test.go) said the opposite — "id-shaped by
// convention only, never validated as UUIDs" — and two artifacts disagreeing
// is how a live gap keeps the appearance of being accounted for. PoC-verified
// end to end: a forged fence in source_atom_ids reached search_skills
// verbatim while Steps in the same payload came back neutralised.
//
// ID, WorkspaceID, SuccessCount, FailureCount, LastUsedAt, CreatedAt and
// UpdatedAt are still left untouched — those really are server-assigned.
func wrapUntrustedSkill(sk *skill.Skill) *skill.Skill {
	if sk == nil {
		return nil
	}
	out := *sk
	out.Name = clipSafe(sk.Name, skillNameMaxRunes)
	out.Description = clipSafe(sk.Description, skillBodyMaxRunes)
	out.Triggers = clipSafeSkillStrings(sk.Triggers)
	out.Steps = clipSafeSkillStrings(sk.Steps)
	out.FailureModes = clipSafeSkillStrings(sk.FailureModes)
	out.VerificationChecklist = clipSafeSkillStrings(sk.VerificationChecklist)
	out.SourceAtomIDs = clipSafeSkillStrings(sk.SourceAtomIDs) // [SEC171-08] read-side neutralisation
	out.Examples = neutralizeSkillExamples(sk.Examples)
	return &out
}

// wrapUntrustedSkills maps wrapUntrustedSkill over a slice of pointers,
// preserving order. Callers (search_skills, list_relevant_skills) already
// normalise nil results to []*skill.Skill{} before calling this (list tools
// MUST return [] not null), so no nil/empty special-casing is needed here.
func wrapUntrustedSkills(skills []*skill.Skill) []*skill.Skill {
	out := make([]*skill.Skill, len(skills))
	for i, sk := range skills {
		out[i] = wrapUntrustedSkill(sk)
	}
	return out
}

// clipSafeSkillStrings applies clipSafe to every element of a []string
// field, preserving order (nil in yields nil out, so JSON null-vs-[]
// semantics for Triggers etc. are unaffected by wrapping).
func clipSafeSkillStrings(items []string) []string {
	if items == nil {
		return nil
	}
	out := make([]string, len(items))
	for i, v := range items {
		out[i] = clipSafe(v, skillItemMaxRunes)
	}
	return out
}

// neutralizeSkillExamples walks the Examples entries UpdateFromOutcome
// appends (map[string]string at write time —
// internal/storage/sqlite/skill.go's UpdateFromOutcome — round-tripped
// through JSON storage as map[string]any on read) and neutralises EVERY key
// and EVERY string value it reaches, at every depth. Non-string leaves
// (numbers, bools, nil) and element shapes the walk does not recognise are
// passed through rather than dropped, so a store schema change degrades to
// no-op instead of silently deleting data.
//
// [F170-SEC-R3-01] This used to neutralise only the value under the literal
// key "notes", on the stated grounds that notes was "the only free-text a
// caller authors in this shape (outcome_id is a code-layer reference ID)".
// That sentence was false. outcome_id is an ordinary caller-supplied tool
// argument — handleUpdateSkillFromOutcome reads it with stringArg — so a
// forged boundary marker placed there was copied byte-for-byte into
// search_skills / use_skill / list_relevant_skills, i.e. into a later
// session's context. The notes leaf coming back neutralised in the same
// payload is what made it look covered.
//
// The lesson is the shape, not the field: a key-name allowlist inside a
// neutraliser is silently wrong for every key nobody thought of, and adding
// "outcome_id" to the list would leave the next key equally exposed. So the
// whole structure now goes through neutralizeAnyValue — the same walker
// neutralizeJSONBlob uses for arbitrary decoded JSON, which already
// neutralises map keys as well as values.
func neutralizeSkillExamples(examples []any) []any {
	if examples == nil {
		return nil
	}
	out := make([]any, len(examples))
	for i, e := range examples {
		switch m := e.(type) {
		case map[string]string:
			// neutralizeAnyValue's map case is map[string]any and would drop
			// this shape into its pass-through default. Handled here in the
			// same key-and-value form rather than widening the shared walker,
			// so the element's static type survives the round trip.
			cp := make(map[string]string, len(m))
			for k, v := range m {
				cp[clipSafe(k, skillNotesMaxRunes)] = clipSafe(v, skillNotesMaxRunes)
			}
			out[i] = cp
		default:
			out[i] = neutralizeAnyValue(e, skillNotesMaxRunes, 0)
		}
	}
	return out
}

func (s *Server) registerSkillTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"extract_skill",
		mcp.WithDescription(
			"Extracts and persists a reusable skill definition from the current session. "+
				"Provide a name, description, trigger conditions, step-by-step approach, "+
				"common failure modes, and a verification checklist.",
		),
		mcp.WithString("name",
			mcp.Description("Short unique name for the skill (max 200 chars)"),
			mcp.Required(), mcp.MaxLength(200)),
		mcp.WithString("description",
			mcp.Description("What the skill does and when to apply it (max 5000 chars)"),
			mcp.MaxLength(5000)),
		mcp.WithString("triggers",
			mcp.Description("Comma-separated trigger conditions that indicate this skill applies")),
		mcp.WithString("steps",
			mcp.Description("Comma-separated ordered steps for executing the skill")),
		mcp.WithString("failure_modes",
			mcp.Description("Comma-separated common failure modes to watch for")),
		mcp.WithString("verification_checklist",
			mcp.Description("Comma-separated verification checks to confirm success")),
		// [F171-07] No mcp.MaxLength here, deliberately, and none on its four
		// siblings either: the five comma-separated arguments share one bounding
		// policy — screened for NUL bytes and newlines at write time
		// (validateSkillCSVField — that is exactly what it screens; "control
		// characters" would overstate it, since ESC/BEL/the rest of C0 pass
		// through, and the write-time check exists to stop a fence gaining
		// its own line, not to run a general control-character filter) and
		// capped per element at read time (clipSafeSkillStrings). Adding a
		// schema length to this one field alone would imply the other four
		// are bounded that way too.
		//
		// [SEC171-14] ⚠ This bounds each ELEMENT, not the element count:
		// splitCSV of a 1 MB body yields ~500k entries, all of which survive
		// wrapUntrustedSkill and are re-served on every later read of this
		// skill. The ceiling is the TRANSPORT (echolog.BodyLimit("1M"),
		// cmd/server/main.go:175), not this policy — same distinction
		// skillOutcomeIDMaxRunes' comment above makes for examples, except
		// there the array is append-only (unbounded over time) and here it
		// is one request body (bounded, just large). No entry-count cap
		// exists in handleExtractSkill or either store; adding one is the
		// same decision as examples' entry-count cap and shares its ticket
		// (GTD 17f08ba8), not attempted here.
		mcp.WithString("source_atom_ids",
			mcp.Description("Comma-separated memory atom IDs that inform this skill (no FK)")),
	), s.handleExtractSkill)

	ms.AddTool(mcp.NewTool(
		"search_skills",
		mcp.WithDescription(
			"Searches persisted skills by name or description. "+
				"Returns matching skills ordered by success_count DESC.",
		),
		mcp.WithString("query",
			mcp.Description("Search query (matches name and description)"),
			mcp.Required()),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10)")),
	), s.handleSearchSkills)

	ms.AddTool(mcp.NewTool(
		"use_skill",
		mcp.WithDescription(
			"Records that a skill was applied successfully. Increments success_count "+
				"and updates last_used_at. Returns the updated skill.",
		),
		mcp.WithString("skill_id",
			mcp.Description("UUID of the skill to mark as used"),
			mcp.Required()),
	), s.handleUseSkill)

	ms.AddTool(mcp.NewTool(
		"update_skill_from_outcome",
		mcp.WithDescription(
			"Records a success or failure outcome for a skill execution. "+
				"Appends the outcome reference and notes to the skill's examples log "+
				"and increments the appropriate counter.",
		),
		mcp.WithString("skill_id",
			mcp.Description("UUID of the skill"),
			mcp.Required()),
		mcp.WithString("outcome_id",
			mcp.Description("Reference ID of the outcome (e.g. task ID, decision ID — no FK, "+
				"not validated as a UUID; max 200 chars)"),
			mcp.MaxLength(skillOutcomeIDMaxRunes)),
		mcp.WithBoolean("success",
			mcp.Description("REQUIRED: true = success outcome, false = failure outcome. No default — "+
				"omitting this is rejected rather than silently recorded as a failure."),
			mcp.Required()),
		mcp.WithString("notes",
			mcp.Description("Notes about the outcome (max 2000 chars)"),
			mcp.MaxLength(2000)),
	), s.handleUpdateSkillFromOutcome)

	ms.AddTool(mcp.NewTool(
		"list_relevant_skills",
		mcp.WithDescription(
			"Lists skills most relevant to the current task context. "+
				"Skills are ordered by success_count DESC, last_used_at DESC. "+
				"Optionally filter by keyword query.",
		),
		mcp.WithString("query",
			mcp.Description("Optional keyword to filter by name or description")),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10)")),
	), s.handleListRelevantSkills)
}

// [F171-07] validateSkillName returns an error message if name is empty,
// exceeds the rune cap, or contains a NUL byte or a newline. Returns empty string on
// success. It does NOT screen control characters generally — ESC/BEL and the
// rest of C0 pass through; the check exists to stop a name occupying a line of
// its own in a rendered response, the same narrow policy validateSkillCSVField
// applies to the five comma-separated arguments.
func validateSkillName(name string) string {
	if name == "" {
		return "name is required"
	}
	if len([]rune(name)) > 200 {
		return "name exceeds 200 character limit"
	}
	if strings.ContainsAny(name, "\x00\r\n") {
		return "name must not contain null bytes or newlines"
	}
	return ""
}

// [F171-07] validateSkillDescription returns an error message if description
// exceeds the rune cap or contains a NUL byte or a newline. Same narrow screen as
// validateSkillName, not a general control-character filter.
func validateSkillDescription(description string) string {
	if len([]rune(description)) > 5000 {
		return "description exceeds 5000 character limit"
	}
	if strings.ContainsAny(description, "\x00\r\n") {
		return "description must not contain null bytes or newlines"
	}
	return ""
}

// validateSkillCSVField returns an error message if a CSV-derived field string
// contains null bytes or newlines (validated before splitting).
func validateSkillCSVField(field, name string) string {
	if strings.ContainsAny(field, "\x00\r\n") {
		return name + " must not contain null bytes or newlines"
	}
	return ""
}

// validateNotes returns an error message if notes text is too long.
func validateNotes(notes string) string {
	if len([]rune(notes)) > 2000 {
		return "notes exceeds 2000 character limit"
	}
	return ""
}

// hasBoolArg reports whether key is present in args and holds a JSON boolean
// value (true or false) — distinguishes "omitted" from "explicitly false".
// Ω8 fix (mcp-surface spec, backend-security-design.md §2.1): boolArg's
// missing-key default of false made an omitted update_skill_from_outcome
// `success` argument silently record a FAILURE outcome — the opposite of
// "caller forgot to say" being a no-op or an error. mcp.Required() on the
// tool schema is client-side advisory only (mcp-go does not enforce it
// server-side, see the existing "X is required" checks throughout this
// package), so the server-side check below is what actually rejects it.
func hasBoolArg(args map[string]any, key string) bool {
	_, ok := args[key].(bool)
	return ok
}

// handleExtractSkill implements the extract_skill MCP tool.
func (s *Server) handleExtractSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name := stringArg(args, "name")
	if msg := validateSkillName(name); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	description := stringArg(args, "description")
	if msg := validateSkillDescription(description); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	rawTriggers := stringArg(args, "triggers")
	if msg := validateSkillCSVField(rawTriggers, "triggers"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	rawSteps := stringArg(args, "steps")
	if msg := validateSkillCSVField(rawSteps, "steps"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	rawFailureModes := stringArg(args, "failure_modes")
	if msg := validateSkillCSVField(rawFailureModes, "failure_modes"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	rawVerification := stringArg(args, "verification_checklist")
	if msg := validateSkillCSVField(rawVerification, "verification_checklist"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	// [SEC171-08] source_atom_ids was the only one of the five CSV arguments
	// that skipped this check. Measured per field before the fix: a newline
	// was rejected in triggers, steps, failure_modes and verification_checklist,
	// and accepted here — making it the one argument where a forged boundary
	// marker could occupy a line of its own in the rendered response.
	rawSourceAtomIDs := stringArg(args, "source_atom_ids")
	if msg := validateSkillCSVField(rawSourceAtomIDs, "source_atom_ids"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	p := skill.AddParams{
		Name:                  name,
		Description:           description,
		Triggers:              splitCSV(rawTriggers),
		Steps:                 splitCSV(rawSteps),
		FailureModes:          splitCSV(rawFailureModes),
		VerificationChecklist: splitCSV(rawVerification),
		SourceAtomIDs:         splitCSV(rawSourceAtomIDs),
		Examples:              []any{},
	}

	if wsID := s.workspaceUUID(); wsID != nil {
		wsStr := wsID.String()
		p.WorkspaceID = &wsStr
	}

	sk, err := s.skill.Add(ctx, p)
	if err != nil {
		return storeErrorResult("extracting skill", err), nil
	}

	s.launchAtomize("skills", mustParseUUID(sk.ID), name+" "+description)
	return jsonText(wrapUntrustedSkill(sk))
}

// handleSearchSkills implements the search_skills MCP tool.
func (s *Server) handleSearchSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}

	f := skill.SearchFilter{
		Query: query,
		Limit: limit,
	}
	if wsID := s.workspaceUUID(); wsID != nil {
		wsStr := wsID.String()
		f.WorkspaceID = &wsStr
	}

	results, err := s.skill.Search(ctx, f)
	if err != nil {
		return storeErrorResult("searching skills", err), nil
	}
	if results == nil {
		results = []*skill.Skill{}
	}
	return jsonText(wrapUntrustedSkills(results))
}

// handleUseSkill implements the use_skill MCP tool.
func (s *Server) handleUseSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	skillID := stringArg(args, "skill_id")
	if skillID == "" {
		return mcp.NewToolResultError("skill_id is required"), nil
	}

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	sk, err := s.skill.IncrementSuccess(ctx, skillID, wsStr)
	if err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			return mcp.NewToolResultError("skill not found"), nil
		}
		return storeErrorResult("using skill", err), nil
	}
	return jsonText(wrapUntrustedSkill(sk))
}

// handleUpdateSkillFromOutcome implements the update_skill_from_outcome MCP tool.
func (s *Server) handleUpdateSkillFromOutcome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	skillID := stringArg(args, "skill_id")
	if skillID == "" {
		return mcp.NewToolResultError("skill_id is required"), nil
	}

	notes := stringArg(args, "notes")
	if msg := validateNotes(notes); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	if !hasBoolArg(args, "success") {
		return mcp.NewToolResultError(
			"success is required: true = success outcome, false = failure outcome (no default)",
		), nil
	}
	success := boolArg(args, "success")

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	p := skill.UpdateFromOutcomeParams{
		SkillID: skillID,
		// [F170-SEC-R3-01] Bounded server-side, not only in the schema:
		// mcp-go does not enforce schema constraints on the server (see
		// hasBoolArg's comment below), so MaxLength above is advisory to the
		// client and this is the line that actually holds.
		OutcomeID: clipSafe(stringArg(args, "outcome_id"), skillOutcomeIDMaxRunes),
		Success:   success,
		Notes:     notes,
	}

	sk, err := s.skill.UpdateFromOutcome(ctx, p, wsStr)
	if err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			return mcp.NewToolResultError("skill not found"), nil
		}
		return storeErrorResult("updating skill from outcome", err), nil
	}

	if notes != "" {
		s.launchAtomize("skills", mustParseUUID(sk.ID), notes)
	}
	return jsonText(wrapUntrustedSkill(sk))
}

// handleListRelevantSkills implements the list_relevant_skills MCP tool.
func (s *Server) handleListRelevantSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	results, err := s.skill.ListRelevant(ctx, wsStr, query, limit)
	if err != nil {
		return storeErrorResult("listing relevant skills", err), nil
	}
	if results == nil {
		results = []*skill.Skill{}
	}
	return jsonText(wrapUntrustedSkills(results))
}

// mustParseUUID parses a UUID string and returns a zero UUID on failure.
// Used for launchAtomize where the ID is a DB-generated UUID string.
func mustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}
	}
	return id
}
