package sqlite

import (
	"context"
	"database/sql"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
)

// schemaEntry is one row of a normalized sqlite_master dump: object type
// (table/index/trigger/view), name, and whitespace-normalized DDL.
type schemaEntry struct {
	typ, name, sql string
}

// sqlLineComment matches a SQL "-- ..." comment to end of line. MUST be
// applied before whitespace is collapsed — once newlines are gone, "--" has
// no line boundary left and would consume the rest of the string.
var sqlLineComment = regexp.MustCompile(`--[^\n]*`)

// normalizeSchemaSQL strips comments then collapses all whitespace runs to a
// single space and trims, so formatting differences (indentation, newlines,
// inline documentation comments) between the runner-built schema and the
// golden dump don't cause false mismatches.
var wsRun = regexp.MustCompile(`\s+`)

func normalizeSchemaSQL(sql string) string {
	sql = sqlLineComment.ReplaceAllString(sql, "")
	return strings.TrimSpace(wsRun.ReplaceAllString(sql, " "))
}

var (
	spaceBeforeOpenParen  = regexp.MustCompile(`\s+\(`)
	spaceAfterOpenParen   = regexp.MustCompile(`\(\s+`)
	spaceBeforeCloseParen = regexp.MustCompile(`\s+\)`)
	spaceAroundComma      = regexp.MustCompile(`\s*,\s*`)
	// trailingDefaultNull / notNullToken / bareNullToken strip SQLite-redundant
	// clauses: "TEXT DEFAULT NULL", "TEXT NULL", and "TEXT NULL CHECK(...)"
	// are byte-different from "TEXT" / "TEXT CHECK(...)" but 100%
	// behaviorally identical — SQLite columns are nullable by default with
	// an implicit NULL default unless NOT NULL is declared, and CHECK
	// constraints are satisfied (not evaluated) for NULL operands regardless
	// of an explicit "NULL" keyword. notNullToken/bareNullToken together
	// guard against stripping the "NULL" out of a genuine "NOT NULL".
	trailingDefaultNull = regexp.MustCompile(`(?i)\s+DEFAULT\s+NULL\s*$`)
	notNullToken        = regexp.MustCompile(`(?i)\bNOT\s+NULL\b`)
	bareNullToken       = regexp.MustCompile(`(?i)\bNULL\b`)
)

// notNullPlaceholder protects genuine "NOT NULL" constraints from
// canonicalizeColumnFragment's bare-NULL stripping pass below.
const notNullPlaceholder = "\x00NOT_NULL\x00"

// canonicalizeSQL strips comments and normalizes all whitespace, including
// the space adjacent to parentheses/commas, so authoring-style differences
// (e.g. "ON repos (x)" vs "ON repos(x)", "a, b" vs "a,b") don't cause false
// mismatches. Used for INDEX/TRIGGER comparison, where SQLite's stored DDL
// text is not reshaped by ALTER TABLE the way CREATE TABLE text is.
func canonicalizeSQL(s string) string {
	s = sqlLineComment.ReplaceAllString(s, "")
	s = wsRun.ReplaceAllString(s, " ")
	s = spaceBeforeOpenParen.ReplaceAllString(s, "(")
	s = spaceAfterOpenParen.ReplaceAllString(s, "(")
	s = spaceBeforeCloseParen.ReplaceAllString(s, ")")
	s = spaceAroundComma.ReplaceAllString(s, ",")
	return strings.TrimSpace(s)
}

// canonicalizeColumnFragment applies canonicalizeSQL plus SQLite-specific
// redundant-clause stripping ("DEFAULT NULL" / trailing "NULL") — used only
// for individual column fragments within canonicalTableSignature, never for
// whole INDEX/TRIGGER statements where "NULL" can appear inside a WHERE
// clause with different meaning (e.g. "WHERE x IS NOT NULL" is NOT redundant).
func canonicalizeColumnFragment(s string) string {
	s = canonicalizeSQL(s)
	s = strings.TrimSpace(trailingDefaultNull.ReplaceAllString(s, ""))
	// Protect "NOT NULL" before stripping bare "NULL" tokens anywhere in the
	// fragment (not just at the end — "TEXT NULL CHECK(...)" has NULL in the
	// middle), then restore it.
	protected := notNullToken.ReplaceAllString(s, notNullPlaceholder)
	stripped := bareNullToken.ReplaceAllString(protected, "")
	s = strings.ReplaceAll(stripped, notNullPlaceholder, "NOT NULL")
	return strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
}

