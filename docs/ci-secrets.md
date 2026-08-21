# CI/CD secrets

The repo is public with four GitHub Actions workflows in
`.github/workflows/`. This lists every secret and repository variable they
actually reference today, and how to rotate each one.

## Repo-level secrets (Settings → Secrets and variables → Actions → Secrets)

| Secret | Used by | Required? | Where to get / rotate it |
|---|---|---|---|
| `WBT_API_KEY` | `gtd-reconcile.yml` — authenticates the `POST /api/tasks/reconcile-merged-prs` call to the deployed server | Optional — if unset the job logs a warning and exits 0 (never blocks a merge) | Must match the `API_KEY` env var the deployed server validates (>= 32 chars). Rotate by generating a new value, updating both the server's `API_KEY` env (Railway) and this secret together — a mismatch just makes reconcile fail open, not a security incident. |
| `GITHUB_TOKEN` | `ci.yml` — repo API access | Always present | Auto-provided by GitHub Actions per run. Never set manually. |

`migration-immutability.yml` uses no secrets — it only diffs `migrations/**/*.sql` against the base branch.

## Repo-level variables (Settings → Secrets and variables → Actions → Variables)

| Variable | Used by | Notes |
|---|---|---|
| `WBT_API_URL` | `gtd-reconcile.yml` | Base URL of the deployed server, e.g. `https://wayneblacktea.up.railway.app`. Not a secret (readable by any collaborator), but the workflow still rejects any non-`https://` value before sending `WBT_API_KEY` to it (SSRF guard). |

## No signing key, and nothing to sign

There is no release workflow and no signing secret. A release is a git tag; the Go module
proxy serves it and `go install` builds locally, so no artifact is produced that would need
signing. Integrity comes from the Go checksum database (fail-closed, on by default) rather
than from a key this repo would have to hold, rotate, or leak — see `docs/security.md`.

The previous setup granted `id-token: write` for cosign keyless signing. It was removed
along with the binary pipeline; **NEVER reintroduce a write-scoped token or `id-token`
permission without a release artifact that actually needs it.**

## Leak response

If a secret above leaks (committed by mistake, logged, or exposed in a fork
PR run):

1. Rotate at the source first — regenerate the PAT / API key — then update
   the GitHub secret. Revoking the old credential before updating the secret
   just breaks the workflow for the gap; do it in either order, but do it
   promptly.
2. For `WBT_API_KEY`: also update the deployed server's `API_KEY` env in
   Railway so the leaked value stops being accepted.

## Not secrets, even though they look like they should be

- `.env.example` — placeholder documentation, no real values, and is the
  only `.env*` file tracked in git (`.env`, `.env.local`, etc. are
  gitignored).
- Discord guild ID / any numeric IDs in code or docs — knowing an ID does
  nothing without the corresponding bot token or API key.
