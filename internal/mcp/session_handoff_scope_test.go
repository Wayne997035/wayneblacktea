package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// sessionHandoffScopeWhitelist maps "file.go:scope" — scope being a
// top-level function/method name, or a top-level type/var name for
// declarations outside any function — to a reason. Every other scope in
// internal/mcp must go through safeSessionHandoff (session_handoff_safe.go)
// instead of ever touching the raw row or the session store field directly.
//
// Granularity is per FUNCTION/TYPE, not per FILE: an earlier version of this
// whitelist listed whole files, which meant any NEW function added later to
// an already-whitelisted file (e.g. tools_dashboard.go) passed silently even
// if it leaked a raw handoff — round 4 security review (二軍) confirmed this
// exact bypass. Add an entry here ONLY when the scope has been reviewed and
// either (a) touches only non-text fields (CreatedAt/ResolvedAt/error
// values, never Intent/ContextSummary/RepoName/NextActions text) or (b)
// immediately wraps the raw row in safeSessionHandoff / passes it through a
// hardened builder before any text field is read. Never add a scope here
// just to make a new, unreviewed read point pass this test.
var sessionHandoffScopeWhitelist = map[string]string{
	"session_handoff_safe.go:safeSessionHandoff": "defines the wrapper type itself; every other " +
		"whitelisted scope below either builds the view MarshalJSON delegates to, or immediately " +
		"constructs this type from a raw row it just obtained",
	"session_handoff_safe.go:newSafeSessionHandoff": "the wrapper's own constructor — the raw row is " +
		"its parameter by design",

	"tools_session.go:buildHardenedHandoffView": "the hardened-view builder safeSessionHandoff." +
		"MarshalJSON delegates to",
	"tools_session.go:handleSetSessionHandoff": "calls SetHandoff and returns the same-turn echo-back " +
		"view (buildPendingHandoffView) — out of scope for this change, PR #158 dispatch",
	"tools_session.go:handleResolveHandoff": "calls Resolve, which returns only an error (no " +
		"*db.SessionHandoff at all) — flagged only because the broad non-nil-comparison rule treats " +
		"any use of the session field as reportable regardless of which method is called; reviewed, " +
		"leaks nothing",
	"tools_session.go:handleMarkNextActionDone": "calls MarkNextActionDone, then renders the hardened " +
		"view via buildHardenedHandoffView",

	"tools_context.go:buildPendingHandoffView": "the same-turn echo-back builder (pendingHandoffView) " +
		"— out of scope for this change",
	"tools_context.go:buildPendingHandoffSummary": "presence-only builder; carries no free text",
	"tools_context.go:todayContextRaw": "holds the raw row only long enough for fetchTodayContext to " +
		"hand it to buildPendingHandoffSummary",
	"tools_context.go:fetchTodayContext": "calls LatestHandoff; the result only ever reaches " +
		"buildPendingHandoffSummary",

	"resources.go:handleResourceHandoffLatest": "the read-only resource's own hardened builder — out " +
		"of scope for this change, PR #158 dispatch (behaviour must stay byte-for-byte unchanged)",
	"resources.go:handleResourceDashboardOverview": "reads only CreatedAt, never free text",
	"resources.go:handleResourceGTDCurrent":        "reads only ResolvedAt/CreatedAt, never free text",

	"tools_closeout.go:fillCloseoutHandoff": "wraps the row in safeSessionHandoff immediately " +
		"(hardenedIntent)",

	"tools_dashboard.go:handleReconcileDashboard": "reads only ResolvedAt/CreatedAt, never free text",

	"tools_watchdog.go:detectStaleHandoffs": "wraps each row in safeSessionHandoff immediately " +
		"(hardenedIntent) before it reaches the finding detail",

	"tools_procedural.go:recallEpisodic": "wraps the row in safeSessionHandoff immediately; the raw " +
		"accessors are used only for the internal, non-serializing substring match",
}

