# wayneblacktea 2.0 Development Prompt

> **Status:** Phase 0/1/2 already shipped (commits `e8b7f16` / `7eaf0f2` /
> `f2e9829`); Phase 3–7 are still pending. This document is internal
> development planning, not a description of current behavior — see
> [`docs/architecture.md`](../architecture.md) for what's actually shipped.

Last reviewed: 2026-06-02

This document is a source-backed development prompt for planning and building
wayneblacktea 2.0. It is intentionally concrete: every phase should map to
code, tests, docs, and acceptance criteria. It is not workspace policy and must
not override `CLAUDE.md` or `.claude/**`.

## Product Target

wayneblacktea 2.0 is a memory and learning runtime for the user's AI workflows.
It should make agents better by giving them the right context before work,
forcing evidence capture after work, and converting repeated outcomes into
reviewable skills, rules, and memory updates.

The target is not a generic chatbot, not an autonomous life manager, and not a
new agent framework. The target is a pragmatic Personal OS layer that improves
Claude/Codex/other agents operating in this workspace.

## Current Ground Truth

The project already has the core primitives:

- Semantic memory: `knowledge_items`, decisions, semantic search, memory atoms.
- Episodic memory: activity log, session handoffs, work sessions, reflections,
  outcomes, evaluations.
- Procedural memory: procedural memories, playbooks, skills, behavior rules.
- Safety model: proposal-gated AI-originated durable changes.
- Runtime surfaces: Echo HTTP API, streamable HTTP MCP, stdio MCP, React
  dashboard, scheduler jobs.
- Storage: Postgres + pgvector in production, SQLite local.
- Observability: `system_health`, watchdog discipline events, AI cost ledger.

The missing part is not another isolated table. The missing part is a required
runtime path:

```text
request/task -> assemble_context -> apply rules/skills -> execute
             -> verify -> record_outcome -> evaluate/reflect
             -> propose memory/skill/rule updates
```

## Source-Backed Design Basis

Use these sources as constraints, not as cargo-cult architecture:

1. MemGPT / Letta: limited context requires memory tiering and explicit context
   management, not "retrieve everything".
   - https://arxiv.org/abs/2310.08560
   - https://docs.letta.com/guides/core-concepts/stateful-agents
2. Generative Agents: observation, reflection, retrieval, and planning work as a
   loop. The useful lesson for wayneblacktea is the loop, not simulating people.
   - https://arxiv.org/abs/2304.03442
3. Reflexion: task feedback can improve later attempts through verbal
   reflection stored in episodic memory, without model weight updates.
   - https://arxiv.org/abs/2303.11366
4. Voyager: lifelong improvement depends on environment feedback,
   self-verification, and a reusable skill library. The useful lesson is
   "verified success -> skill", not Minecraft autonomy.
   - https://arxiv.org/abs/2305.16291
5. CoALA: language agents can be organized around modular memory, structured
   actions, and a decision process. This maps well to wayneblacktea's existing
   MCP tools and proposal gate.
   - https://arxiv.org/abs/2309.02427
6. LangGraph memory docs: useful agent memory is not one bucket. It distinguishes
   short-term state, long-term memory, and semantic/episodic/procedural memory.
   It also calls out hot-path vs background memory updates.
   - https://docs.langchain.com/oss/python/concepts/memory
7. Zep/Graphiti: temporal knowledge graphs matter because facts and
   relationships change over time. For wayneblacktea, this means supersession,
   contradiction, and validity metadata are not optional long-term.
   - https://help.getzep.com/v2/understanding-the-graph
8. OpenAI agent eval docs: agent quality needs traces, graders, datasets, and
   repeatable eval runs. For wayneblacktea, `task check` is necessary but not
   enough to prove behavior improvement.
   - https://developers.openai.com/api/docs/guides/agent-evals

## Required First Steps For Any Agent

Before proposing or changing code:

1. Read whatever workspace-level policy your environment defines (coding
   standards, commit/PR rules, and the entry files for any skills it ships).
