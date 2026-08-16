package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// sessionHandoffScopeWhitelist maps "file.go:scope" to a reason. scope is
// "ReceiverType.MethodName" for a method (round-5 二軍 finding m-4-1: a bare
// method name alone would let one whitelisted method silently exempt an
// unrelated method of the SAME name on a DIFFERENT type declared in the same
// file — funcScopeName below always includes the receiver type when one
// exists), the bare function name for a free function, or a top-level
// type/var name for a declaration outside any function. Every other scope in
// internal/mcp must go through safeSessionHandoff (session_handoff_safe.go)
// instead of ever touching the raw row, the store field, or the wrapper's
// own raw accessors directly.
//
// Granularity is per FUNCTION/TYPE, not per FILE: an earlier version of this
// whitelist listed whole files, which meant any NEW function added later to
// an already-whitelisted file passed silently even if it leaked a raw
// handoff — round-4 security review confirmed this exact bypass (probe 7,
// chokepoint-r4.md). Add an entry here ONLY when the scope has been reviewed
// and either (a) touches only non-text fields (CreatedAt/ResolvedAt/error
// values, never Intent/ContextSummary/RepoName/NextActions text) or (b)
// immediately wraps the raw row in safeSessionHandoff / passes it through a
// hardened builder before any text field is read. Never add a scope here
// just to make a new, unreviewed read point pass this test.
//
// Known limitation of this granularity (round-5 一軍 finding, kept alongside
// C-4-1 rather than filed separately since it is the same "whitelist exempts
// the whole scope, not the specific line that justified whitelisting it"
// root cause): once a scope is whitelisted, this test does not re-verify
// that its ONLY reason for being here still holds. A whitelisted scope that
// later grows a second, unreviewed read of the same field (e.g.
// handleResourceGTDCurrent starts also reading .Intent, not just
// .ResolvedAt/.CreatedAt as its entry here claims) compiles and passes this
// test silently. Closing that would need per-selector, not per-scope,
// whitelisting — deliberately not attempted here.
//
// The mitigation is that every entry's reason below names EXACTLY which
// fields it reads (or states "reads no field" when it passes the row whole
// to another whitelisted scope), so a reviewer diffing a change against its
// whitelist reason — not just against this test passing — can catch drift a
// green run cannot. This was NOT true of 10 of the 18 entries as of round 5
// (二軍 finding M-5-1: their reasons described the treatment applied, not
// the fields read, giving a reviewer nothing to diff); those 10 have been
// rewritten to name fields. Any new entry added to sessionHandoffScopeWhitelist
// MUST follow the same convention — a reason that does not name a field (or
// explicitly say it reads none) reintroduces exactly the gap M-5-1 found.
var sessionHandoffScopeWhitelist = map[string]string{
	"session_handoff_safe.go:safeSessionHandoff": "reads no field of h itself — holds h as its only " +
		"struct field; every method below reads *db.SessionHandoff fields through v.h, not through " +
		"this type declaration",
	"session_handoff_safe.go:newSafeSessionHandoff": "reads no field of h — stores the pointer " +
		"verbatim as the constructed value's h field",
	"session_handoff_safe.go:safeSessionHandoff.MarshalJSON": "reads h as a whole (nil-checked) and " +
		"delegates to buildHardenedHandoffView(h); dereferences no individual field of h directly here",
	"session_handoff_safe.go:safeSessionHandoff.rawIntent": "reads h.Intent directly, unhardened — " +
		"the wrapper's sanctioned raw-text escape hatch (round-5 二軍 finding C-5-1: this scope, along " +
		"with rawContextSummary/hardenedIntent/MarshalJSON below, is where sessionHandoffValueFields' " +
		"\"h\" entry is SUPPOSED to still match — these four are the only legitimate readers of v.h)",
	"session_handoff_safe.go:safeSessionHandoff.rawContextSummary": "reads h.ContextSummary directly " +
		"(via textValue), unhardened — same escape-hatch contract as rawIntent",
	"session_handoff_safe.go:safeSessionHandoff.hardenedIntent": "reads h.Intent directly and hardens " +
		"it via clipAndFenceStoredContext before returning",

	"tools_session.go:buildHardenedHandoffView": "reads ID/RepoName/Intent/ContextSummary/CreatedAt/" +
		"NextActions from h and hardens every one of them (clip/fence/neutralise/byte-budget) before " +
		"returning — the hardened-view builder safeSessionHandoff.MarshalJSON delegates to",
	"tools_session.go:Server.handleSetSessionHandoff": "reads no field of h itself — passes the store's " +
		"return value whole to buildPendingHandoffView (the same-turn echo-back builder), out of scope " +
		"for this change per PR #158 dispatch",
	"tools_session.go:Server.handleResolveHandoff": "reads no field of any handoff — there is none to " +
		"read; Resolve returns only an error. Flagged only because the broad non-nil-comparison rule " +
		"treats any use of the session field as reportable regardless of which method is called",
	"tools_session.go:Server.handleMarkNextActionDone": "reads no field of h itself — passes the " +
		"store's return value whole to buildHardenedHandoffView",

	"tools_context.go:buildPendingHandoffView": "reads every field of h verbatim (ID/ProjectID/" +
		"RepoName/Intent/ContextSummary/ResolvedAt/CreatedAt/WorkspaceID/NextActions) with NO " +
		"hardening — the same-turn echo-back builder (pendingHandoffView), safe only because its " +
		"callers restrict it to the caller's own just-written text; out of scope for this change",
	"tools_context.go:buildPendingHandoffSummary": "reads ID/CreatedAt and the decoded NextActions " +
		"length only, via buildPendingHandoffView; carries no free text of its own",
	"tools_context.go:todayContextRaw": "the struct field (.handoff) that holds the raw row only long " +
		"enough for fetchTodayContext to write it and toContext to hand it to " +
		"buildPendingHandoffSummary — neither reader dereferences .Intent/.ContextSummary/.RepoName",
	"tools_context.go:Server.fetchTodayContext": "calls LatestHandoff and assigns the raw result to " +
		"todayContextRaw.handoff; reads only that assignment, never a text field",
	"tools_context.go:todayContextRaw.toContext": "reads r.handoff only to pass it whole into " +
		"buildPendingHandoffSummary(r.handoff) — round-5 二軍 C-4-1: an earlier version of this " +
		"whitelist recognised only the *Server.session field, so this second raw-row-holding field " +
		"(todayContextRaw.handoff) was invisible to the check even though toContext reads it " +
		"directly; see sessionHandoffValueFields below",

	"resources.go:Server.handleResourceHandoffLatest": "reads RepoName/Intent/ContextSummary/ID/" +
		"CreatedAt/NextActions from h and hardens every one of them — the read-only resource's own " +
		"hardened builder, out of scope for this change, PR #158 dispatch (behaviour must stay " +
		"byte-for-byte unchanged)",
	"resources.go:Server.handleResourceDashboardOverview": "reads only CreatedAt, never free text",
	"resources.go:Server.handleResourceGTDCurrent":        "reads only ResolvedAt/CreatedAt, never free text",

	"tools_closeout.go:Server.fillCloseoutHandoff": "reads no field of handoff itself — wraps the row " +
		"in safeSessionHandoff immediately (hardenedIntent)",

	"tools_dashboard.go:Server.handleReconcileDashboard": "reads only ResolvedAt/CreatedAt, never free text",

	"tools_watchdog.go:Server.detectStaleHandoffs": "reads ResolvedAt/CreatedAt (filter, non-text) and " +
		"ID directly; Intent only via safeSessionHandoff.hardenedIntent() before it reaches the " +
		"finding detail",

	"tools_procedural.go:recallEpisodic": "reads Intent/ContextSummary only via safeSessionHandoff's " +
		"raw accessors (rawIntent/rawContextSummary), for an internal, non-serializing substring " +
		"match; returns the safeSessionHandoff wrapper itself, never h, so the raw text never crosses " +
		"a serialization boundary here",
}

