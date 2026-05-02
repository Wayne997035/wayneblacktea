# wayneblacktea HTTP API

All `/api/*` routes require auth. Replace `https://your-host` and `YOUR_API_KEY` throughout.

## Authentication

**Session cookie (browser):** `POST /api/session` with `X-API-Key: YOUR_API_KEY` header → returns `wbt_session` cookie.

**Bearer token (scripts/API):** `Authorization: Bearer YOUR_API_KEY` on every request.

---

## Endpoint reference

| Method | Path | Auth | Rate limit | Description |
|--------|------|------|-----------|-------------|
| GET | `/health` | No | — | Server liveness: `{"status":"ok"}` |
| POST | `/api/session` | X-API-Key | — | Issue browser session cookie |
| GET | `/api/context/today` | Yes | — | Goals + projects + weekly progress + handoff |
| GET | `/api/goals` | Yes | — | Active goals ordered by due date |
| POST | `/api/goals` | Yes | — | Create goal |
| GET | `/api/projects` | Yes | — | Active projects |
| POST | `/api/projects` | Yes | — | Create project |
| GET | `/api/projects/:id` | Yes | — | Project + recent decisions |
| PATCH | `/api/projects/:id/status` | Yes | — | Update project status |
| GET | `/api/projects/:id/tasks` | Yes | — | Tasks for a project |
| POST | `/api/tasks` | Yes | — | Create task |
| PATCH | `/api/tasks/:id/status` | Yes | — | Update task status |
| PATCH | `/api/tasks/:id/complete` | Yes | — | Complete task + record artifact |
| GET | `/api/decisions` | Yes | — | List decisions (`repo_name`, `project_id`, `limit`) |
| POST | `/api/decisions` | Yes | — | Log a decision |
| GET | `/api/knowledge` | Yes | — | List knowledge (`limit`, `offset`) |
| POST | `/api/knowledge` | Yes | — | Save knowledge item |
| GET | `/api/knowledge/search` | Yes | — | Full-text + vector search (`q`, `limit`) |
| GET | `/api/proposals/pending` | Yes | 10/min | Pending proposals |
| POST | `/api/proposals/:id/confirm` | Yes | 10/min | Accept or reject proposal |
| GET | `/api/search` | Yes | 20/min | Cross-domain search (`q`) |
| GET | `/api/dashboard/stats` | Yes | 30/min | Aggregate counts |
| GET | `/api/dashboard/recent-decisions` | Yes | 30/min | Recent decisions |
| GET | `/api/dashboard/active-projects` | Yes | 30/min | Active projects + task counts |
| GET | `/api/dashboard/weekly-progress` | Yes | 30/min | This week's task completion |
| GET | `/api/dashboard/pending-knowledge-proposals` | Yes | 30/min | Pending knowledge proposals |
| GET | `/api/learning/reviews` | Yes | — | Concepts due for review |
| POST | `/api/learning/reviews/:id/submit` | Yes | — | Submit FSRS review rating |
| POST | `/api/learning/concepts` | Yes | — | Create concept + schedule |
| GET | `/api/learning/suggestions` | Yes | — | Learning suggestions |
| POST | `/api/learning/from-knowledge` | Yes | — | Create concept from knowledge item |
| POST | `/api/activity` | Yes | 30/min | Log activity entry |
| POST | `/api/auto-handoff` | Yes | 5/min | Create session handoff (Stop hook) |
| GET | `/api/workspace/repos` | Yes | — | Tracked repositories |
| POST | `/api/workspace/repos` | Yes | — | Create/update repository |
| GET | `/api/session/handoff` | Yes | — | Latest unresolved handoff |
| POST | `/api/session/handoff` | Yes | — | Create session handoff |

---

## Request bodies

### POST /api/goals

```json
{"title": "Ship v2", "area": "engineering", "description": "...", "due_date": "2026-12-31T00:00:00Z"}
```

`title` and `area` required. `due_date` in RFC3339.

### POST /api/projects

```json
{"name": "sprint-5", "title": "Sprint 5", "area": "engineering", "priority": 1, "goal_id": "UUID"}
```

`name` (unique slug), `title`, `area` required. `priority` 1–5.

### PATCH /api/projects/:id/status

```json
{"status": "completed"}
```

Values: `active` `completed` `archived` `on_hold`

### POST /api/tasks

```json
{"title": "Write runbook", "project_id": "UUID", "priority": 2, "importance": 1, "context": "..."}
```

`title` required. `priority` 1–5 (execution order). `importance` 1=high 2=medium 3=low.

### PATCH /api/tasks/:id/status

```json
{"status": "in_progress"}
```

Values: `pending` `in_progress` `cancelled`

### PATCH /api/tasks/:id/complete

```json
{"artifact": "https://github.com/your-org/repo/pull/42"}
```

### POST /api/decisions

```json
{
  "title": "Use pgvector", "context": "Need semantic search", "decision": "Adopt pgvector",
  "rationale": "Avoids separate vector DB", "repo_name": "wayneblacktea", "alternatives": "Pinecone"
}
```

`title`, `context`, `decision`, `rationale` required.

### POST /api/knowledge

```json
{"type": "til", "title": "Go generics tip", "content": "...", "url": "https://...", "tags": "go,generics"}
```

`type` and `title` required. Types: `article` `til` `bookmark` `zettelkasten`.

Response includes `concept_proposal_id` when a spaced-repetition card is auto-proposed.

### POST /api/proposals/:id/confirm

```json
{"action": "accept"}
```

`action`: `accept` (materialises entity) or `reject`.

### POST /api/learning/reviews/:id/submit

```json
{"rating": 3}
```

Ratings: 1=Again 2=Hard 3=Good 4=Easy.

### POST /api/learning/concepts

```json
{"title": "FSRS algorithm", "content": "An open scheduler...", "tags": "learning"}
```

### POST /api/learning/from-knowledge

```json
{"knowledge_id": "KNOWLEDGE_UUID"}
```

### POST /api/activity

```json
{"actor": "claude-code", "action": "completed sprint-5 runbook", "project_id": "UUID"}
```

### POST /api/auto-handoff

```json
{"intent": "Continue sprint-5 docs PR", "context_summary": "api.md done"}
```

### POST /api/workspace/repos

```json
{"name": "wayneblacktea", "path": "/Users/YOURNAME/project", "language": "Go", "current_branch": "main"}
```

### POST /api/session/handoff

```json
{"intent": "Finish runbook PR", "repo_name": "wayneblacktea", "context_summary": "api.md done"}
```

---

## Errors

All errors: `{"error": "human-readable message"}`

| Status | Meaning |
|--------|---------|
| `400` | Bad request — missing or invalid field |
| `401` | Missing or invalid API key |
| `404` | Resource not found |
| `409` | Conflict (e.g. duplicate project name) |
| `429` | Rate limit exceeded |
| `500` | Internal server error |

---

## curl examples

```bash
# Health check
curl https://your-host/health

# List active projects
curl -H "Authorization: Bearer YOUR_API_KEY" https://your-host/api/projects

# Create a task
curl -s -X POST https://your-host/api/tasks \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"title":"Write API docs","priority":1}'

# Search knowledge
curl -H "Authorization: Bearer YOUR_API_KEY" \
  "https://your-host/api/knowledge/search?q=go+generics&limit=5"

# Cross-domain search
curl -H "Authorization: Bearer YOUR_API_KEY" \
  "https://your-host/api/search?q=pgvector"
```
