---
id: WBT3-500
title: "Web 3.0 Information Architecture"
status: frozen
order: 500
depends_on: [WBT3-100, WBT3-300]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Web 3.0 Information Architecture

## Current problem

Current navigation is a domain CRUD list:
Dashboard / GTD / Workspace / Decisions / Knowledge / Reviews / Calendar / ...

Useful, but it communicates:
> manage WBT's database

3.0 must communicate:
> work with WBT

## Primary nav

```text
Presence
World
Work
Mind
Operations
Control
Explore
```

## Mapping

### Presence
Direct interaction.

### World
Current entities/state/events/observations.

### Work
GTD, workspace, calendar, active work session.

### Mind
Decisions, knowledge, rules, skills, outcomes, reflections.

### Operations
Runtime runs, operators, traces, automation health, cost.

### Control
Autonomy, providers, permissions, privacy, interaction profile.

### Explore
Legacy/direct CRUD pages.

## Route proposal

```text
/                    Presence
/world
/work
/mind
/operations
/control
/explore

/explore/gtd
/explore/workspace
/explore/decisions
...
```

During migration retain legacy route aliases/bookmarks.