// sessionHandoffFreeMethods are session.StoreIface methods whose name is
// unique to that interface within this package — no other store has a
// method by these names, so matching on Sel.Name ALONE (no receiver check)
// cannot false-positive on an unrelated store. Matching without a receiver
// check is what closes the round-4 bypasses that all route through a
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

// sessionHandoffRawAccessors are safeSessionHandoff's own unexported escape
// hatch (session_handoff_safe.go rawIntent/rawContextSummary) — the only
// sanctioned way to reach unhardened text out of an already-wrapped value.
// Before round 5 (二軍 finding M-4-1), a doc comment alone ("MUST NOT let the
// returned string cross a JSON serialization boundary") was the only thing
// stopping a new caller from grabbing raw text through this hatch and
// serializing it — exactly the kind of protection this whole mechanical
// check exists to replace with something enforceable; a comment guarding the
// wrapper's own trapdoor is no more trustworthy than a comment guarding the
// row directly. recallEpisodic (tools_procedural.go) is the sole legitimate
// caller and is already whitelisted above for the LatestHandoff call, so
// this rule adds zero new whitelist entries.
var sessionHandoffRawAccessors = map[string]bool{
	"rawIntent":         true,
	"rawContextSummary": true,
}

// sessionHandoffCollidingMethod is SearchByCosine, which is ALSO a method on
// the knowledge and decision stores (s.knowledge.SearchByCosine,
// s.decision.SearchByCosine) — matching it by name alone would false-positive
// on those unrelated calls, so it keeps a receiver check the methods above
// deliberately drop. This is a narrower, accepted gap: a call to
// SearchByCosine reached through anything other than a literal
// `<field>.session.SearchByCosine(...)` shape (a local variable alias, a
// renamed field, an embedded wrapper) is NOT caught by this check. No
// production call site in this package uses SearchByCosine on the session
// store today (verified: `grep -rn '\.SearchByCosine(' internal/mcp/*.go`
// outside _test.go files shows zero session-store call sites), so the
// residual risk is theoretical, not live.
const sessionHandoffCollidingMethod = "SearchByCosine"