// sessionHandoffFreeMethods are session.StoreIface methods whose name is
// unique to that interface within this package — no other store has a
// method by these names, so matching on Sel.Name ALONE (no receiver check)
// cannot false-positive on an unrelated store. Matching without a receiver
// check is what closes the round-4 (二軍) bypasses that all route through a
// different-looking receiver expression while still calling the method by
// its real name: a local variable (`store := s.session; store.
// LatestHandoff(ctx)`), a renamed field (`s.sessionRO.LatestHandoff(ctx)`),
// a wrapper method (`s.Session().LatestHandoff(ctx)`), or an anonymously
// embedded interface — in every one of those shapes, `.LatestHandoff(` still
// has to appear as literal source text for the call to compile, and that is
// exactly what this check looks for.
var sessionHandoffFreeMethods = map[string]bool{
	"LatestHandoff":      true,
	"SetHandoff":         true,
	"MarkNextActionDone": true,
	"HandoffsSince":      true,
	"HandoffsByRepo":     true,
}

// sessionHandoffCollidingMethod is SearchByCosine, which is ALSO a method on
// the knowledge and decision stores (s.knowledge.SearchByCosine,
// s.decision.SearchByCosine) — matching it by name alone would false-positive
// on those unrelated calls, so it keeps the receiver check
// sessionHandoffFreeMethods above deliberately drops. This is a narrower,
// accepted gap: a call to SearchByCosine reached through anything other than
// a literal `<field>.session.SearchByCosine(...)` shape (a local variable
// alias, a renamed field, an embedded wrapper) is NOT caught by this check.
// No production call site in this package uses SearchByCosine on the session
// store today (verified: `grep -rn '\.SearchByCosine(' internal/mcp/*.go`
// outside _test.go files shows zero session-store call sites), so the
// residual risk is theoretical, not live.
const sessionHandoffCollidingMethod = "SearchByCosine"

// sessionHandoffField is the *Server struct field name that holds the
// session store (session.StoreIface). Any appearance of this field selector
// OUTSIDE a nil comparison (`s.session == nil` / `s.session != nil`, the
// idiomatic "is this store configured" guard used throughout this package)
// is reportable on its own, independent of whether a sentinel method name
// appears anywhere nearby — this is what closes the bypasses where the raw
// row's SOURCE (the store handle itself) is captured into a local variable,
// passed as a function argument, or embedded into a wrapper struct, before
// any method is ever called on it.
const sessionHandoffField = "session"

// TestSessionHandoffTypeConfinedToWhitelist is the mechanical half of the PR
// #158 chokepoint. It reports a violation, at file:scope granularity
// (sessionHandoffScopeWhitelist), for any of:
//
//   - a selector expression named SessionHandoff, regardless of which
//     package/alias qualifies it (`db.SessionHandoff`, `dbx.SessionHandoff`,
//     ...) — catches import-alias evasion of a plain "db.SessionHandoff"
//     text scan;
//   - a call to any of sessionHandoffFreeMethods, regardless of the receiver
//     expression's shape;
//   - a call to sessionHandoffCollidingMethod (SearchByCosine) specifically
//     through the `<x>.session.SearchByCosine(...)` shape;
//   - the sessionHandoffField selector (`<x>.session`) appearing anywhere
//     that is not the operand of a nil comparison.
//
// What this does NOT cover (accepted residual, see the doc comments on
// sessionHandoffCollidingMethod above and the whitelist var doc comment):
// SearchByCosine reached through a non-`.session.` receiver shape, and any
// leak that crosses into a DIFFERENT package (tracked separately, GTD
// 49f2ed81 — internal/contextpack is explicitly out of scope for this test).
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

		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			for _, scope := range declScopes(decl) {
				if _, allowed := sessionHandoffScopeWhitelist[name+":"+scope]; allowed {
					continue
				}
				checkScope(t, name, scope, decl)
			}
		}
	}
}

// declScopes returns the whitelist scope name(s) a top-level declaration
// introduces: the function/method name for a *ast.FuncDecl, or the
// type/var/const name(s) for a *ast.GenDecl's specs. A GenDecl grouping
// multiple specs in one `var ( ... )`/`type ( ... )` block yields one scope
// per spec, so a whitelisted sibling never accidentally shields an
// unreviewed one declared in the same block.
func declScopes(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []string{d.Name.Name}
	case *ast.GenDecl:
		var scopes []string
		for _, spec := range d.Specs {
			switch sp := spec.(type) {
			case *ast.TypeSpec:
				scopes = append(scopes, sp.Name.Name)
			case *ast.ValueSpec:
				for _, n := range sp.Names {
					scopes = append(scopes, n.Name)
				}
			}
		}
		return scopes
	default:
		return nil
	}
}

