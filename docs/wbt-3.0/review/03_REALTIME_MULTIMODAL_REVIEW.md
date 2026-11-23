---
id: WBT3-Q03
title: "Realtime and Multimodal Review"
status: reviewed
order: 1103
depends_on: []
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Realtime and Multimodal Review

## Provider reality

Current realtime providers differ:
- audio/text are broadly supported
- image support exists
- native continuous video is not universal
- live session duration/context behavior differs

## Correction

WBT core defines:
- audio session
- visual frame/image observations
- adaptive sampling
- provider adapter

rather than "one infinite video model connection."

## Web reality

- camera/mic require secure context/user permission
- screen sharing requires explicit user action and cannot assume persistent permission
- background UI timers may throttle
- WebRTC/WebSocket session behavior is distinct from server-side operator execution

## Verdict
PASS.
