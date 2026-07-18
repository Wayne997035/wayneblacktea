# CI/CD secrets

The repo is public with four GitHub Actions workflows in
`.github/workflows/`. This lists every secret and repository variable they
actually reference today, and how to rotate each one.

## Repo-level secrets (Settings → Secrets and variables → Actions → Secrets)

| Secret | Used by | Required? | Where to get / rotate it |
|---|---|---|---|
| `HOMEBREW_TAP_GITHUB_TOKEN` | `release.yml` — push-access token for the public `Wayne997035/homebrew-tap` repo, so goreleaser can write generated Homebrew casks | Required for the release workflow to publish casks | A fine-grained GitHub PAT scoped to `contents: write` on `Wayne997035/homebrew-tap` only. Rotate by generating a new PAT with the same scope and updating the secret value; the old PAT can be revoked immediately after. |
| `WBT_API_KEY` | `gtd-reconcile.yml` — authenticates the `POST /api/tasks/reconcile-merged-prs` call to the deployed server | Optional — if unset the job logs a warning and exits 0 (never blocks a merge) | Must match the `API_KEY` env var the deployed server validates (>= 32 chars). Rotate by generating a new value, updating both the server's `API_KEY` env (Railway) and this secret together — a mismatch just makes reconcile fail open, not a security incident. |
| `GITHUB_TOKEN` | `ci.yml`, `release.yml` — repo API access, goreleaser's release/asset upload | Always present | Auto-provided by GitHub Actions per run. Never set manually. |

`migration-immutability.yml` uses no secrets — it only diffs `migrations/**/*.sql` against the base branch.

## Repo-level variables (Settings → Secrets and variables → Actions → Variables)

| Variable | Used by | Notes |
|---|---|---|
| `WBT_API_URL` | `gtd-reconcile.yml` | Base URL of the deployed server, e.g. `https://wayneblacktea.up.railway.app`. Not a secret (readable by any collaborator), but the workflow still rejects any non-`https://` value before sending `WBT_API_KEY` to it (SSRF guard). |

## OIDC, not a stored secret

`release.yml` grants `id-token: write` for cosign keyless signing of release
binaries via GitHub's OIDC issuer — there is no long-lived signing key to
rotate or leak.

## Leak response

If a secret above leaks (committed by mistake, logged, or exposed in a fork
PR run):

1. Rotate at the source first — regenerate the PAT / API key — then update
   the GitHub secret. Revoking the old credential before updating the secret
   just breaks the workflow for the gap; do it in either order, but do it
   promptly.
2. For `WBT_API_KEY`: also update the deployed server's `API_KEY` env in
   Railway so the leaked value stops being accepted.
3. For `HOMEBREW_TAP_GITHUB_TOKEN`: revoke the PAT in GitHub settings — a
   leaked fine-grained PAT scoped to one repo has limited blast radius, but
   revoke it anyway.

## Not secrets, even though they look like they should be

- `.env.example` — placeholder documentation, no real values, and is the
  only `.env*` file tracked in git (`.env`, `.env.local`, etc. are
  gitignored).
- Discord guild ID / any numeric IDs in code or docs — knowing an ID does
  nothing without the corresponding bot token or API key.
