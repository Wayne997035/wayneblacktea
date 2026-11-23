---
id: WBT3-350
title: "Screen Perception"
status: frozen
order: 350
depends_on: [WBT3-300, WBT3-320]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Screen Perception

## Web rule

Browser screen capture must be initiated by an explicit user action and treated as session-scoped.

Do not promise persistent permission.

## Use cases

- terminal error diagnosis
- browser/admin UI assistance
- IDE/code error inspection
- diagram/document analysis
- guided troubleshooting

## Pipeline

```text
user starts share
→ source selection
→ frame sampler
→ optional OCR/VLM
→ observation
→ Runtime + current project ContextPack
→ response / active perception
```

## Privacy

UI always displays:
- source currently shared
- active capture state
- stop button
- whether frames are sent to a cloud provider

## Data minimization

- adaptive frame rate
- crop/region capture when possible
- do not persist frames by default
- redact/separate secrets when deterministic detection is possible

## Desktop client

A Go/React desktop shell may expose the same web capture path where supported.

Deep native macOS capture that requires non-Go/TS helper code is outside the 3.0 core package.