// splitTopLevelCommas splits s on commas that are not nested inside
// parentheses or single-quoted string literals — e.g. it will NOT split
// inside "CHECK (status IN ('a','b'))" at the commas within the IN-list.
func splitTopLevelCommas(s string) []string {
	var parts []string
	depth := 0
	inQuote := false
	start := 0
	for i, r := range s {
		switch r {
		case '\'':
			inQuote = !inQuote
		case '(':
			if !inQuote {
				depth++
			}
		case ')':
			if !inQuote {
				depth--
			}
		case ',':
			if !inQuote && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// canonicalTableSignature turns a CREATE TABLE statement's SQL text into an
// order-independent, comment-free signature: comments are stripped, the
// column/constraint list is split on top-level commas, each fragment is
// canonicalized, and the fragments are sorted before rejoining.
//
// This is necessary because SQLite's stored schema text differs mechanically
// between "one CREATE TABLE with every column" (schema.sql) and "CREATE
// TABLE + N sequential ALTER TABLE ADD COLUMN" (real migration replay) even
// when the resulting set of columns/types/constraints is identical:
//   - ALTER TABLE ADD COLUMN always appends at the end (SQLite has no way to
//     insert a column mid-list), so column order differs from schema.sql's
//     hand-chosen "logical" ordering.
//   - ALTER TABLE RENAME (used by the 000026 FK-drop rebuild dance) causes
//     SQLite to re-quote the table name in the stored CREATE TABLE text.
//   - Migration files don't carry the same inline "-- ..." column comments
//     schema.sql has, since those are documentation, not schema.
//
// None of these are real schema differences — same columns, same types, same
// constraints, same defaults — so exact-text comparison would produce false
// positives for every table touched by more than one migration.
func canonicalTableSignature(sql string) string {
	sql = sqlLineComment.ReplaceAllString(sql, "")
	open := strings.IndexByte(sql, '(')
	closeIdx := strings.LastIndexByte(sql, ')')
	if open < 0 || closeIdx < 0 || closeIdx <= open {
		return canonicalizeSQL(sql)
	}
	inner := sql[open+1 : closeIdx]
	frags := splitTopLevelCommas(inner)
	for i, f := range frags {
		frags[i] = canonicalizeColumnFragment(f)
	}
	sort.Strings(frags)
	return strings.Join(frags, "|")
}

// dumpNormalizedSchema queries sqlite_master for every table/index/trigger/view
// definition (excluding SQLite-internal sqlite_% objects), normalizes each SQL
// text, and returns entries sorted by (type, name) for deterministic comparison.
func dumpNormalizedSchema(ctx context.Context, conn *sql.DB) ([]schemaEntry, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT type, name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []schemaEntry
	for rows.Next() {
		var e schemaEntry
		if err := rows.Scan(&e.typ, &e.name, &e.sql); err != nil {
			return nil, err
		}
		e.sql = normalizeSchemaSQL(e.sql)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].typ != entries[j].typ {
			return entries[i].typ < entries[j].typ
		}
		return entries[i].name < entries[j].name
	})
	return entries, nil
}

// runMigrationsOnMemoryDB opens a fresh :memory: SQLite DB and applies every
// migrations/sqlite/*.sql file via golang-migrate, returning the resulting
// *sql.DB. The caller must Close() it.
func runMigrationsOnMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// NoTxWrap: migration 000026_drop_fk_constraints.up.sql wraps its own
	// BEGIN TRANSACTION/COMMIT (a self-contained table-rebuild dance); letting
	// golang-migrate also wrap each file in an implicit transaction produces
	// "cannot start a transaction within a transaction". NoTxWrap disables the
	// implicit wrap for every file; 000026 keeps its own atomicity via its
	// explicit BEGIN/COMMIT, and all other files are idempotent
	// (CREATE TABLE/INDEX IF NOT EXISTS) so they don't need one.
	driver, err := migratesqlite.WithInstance(conn, &migratesqlite.Config{NoTxWrap: true})
	if err != nil {
		_ = conn.Close()
		t.Fatalf("sqlite.WithInstance: %v", err)
	}
	src, err := iofs.New(sqlitemigrations.FS, ".")
	if err != nil {
		_ = conn.Close()
		t.Fatalf("iofs.New: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("migrate.NewWithInstance: %v", err)
	}
	if err := m.Up(); err != nil {
		_ = conn.Close()
		t.Fatalf("migrate Up: %v", err)
	}
	return conn
}

// buildLegacySchemaDB replicates EXACTLY what Open() did before this package
// switched to the golang-migrate runner: apply schemaSQL verbatim to a fresh
// :memory: DB. This is deliberately NOT the same as calling Open(), which
// now runs the migration runner — TestGenerateGoldenSchema must capture the
// pre-refactor baseline, decoupled from whatever Open() does today, or
// regenerating golden would just capture the new code's own output and the
// equivalence test would become a tautology.
//
// The retired applyColumnUpgrades() step is intentionally NOT replicated
// here: it only mattered for pre-existing DBs whose tables predated a given
// column being added to schema.sql's CREATE TABLE (which uses IF NOT EXISTS,
// so it's a no-op against an existing table). On a fresh :memory: DB — which
// is what this function always builds — every column in schema.sql's current
// CREATE TABLE text is already present from the single CREATE TABLE
// statement, so applyColumnUpgrades would itself be a no-op here.
func buildLegacySchemaDB(ctx context.Context, t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := conn.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("apply legacy schema.sql: %v", err)
	}
	return conn
}