// sessionHandoffStoreField is the *Server struct field name that holds the
// session store (session.StoreIface) — the only field this check knows of
// that could plausibly have a SearchByCosine method, so it is the receiver
// check sessionHandoffCollidingMethod above is restricted to.
const sessionHandoffStoreField = "session"

// sessionHandoffValueFields is an ALLOWLIST, not an inventory: these are the
// field names, across ANY type in this package, that this check currently
// knows to hold either the session store itself or a raw *db.SessionHandoff
// row — "session" (*Server's own store field), "handoff"
// (todayContextRaw's field, tools_context.go), and "h" (safeSessionHandoff's
// own field, session_handoff_safe.go). Any appearance of one of these three
// names as a selector OUTSIDE a nil comparison (`s.session == nil`, the
// idiomatic "is this configured" guard used throughout this package) is
// reportable on its own, independent of whether a sentinel method name
// appears anywhere nearby — this is what closes the bypasses where the raw
// value's SOURCE is captured into a local variable, passed as a function
// argument, or embedded into a wrapper struct, before any method is ever
// called on it.
//
// This list is NOT exhaustive by construction and cannot be made so by
// adding more names to it: a field added to hold a raw row under a FOURTH
// name — on an already-whitelisted type or a brand new one — is invisible to
// this rule the moment it is written, because the rule matches names, not
// "does this field's declared type happen to be *db.SessionHandoff or
// session.StoreIface". round-5 二軍 finding C-5-1 found the third name ("h",
// safeSessionHandoff's own field) missing from what was, at the time, a
// two-name list whose OWN doc comment claimed to be a complete inventory;
// that claim was false the moment it was written, for the same reason this
// paragraph does not claim completeness now. If this list is ever wrong
// again, the fix is the same one-line addition C-5-1 needed — it is
// list-maintenance, not a rewrite of the mechanism.
var sessionHandoffValueFields = map[string]bool{
	sessionHandoffStoreField: true,
	"handoff":                true,
	"h":                      true,
}

