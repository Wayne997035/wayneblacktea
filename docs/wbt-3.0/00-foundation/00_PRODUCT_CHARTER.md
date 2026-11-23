---
id: WBT3-000
title: "Product Charter"
status: frozen
order: 10
depends_on: []
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Product Charter

## Mission

WBT 3.0 should become the user's direct AI collaboration surface and durable personal intelligence layer.

The user should no longer need this architecture as the normal path:

```text
User → Claude Code / another assistant → MCP → WBT
```

The normal interactive path becomes:

```text
User
 ↕ text / voice / image / camera / screen
WBT Presence
 ↕
WBT Runtime
 ↕
Context + Models + Capabilities + World State
```

External AI agents may still use WBT over MCP, but they are clients—not WBT's brain.

## Core collaboration promise

For a meaningful request WBT should be able to:

1. Understand what the user is asking.
2. Retrieve the relevant personal/work context.
3. Inspect current observable state when needed.
4. Distinguish observation from inference.
5. Reason using an appropriate model.
6. Ask for missing evidence when confidence is insufficient.
7. Use approved capabilities.
8. Verify the result.
9. Record evidence/outcomes where appropriate.
10. Make the learned result available to future work.

## Example end state

User says:

> 「WBT，幫我看這東西為什麼不能用。」

The user points a camera at a device.

WBT may:

```text
capture observation
→ identify visible object/labels
→ notice uncertainty
→ request a closer/back-side view
→ OCR model/part number
→ query relevant knowledge/web/tool
→ relate it to the user's current project
→ propose diagnostic step
→ observe measurement/result
→ narrow diagnosis
→ record outcome if user chooses
```

This is a collaboration loop, not a one-shot visual answer.

## Product posture

WBT is:

- competent
- direct
- context-aware
- evidence-seeking
- interruptible
- traceable
- bounded by authority
- comfortable saying "I don't have enough evidence"

WBT is not designed as an emotional dependency product.

## Fiction references

### E.V.
Reference for:
- direct natural conversation
- continuous context
- sensor-assisted analysis
- proactive alerts
- real-world tool/device use
- closed-loop diagnosis

### TARS / CASE
Reference for:
- crew-member / mission-partner posture
- operational reliability
- concise communication under pressure
- adjustable interaction style
- personality that serves work instead of replacing work

### OpenJarvis
Reference for:
- tool loop
- operators
- model/inference abstraction
- tracing
- loop guards
- persistent agent execution

None of these references override WBT's stronger structured state and governance model.
