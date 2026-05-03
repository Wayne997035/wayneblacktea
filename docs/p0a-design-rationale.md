# P0a Design Rationale

> Sprint: P0a Session Core (α/β/γ split)
> Baseline: After PR #49 merge (2026-05-02)
> Status: Frozen — α开工 2026-05-02

This document records why the P0a sprint is structured the way it is, captured at the end of three rounds of design discussion. It is not a how-to-build doc — that lives in the GTD tasks. This is the why-we-decided doc, so future readers (including future Claude sessions) can judge whether subsequent changes still align with the original intent.

---

## 1. Why P0a Exists (Core Problem)

| Dimension | Status before P0a | Pain Point |
|-----------|------------------|------------|
| Memory mechanism | 35 MCP tools record state (confirm_plan / log_decision / set_session_handoff …) | All "after-the-fact" — AI starts coding then *maybe* records |
| Hook coverage | SessionStart + PostToolUse + Stop | All fire **after** execution — cannot prevent |
| Enforcement | Zero | User must verbally remind "remember to save the plan" — violates the "do not rely on the model remembering instructions" principle |
| Result | AI frequently edits code without plan / decisions / GTD | User must continuously supervise; automation broken |

**One-line definition:** Upgrade wayneblacktea from "auto-memory after the fact" into "no active work session, no implementation" — a workflow state machine.

---

## 2. Three Rounds of Design Discussion

| Round | Proposal | Counter-arguments | Outcome |
|-------|----------|------------------|---------|
| **Round 1 (original)** | Single P0 PR doing it all: global guard hard-deny + confirm_plan rewrite + 4 new tools + 2 new binaries | Scope too large; global hook would block unrelated repos; confirm_plan breaking change; allow-list too narrow; no escape hatch; ignored Codex/Gemini | Sent back |
| **Round 2 (revised P0a)** | repo opt-in + observe→ask→enforce + deny-list + auto_start bool + git-hooks fallback + decay retention + bypass | Caught the 5 main risks but: 1) scope still too big, 2) decisions over-normalized via join table, 3) confirm_plan still gated by bool, 4) mode upgrade conditions undefined | Direction correct, details still off |
| **Round 3 (final)** | α/β/γ split + always-create session + no decisions FK + 6-condition readiness gate + throttled nudge | No remaining objections; three-way alignment | **Frozen — ready to ship** |

---

## 3. Key Design Decisions

| # | Decision | Final | Rejected alternatives | Reason for rejection |
|---|----------|-------|----------------------|---------------------|
| D1 | Go pkg name | `internal/worksession/` | `internal/session/` | Collides with existing session_handoff package |
| D2 | confirm_plan API | Always creates in_progress work_session, **no bool** | `auto_start: bool` parameter | wayneblacktea's only caller is the user; no API compatibility concern; optional behavior branches are *harder* for the model to learn, not easier |
| D3 | decisions↔session link | No FK; query at application layer using `(repo_name, created_at)` window | `decisions.work_session_id` FK or `work_session_decisions` join table | DB-level coupling is over-engineering; time-window query is sufficient |
| D4 | MCP tool naming | `start_work` / `get_active_work` / `checkpoint_work` / `finish_work` | `_work_session` suffix | 35 tools already; saves token cost; `_session` suffix would conflict with existing session_handoff vocabulary |
| D5 | observe → ask upgrade | 6-condition multi-gate (≥3 days + ≥5 sessions + ≥100 events + FP < 5% + quality_gate would_deny=0 + user confirm) | Fixed 7 days | Pure day-count can hit a low-activity week; multi-gate is pragmatic |
| D6 | `.wayneblacktea/config.json` in git | In .gitignore template, per-machine | Commit to git | Different machines may want different guard modes; commit creates cross-machine drift |
| D7 | `Task` matcher | Required in P0a-β | Defer to P0b | Sub-agent (`Task` tool) runs in independent process; parent's PreToolUse is the *only* interception point |
| D8 | `wbt doctor` full version | P0b | P0a | The 16-item check is a separate concern |
| D9 | bypass `--reason` | Mandatory | Warn-only | Auditable bypass requires actual audit content |
| D10 | git-hooks fallback | P0b | P0a | Codex/Gemini coverage is nice-to-have; validate P0a value first |
| D11 | enforce mode | After P0b | P0a wrap-up | Need ≥7 days of ask-mode data before flipping to enforce |
| D12 | observe-stage model nudge | Throttled (1/session, 1/30min/repo, only high-risk would_deny, <300 char) | Always-inject / fully-silent | Balance training effect vs context pollution |