// checkScope runs every rule described in TestSessionHandoffTypeConfinedToWhitelist's
// doc comment against decl (a single top-level declaration) and reports each
// violation via t.Errorf, prefixed with file:scope so a failure is
// immediately actionable.
func checkScope(t *testing.T, file, scope string, decl ast.Decl) {
	// First pass: collect every expression that is an operand of a nil
	// comparison (`x == nil` / `x != nil`) ANYWHERE in decl, keyed by AST
	// node identity. This is what lets `if s.session == nil { ... }` — the
	// idiomatic guard used throughout this package — pass, while
	// `store := s.session` two lines later still fails.
	nilExempt := map[ast.Expr]bool{}
	ast.Inspect(decl, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || (be.Op != token.EQL && be.Op != token.NEQ) {
			return true
		}
		if isNilIdent(be.X) {
			nilExempt[be.Y] = true
		}
		if isNilIdent(be.Y) {
			nilExempt[be.X] = true
		}
		return true
	})

	ast.Inspect(decl, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "SessionHandoff" {
			t.Errorf("%s:%s references a *.SessionHandoff type directly (package qualifier: %s) — "+
				"use safeSessionHandoff (session_handoff_safe.go) instead, or add \"%s:%s\" to "+
				"sessionHandoffScopeWhitelist with a reason if this scope is itself a reviewed "+
				"hardened-view builder", file, scope, selectorPackageText(sel.X), file, scope)
		}
		if sessionHandoffFreeMethods[sel.Sel.Name] {
			t.Errorf("%s:%s calls %s, which returns a raw *db.SessionHandoff (or "+
				"[]db.SessionHandoff) regardless of receiver shape — use safeSessionHandoff "+
				"(session_handoff_safe.go) instead, or add \"%s:%s\" to sessionHandoffScopeWhitelist "+
				"with a reason if this scope is itself a reviewed hardened-view builder",
				file, scope, sel.Sel.Name, file, scope)
		}
		if sel.Sel.Name == sessionHandoffCollidingMethod && isFieldSelector(sel.X, sessionHandoffField) {
			t.Errorf("%s:%s calls %s on the session store, which returns raw handoff rows — use "+
				"safeSessionHandoff (session_handoff_safe.go) instead, or add \"%s:%s\" to "+
				"sessionHandoffScopeWhitelist with a reason if this scope is itself a reviewed "+
				"hardened-view builder", file, scope, sessionHandoffCollidingMethod, file, scope)
		}
		if isFieldSelector(sel, sessionHandoffField) && !nilExempt[sel] {
			t.Errorf("%s:%s uses the session store field outside a nil comparison — this is exactly "+
				"how a raw handoff escapes into a local variable, function argument, or embedded "+
				"wrapper without ever spelling a method or type name in this scope; use "+
				"safeSessionHandoff (session_handoff_safe.go) instead, or add \"%s:%s\" to "+
				"sessionHandoffScopeWhitelist with a reason if this scope is itself reviewed and "+
				"leaks nothing", file, scope, file, scope)
		}
		return true
	})
}

// isNilIdent reports whether e is the literal identifier `nil`.
func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// isFieldSelector reports whether e is a selector expression whose field
// name is field — e.g. `s.session` for field="session". Deliberately does
// not attempt full type resolution (no go/types dependency): matching on the
// field NAME is sufficient here because sessionHandoffField is checked
// wherever it appears as a selector, not just as a call receiver.
func isFieldSelector(e ast.Expr, field string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == field
}

// selectorPackageText renders the qualifier of a selector expression for
// error messages — e.g. "db" in `db.SessionHandoff`, or "<non-identifier>"
// for the (currently unreached) case of a qualifier that isn't a simple
// package identifier.
func selectorPackageText(x ast.Expr) string {
	if id, ok := x.(*ast.Ident); ok {
		return id.Name
	}
	return "<non-identifier>"
}
