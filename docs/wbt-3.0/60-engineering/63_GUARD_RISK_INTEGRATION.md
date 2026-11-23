---
id: WBT3-830
title: "Guard and Risk Integration"
status: frozen
order: 830
depends_on: [WBT3-140, WBT3-150]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Guard and Risk Integration

## Existing asset

`internal/guard` has T0–T7 tool/command classification.

## Problem

It is oriented around Claude Code PreToolUse observation and has fail-open persistence behavior.

3.0 needs canonical risk semantics usable by Capability Policy.

## Recommended migration

Introduce:

```text
internal/risk
```

with the canonical Tier type/constants.

Maintain source compatibility via alias/adaptation in `internal/guard`.

Do not rewrite all guard logic at once.

## Risk vs effect

Risk tier and effect class are separate.

Example:

```text
git commit
effect = WRITE_REVERSIBLE
risk   = T3

gh pr merge
effect = WRITE_EXTERNAL
risk   = T6
```

## Unknown action

Unknown/unparseable action → T7.

For autonomous external action, T7 is denied.

## Failure semantics

Guard event logging may remain best-effort.
Authorization may not.