2. Read project files:
   - `CLAUDE.md`
   - `docs/architecture.md`
   - `docs/mcp-tools.md`
   - `docs/retention-policy.md`
   - `cmd/server/main.go`
   - `internal/storage/server_stores.go`
   - `internal/storage/factory.go`
   - `internal/mcp/server.go`
   - `internal/mcp/tools_procedural.go`
   - `internal/mcp/tools_reflection.go`
   - `internal/mcp/tools_outcome.go`
   - `internal/mcp/tools_behaviorrule.go`
   - `internal/ai/atomizer.go`
   - `internal/atom/atom.go`
   - `internal/scheduler/cognitive_jobs.go`
   - `internal/scheduler/atom_bridge.go`
   - `internal/scheduler/behavior_governance.go`
3. Query wayneblacktea memory:
   - `get_today_context`
   - `list_decisions(repo_name="wayneblacktea")`
   - `get_project_arch(slug="wayneblacktea")`
4. Verify current GTD task state. Dynamic Workflow outputs are candidate
   artifacts until Lead intake confirms final integration.

## Non-Negotiable Constraints

- Do not rename the product or subsystem. Use `wayneblacktea 2.0`, `memory and
  learning runtime`, or the existing module names.
- Do not edit `CLAUDE.md` or `.claude/**` unless the user explicitly asks in
  the current task and reconfirms.
- No database FK constraints. Keep referential integrity in code.
- Preserve proposal-gated learning for durable behavior changes.
- Preserve Postgres/SQLite parity when the project already has parity.
- Prefer existing `ServerStores` and domain store interfaces.
- Prefer HTTP `/mcp` for long-lived multi-agent use; stdio MCP clients each
  create their own pool.
- No hidden permanent AI mutation of policy, rules, skills, or memory.
- No unbounded LLM fanout, graph traversal, scheduler loops, or DB writes.
- Every phase must add deterministic tests. Provider-backed tests are optional
  and must be separated from default `task check`.

## Implementation Gap Matrix

Use this matrix to avoid rebuilding features that already exist.

| Area | Current implementation | 2.0 gap | Build in |
| --- | --- | --- | --- |
| Context recall | `recall`, `query_procedural`, knowledge search, decisions, atoms | No required pre-action context pack with ranking, provenance, budget, warnings | Phase 1 |
| Work lifecycle | `start_work`, `checkpoint_work`, `finish_work`, `work_sessions`, `work_session_tasks` | Work sessions do not persist the exact context pack and verification evidence used for later learning | Phase 2 |
| Outcomes | `record_outcome`, `evaluate_outcome`, recent outcome listing, failed pattern lookup | Outcomes are not automatically tied to pre-action context and verification traces | Phase 2 |
| Memory atoms | `memory_atoms`, atom links, digest status | No confidence/source model/last verified/validity status for long-term trust | Phase 3 |
| Governance | proposal-gated scheduler jobs, behavior rule governance, closeout checks | No explicit stale/superseded/conflict review path across memory types | Phase 3 |
| Skills | `extract_skill`, `search_skills`, `use_skill`, `update_skill_from_outcome` | No compiler that proposes skills from repeated verified outcomes | Phase 4 |
| Provider layer | provider chain for classifier/concept reviewer | summarizer, reflector, atomizer still have direct Claude/Anthropic paths | Phase 5 |
| Evals | Go tests and `task check` | No deterministic behavior eval suite for context quality and learning loops | Phase 6 |
| Dashboard | Knowledge, Reviews, Learning History, automation feed, AI cost surfaces | No context-pack inspector, conflict queue, or skill-candidate review view | Phase 7 |

## Do Not Build First

These are tempting but low-leverage for the next version:

- Do not replace Postgres/pgvector with a new graph database.
- Do not add model fine-tuning.
- Do not build a new autonomous agent framework.
- Do not let AI mutate `CLAUDE.md`, `.claude/**`, rules, skills, or durable
  memory without proposal review.