---

## 4. PR Slicing (α / β / γ)

| PR | Name | Contents | Estimate | What user can do after merge |
|----|------|----------|----------|------------------------------|
| **P0a-α** | Session Core | 2 migrations (work_sessions + work_session_tasks) + `internal/worksession/` pkg + 4 MCP tools + confirm_plan integration (always creates session) + HTTP `/api/work-sessions/active` + decay retention | 4 days, ~2000 LoC | `confirm_plan` auto-creates active session; `get_active_work` checks status; `checkpoint_work` saves progress; `finish_work` closes |
| **P0a-β** | Guard Observe | 2 migrations (guard_events + guard_bypasses) + `cmd/wbt-guard` + `.wayneblacktea/config.json` marker + Bash deny-list classifier + matcher (Bash/Edit/Write/MultiEdit/**Task**) + observe-only (does not block) + bypass schema/CLI (mandatory reason) | 3 days | Guard starts collecting telemetry; user sees "what would have been blocked" |
| **P0a-γ** | Visibility + Ask | `cmd/wbt-prompt-hook` (unconditional session-state injection) + `wbt guard set-mode` + 6-condition readiness gate + observe throttled nudge + ask mode activation | 2 days | Model sees session state every prompt; dangerous operations enter `ask` |

Dependency: α → β → γ; not parallelizable.

---

## 5. What Got Better Each Round

| Dimension | Round 1 | Round 2 | **Round 3 (shipped)** |
|-----------|---------|---------|-----------------------|
| Scope | 1 giant PR | 1 large P0a PR | **3 independently shippable PRs** |
| Breaking change | confirm_plan rewrite | Adds auto_start bool | **No break + no flag** (semantics redefined as "always creates session") |
| Schema complexity | 4 tables (incl. decisions join) | 5 tables | **4 tables** (decisions FK dropped) |
| Cross-repo risk | Global hook blocks anything | Repo opt-in marker | Repo opt-in + file_path matching |
| Bash blocking | allow-list | deny-list | deny-list + 8-tier risk classification |
| Mode upgrade | Direct to enforce | 3-stage but no threshold | **6-condition readiness gate** |
| Codex/Gemini | Not mentioned | optional git-hooks in P0a | Deferred to P0b (validate P0a value first) |
| Sub-agent (`Task`) | Not mentioned | optional later | **Required in P0a-β** (only interception point) |
| Bypass | Env var only | 4 scopes + TTL | 4 scopes + TTL + **mandatory reason** |
| Mode nudge | Fully silent | Fully silent | **Throttled high-signal nudge** |

---

## 6. Risk Reduction Summary

| Risk | Round 1 outcome | Round 3 mitigation |
|------|----------------|--------------------|
| Misfires in chat-gateway / chatbot-go / etc. | Global hook always active | `.wayneblacktea/config.json` absent → fail-open |
| `git commit` / `npm install` / `go build` flagged dangerous | allow-list omits → blocked | deny-list defaults to allow; only known-dangerous patterns blocked |
| `Edit ~/Downloads/foo.md` while cwd in opted-in repo | matcher only checks cwd | matcher checks `tool_input.file_path` against repo root |
| `confirm_plan` existing callers broken | Direct rewrite → 100% break | Round 3: semantics redefined (only caller is user; zero external impact) |
| Mode stuck in observe forever (user forgets) | No threshold | 6-condition gate + `wbt doctor` shows "ready to upgrade" |
| Mode upgraded to enforce too early; dev frozen | Direct to enforce | enforce deferred to P0b; P0a max is `ask` |
| Sub-agent edits bypass guard | No `Task` matcher | P0a-β includes `Task` matcher |
| Guard panics and locks user out | Not addressed | Fail-open + log error; guard itself can never block |
| `guard_events` table explosion | Logged on every call | Default writes only `would_deny=true`; 30-day TTL via `task guard-prune` (see §6.1) |
| Decay prunes session-linked decisions | Not addressed | Decay retention rule: decisions class entirely retained |
| Model can't learn behavior in observe | Pure silence | Throttled high-signal nudge on would_deny events |
| Bypass abused | Single env var, no audit | Mandatory `--reason`, TTL expiration, `--dangerously-global` flag, `wbt doctor` WARN |

---

## 7. Why Now vs Defer

| Lens | Cost of deferring | Cost of doing now |
|------|------------------|-------------------|
| User dev rhythm | Continuously interrupted by "you forgot to save the plan" | 9 days work (4+3+2) across α/β/γ |
| wayneblacktea positioning | Stays at "after-the-fact" tier — undifferentiated from claude-mem etc. | Upgraded to "before-the-fact enforcement" — clear differentiation |
| Tech debt | More PostToolUse data accumulates *proving* "after-the-fact is insufficient" | Schema is still small, tools still few — cheapest moment to redefine semantics |
| Public release timing | Ship with the "AI forgets" defect | Complete the enforcement story before going public |

**Conclusion: now is the cheapest moment, and each sub-PR is independently reversible.**

---

## 6.1 30-day TTL Implementation

Round 1 review flagged the "30-day TTL" claim in §6 as undocumented. The implementation:

- **Mechanism**: Taskfile target `guard-prune` (`build/Taskfile.yml`) issues two `DELETE` statements via `psql`:
  - `DELETE FROM guard_events WHERE created_at < NOW() - INTERVAL '30 days';`
  - `DELETE FROM guard_bypasses WHERE expires_at IS NOT NULL AND expires_at < NOW() - INTERVAL '30 days';`
- **Cadence**: run from a host cron / Railway scheduled job once per day (operator concern, not in-repo automation).
- **Backend scope**: Postgres only. SQLite is single-tenant dev-local — no growth concern, no-op.
- **Why a Taskfile target, not a SQL migration**: Postgres `pg_cron` is not available on Aiven's free / hobby plans, and the platform has no built-in TTL primitive. A scheduled `DELETE` is the simplest mechanism that doesn't require an extension. If we ever migrate to a host where `pg_cron` is available, the same SQL can be wrapped in `cron.schedule(...)` without changing the rest of the stack.
- **Credentials redaction**: see `internal/guard/redact.go` — every `tool_input` JSON value is regex-scrubbed before INSERT (Stripe / GitHub / Slack / AWS / DSN / generic key=value patterns), so the audit trail does not become a credential reservoir even before the 30-day window kicks in.

---

## 8. Out of Scope for P0a (Explicit Non-Goals)

- Full `wbt doctor` with 16 health checks → P0b
- `memory_coverage_report` MCP tool + dashboard panel → P0b
- `wbt git-hooks install` (Codex/Gemini fallback) → P0b
- `enforce` mode activation → after P0b
- Local SQLite mirror + background Postgres sync → P0b
- `docs/security-and-privacy.md` formal document → P1
- Dashboard UI panels for active session / coverage / guard denials → P2
- `wbt backup` / `restore` / `export` / `forget` → P2
- Agent team mode (lead/engineer/reviewer/security/testing) → not now

---

## 9. Acceptance Criteria for P0a Completion

P0a is considered complete only when **all three** sub-PRs (α / β / γ) are merged and:

1. Guard activates only in opted-in repos. Unrelated repos (chat-gateway, chatbot-go, chat-web, etc.) are 100% unaffected.
2. `confirm_plan` calls always produce an active work session linked to the primary task.
3. `get_active_work` returns the correct active session via both MCP and HTTP.
4. `wbt-guard` supports `observe` and `ask` modes (enforce stays disabled in P0a).
5. Dangerous Bash without active session is asked-or-denied in opted-in repos in ask mode.
6. Quality-gate commands (`task check`, `go test`, `npm test`, `pytest`, etc.) are **never** blocked.
7. Per-call / TTL / scope bypass works and is audited (mandatory reason).
8. `wbt-prompt-hook` injects ≤ 3000 chars of session state every prompt.
9. Decay never prunes decisions (any) or completed work session final summaries.
10. `cd build && task check` passes with 0 issues for each sub-PR.
11. README / docs/ are consistent with shipped behavior.
