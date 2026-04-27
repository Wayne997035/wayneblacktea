# CI/CD secrets — what to set when the GitHub repo goes up

This file lists every secret a GitHub Actions workflow will need
**when** we add `.github/workflows/`. None of these belong in the
repo. Nothing in `.env` (currently tracked) is a real secret — it's
a Railway-style file with `${VAR}` placeholders only; the real
values live in Railway's dashboard.

## Repo-level secrets to add (Settings → Secrets and variables → Actions)

| Secret name | Used by | Required? | Where to get it |
|---|---|---|---|
| `RAILWAY_TOKEN` | release / deploy workflow that runs `railway up` from CI | Optional — only if we want push-to-main auto-deploy. Manual `railway up` from laptop also works. | Railway dashboard → Account Settings → Tokens → New Token |
| `GITHUB_TOKEN` | release workflow (goreleaser uploads binaries) | Auto-provided by Actions, do not add manually. | n/a |
| `DOCKER_HUB_TOKEN` (optional) | publishing the Docker image to Docker Hub | Only if we publish images — Railway builds its own image. | Docker Hub → Account Settings → Security |

## Test-time env (CI runs `task test` only — no DB needed)

The default `task test` runs every `*_test.go` that lacks a build
tag. None of those tests touch a database.

Integration tests are gated behind `//go:build integration` and need
`DATABASE_URL` to be set. We do **not** plan to run them in CI today
— they're a local pre-merge gate. If we ever want them in CI, the
options are:

- spin up a Postgres + pgvector service container in the workflow
- point at a CI-only Aiven instance via `secrets.DATABASE_URL`

Either path adds ~30–60 s to the workflow and brings DB credentials
into CI. Skipping them keeps the workflow fast and secret-free.

## Things that are NOT secrets but feel like they should be

- `.env` (root, tracked) — `${VAR}` placeholders only, see Dockerfile
  comment line 34.
- `.env.example` — documentation, no values.
- Discord guild ID / Notion database ID — these are IDs, not tokens;
  knowing one does nothing without the corresponding bot token /
  integration secret.

## When we open the GH repo

Order of operations to push without exposing anything:

1. Final scan: `git log --all -p | grep -E '(AVN|AIza|gsk_|ntn_|MT[A-Z]).{20,}'`
   should print nothing.
2. `gh repo create Wayne997035/wayneblacktea --private --source . --remote origin`
3. `git push -u origin master` — only after the master merge.
4. Add `RAILWAY_TOKEN` if we plan to wire CI deploy.
5. Flip to public **only** after one more secret scan and after the
   logo is in place.