- Do not implement graph-wide reasoning before context packs and action traces
  are measurable.
- Do not make provider-backed evals part of the default quality gate.
- Do not split the action lifecycle away from existing `work_sessions` unless
  the existing model demonstrably cannot represent the needed trace.

## Phase 0: 1.0 Readiness And Documentation Cleanup

Purpose: make the current system truthful and shippable before adding 2.0
runtime layers.

Why this matters:

- Public docs and operator docs currently underdescribe the memory/learning
  surface.
- Stale docs cause agents to choose wrong implementation points.
- 2.0 planning depends on accurate module maps.

Code/doc scope:

- `docs/architecture.md`
- `docs/mcp-tools.md`
- `docs/retention-policy.md`
- `docs/DESIGN.md`
- `docs/install.md`
- `docs/operations.md`
- `docs/runbook.md`
- `docs/security.md`

Required updates:

- Architecture docs must include:
  - atoms and memory links
  - outcomes and evaluations
  - reflections
  - behavior rules
  - skills
  - procedural memories
  - playbooks
  - watchdog discipline events
  - AI cost ledger
  - HTTP `/mcp` vs stdio MCP connection behavior
- MCP docs must be regenerated or manually synced with all registered tools in
  `internal/mcp/server.go`.
- Retention docs must include current TTL/prune behavior for:
  - outcomes/evaluations
  - reflections
  - behavior rules
  - discipline_events_m8
  - ai_cost_ledger
  - memory atom digest statuses
- Design docs must stop describing implemented Knowledge/Reviews routes as
  future stubs.

Acceptance:

- Docs match live code registration and route/store wiring.
- No secrets or production credential shapes are documented.
- `cd build && task check` passes.
- Agent reading only docs can correctly identify where to implement:
  context packing, outcomes, reflections, behavior rules, and skills.

Non-goals:

- Do not redesign UI in this phase.
- Do not add new memory schema unless docs reveal an actual blocker.

## Phase 1: Context Pack MVP

Purpose: build the first useful pre-action context assembler. This is the most
important 2.0 phase.

Research mapping:

- MemGPT/Letta: context window is scarce, so decide what is in-context vs
  external.
- LangGraph: short-term state and long-term memory have different recall scope.
- CoALA: a decision process should choose which memory/action resources to use.

New domain:

- Add `internal/contextpack` or `internal/context`
- Optional table only if packs must be auditable:
  - `context_packs`
  - `context_pack_items`

Start without persistence if that reduces scope. Persistence becomes required in
Phase 2 when action lifecycle needs to link outcomes to the context used.

MCP tool:

```text
assemble_context
```

Input JSON:

```json
{
  "objective": "string, required",
  "repo_name": "string, optional",
  "project_id": "uuid, optional",
  "task_id": "uuid, optional",
  "branch_name": "string, optional",
  "files_touched": ["string"],
  "budget_chars": 12000,
  "include_types": ["semantic", "episodic", "procedural", "rules", "skills", "atoms", "outcomes"],
  "persist": false
}
```

Output JSON:

```json
{
  "pack_id": "uuid or null",
  "objective": "...",
  "budget_chars": 12000,
  "used_chars": 7340,
  "items": [
    {
      "type": "decision|knowledge|atom|procedure|skill|rule|outcome|reflection|handoff|task",
      "id": "uuid",
      "source_table": "decisions",
      "summary": "...",
      "score": 0.87,
      "reasons": ["repo_match", "recent", "failed_outcome_match"],
      "provenance": {"repo_name": "wayneblacktea", "created_at": "..."}
    }
  ],
  "warnings": [
    {"type": "stale_or_conflict", "summary": "..."}
  ],
  "omitted": [
    {"type": "knowledge", "count": 12, "reason": "budget"}
  ]
}
```

Retrieval sources:

