---
id: WBT3-430
title: "Conversational Continuity"
status: frozen
order: 430
depends_on: [WBT3-120, WBT3-300]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Conversational Continuity

## Goal

User can say:

> "昨天那個問題後來怎樣？"

WBT should resolve the referent from durable structures, not only provider chat history.

## Sources

- current Presence Session
- recent runtime runs
- work sessions
- session handoff
- recent events
- project/task state
- outcomes

## Resolution

Use:
1. explicit current conversation reference
2. active work context
3. recent correlated run/event
4. semantic retrieval

If ambiguous:
- ask a precise clarification

## Provider reset

A new realtime provider session should be able to reconstruct useful continuity from WBT.

This is a primary reason provider conversation memory must not become authoritative.
