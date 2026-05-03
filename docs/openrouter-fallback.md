# OpenRouter fallback plan

Last verified: 2026-05-03.

This document describes how to add OpenRouter as an optional backup provider for
AI analysis features. It is not required for core MCP memory features.

## Status

- Phases 1-4 shipped: `internal/llm` provider abstraction + chain, OpenRouter
  client, migration of Discord `/analyze`, ActivityClassifier, and
  ConceptReviewer onto the abstraction. Backward compat preserved when only
  `CLAUDE_API_KEY` is set.
- Phase 5 deferred: Reflector / Summarizer / snapshot generator still bind
  Claude directly. They will move to the abstraction once the JSON tasks
  have soaked.
- Phase 6 deferred: optional installer AI setup wizard.
- Out of scope for this round: `internal/ai/embedder.go` (Gemini embeddings)
  remains unchanged — embeddings affect vector dimensions and stored search
  data, so a separate migration plan is required.

## Current state

Core features already work without any AI provider key:

- MCP memory tools: GTD, decisions, sessions, proposals, workspace context.
- Learning review CRUD: `get_due_reviews`, `submit_review`, `create_concept`.
- Claude Code MCP startup through `.mcp.json` and `wbt mcp`.

Existing optional AI integrations:

| Feature | Current key | Current provider | Required for core MCP memory |
|---------|-------------|------------------|------------------------------|
| Activity classification | `CLAUDE_API_KEY` | Anthropic Claude | No |
| Weekly concept review | `CLAUDE_API_KEY` | Anthropic Claude | No |
| Reflection / snapshots | `CLAUDE_API_KEY` | Anthropic Claude | No |
| Discord `/analyze` | `GROQ_API_KEY` | Groq | No |
| Knowledge embeddings | `GEMINI_API_KEY` | Gemini embedding API | No |

Important distinction: OpenRouter is a good fit for text generation and JSON
analysis. It should not replace `GEMINI_API_KEY` embeddings in the first pass,
because embeddings affect vector dimensions and stored search data.

## Verified OpenRouter facts

As of 2026-05-03:

- Chat completions are available at `POST https://openrouter.ai/api/v1/chat/completions`.
- Authentication uses `Authorization: Bearer <OPENROUTER_API_KEY>`.
- Request and response schemas are close to the OpenAI Chat API.
- OpenRouter supports `response_format` with JSON object / JSON schema modes.
- The OpenRouter-specific `models` array can be used for model fallback.
- `openrouter/free` automatically routes to an available free model.
- Specific free models use the `:free` suffix.
- Free models have lower limits: 20 requests/minute, 50 requests/day before
  purchasing at least 10 credits, and 1000 requests/day after that threshold.

Sources:

- https://openrouter.ai/docs/api/api-reference/chat/send-chat-completion-request
- https://openrouter.ai/docs/api/reference/overview
- https://openrouter.ai/docs/guides/routing/model-fallbacks
- https://openrouter.ai/docs/guides/routing/routers/free-models-router
- https://openrouter.ai/docs/api/reference/limits
- https://openrouter.ai/collections/free-models

## Product decision

OpenRouter should be optional and off by default.

Default install experience:

```text
wbt init
```

Only asks for:

1. SQLite or Postgres
2. Database path or DSN
3. Server port
4. `API_KEY`

No AI provider key is requested in the default wizard.

Optional AI setup should be a separate flow later:

```text
Enable optional AI analysis features? [y/N]
```

If the user answers `n` or presses Enter, skip all provider prompts.

If the user answers `y`, explain clearly:

```text
AI analysis features need a provider API key. You can press Enter at any prompt
to skip and keep memory-only mode.
```

Then ask provider-specific prompts. Empty input always cancels that provider and
returns to memory-only mode. A user who presses `y` and then regrets it should
not need to reinstall.

## Recommended env shape

Use provider-neutral routing variables plus provider-specific keys:

```env
# Core server auth; still required for HTTP API / dashboard.
API_KEY=...

# Optional AI generation provider.
# empty/off = memory-only mode
# openrouter = use OpenRouter for supported JSON analysis tasks
# groq = use Groq for supported JSON analysis tasks
# claude = use Anthropic Claude for supported JSON analysis tasks
AI_PROVIDER=

# Optional ordered provider fallback chain. First successful provider wins.
# Example: claude,openrouter,groq
AI_FALLBACK_PROVIDERS=

# OpenRouter.
OPENROUTER_API_KEY=
OPENROUTER_MODEL=openrouter/free

# Optional OpenRouter model fallback list. This is passed as the OpenRouter
# `models` array when set.
OPENROUTER_MODELS=

# Existing provider keys remain supported.
CLAUDE_API_KEY=
GROQ_API_KEY=
GEMINI_API_KEY=
```