- `GTD`: current task/project/goal, active work, checklist.
- `Decision`: repo/project/task decisions.
- `Knowledge`: semantic search.
- `Atom`: keyword search and shallow traversal.
- `Procedural`: query by objective/repo/files.
- `Skill`: search/list relevant skills.
- `Outcome`: recent failure/regression/partial outcomes.
- `Reflection`: latest and recent pattern reflections.
- `BehaviorRule`: active rules, high-confidence proposed rules separately.
- `Session`: current handoff and recent session continuity.

Ranking v1:

- Deterministic score, not LLM-only.
- Suggested weights:
  - explicit task/project/repo match: +0.30
  - file/path match: +0.20
  - semantic/keyword match: +0.20
  - recent and not stale: +0.10
  - success_count or rule confidence: +0.10
  - failed/regressed outcome match: +0.15
  - user-confirmed decision: +0.15
  - deprecated/rejected/stale: negative weight
- Cap each source type so one table cannot dominate the pack.
- Always include current task/project if available.
- Do not include proposed rules as active instructions.

Tests:

- Unit tests:
  - score ordering
  - budget trimming
  - per-type caps
  - stale/deprecated exclusion
  - empty-state output
  - provenance is present
- Store tests:
  - Postgres and SQLite if persistence is added
- MCP tests:
  - tool validation
  - JSON shape
  - nil-store behavior

Acceptance:

- A real wayneblacktea task produces a context pack with current GTD state,
  relevant decisions, relevant procedures/skills, and recent failure patterns.
- The pack is bounded and deterministic enough to test.
- The tool does not mutate memory unless `persist=true`.

Non-goals:

- Do not build graph-wide reasoning yet.
- Do not let the LLM decide all ranking.
- Do not auto-edit prompts or policy.

## Phase 2: Action Lifecycle And Evidence Chain

Purpose: close the loop from context to action to outcome.

Research mapping:

- Reflexion: feedback becomes future decision support through reflection.
- Voyager: execution errors and self-verification are part of skill improvement.
- OpenAI agent eval docs: traces reveal workflow-level failures.

Existing base:

- `work_sessions` is already the runtime unit for agent work.
- `start_work` already links tasks and marks them `in_progress`.
- `finish_work` already records completion summaries, artifacts, completed
  task IDs, and optional decisions.
- Completion-candidate detection already reads `work_sessions`.

Recommended v1 design: extend `work_sessions`, plus companion evidence tables
only where needed. Do not start by adding a parallel `agent_actions` domain.

Rationale:

- It keeps the Lead/GTD discipline path intact.
- It avoids duplicating `start_work`/`finish_work` semantics.
- It lets current completion-candidate and closeout code benefit from the new
  evidence chain.
- It is simpler for Dynamic Workflow intake: candidate branches become work
  session evidence, not separate workflow primitives.

Add or extend:

```text
work_sessions
- id uuid/text
- workspace_id nullable uuid/text
- repo_name text
- branch_name text
- objective text not null
- context_pack_id nullable uuid/text
- verification_status text nullable -- not_run|passed|failed|unknown
- verification_command text nullable
- verification_output_excerpt text nullable
- outcome_id nullable uuid/text
- final_result text nullable -- success|failure|partial|unknown|regressed
```

If verification evidence needs multiple rows, add:

```text
work_session_evidence
- id uuid/text
- workspace_id nullable uuid/text
- session_id uuid/text
- evidence_type text -- command|pr|ci|railway|manual_note
- status text -- passed|failed|unknown
- command text nullable
- artifact text nullable
- output_excerpt text nullable
- created_at timestamptz/text
```

No FK constraints. Use code-side lookups.

MCP tool changes:

```text
start_work
finish_work
list_recent_work_sessions
get_work_session_trace
```

`start_work` behavior:

- Optionally calls `assemble_context(persist=true)`.
- Links to task/project/repo/branch using existing task linkage.
- Returns context pack and session id.

`finish_work` behavior:

- Stores verification evidence.
- Calls or prepares `record_outcome`.
- If result is failure/partial/regressed, requires `analysis` or creates a
  pending evaluation prompt.