// TestSessionHandoffTypeConfinedToWhitelist is the mechanical half of the PR
// #158 chokepoint. It reports a violation, at file:scope granularity
// (sessionHandoffScopeWhitelist; scope includes the receiver type for a
// method, funcScopeName below), for any of:
//
//   - a selector expression named SessionHandoff, regardless of which
//     package/alias qualifies it (`db.SessionHandoff`, `dbx.SessionHandoff`,
//     ...) — catches import-alias evasion of a plain "db.SessionHandoff"
//     text scan;
//   - a call to any of sessionHandoffFreeMethods, regardless of the receiver
//     expression's shape;
//   - a call to any of sessionHandoffRawAccessors (safeSessionHandoff's own
//     rawIntent/rawContextSummary escape hatch), regardless of receiver;
//   - a call to sessionHandoffCollidingMethod (SearchByCosine) specifically
//     through the `<x>.session.SearchByCosine(...)` shape;
//   - any of sessionHandoffValueFields ("session", "handoff") appearing as a
//     selector anywhere that is not the operand of a nil comparison.
//
// Known gaps, not covered by any rule above and not planned for this
// mechanical check specifically (residual risk, accepted and named rather
// than silently absent):
//
//   - SearchByCosine reached through a non-`.session.` receiver shape (see
//     sessionHandoffCollidingMethod's doc comment);
//   - a leak that crosses into a DIFFERENT package — internal/contextpack is
//     explicitly out of scope for this test (tracked separately, GTD
//     49f2ed81);
//   - laundering a raw value through an intermediate `any`/`interface{}`
//     value in a way that never spells a matched selector name in THIS
//     scope (e.g. a generic "store it somewhere, read it back via a type
//     assertion in a different, also-unreviewed function" path) — this test
//     matches syntax, not data flow, so it cannot follow a value across an
//     opaque `any` boundary;
//   - reflection (`reflect.ValueOf(x).MethodByName("LatestHandoff").Call(...)`)
//     — a call reached this way has no `.LatestHandoff(` selector in source
//     text at all, so no AST-based rule in this file can see it;
//   - within an ALREADY-whitelisted scope, this test does not re-verify that
//     the scope still only does what its whitelist reason claims — see the
//     "Known limitation" paragraph on sessionHandoffScopeWhitelist's own doc
//     comment (round-5 一軍 finding, same root as the field-map gap above);
//   - a field added under a name NOT in sessionHandoffValueFields — even on
//     an already-whitelisted type (e.g. todayContextRaw gaining a second
//     field `prev *db.SessionHandoff` alongside its existing `handoff`
//     field) — is invisible to the field-selector rule until that name is
//     added to the map; see sessionHandoffValueFields' own doc comment
//     (round-5 二軍 finding C-5-1, reproduced as that finding's q12b probe).
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
// introduces: funcScopeName for a *ast.FuncDecl, or the type/var/const
// name(s) for a *ast.GenDecl's specs. A GenDecl grouping multiple specs in
// one `var ( ... )`/`type ( ... )` block yields one scope per spec, so a
// whitelisted sibling never accidentally shields an unreviewed one declared
// in the same block.
func declScopes(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		return []string{funcScopeName(d)}
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

// funcScopeName returns "ReceiverType.MethodName" for a method, or the bare
// function name for a free function. Including the receiver type (round-5
// 二軍 finding m-4-1) closes a name-collision gap: a bare method name alone
// would let one whitelisted method silently exempt an unrelated method of
// the same name on a DIFFERENT type declared in the same file.
func funcScopeName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return d.Name.Name
	}
	return receiverTypeName(d.Recv.List[0].Type) + "." + d.Name.Name
}

// receiverTypeName unwraps a possible pointer receiver (`*T` -> `T`) and
// returns the bare type identifier, or "?" for a receiver shape this
// package never actually uses (generic type parameters, etc.) so a scope
// name is still produced rather than a panic.
func receiverTypeName(t ast.Expr) string {
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return "?"
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
		if sessionHandoffRawAccessors[sel.Sel.Name] {
			t.Errorf("%s:%s calls safeSessionHandoff.%s, the wrapper's own unhardened escape hatch "+
				"(session_handoff_safe.go) — its return value must never cross a serialization "+
				"boundary; use safeSessionHandoff's MarshalJSON (or hardenedIntent) instead, or add "+
				"\"%s:%s\" to sessionHandoffScopeWhitelist with a reason if this scope only uses the "+
				"raw text for an internal, non-serializing comparison", file, scope, sel.Sel.Name, file, scope)
		}
		if sel.Sel.Name == sessionHandoffCollidingMethod &&
			isFieldSelectorIn(sel.X, map[string]bool{sessionHandoffStoreField: true}) {
			t.Errorf("%s:%s calls %s on the session store, which returns raw handoff rows — use "+
				"safeSessionHandoff (session_handoff_safe.go) instead, or add \"%s:%s\" to "+
				"sessionHandoffScopeWhitelist with a reason if this scope is itself a reviewed "+
				"hardened-view builder", file, scope, sessionHandoffCollidingMethod, file, scope)
		}
		if isFieldSelectorIn(sel, sessionHandoffValueFields) && !nilExempt[sel] {
			t.Errorf("%s:%s uses a session-handoff-holding field (%q) outside a nil comparison — "+
				"this is exactly how a raw handoff escapes into a local variable, function argument, "+
				"or embedded wrapper without ever spelling a method or type name in this scope; use "+
				"safeSessionHandoff (session_handoff_safe.go) instead, or add \"%s:%s\" to "+
				"sessionHandoffScopeWhitelist with a reason if this scope is itself reviewed and "+
				"leaks nothing", file, scope, sel.Sel.Name, file, scope)
		}
		return true
	})
}

// isNilIdent reports whether e is the literal identifier `nil`.
func isNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// isFieldSelectorIn reports whether e is a selector expression whose field
// name is a key of names — e.g. `s.session` matches names={"session":true}.
// Deliberately does not attempt full type resolution (no go/types
// dependency): matching on the field NAME is sufficient here because every
// name in names is checked wherever it appears as a selector, not just as a
// call receiver.
func isFieldSelectorIn(e ast.Expr, names map[string]bool) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return names[sel.Sel.Name]
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
