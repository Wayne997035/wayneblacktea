package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// sessionHandoffScopeWhitelist lists every internal/mcp non-test file allowed
// to reference the db.SessionHandoff type directly, or to call one of
// sessionHandoffReturningMethods below. Every other file in the package must
// go through safeSessionHandoff (session_handoff_safe.go) instead of ever
// touching the raw row.
//
// Add a file here ONLY when it is itself the safe type's definition, a
// hardened-view builder, or an existing call site that has been reviewed and
// either (a) touches only non-text fields (CreatedAt/ResolvedAt timestamps)
// or (b) immediately wraps the raw row in safeSessionHandoff / passes it
// through a hardened builder before any text field is read. Never add a file
// here just to make a new, unreviewed read point pass this test.
var sessionHandoffScopeWhitelist = map[string]string{
	"session_handoff_safe.go": "defines safeSessionHandoff itself",
	"tools_session.go": "buildHardenedHandoffView (the hardened-view builder) plus " +
		"set_session_handoff/mark_next_action_done, which legitimately call " +
		"SetHandoff/MarkNextActionDone",
	"tools_context.go": "buildPendingHandoffView/buildPendingHandoffSummary (existing " +
		"builders — pendingHandoffView is the same-turn echo-back type, " +
		"pendingHandoffSummary is presence-only, no free text) plus " +
		"fetchTodayContext's LatestHandoff call, whose result only ever reaches " +
		"buildPendingHandoffSummary",
	"resources.go": "handleResourceHandoffLatest (the read-only resource's own " +
		"hardened builder, out of scope for this change — see PR #158 dispatch) " +
		"plus two other resources that only read CreatedAt/ResolvedAt timestamps",
	"tools_closeout.go": "fillCloseoutHandoff wraps the row in safeSessionHandoff " +
		"immediately (hardenedIntent)",
	"tools_dashboard.go": "missing-handoff health check reads only " +
		"CreatedAt/ResolvedAt timestamps, never free text",
	"tools_watchdog.go": "detectStaleHandoffs wraps each row in safeSessionHandoff " +
		"immediately (hardenedIntent) before it reaches the finding detail",
	"tools_procedural.go": "recallEpisodic wraps the row in safeSessionHandoff " +
		"immediately and uses only the raw accessors for its internal, " +
		"non-serializing substring match",
}

// sessionHandoffReturningMethods are the session.StoreIface methods whose
// return type carries a raw *db.SessionHandoff or []db.SessionHandoff. A call
// to any of these from a file outside sessionHandoffScopeWhitelist is exactly
// the "new read point forgot to harden" bug this test exists to catch — even
// when the call site never spells the literal type name. Go's `:=` type
// inference means `h, err := s.session.LatestHandoff(ctx)` never mentions
// db.SessionHandoff in source text at all, which is why scanning for the
// type name alone (the other half of this test, below) is not sufficient:
// tools_procedural.go:306's pre-fix `return []any{h}` is the exact
// reproduction — h's type never appears as text in that function.
var sessionHandoffReturningMethods = map[string]bool{
	"LatestHandoff":      true,
	"SetHandoff":         true,
	"MarkNextActionDone": true,
	"SearchByCosine":     true,
	"HandoffsSince":      true,
	"HandoffsByRepo":     true,
}

// sessionHandoffMethodReceiverField is the *Server field name these methods
// must be called through for a match to count. Restricting to this receiver
// avoids false positives: SearchByCosine, for instance, is also a method on
// the knowledge and decision stores (s.knowledge.SearchByCosine,
// s.decision.SearchByCosine), which return unrelated types and are outside
// this test's concern entirely.
const sessionHandoffMethodReceiverField = "session"

// TestSessionHandoffTypeConfinedToWhitelist is the mechanical half of the PR
// #158 chokepoint: safeSessionHandoff's MarshalJSON makes accidental leaks
// safe BY DEFAULT once a value is wrapped, but nothing in the type system
// stops a new read point from skipping the wrapper entirely — either by
// naming db.SessionHandoff directly, or by calling one of the session store
// methods that return it. This test fails the moment either happens in a
// file outside sessionHandoffScopeWhitelist.
func TestSessionHandoffTypeConfinedToWhitelist(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/mcp: %v", err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if _, allowed := sessionHandoffScopeWhitelist[name]; allowed {
			continue
		}

		// go/parser discards comments from the AST it returns here (parser
		// runs without parser.ParseComments), so a doc comment that merely
		// mentions "db.SessionHandoff" in prose — e.g. resources.go's note
		// that recall used to return the raw row — can never trigger this
		// check. Only real, compiled references do.
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "db" && sel.Sel.Name == "SessionHandoff" {
				t.Errorf("%s: references db.SessionHandoff directly — use safeSessionHandoff "+
					"(session_handoff_safe.go) instead, or add this file to "+
					"sessionHandoffScopeWhitelist with a reason if it is itself a reviewed "+
					"hardened-view builder", name)
			}
			if sessionHandoffReturningMethods[sel.Sel.Name] && receiverEndsInField(sel.X, sessionHandoffMethodReceiverField) {
				t.Errorf("%s: calls %s on the session store, which returns a raw "+
					"*db.SessionHandoff (or []db.SessionHandoff) — use safeSessionHandoff "+
					"(session_handoff_safe.go) instead, or add this file to "+
					"sessionHandoffScopeWhitelist with a reason if it is itself a reviewed "+
					"hardened-view builder", name, sel.Sel.Name)
			}
			return true
		})
	}
}

// receiverEndsInField reports whether expr is (or ends with a selector on) a
// field access named field — e.g. `s.session` for field="session". Handles
// the direct `s.session.Method(...)` shape used throughout this package;
// deliberately does not attempt full type resolution (no go/types dependency)
// since every call site in this package reaches the session store through
// exactly this shape.
func receiverEndsInField(expr ast.Expr, field string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == field
}