- Does not silently complete GTD tasks unless existing workflow rules allow it.

Only add a new `agent_actions` table later if one work session must contain
multiple independently evaluated actions with separate context packs. That is a
Phase 2b decision, not the default implementation.

Tests:

- Started work session can finish with success and create or link an outcome.
- Failure work session creates an outcome that future context packs include.
- Verification output is length-capped and redacted.
- Invalid task/session ids return tool errors.
- No mutation happens when finish validation fails.

Acceptance:

- A completed engineering task has this trace:

```text
task -> work_session -> context_pack -> verification evidence -> outcome
     -> optional evaluation/reflection -> proposed rule/skill/memory update
```

Non-goals:

- Do not replace `update_task`/`complete_task` immediately.
- Do not introduce `start_action`/`finish_action` before proving
  `work_sessions` cannot hold the evidence chain.
- Do not auto-merge PRs or act outside existing workflow policy.

## Phase 3: Memory Validity And Governance

Purpose: prevent stale or contradictory memory from harming future agents.

Research mapping:

- Zep/Graphiti: relationships are temporal; old facts may still matter but must
  not be treated as current.
- LangGraph docs: collections improve recall but create update/delete/search
  complexity.

Add metadata gradually:

Memory atoms:

- `confidence`
- `source_model`
- `extraction_version`
- `last_verified_at`
- `validity_status` -- active|stale|superseded|conflicted|rejected

Cross-memory links:

- `supports`
- `contradicts`
- `supersedes`
- `derived_from`
- `applied_in`

Start with code-side relation storage if a generic table is easier:

```text
memory_relations
- id
- workspace_id
- from_type
- from_id
- to_type
- to_id
- relation_type
- confidence
- evidence
- created_at
```

No FK constraints.

Governance jobs:

- Detect decisions superseded by newer decisions for same repo/topic.
- Detect active behavior rules with recent regressed outcomes.
- Detect high-recall knowledge that causes low usefulness feedback.
- Detect atoms promoted to knowledge but later contradicted.

MCP tools:

```text
list_memory_conflicts
mark_memory_stale
mark_memory_superseded
confirm_memory_relation
```

Proposal behavior:

- Scheduler may propose stale/superseded/conflict review.
- It must not silently delete or rewrite durable knowledge.

Tests:

- Conflict relation appears in `assemble_context` warnings.
- Superseded decisions are not selected as primary context unless explicitly
  requested.
- Deprecated behavior rules never become active instructions.

Acceptance:

- The system can answer: "Which remembered things might be stale, superseded,
  or contradictory?"
- Context packs surface conflicts instead of hiding them.

Non-goals:

- Do not implement a full general-purpose graph database.
- Do not rewrite all atoms into a new graph model in one PR.

## Phase 4: Skill Compiler

Purpose: convert repeated verified successes into reusable skills.

Research mapping:

- Voyager shows the practical pattern: environment feedback + self-verification
  + successful behavior -> reusable skill library.
- Reflexion shows failures should also become future guidance.

Inputs:

- successful `work_sessions`
- `outcomes` and `evaluations`
- procedural memories with high `success_count`
- behavior rules with high confidence and repeated successful application
- relevant memory atoms/reflections

Skill candidate output:

```json
{
  "name": "string",
  "description": "string",
  "triggers": ["..."],
  "steps": ["..."],
  "failure_modes": ["..."],
  "verification_checklist": ["..."],
  "source_atom_ids": ["..."],
  "source_outcome_ids": ["..."],
  "confidence": 0.72,
  "examples": [...]
}
```

Implementation:

- Add scheduler job or MCP tool:
  - `propose_skill_from_outcomes`
  - `compile_skill_candidate`
- Candidate should go through existing proposal/governance pattern.
- Existing `extract_skill`, `search_skills`, `use_skill`, and
  `update_skill_from_outcome` should remain the execution surface.

Tests:

