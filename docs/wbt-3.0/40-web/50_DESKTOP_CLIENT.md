---
id: WBT3-600
title: "Optional Desktop Client"
status: frozen
order: 600
depends_on: [WBT3-500]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Optional WBT Desktop Client

## Naming

`WBT Desktop Client`

It is a client/control surface, not a separate personality or relationship product.

## Purpose

Package the same collaboration surface as a desktop application when useful.

## Allowed core implementation

Go + React/TypeScript.

A Go webview framework such as Wails is an acceptable implementation candidate because it preserves the language boundary.

## Benefits

- dedicated app window
- OS-level launch/distribution
- stable localhost/server lifecycle
- camera/microphone through webview where platform supports it
- notifications if framework/platform supports them

## Not guaranteed by 3.0 core

- persistent screen recording permission
- macOS ScreenCaptureKit bridge
- Accessibility automation
- wake word while app is closed
- platform-specific Swift helper

If one of these requires another native language, it belongs to a future adapter project.

## Architecture

Desktop Client must use the same:
- Presence protocol
- Runtime backend
- Capability policy
- auth model
- React surface

No second AI process.
