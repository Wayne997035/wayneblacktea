---
id: WBT3-570
title: "Visual Design System — Precision Futurism"
status: frozen
order: 570
depends_on: [WBT3-500]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Visual Design System — Precision Futurism

## Direction

```text
85% professional precision instrument
15% cinematic technology
```

Avoid:
- random HUD rings
- meaningless CPU graphs
- excessive neon
- fake "AI thinking" graphics

## Existing palette evolution

Current WBT:
- dark navy base
- cyan
- purple

Keep brand continuity but deepen contrast.

Suggested dark tokens:

```text
void          #05090F
surface       #09111D
elevated      #0D1826
panel         #101E2E
border        rgba(105,180,220,.18)

energy-cyan   #57D7FF
reason-violet #9277FF
observe-teal  #45E0C1
approval      #F4B860
danger        #FF667A

text-primary  #EAF7FF
text-muted    #7F9DB2
```

Exact values must pass contrast checks before merge.

## Meaningful visual semantics

- cyan: ready / primary interaction
- teal: observation/perception
- violet: reasoning/context
- blue pulse outward: action
- amber: confirmation
- red: failure / blocked

## Core/Orb

Implement first with CSS/SVG/Canvas 2D.

Three.js/react-three-fiber is optional, not a prerequisite.

## Texture

Allowed:
- subtle grid
- low-opacity noise
- radial illumination
- glass border
- restrained bloom

Motion must encode system state.
