//go:build integration

package db

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F1003-08][F1003-09] Shared EXPLAIN-measurement helpers for Lead's
// supplement 1 methodology (sprint-1003 dispatch record): custom-plan vs
// generic-plan cost comparison for migrations 000086/000087's new indexes.
// package db (not db_test): needs the unexported sqlc-generated
// listActiveGoals / listPendingProposals constants so tests never
// hand-transcribe the SQL text.

// topLevelCost matches the top-level node's "cost=X..Y" pair in a plain
// (non-FORMAT JSON) EXPLAIN text plan — the first line is always the
// outermost node (Limit, in both queries under test here).
var topLevelCost = regexp.MustCompile(`cost=[0-9.]+\.\.([0-9.]+)`)

// explainTopCost parses the top-level (first line's) total cost upper
// bound ("Y" in "cost=X..Y") out of a plain-text EXPLAIN plan.
func explainTopCost(t *testing.T, plan string) float64 {
	t.Helper()
	lines := strings.SplitN(plan, "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("empty EXPLAIN plan")
	}
	m := topLevelCost.FindStringSubmatch(lines[0])
	if m == nil {
		t.Fatalf("no cost=X..Y found in top-level EXPLAIN line: %q", lines[0])
	}
	y, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse top-level cost %q: %v", m[1], err)
	}
	return y
}

// cpuOperatorCostOf reads the live cpu_operator_cost GUC — Lead's
// supplement 1 explicitly forbids hardcoding this value.
func cpuOperatorCostOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool) float64 {
	t.Helper()
	var raw string
	if err := pool.QueryRow(ctx, `SHOW cpu_operator_cost`).Scan(&raw); err != nil {
		t.Fatalf("SHOW cpu_operator_cost: %v", err)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		t.Fatalf("parse cpu_operator_cost %q: %v", raw, err)
	}
	return v
}

// explainCustomPlan runs "EXPLAIN " + query through pgx's normal Extended
// Query Protocol with real bound parameter values — the first execution of
// a given prepared-statement text is always planned as a custom plan
// (PostgreSQL's plan_cache_mode=auto only considers switching to a generic
// plan starting at the 6th execution — see
// https://www.postgresql.org/docs/current/sql-prepare.html, "the first
// five executions are done with custom plans"), so a single EXPLAIN call
// per unique query text is sufficient to observe a genuine custom plan.
func explainCustomPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()
	rows, err := pool.Query(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN custom-plan query: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan EXPLAIN line: %v", err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate EXPLAIN rows: %v", err)
	}
	return strings.Join(lines, "\n")
}

// literalizeLimitOffset substitutes the sqlc-generated query's $3::int
// (LIMIT) and $2::int (OFFSET) placeholders with concrete literals,
// leaving $1 (workspace_id) as a genuine symbolic placeholder — Lead's
// supplement 1 technique keeps "$1…佔位符不綁值" (the parameter whose
// NULL-ness creates the custom/generic plan question under test) while
// still letting the generic-plan cost vary by LIMIT (the swept variable in
// the "limit 跑 50 與 500 兩組" requirement); GENERIC_PLAN mode has no
// binding mechanism at all via simple query protocol, so there is no way
// to vary LIMIT for the generic measurement other than literal
// substitution into the text. The base string is always the literal
// sqlc-generated constant (listActiveGoals / listPendingProposals) — this
// function only substitutes two already-present placeholder tokens, it
// does not hand-transcribe a new query.
func literalizeLimitOffset(base string, limit int) string {
	s := strings.Replace(base, "$3::int", strconv.Itoa(limit), 1)
	s = strings.Replace(s, "$2::int", "0", 1)
	return s
}

// explainGenericPlan runs "EXPLAIN (GENERIC_PLAN) " + query (with $1 left
// as a genuine unbound placeholder) via the simple query protocol —
// PgConn.Exec, not the normal Extended Protocol Query/QueryRow — because
// EXPLAIN (GENERIC_PLAN) with an unbound placeholder cannot go through
// Parse/Bind/Execute (there is nothing to bind). pgx v5.9.2's PgConn.Exec
// sends pgproto3.Query (simple query protocol) — verified at
// github.com/jackc/pgx/v5@v5.9.2/pgconn/pgconn.go: "pgConn.frontend.SendQuery(&pgproto3.Query{String: sql})".
func explainGenericPlan(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string) string {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("pool.Acquire: %v", err)
	}
	defer conn.Release()

	mrr := conn.Conn().PgConn().Exec(ctx, "EXPLAIN (GENERIC_PLAN) "+query)
	results, err := mrr.ReadAll()
	if err != nil {
		t.Fatalf("EXPLAIN (GENERIC_PLAN) simple-query Exec: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("EXPLAIN (GENERIC_PLAN): expected exactly 1 result set, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("EXPLAIN (GENERIC_PLAN) result error: %v", results[0].Err)
	}
	var lines []string
	for _, row := range results[0].Rows {
		if len(row) != 1 {
			t.Fatalf("EXPLAIN (GENERIC_PLAN) row has %d columns, want 1", len(row))
		}
		lines = append(lines, string(row[0]))
	}
	return strings.Join(lines, "\n")
}

// wsUUID converts a 16-byte UUID array into the pgtype.UUID shape
// ListActiveGoalsParams/ListPendingProposalsParams expect for $1.
func wsUUID(id [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}
