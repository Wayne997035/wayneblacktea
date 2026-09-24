package gtd_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// [SEC-PR191-02] A hand-written list of "who writes the reserved audit
// actions" drifts as code changes, so it gets a
// machine-checked closure here instead of a hand-maintained list nobody
// re-derives: every place in the tree that writes one of the three reserved
// activity_log action literals must be either gtd.go's reserved-set
// definition or one of the six legitimate in-tx audit writes. Anything else
// is exactly the forgery path SEC-PR191-02 exists to close.

// reservedAuditActionLiterals mirrors gtd.reservedAuditActions' three keys.
// Kept as a literal copy here (not a reference into the unexported map) so
// this scan's target is stated inline — a future rename inside gtd.go would
// otherwise silently widen or narrow what this gate looks for without a
// reviewer seeing a diff in this file too.
var reservedAuditActionLiterals = []string{"project_deleted", "task_deleted", "project_restored"}

// reservedAuditActionAllowedFiles is the exhaustive, machine-checked list of
// files (relative to the module root, forward-slash separated) allowed to
// contain a string literal naming one of reservedAuditActionLiterals, each
// with the EXACT occurrence count expected. A new file appearing here, or an
// existing count drifting, means a string literal was added or removed
// somewhere in the tree — reviewed by a human reading this diff, which is
// the whole point: P3 already tried a hand-maintained prose list once (the
// dispatch's own P3 section) and it undercounted by 4 across two rounds of
// front-review before this test existed.
var reservedAuditActionAllowedFiles = map[string]int{
	// gtd.go's reservedAuditActions map: three keys, one literal each.
	"internal/gtd/gtd.go": 3,
	// The three in-tx PG audit writes (P2/P3): pgDeleteProjectAdapter.
	// WriteDeletionAuditLog, pgDeleteTaskAdapter.WriteDeletionAuditLog,
	// Store.RestoreProject.
	"internal/gtd/store.go": 3,
	// The three in-tx SQLite audit writes: sqliteDeleteProjectAdapter.
	// WriteDeletionAuditLog, sqliteDeleteTaskAdapter.WriteDeletionAuditLog,
	// GTDStore.RestoreProject.
	"internal/storage/sqlite/gtd.go": 3,
}

// moduleRootForReservedActionScan walks up from the current working
// directory (Go always cwd's a test binary into its own package directory,
// internal/gtd here, regardless of where `go test` was invoked from) to
// find the repo's go.mod.
func moduleRootForReservedActionScan(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("walked up to filesystem root without finding go.mod")
		}
		dir = parent
	}
}

// literalNamesReservedAction reports whether unquoted — the decoded value of
// one *ast.BasicLit string literal — names action. Two shapes match: a bare
// Go string constant equal to action (gtd.go's map keys, store.go's
// Action: "project_deleted" struct fields), and a single-quoted SQL literal
// 'action' embedded inside a larger raw-string SQL statement
// (internal/storage/sqlite/gtd.go's `... VALUES (..., 'project_deleted', ...)`
// — the whole backtick-quoted query is ONE BasicLit, so an exact-equality
// check alone would miss every SQLite write site).
func literalNamesReservedAction(unquoted, action string) bool {
	return unquoted == action || strings.Contains(unquoted, "'"+action+"'")
}

// scanReservedActionLiterals walks every non-_test.go .go file under
// root/internal and root/cmd, parses it, and counts — per file, relative to
// root — how many *ast.BasicLit string literals name one of
// reservedAuditActionLiterals.
//
// Comments are never visited: they live in ast.File.Comments, a separate
// list ast.Inspect over the declaration tree never walks. That is exactly
// why this is an AST scan and not a text grep — deleteproject_orchestration.go
// and deletetask_orchestration.go both have a doc comment quoting
// "project_deleted"/"task_deleted" by name (WriteDeletionAuditLog's
// interface doc), which a substring grep would misreport as extra writers.
func scanReservedActionLiterals(t *testing.T, root string) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	fset := token.NewFileSet()

	for _, sub := range []string{"internal", "cmd"} {
		start := filepath.Join(root, sub)
		walkErr := filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				t.Fatalf("parse %s: %v", path, parseErr)
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				t.Fatalf("Rel(%s, %s): %v", root, path, relErr)
			}
			rel = filepath.ToSlash(rel)
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				unquoted, uqErr := strconv.Unquote(lit.Value)
				if uqErr != nil {
					return true
				}
				for _, action := range reservedAuditActionLiterals {
					if literalNamesReservedAction(unquoted, action) {
						counts[rel]++
					}
				}
				return true
			})
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", start, walkErr)
		}
	}
	return counts
}

// diffReservedActionCounts compares got (a file->count map, whether from a
// real scan or the reverse-control subtest's synthetic one below) against
// reservedAuditActionAllowedFiles, returning a non-empty, human-readable
// description of every mismatch — empty means got matches exactly.
func diffReservedActionCounts(got map[string]int) string {
	var problems []string
	seen := make(map[string]bool, len(reservedAuditActionAllowedFiles))
	for file, want := range reservedAuditActionAllowedFiles {
		seen[file] = true
		if got[file] != want {
			problems = append(problems,
				fmt.Sprintf("%s: got %d reserved-action literal(s), want %d", file, got[file], want))
		}
	}
	for file, n := range got {
		if seen[file] || n == 0 {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"%s: %d reserved-action literal(s) found, but this file is not in "+
				"reservedAuditActionAllowedFiles at all — an unauthorized direct write to "+
				"activity_log using a reserved action name?", file, n))
	}
	sort.Strings(problems)
	return strings.Join(problems, "; ")
}

// TestReservedAuditActions_OnlyWrittenByAuditSites is SEC-PR191-02's
// machine-checked closure over P3's "the writer list will drift" concern:
// the three reserved action name literals may only appear in the
// reserved-set definition and the six legitimate in-tx audit writes. A new
// direct write to activity_log elsewhere in the tree using one of these
// three literals — bypassing LogActivity's IsReservedAuditAction guard —
// turns this red.
func TestReservedAuditActions_OnlyWrittenByAuditSites(t *testing.T) {
	root := moduleRootForReservedActionScan(t)
	got := scanReservedActionLiterals(t, root)

	if diff := diffReservedActionCounts(got); diff != "" {
		t.Errorf("reserved audit action literal count drifted from reservedAuditActionAllowedFiles: %s", diff)
	}

	t.Run("reverse_control_extra_writer_turns_red", func(t *testing.T) {
		// An in-memory fake file: a synthetic entry standing in for an
		// unauthorized direct write elsewhere in the tree, without actually
		// creating a file on disk. Built from the REAL scan's own result
		// (got, from the outer test) plus one extra entry, so this subtest
		// proves the check has teeth against the same data the outer
		// assertion just certified as clean.
		withExtraWriter := make(map[string]int, len(got)+1)
		for k, v := range got {
			withExtraWriter[k] = v
		}
		withExtraWriter["internal/fake/extra_writer.go"] = 1

		if diff := diffReservedActionCounts(withExtraWriter); diff == "" {
			t.Fatal("injecting an extra reserved-action literal outside reservedAuditActionAllowedFiles " +
				"produced no diff — the completeness check cannot catch an unauthorized new writer")
		}
	})
}
