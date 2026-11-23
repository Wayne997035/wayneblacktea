---
id: WBT3-810
title: "Existing Component Evolution Map"
status: frozen
order: 810
depends_on: [WBT3-003, WBT3-800]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Existing Component Evolution Map

## contextpack

Add optional read ports for:
- world beliefs
- recent runtime outcome/run summary
- operator state

Do not make all new ports mandatory on day one; use versioned constructor/options or a higher-level compiler wrapper to avoid a flag-day migration.

## internal/llm

Keep `JSONClient`.
Add richer generation/realtime capability contracts.
Migrate direct provider usages gradually.

## internal/evals

Add:
- runtime loop grader
- capability-selection grader
- policy grader
- attention-noise grader
- world-state freshness/conflict grader
- active-perception grader
- multimodal fixture grader

## scheduler

Categorize current jobs:

### remain deterministic scheduler
- prune/TTL
- simple maintenance
- fixed notifications without reasoning

### candidate operator migration
- jobs that use LLM reasoning
- pattern detection with multi-step analysis
- proactive project/incident analysis

## web

Add new shell/nav first.
Preserve route compatibility.
