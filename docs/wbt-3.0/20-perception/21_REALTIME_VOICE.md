---
id: WBT3-310
title: "Realtime Voice"
status: frozen
order: 310
depends_on: [WBT3-300, WBT3-130]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Realtime Voice

## Target behavior

- natural turn-taking
- user interruption / barge-in
- low-latency first audio
- clear indication when WBT is listening/speaking
- automatic recovery from transient provider disconnect

## Two supported patterns

### Native realtime provider
Audio ↔ realtime multimodal model.

Preferred for conversational latency.

### Composed fallback
STT → Runtime text/deep model → TTS.

Useful for provider independence and graceful degradation.

## Deep analysis delegation

Voice shell should not monopolize all reasoning.

A complex question can invoke:

```text
delegate_analysis
→ backend Runtime Run
→ full ContextPack
→ stronger model/tools
→ structured result
→ realtime voice presents it
```

## Transcript semantics

Store only what product policy chooses:
- user-visible transcript may be retained optionally
- durable knowledge does not arise automatically from transcript
- private audio bytes are not durable by default

## Interruption

When user begins speaking while WBT is speaking:
- stop/duck playback immediately
- mark previous model output interrupted
- do not treat unheard tail as user-confirmed content

## Operational profile

Voice style follows Collaboration Partner profile, not emotional-performance goals.