// TestGenerateGoldenSchema is a one-shot generator, not a real test. Run it
// manually (go test -run TestGenerateGoldenSchema -v) to (re)produce
// testdata/schema_golden.sql from the legacy schema.sql mechanism — NOT from
// Open(), which now runs the migration runner (see buildLegacySchemaDB).
func TestGenerateGoldenSchema(t *testing.T) {
	if os.Getenv("WBT_GENERATE_GOLDEN") != "1" {
		t.Skip("set WBT_GENERATE_GOLDEN=1 to (re)generate testdata/schema_golden.sql")
	}
	ctx := context.Background()
	conn := buildLegacySchemaDB(ctx, t)
	defer func() { _ = conn.Close() }()

	entries, err := dumpNormalizedSchema(ctx, conn)
	if err != nil {
		t.Fatalf("dumpNormalizedSchema: %v", err)
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.typ)
		sb.WriteString("|")
		sb.WriteString(e.name)
		sb.WriteString("|")
		sb.WriteString(e.sql)
		sb.WriteString("\n")
	}
	if err := os.WriteFile("testdata/schema_golden.sql", []byte(sb.String()), 0o600); err != nil {
		t.Fatalf("write golden: %v", err)
	}
	t.Logf("wrote %d entries to testdata/schema_golden.sql", len(entries))
}

// readGoldenSchema parses testdata/schema_golden.sql (format: type|name|sql
// per line, produced by TestGenerateGoldenSchema) into a name->entry map for
// exact-match comparison.
func readGoldenSchema(t *testing.T) map[string]schemaEntry {
	t.Helper()
	raw, err := os.ReadFile("testdata/schema_golden.sql")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	golden := map[string]schemaEntry{}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			t.Fatalf("malformed golden line: %q", line)
		}
		e := schemaEntry{typ: parts[0], name: parts[1], sql: parts[2]}
		golden[e.typ+"|"+e.name] = e
	}
	return golden
}