- Two or more successful outcomes with similar repo/files/procedure produce one
  candidate, not duplicates.
- A failed use of a skill updates examples and lowers usefulness/ranking.
- Context assembler includes relevant skills but also includes known failure
  modes.

Acceptance:

- Repeated success becomes a reviewable skill candidate.
- Repeated failure becomes a warning or failure mode.
- Skills improve future context packs.

Non-goals:

- Do not auto-create `.agents/skills/**` or `.claude/skills/**` files.
- Do not let the system rewrite workspace policy.

## Phase 5: Provider Abstraction Completion

Purpose: make memory/learning features provider-independent.

Current gap:

- `cmd/server/main.go` routes classifier and concept reviewer through the LLM
  provider chain.
- summarizer, reflector, and atomizer still bind directly to Claude/Anthropic.

Work:

- Define generic interfaces in `internal/llm` or `internal/ai`:
  - JSON generation
  - summarization
  - reflection
  - atom extraction
  - atom consolidation
- Move provider-specific SDK construction into provider adapters only.
- Preserve cost recording with consistent caller/model names.
- Preserve prompt-injection boundaries around untrusted text.

Tests:

- Fake provider tests for summarizer/reflector/atomizer.
- Cost recorder called with expected caller/model.
- Provider-chain fallback works for memory/learning calls.
- No direct provider SDK construction in new memory/learning code.

Acceptance:

- wayneblacktea can run memory/learning features through configured provider
  chain.
- Claude-only behavior is either removed or explicitly documented as fallback.

Non-goals:

- Do not change model choices for unrelated features unless necessary.
- Do not add a new provider dependency without checking latest stable docs.

## Phase 6: Evaluation Harness

Purpose: prove that the system improves agent behavior, not just that code
compiles.

Research mapping:

- OpenAI agent eval docs emphasize traces, graders, datasets, and repeatability.
- For wayneblacktea, default evals must run without live model calls.

Add package:

- `internal/evals` or `internal/agenteval`
- fixtures under `testdata/`

Eval categories:

1. Context relevance
   - includes current task/project
   - includes latest relevant decision
   - excludes deprecated/superseded rules
2. Memory conflict handling
   - surfaces contradictions
   - does not silently pick old memory
3. Outcome learning
   - failed outcome appears in next similar context
   - successful repeated outcome can become skill candidate
4. Discipline
   - action lifecycle requires verification evidence
   - task closeout chain is complete
5. Budget behavior
   - context pack stays within budget
   - reports omitted item counts

Implementation:

- Deterministic tests as part of `task check`.
- Optional provider-backed eval command, not part of default gate.
- Store representative traces or synthetic actions as fixtures.

Acceptance:

- Every 2.0 phase adds at least one eval case.
- A regression in context ranking or outcome learning fails tests.

Non-goals:

- Do not depend on OpenAI/Anthropic/Groq network calls for default tests.
- Do not use eval score as the only review gate; code review still applies.

## Phase 7: Dashboard And Operator UX

Purpose: make the memory and learning system inspectable and correctable.

Views:

1. System Overview
   - active rules
   - recent reflections
   - recent failed/regressed outcomes
   - skill candidates
   - stale/conflict warnings
   - AI cost trend
2. Context Pack Inspector
   - selected memories
   - reasons and scores
   - omitted items due to budget
   - warnings
3. Learning Queue
   - proposed skills
   - proposed rules
   - proposed stale/superseded memory relations
4. Memory Graph Lite
   - selected atom and related links
   - promoted knowledge
   - conflict/supersession relations

Implementation:

- Add backend endpoints only after the MCP/domain layer is stable.
- Use existing dashboard density and navigation patterns.
- Do not add marketing-style pages.

Tests:

- API handler tests.
- TypeScript type alignment with backend JSON.
- Frontend lint/build.
- Integration QA if frontend-affecting backend changes exist.

Acceptance:

