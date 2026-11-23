---
id: WBT3-950
title: "AI Coding Agent Guide"
status: frozen
order: 950
depends_on: [WBT3-002, WBT3-900]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# AI Coding Agent Guide

## Before code

Read:
1. repo `AGENTS.md`
2. current `docs/architecture.md`
3. this pack's locked decisions
4. subsystem spec
5. relevant existing package
6. current tests

## Do not invent a second subsystem

Before adding:
- context
- model provider
- eval
- scheduler
- work lifecycle
- risk classifier
- proposal flow

search existing repo first.

## PR design checklist

State:
- ownership
- new durable state
- new external side effects
- risk tier/effect
- failure behavior
- retry/idempotency
- trace
- rollback
- SQLite/Postgres parity
- tests/evals

## Forbidden shortcuts

- LLM call in React component with master API key
- LLM call directly in Echo handler
- AI loop directly in scheduler callback after Runtime exists
- external action directly from realtime browser code
- direct durable decision/rule/skill writes from model
- hidden screen/camera capture
- FK constraints
- editing merged migration
- unbounded model/tool loop
- storing private chain-of-thought

## Scope discipline

If a task needs Swift/ObjC to deliver a 3.0 core requirement:
stop and classify it as an external/native adapter gap instead of silently changing the project language boundary.