// expectedNewEntries lists schema objects that exist only in the migration-
// runner-built schema and NOT in testdata/schema_golden.sql (which was
// captured from the pre-refactor schema.sql). Each entry here is a documented,
// deliberate addition — see the migration file's own header comment for
// rationale. Anything else showing up as "extra" fails the equivalence test.
var expectedNewEntries = map[string]bool{
	// migrations/sqlite/000041_idx_tasks_due_date.up.sql: the SQLite twin for
	// PG migration 000041 was entirely missing (numbering gap 000040->000042);
	// schema.sql never carried this index. Net-new versus the golden baseline.
	"index|idx_tasks_due_date": true,

	// migrations/sqlite/000074_outcomes_supersession.up.sql (arch-r2 A13,
	// decision 80c1e8ae, outcome lifecycle convergence): one brand-new
	// index on the outcomes table, added after the legacy schema.sql
	// baseline was frozen (schema.sql itself is untouched — it is a frozen
	// pre-E1 historical snapshot per its own header comment, not a live
	// target). An earlier draft of the migration also created a second
	// index, idx_outcomes_supersedes_id, which was dropped before merge
	// because nothing queries supersedes_id yet (see migrate_test.go's
	// TestRunMigrations_AdoptsPreExistingDB for the same note) — it never
	// appears in the replay-built schema, so it has no entry here. The
	// migration's other change, the supersedes_id column, is
	// hand-added directly to testdata/schema_golden.sql's "table|outcomes"
	// line instead — it is a real, intentional new column (not an inert
	// difference eligible for acceptedDifferences below), and
	// canonicalTableSignature's alphabetical column sort makes the exact
	// insertion position in that line comparison-irrelevant.
	//
	// KNOWN LIMITATION (flagged during GTD a4fa8f58 / PR #152 fix): this
	// hand-edit to testdata/schema_golden.sql is not itself derived from any
	// replay or generator — TestGenerateGoldenSchema only ever regenerates
	// from schema.sql, never from this hand-typed column — so a future
	// hand-edit here could drift from what a real legacy DB (and the real
	// 000074 migration) actually produce, and TestGoldenSchemaEquivalence
	// would still pass against its own wrong golden. The adoption-path test
	// (TestRunMigrations_AdoptsPreExistingDB in migrate_test.go) is the
	// independent check against this: it builds a legacy DB from the real
	// schemaSQL (not this golden file), runs it through the real adoption +
	// replay code path, and asserts supersedes_id and the new index exist
	// via a live query against the resulting DB — so it cannot be fooled by
	// a wrong hand-edit here. That test only covers the specific columns
	// asserted in it, though; it is not a systematic fix for hand-maintained
	// golden drift in general. A systematic fix — teaching
	// TestGenerateGoldenSchema to itself run the adoption+replay path as an
	// alternate generation mode, so testdata/schema_golden.sql is always
	// derived from real migration execution rather than typed by hand — is
	// out of scope here (it would require restructuring how the golden file
	// is produced, beyond this adoption-path bug fix) and is left as
	// follow-up.
	"index|idx_outcomes_one_open_draft": true,
}

// migrations/sqlite/000076_decision_actor_provenance.up.sql (U15 contract
// layer) adds two new columns — actor_session_id and confirmed_by_human — to
// the decisions table. Unlike idx_outcomes_one_open_draft above, this is NOT
// a new expectedNewEntries key: table|decisions already exists in the golden
// baseline (with the source column from 000073), so the new columns are
// hand-added directly to testdata/schema_golden.sql's existing table|decisions
// line, mirroring how 000074's outcomes.supersedes_id column was hand-added
// to table|outcomes rather than routed through this map. Same KNOWN
// LIMITATION as that precedent: this hand-edit is not itself derived from a
// replay or generator, so it could in principle drift from what the real
// migration produces — TestMigration000076_SQLite_UpDownUp
// (internal/decision/actor_provenance_contract_test.go) is the independent
// check against that: it applies 000076's real up.sql via golang-migrate and
// asserts the columns exist via PRAGMA table_info against the resulting DB,
// so it cannot be fooled by a wrong hand-edit here.

