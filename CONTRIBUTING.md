# Contributing

Thanks for your interest. This is a personal project that opened up to friends
running self-hosted instances; contributions are welcome but the bar is
"keep things simple and well-tested".

## Workflow

1. **Branch off `master`** with a `feature/<slug>` or `fix/<slug>` name.
2. **Write a 6-element dispatch prompt for yourself** before coding (Goal /
   Scope IN+OUT / Input / Output / Acceptance / Boundaries). It catches
   scope creep before you start typing.
3. **Run `task check` from `build/`** until you see `0 issues`. Lint, tests,
   and both binaries must build before you commit.
4. **One logical change per commit.** `feat:` / `fix:` / `chore:` / `docs:` /
   `ci:` prefixes, no `(scope)` suffix.
5. **Open a PR against `master`.** Small (<500 LoC) PRs use the light
   template; larger ones use the full template (see `CLAUDE.md`).

## Code style

- Domain code under `internal/<bounded-context>/`.
- Public CRUD surface declared in `internal/<domain>/iface.go`; the concrete
  Postgres-backed `*Store` satisfies it via compile-time `var _ StoreIface`.
- SQL queries live in `sql/queries/<domain>.sql`. Run `sqlc generate` after
  any change. Workspace scoping uses `sqlc.narg('workspace_id')` so a NULL
  argument disables the filter (legacy mode).
- HTTP handlers consume small interfaces in `internal/handler/interfaces.go`
  rather than the concrete Store, so tests can pass a fake.
- Integration tests use the `//go:build integration` tag and skip when
  `DATABASE_URL` is unset, so the default `task test` run never needs a DB.

## Local checks

```bash
cd build
task check         # lint + test + build × 2 binaries
```

For DB-touching tests:

```bash
DATABASE_URL='postgres://...' go test -tags=integration ./...
```

## Releasing

Maintainers tag a version (`git tag -s v0.x.y && git push --tags`); GitHub
Actions runs GoReleaser to publish binaries and the changelog snippet.

## Questions

Open an issue. The issue tracker is the primary discussion channel.