Recommended default for early OpenRouter support:

```env
AI_PROVIDER=openrouter
OPENROUTER_API_KEY=...
OPENROUTER_MODEL=openrouter/free
```

Candidate free models for analysis fallback, verified from OpenRouter on
2026-05-03:

| Model id | Best fit | Notes |
|----------|----------|-------|
| `openrouter/free` | Default fallback router | Safest free default; OpenRouter chooses an available free model. |
| `nvidia/nemotron-3-super-120b-a12b:free` | Long-context analysis, agentic reasoning | 262k context on OpenRouter page; strong general backup candidate. |
| `openai/gpt-oss-120b:free` | Structured analysis, reasoning, general fallback | Good first specific model to try when availability is stable. |
| `z-ai/glm-4.5-air:free` | Fast general analysis | Useful secondary fallback. |
| `minimax/minimax-m2.5:free` | Productivity-style summarization and analysis | Good backup for Discord `/analyze` and review suggestions. |
| `poolside/laguna-m.1:free` | Code-heavy analysis | Better fit for engineering/code context. |
| `poolside/laguna-xs.2:free` | Lightweight code-heavy analysis | Smaller coding fallback. |
| `google/gemma-4-31b-it:free` | General multilingual analysis | Good conservative fallback with broad language support. |
| `openai/gpt-oss-20b:free` | Lower-latency fallback | Smaller backup if larger free models are slow or rate-limited. |
| `tencent/hy3-preview:free` | Agentic reasoning, code generation | Short-lived: OpenRouter page says going away 2026-05-08. Do not pin as a long-term default. |
| `inclusionai/ling-2.6-1t:free` | Fast large-model reasoning | Short-lived: OpenRouter page says going away 2026-05-07. Do not pin as a long-term default. |

For more deterministic behavior, prefer a specific model list instead of
`openrouter/free`. Pick current model IDs from OpenRouter's free models page
at implementation time:

```env
OPENROUTER_MODEL=<specific-model-id>:free
OPENROUTER_MODELS=<specific-model-id>:free,<backup-model-id>:free,openrouter/free
```

Reasonable first fallback chain:

```env
OPENROUTER_MODEL=openrouter/free
OPENROUTER_MODELS=openai/gpt-oss-120b:free,nvidia/nemotron-3-super-120b-a12b:free,z-ai/glm-4.5-air:free,minimax/minimax-m2.5:free,openrouter/free
```

Do not pin the public installer to a specific free model. Free model availability
changes frequently; docs and examples can show `openrouter/free` as the safest
zero-cost default.

## Provider abstraction

Add a small internal abstraction for text generation tasks instead of wiring
OpenRouter directly into each feature.

Suggested package:

```text
internal/llm/
  client.go
  chain.go
  openrouter.go
  groq.go
  claude.go
```

Suggested interface:

```go
type JSONRequest struct {
    Task        string
    System      string
    User        string
    MaxTokens   int
    Temperature float64
    JSONMode    bool
}

type JSONClient interface {
    CompleteJSON(ctx context.Context, req JSONRequest) (string, error)
}
```

The feature-specific packages still own their prompts and result validation.
The provider layer only sends messages and returns raw model text.

## Fallback behavior

There are two fallback layers:

1. Application provider chain:
   `AI_FALLBACK_PROVIDERS=claude,openrouter,groq`
2. OpenRouter model chain:
   `OPENROUTER_MODELS=model-a,model-b,openrouter/free`

Design intent (important): both layers exist for *availability / per-model
rate limiting / latency* failover. Neither layer is a way to multiply the
daily free quota. The OpenRouter model chain is sent as the OpenRouter
`models` array in a single HTTP call (server-side routing → one billing
event), not iterated client-side. The application provider chain crosses
*different vendors* (Claude / OpenRouter / Groq) — different billing
entities — so iterating across them is fine, but never iterate across
multiple keys at the same vendor expecting more quota.

Application-level behavior:

- Try providers in order.
- Retry next provider on timeout, network error, HTTP 429, HTTP 5xx, empty
  response, or invalid JSON.
