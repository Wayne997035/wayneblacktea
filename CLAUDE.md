# wayneblacktea CLAUDE.md

## Session Handoff Rule (MUST)

Whenever the user expresses intent to continue work in a future session
(e.g. "明天繼續做 XX", "下次繼續", "tomorrow I'll work on XX"), you MUST:

1. Call the `set_session_handoff` MCP tool BEFORE responding
2. Pass: intent (what to continue), repo_name (if applicable), context_summary (current state)

This is a mandatory protocol, not optional.

## Session Start Rule (MUST)

At the start of every session, call `get_today_context` to load current project priorities
and any pending session handoff. Announce what you found before asking what to work on.
