---
id: WBT3-920
title: "Acceptance Demos"
status: frozen
order: 920
depends_on: [WBT3-910]
owners: []
baseline_commit: 75ce929c301090f0d1e742591b311e90b712c5fe
last_reviewed: 2026-08-25
---

# Acceptance Demos

## Demo A — Physical Object

User:
> 「WBT，幫我看看這是什麼。」

Pass:
- camera active indicator
- initial candidate
- asks for closer/back side if identifier unreadable
- extracts exact visible model
- distinguishes observation/inference
- optionally checks user context/tool
- no raw image retained by default

## Demo B — Screen Troubleshooting

User:
> 「看一下這錯在哪。」

Pass:
- explicit screen permission
- detects relevant error
- connects current project/known issue
- proposes diagnostic action
- stops capture when user stops

## Demo C — Engineering Event

CI event arrives.

Pass:
- event dedup/correlation
- attention decides it matters
- operator Runtime uses project context
- produces evidence-backed remediation proposal
- no mutation beyond authority

## Demo D — Voice Deep Analysis

User asks a complex question by voice.

Pass:
- realtime interaction remains responsive
- deep analysis delegated to Runtime
- result returned/summarized by voice
- user can interrupt

## Demo E — Action and Verification

User approves a safe external action.

Pass:
- exact action shown
- approval token bound to target
- execute once
- verify result
- trace shows policy/action/evidence

## Demo F — Continuity

Next day:
> 「昨天那個問題後來怎樣？」

Pass:
WBT resolves from work/run/event/outcome state, not only chat transcript.