- Do not retry on missing key or disabled provider.
- Log only provider name, model id, status code, latency, and sanitized error.
  Never log API keys or full prompt bodies.

OpenRouter-level behavior:

- If `OPENROUTER_MODELS` is set, send `models: [...]`.
- Otherwise send `model: OPENROUTER_MODEL`.
- Keep `OPENROUTER_MODEL=openrouter/free` as the low-cost default.
- Store the returned `model` field in debug logs when available, because
  `openrouter/free` may select different models per request.

## Feature wiring order

Implement in small steps.

1. Add `internal/llm` and OpenRouter client.
2. Move Discord `/analyze` from Groq-only to the provider abstraction.
3. Move activity classifier from Claude-only to the provider abstraction.
4. Move weekly concept review from Claude-only to the provider abstraction.
5. Move reflection / snapshots after the JSON tasks are stable.
6. Add optional installer AI setup only after runtime behavior is tested.

Do not change Gemini embeddings in this phase.

## OpenRouter request shape

Minimum request:

```json
{
  "model": "openrouter/free",
  "messages": [
    {"role": "system", "content": "Return ONLY valid JSON."},
    {"role": "user", "content": "..." }
  ],
  "temperature": 0.2,
  "max_tokens": 1024,
  "response_format": {"type": "json_object"}
}
```

Fallback request:

```json
{
  "models": [
    "<specific-model-id>:free",
    "<backup-model-id>:free",
    "openrouter/free"
  ],
  "messages": [
    {"role": "system", "content": "Return ONLY valid JSON."},
    {"role": "user", "content": "..." }
  ],
  "temperature": 0.2,
  "max_tokens": 1024,
  "response_format": {"type": "json_object"}
}
```

Headers:

```text
Authorization: Bearer ${OPENROUTER_API_KEY}
Content-Type: application/json
HTTP-Referer: https://github.com/Wayne997035/wayneblacktea
X-OpenRouter-Title: wayneblacktea
```

The attribution headers are optional, but useful and non-secret.

## Security rules

- Keep all API keys in env vars only.
- Never write provider keys into `.mcp.json`.
- Never require provider keys during `wbt init`.
- Continue wrapping untrusted content in explicit boundary markers.
- Preserve existing JSON allowlists before writing AI output to DB.
- Use request body limits and response body limits.
- Set per-request timeouts.
- Redact URLs and headers before returning provider errors.
- Treat model output as untrusted until parsed and validated.

## Tests

Add tests before enabling this publicly:

- OpenRouter client sends bearer auth and JSON mode.
- OpenRouter client redacts auth on request failure.
- OpenRouter client parses `choices[0].message.content`.
- OpenRouter client returns a typed retryable error for 429 / 5xx.
- Provider chain skips providers with missing keys.
- Provider chain falls back when primary returns invalid JSON.
- Discord `/analyze` still returns the same `AnalysisResult` shape.
- Activity classifier still returns zero-value result on provider failure.
- Concept reviewer still rejects unknown statuses and invalid UUIDs.
- `wbt init` does not prompt for `OPENROUTER_API_KEY` by default.
- Optional AI setup allows Enter to skip after answering `y`.

Quality gate after implementation:

```bash
cd build && task check
```

## Acceptance criteria

- Fresh install remains memory-only and does not ask for AI keys.
- Users can enable OpenRouter by setting only:
  `AI_PROVIDER=openrouter`, `OPENROUTER_API_KEY`, and optionally
  `OPENROUTER_MODEL`.
- Missing OpenRouter key disables OpenRouter cleanly.
- `GROQ_API_KEY`, `CLAUDE_API_KEY`, and `GEMINI_API_KEY` remain backward
  compatible.
- `GEMINI_API_KEY` remains only for embeddings unless a separate embedding
  migration is designed.
- All AI features degrade gracefully when every provider is missing or failing.

## Open questions

- Should `AI_PROVIDER=openrouter` replace Groq for Discord `/analyze`, or should
  `/analyze` keep `GROQ_API_KEY` as the default until a major release?
- Should OpenRouter fallback be available in `cmd/server` only, or also in
  standalone `wayneblacktea-mcp` advanced automation?
- Should provider selection be global, or should each feature support overrides
  such as `ANALYZE_PROVIDER=openrouter` and `CLASSIFIER_PROVIDER=claude`?

Conservative first answer: global provider plus optional fallback chain is enough
for the first implementation. Add per-feature overrides only after there is a
real need.