// acceptedDifferences lists schema objects present in BOTH golden and the
// migration-runner-built schema, but whose content is known to differ in a
// way that has been explicitly reviewed and accepted as functionally inert —
// this is NOT a blanket normalization rule (see canonicalizeColumnFragment
// for those); each entry here documents EXACTLY what differs and why, so a
// future reader never has to re-derive the reasoning. Per team-lead ruling
// 2026-07-18 (E1 golden-equivalence review, item #3): fixing these would
// require a full table-rebuild (SQLite cannot ALTER a column's type or drop
// a DEFAULT in place) for zero functional benefit, so they're documented as
// accepted instead of "fixed" via a risky rebuild migration.
var acceptedDifferences = map[string]string{
	// table|procedural_memories: migrations/sqlite/000032_procedural_memories.up.sql
	// (merged, immutable per backend-security-design.md §6.4) differs from
	// schema.sql in two specific, itemized ways:
	//   1. `id` has DEFAULT (lower(hex(randomblob(4)))||...) — an
	//      auto-generating UUID fallback schema.sql's version lacks. Dead
	//      code in practice: every write path for procedural_memories
	//      supplies an explicit app-generated UUID (project-wide
	//      convention — see internal/storage/sqlite/procedural.go), so the
	//      DEFAULT is never triggered by any real INSERT.
	//   2. `created_at`/`last_used_at` are typed DATETIME (SQLite NUMERIC
	//      type affinity) vs schema.sql's TEXT (TEXT affinity). Functionally
	//      inert: values are always RFC3339 strings from
	//      strftime('%Y-%m-%dT%H:%M:%fZ', ...), which do not parse as a
	//      well-formed integer/real, so SQLite's NUMERIC affinity leaves
	//      them stored as TEXT regardless of the declared column type —
	//      identical bytes on disk either way.
	"table|procedural_memories": "id DEFAULT(randomblob uuid) + created_at/last_used_at " +
		"DATETIME-vs-TEXT affinity — both accepted dead-in-practice differences, " +
		"not fixed (table-rebuild risk exceeds the zero functional benefit)",
}

// runnerBookkeepingObjects are schema objects golang-migrate itself creates
// (via the sqlite driver's ensureVersionTable) to track applied migration
// versions. They are runner machinery, not application schema, and were
// never part of schema.sql — excluded from comparison entirely rather than
// listed as "expected new" (which is reserved for deliberate app-schema
// additions like idx_tasks_due_date).
var runnerBookkeepingObjects = map[string]bool{
	"table|schema_migrations": true,
	"index|version_unique":    true,
}

// TestGoldenSchemaEquivalence is the acceptance test for E1: the schema
// produced by replaying every migrations/sqlite/*.sql file via golang-migrate
// against a fresh :memory: DB MUST match the schema previously produced by
// schema.sql (captured in testdata/schema_golden.sql), except for the
// documented additions in expectedNewEntries and the documented, itemized
// content differences in acceptedDifferences (see backend-security-design.md
// §6.3/§6.4 and the team-lead ruling cited on each entry).
func TestGoldenSchemaEquivalence(t *testing.T) {
	ctx := context.Background()
	conn := runMigrationsOnMemoryDB(t)
	defer func() { _ = conn.Close() }()

	built, err := dumpNormalizedSchema(ctx, conn)
	if err != nil {
		t.Fatalf("dumpNormalizedSchema: %v", err)
	}
	golden := readGoldenSchema(t)

	seen := map[string]bool{}
	for _, e := range built {
		key := e.typ + "|" + e.name
		if runnerBookkeepingObjects[key] {
			continue
		}
		seen[key] = true
		g, ok := golden[key]
		if !ok {
			if expectedNewEntries[key] {
				continue
			}
			t.Errorf("unexpected new schema object not in golden and not in expectedNewEntries: %s", key)
			continue
		}
		if reason, accepted := acceptedDifferences[key]; accepted {
			t.Logf("accepted documented difference for %s: %s", key, reason)
			continue
		}
		var gotSQL, wantSQL string
		if e.typ == "table" {
			gotSQL, wantSQL = canonicalTableSignature(e.sql), canonicalTableSignature(g.sql)
		} else {
			gotSQL, wantSQL = canonicalizeSQL(e.sql), canonicalizeSQL(g.sql)
		}
		if gotSQL != wantSQL {
			t.Errorf("schema mismatch for %s:\n golden: %s\n built:  %s", key, wantSQL, gotSQL)
		}
	}
	for key := range golden {
		if runnerBookkeepingObjects[key] {
			continue
		}
		if !seen[key] {
			t.Errorf("golden schema object missing from migration-runner-built schema: %s", key)
		}
	}
}
