---
status: accepted
---

# MCP tool handler seam: typed ToolSpec derived from registration, with hand-written per-tool arg structs

`internal/mcp/tools_*.go` (27 files, 93 handlers) is a shallow module: each `handleXxx` repeats arg parsing, required/uuid/enum validation, and error-text formatting inline, even though the same required/enum/maxlength constraints are already declared once at `mcp.NewTool()` registration time. We deepen it with a `handleTool` seam: validation metadata (required/enum/maxlength) is derived from the existing `mcp.NewTool()` registration at init and cached — not hand-duplicated — so there remains exactly one declared source for those constraints. Handlers receive a fully typed per-tool arg struct (e.g. `UpdateProjectArgs`), populated by a shared reflection-based decoder after validation passes, so handler bodies contain business logic only.

Two constraints outside our control shaped this: (1) Go has no way to derive a compile-time named struct type from a runtime registration call, so each tool's arg struct must still be hand-written — this is unavoidable, not laziness; (2) the refactor's own non-goals forbid changing the MCP tool's advertised schema or its error-text contract, which rules out the cleanest version of two sub-decisions below.

## Considered Options

- **UUID marking**: mcp-go supports `Pattern(regex)` at registration, which could express "this is a UUID" in the single declared schema. Rejected — adding it would change the JSON schema MCP clients see, violating the "no schema changes" non-goal. Chose a separate lightweight annotation layered on top of the derived ToolSpec instead; it is a much smaller drift surface than the 47 scattered `uuid.Parse` calls it replaces, just not a single point.
- **Error message text**: centralizing validation naturally wants one generic message format. Rejected — the "no error-text contract changes" non-goal requires preserving all 18 existing per-field messages verbatim. Each `ToolSpec` arg entry carries an optional message override instead.
- **`requireUUIDArg` (`server.go:525`)**: could be fully absorbed and deleted. Kept as an internal helper the new seam calls — it already has 8 verified call sites and rewriting it buys nothing.
- **Seam depth**: could stop at a validation gate (handlers keep calling `stringArg`/`numberArg` themselves). Went deeper — full typed structs — accepting the cost of one hand-written struct per tool, because "business logic only in handlers" was the actual goal, not just "less inline validation."
- **Rollout**: full 93-handler migration in one PR vs. a single-file pilot. Piloting on `tools_gtd.go` (20 handlers) first — this whole seam design is unvalidated in practice (typed structs, reflection decode, and message-override are all new), and a pilot surfaces design gaps (e.g. `handleUpdateProject`'s preserve-on-omit logic, which needs optional fields to stay business-logic-visible rather than being absorbed into validation) before committing to all 27 files.

## Consequences

- Adding tool #94 means: one `mcp.NewTool()` registration (unchanged today) + one small hand-written `XxxArgs` struct + a handler that takes that struct — not a new interface, not new boilerplate validation.
- `preserve-on-omit`-style handlers (fields default to an existing DB row when the caller omits them) must express "omitted" via a pointer/nil in the typed struct — the seam does not and cannot decide domain defaulting.
- The remaining 26 files stay on the old inline pattern until later PRs extend the seam to them; expect visible inconsistency between migrated and unmigrated files during that window.