- User can inspect why a context item was selected.
- User can accept/reject proposed memory/skill/rule updates.
- UI does not hide autonomous changes.

## Recommended Delivery Order

Do not build all phases in one PR.

Recommended PR sequence:

1. `docs/2.0-readiness-doc-sync`
   - Phase 0 docs only.
2. `feature/context-pack-mvp`
   - contextpack domain + MCP tool + deterministic tests.
3. `feature/action-lifecycle`
   - action trace + persisted context pack + outcome wiring.
4. `feature/context-pack-evals`
   - eval fixtures for relevance, stale memory, failed outcome inclusion.
5. `feature/memory-governance-v1`
   - relation metadata + conflict/supersession warnings.
6. `feature/skill-compiler-v1`
   - candidate generation from verified outcomes.
7. `feature/provider-abstraction-memory`
   - summarizer/reflector/atomizer provider-chain migration.
8. `feature/learning-dashboard`
   - inspector and governance queue.

If Dynamic Workflow is used, treat generated branches/PRs as candidate artifacts.
Lead must intake, validate, and choose final PR shape before review/merge.

## File-Touch Map For Sprint Planning

Likely groups:

1. Docs/readiness:
   - `docs/*.md`
2. Context pack backend:
   - `internal/contextpack/**`
   - `internal/mcp/tools_contextpack.go`
   - `internal/mcp/server.go`
   - `internal/storage/server_stores.go` if persistence is added
   - `migrations/**` and `internal/storage/sqlite/schema.sql` if persistence is
     added
3. Action lifecycle:
   - `internal/action/**` or `internal/worksession/**`
   - `internal/mcp/tools_action.go`
   - `internal/outcome/**`
   - migrations if new table
4. Governance:
   - `internal/atom/**`
   - `internal/behaviorrule/**`
   - `internal/scheduler/**`
   - migrations
5. Skill compiler:
   - `internal/skill/**`
   - `internal/procedural/**`
   - `internal/scheduler/**`
   - MCP tools
6. Provider abstraction:
   - `internal/llm/**`
   - `internal/ai/**`
   - `cmd/server/main.go`
   - `internal/mcp/server.go`
7. Dashboard:
   - `internal/handler/**`
   - `web/src/**`

Do not split tasks that share migrations or `cmd/server/main.go` scheduler
wiring unless Lead explicitly sequences them.

## Quality Gate

Before claiming completion:

```bash
cd build && task check
```

Report actual command output evidence. "Passed" without output is not enough.

Additional gates:

- Frontend changes: lint/build and integration QA per workspace rules.
- Migration/schema changes: db-inspector review.
- MCP tool description or instruction changes: security review.
- Provider changes: fake-provider tests and cost-ledger assertions.
- Context ranking changes: deterministic eval fixtures.

## Review Expectations

At final PR stage, expect:

- reviewer
- security-engineer
- testing-reality-checker
- frontend review if `web/` changed
- db-inspector if migrations/schema changed
- integration-qa if frontend or frontend-affecting backend behavior changed

Security checks:

- no secrets in docs or fixtures
- no prompt-injection escape from untrusted memory text
- no destructive MCP tool without gate
- no unbounded retrieval/traversal/generation
- no hidden permanent mutation
- no FK constraints
- no provider key leakage into logs
- no raw verification output stored without cap/redaction

## Definition Of Done For wayneblacktea 2.0

wayneblacktea 2.0 is not complete when more memories can be stored.

It is complete when:

- Every meaningful agent action can start from a ranked, provenance-backed
  context pack.
- Every meaningful agent action can end with verification evidence and an
  outcome.
- Failed and regressed outcomes influence future context.
- Repeated verified successes become reviewable skill candidates.
- Rules and skills are applied only when active/relevant and are auditable.
- Stale or conflicting memory is surfaced instead of silently trusted.
- The user can inspect and correct the system's memory/learning decisions.
- The default quality gate includes deterministic behavior evals.
- The system remains proposal-gated, reversible, and aligned with workspace
  policy.
