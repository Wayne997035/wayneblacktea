---
id: WBT3-003
title: "Current-state Codebase Audit"
status: frozen
order: 13
depends_on: [WBT3-002]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Current-state Codebase Audit

Baseline: `75ce929c301090f0d1e742591b311e90b712c5fe` (`master`, observed 2026-08-25).

## Existing strengths that MUST be preserved

### Durable domains

Current architecture documents 22 bounded contexts and 24 store-owning packages, including:

- GTD
- Decisions
- Knowledge
- Learning
- Sessions
- Proposals
- Workspace
- Architecture / status snapshots
- Memory atoms
- Outcomes/evaluations
- Reflections
- Behavior rules
- Skills
- Procedural memories
- Playbooks
- Watchdog/Discipline/Guard
- AI cost
- Vision
- Work sessions
- Completion candidates
- Merged PR observations

Reference: `docs/architecture.md`.

### Context compiler precursor

`internal/contextpack` already:
- retrieves multiple memory/domain types
- scores deterministically
- caps per type
- trims to a character budget
- records reasons/provenance/warnings

Do not build another retrieval stack.

### Work lifecycle

`internal/worksession` already has:
- active work session
- task linkage
- branch
- verification status
- verification command/output excerpt
- final result
- evidence rows
- optional outcome link
- redact-before-cap controls

3.0 Runtime must link to this when appropriate, not replace it.

### LLM provider abstraction

`internal/llm` already has:
- provider-neutral JSON completion contract
- provider chain
- Claude
- Groq
- OpenAI-compatible
- OpenRouter
- timeout / retry classification
- no prompt/body/authorization logging rule

3.0 must expand this package instead of adding a competing ModelGateway package.

### Evals

`internal/evals` already includes deterministic graders for:
- context relevance
- budget
- conflict
- discipline
- outcome learning
- provider eval support

Extend it.

### Scheduler

Current architecture documents 33 named jobs.

Do not create a second cron system.
Time-based triggers continue to originate from scheduler.

### Risk / guard

`internal/guard` already has T0–T7 classification, command parsing, unknown-tool conservative handling and bypass/event audit.

Reuse the taxonomy/knowledge, but do not reuse observational fail-open semantics for action authorization.

### Auth

Browser SPA currently receives an httpOnly session cookie:
- HMAC signed
- SameSite Strict
- Secure in production/TLS
- 24 hour TTL
- raw API key not exposed to SPA after session creation

Presence/realtime bootstrap should build on authenticated browser sessions.

### Web

Current stack:
- React 19
- TypeScript 5.9
- Vite 7
- Tailwind CSS 4
- TanStack Query
- Zustand
- React Router
- Lucide
- Vitest

Current shell is a classic 240px dashboard sidebar.
Current `/` is a dashboard card grid.

The frontend stack is sufficient. The information architecture needs redesign, not framework replacement.

## Existing 2.0 plan conflict

`docs/internal/wayneblacktea-2.0-development-prompt.md` explicitly states 2.0 is not a new autonomous agent framework.

3.0 supersedes that **product target only**.

Its engineering constraints remain valuable:
- no hidden permanent mutation
- bounded retrieval/generation
- dual-backend parity
- no FKs
- deterministic tests
- proposal-gated learning
