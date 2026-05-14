# Historical Migration Exceptions

Migrations are immutable after merge. If schema changes are needed, add a new
numbered migration instead of editing a historical one.

Known exception:

- `000011_backfill_workspace_id.up.sql`: manual incident-recovery backfill that
  intentionally uses `psql` metacommands and is skipped by pgx-based test
  migration runners.
