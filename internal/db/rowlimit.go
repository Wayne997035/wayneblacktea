package db

import "math"

// Hand-written, not sqlc output. sqlc only ever adds/overwrites the *.sql.go
// files it generates, so this file survives `task sqlc`.

// UnboundedRowLimit is the row_limit a caller passes when it genuinely wants
// every matching row — [F170-04].
//
// It exists so that "no cap" is something a call site STATES rather than
// something a query silently does. ListActiveProjects / ListActiveGoals /
// ListPendingProposals had no LIMIT clause at all; the MCP list tools built on
// them therefore grew with the tables, and nothing in the code said whether
// that was intended. Adding LIMIT to the queries forces every caller to answer
// the question, and the ones whose contract really is "all rows" (the HTTP
// dashboard, the context handler, qa-seed) answer it with this constant.
//
// math.MaxInt32 rather than a smaller round number because the parameter is an
// int32 on the wire: any value below the maximum would be a cap wearing the
// name "unbounded", which is the kind of quiet truncation this whole change
// exists to remove.
const UnboundedRowLimit int32 = math.MaxInt32

// ClampRowLimit maps a caller-supplied row limit onto the range the paginated
// queries accept.
//
// limit <= 0 becomes 1, deliberately NOT UnboundedRowLimit: a zero limit
// almost always means an unset field, and resolving "unset" to "no cap" is how
// a pagination guard gets disabled by accident — exactly the failure this
// guards against. Callers that want everything say so with UnboundedRowLimit.
func ClampRowLimit(limit int32) int32 {
	if limit <= 0 {
		return 1
	}
	return limit
}

// ClampRowOffset floors a negative offset at 0. Postgres rejects a negative
// OFFSET outright and SQLite silently treats it as 0; neither is a useful
// answer to give a caller, so both backends get the same defined behaviour.
func ClampRowOffset(offset int32) int32 {
	if offset < 0 {
		return 0
	}
	return offset
}
