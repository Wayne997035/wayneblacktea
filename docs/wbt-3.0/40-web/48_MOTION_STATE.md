---
id: WBT3-580
title: "Motion and Runtime State"
status: frozen
order: 580
depends_on: [WBT3-570, WBT3-300]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Motion and Runtime State

## Core visual mapping

| Runtime state | Motion |
|---|---|
| READY | slow breathing |
| LISTENING | audio-reactive outer ring |
| OBSERVING | scanning aperture / teal sweep |
| RETRIEVING | nodes converge inward |
| REASONING | controlled violet inner motion |
| ACTING | outward pulse |
| VERIFYING | orbit/lock animation |
| WAITING_APPROVAL | amber hold |
| SPEAKING | waveform/ring modulation |
| DEGRADED | low-energy asymmetric pulse |
| ERROR | brief restrained red signal |

## Requirements

- animation derives from real backend/client state
- no `requestAnimationFrame` work when unnecessary
- respect `prefers-reduced-motion`
- no critical information conveyed only by motion/color
- state transition events are testable

## Background tab

Do not use animation timers as Runtime scheduling.
Visual animation may pause when hidden; server/runtime state remains authoritative.
