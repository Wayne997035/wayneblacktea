---
id: WBT3-590
title: "Web Accessibility and Performance"
status: frozen
order: 590
depends_on: [WBT3-570]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Web Accessibility and Performance

## Accessibility

- keyboard navigation
- focus-visible
- ARIA labels for media controls
- transcript accessible without animation
- state text equivalent to orb color/motion
- contrast AA minimum
- reduced motion
- captions/transcription for voice

## Performance targets

- existing CRUD surfaces stay lightweight
- Presence shell first interactive without loading graph/3D bundles
- lazy-load Mind graph / heavy visualizations
- adaptive camera resolution/frame rate
- no full-resolution camera frames stored in React state history
- use object URLs / controlled buffers

## Bundle policy

Do not add a large 3D dependency just for visual decoration.
A dependency must provide measurable UX value.

## Failure UX

Network/provider failures show:
- what failed
- what still works
- retry/degraded option

Avoid generic "Something went wrong" on Runtime operations.
