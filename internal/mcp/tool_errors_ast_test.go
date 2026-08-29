package mcp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// [F170-08] U14's provenance gate.
//
// Why a second gate exists next to TestNoRawErrorReachesToolResult: that one
// asks a purely textual question — "does an error-shaped identifier appear
// INSIDE this NewToolResultError( call's argument list?" — and
// tools_proposal.go answered "no" while shipping the pgx error verbatim. The
// materialise layer returns (any, string); the error was consumed two frames
// below the call and only a `string` named errMsg travelled up. `errMsg` does
// not match `\b\w*[eE]rr\b` (the trailing "Msg" eats the closing word
// boundary), so the gate saw `NewToolResultError(errMsg)` and passed it.
//
// Two rejected alternatives, and why:
//
//   - Widen the regex to `\w*[eE]rr[A-Za-z]*`. It reds every
//     NewToolResultError(errMsgInvalidProjectIDUUID)-style call site — 9 of
//     them today — whose value is a package const holding a fixed validation
//     sentence. The gate would then need an exemption list to stay usable.
//   - Keep the regex and add that exemption list. It has to be hand-maintained,
//     and this repo has the receipts on hand-maintained lists drifting
//     (sqlc.yaml's comment records a schema list that tracked 22 of 67
//     migrations). Worse, it leaves the gate structurally blind to indirection:
//     the next handler to name its variable `detailMsg` walks straight through.
//
// So this gate answers the question the leak was actually about — where did
// this value COME FROM — by walking the package's AST: local assignments,
// package-level consts, package-local function returns at the right tuple
// index, and function parameters back to their call sites.
//
// FAIL-CLOSED is the point, not a detail. A gate that shrugs at what it cannot
// follow reproduces the bug it exists to stop, one indirection deeper. So
// provUnknown is a violation exactly like provError is, and
// TestSEC_U14ProvenanceFailsClosedOnUntraceableSource pins that.
//
// Two boundaries are deliberate, documented, and pinned by
// TestSEC_U14ProvenanceStaticConstSitesStayGreen rather than left implicit:
//
//   - A struct field read (`spec.args`, `a.uuidMsg`) is treated as data, not as
//     an error, unless the field's own name is error-shaped. Syntactic
//     provenance ends at a field; following it would need types.
//   - A value assigned from a call into ANOTHER package is judged by the
//     RECEIVING identifier's name, because nothing syntactic remains to
//     follow: `x, err := pkg.F()` makes err an error (Go's naming convention
//     is the only available signal, and it is the same one the previous gate
//     relied on — applied at the DEFINITION site now, which is what closes
//     the indirection), while `msg := pkg.G()` is untraceable and therefore
//     still a violation. The same call used inline as an expression, with no
//     receiving identifier, is data: strings.Join(...) is not an error.

// provenance is the verdict for one expression. Ordering matters: combine()
// takes the maximum, so "an error reaches this" outranks "I could not tell",
// which outranks "provably not an error".
type provenance int

const (
	provStatic provenance = iota
	provUnknown
	provError
)

func (p provenance) String() string {
	switch p {
	case provError:
		return "ERROR-DERIVED"
	case provUnknown:
		return "UNTRACEABLE"
	default:
		return "static"
	}
}

func combine(a, b provenance) provenance {
	if b > a {
		return b
	}
	return a
}

// errShapedName matches an identifier or field name that Go convention marks
// as holding an error: err, readErr, ErrNotFound, dbErr. It is deliberately
// anchored on word parts rather than the whole identifier so `errMsg` does NOT
// match — errMsg holds a STRING, and whether that string came from an error is
// the question this gate traces rather than guesses.
var errShapedName = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)?[eE]rr(or)?$`)

// maxProvenanceDepth bounds the trace. Exceeding it yields provUnknown (not
// provStatic) so a pathological chain fails closed like everything else.
const maxProvenanceDepth = 32

// errProvenanceAnalyzer holds the parsed package and answers "where did this
// expression's value come from".
type errProvenanceAnalyzer struct {
	fset *token.FileSet
	// funcs maps a declared function or method NAME to every declaration
	// carrying it. Name-keyed on purpose: without type information there is no
	// way to tell which receiver `s.materializeFromPayloadPg(...)` binds to, so
	// same-named declarations are all traced and their verdicts combined —
	// conservative in the fail-closed direction.
	funcs map[string][]*ast.FuncDecl
	// pkgValues maps a package-level const/var name to its single initialiser.
	pkgValues map[string]ast.Expr
	// sanctioned names the helpers declared in tool_errors.go. Reaching one
	// ends the trace green: those functions ARE the reviewed decision about
	// what a client may see, and re-deriving that decision here would just
	// duplicate their doc comments in regex form.
	sanctioned map[string]bool
	// callSites indexes every call in the package by callee name, for tracing
	// a parameter back to the arguments it is given.
	callSites map[string][]*ast.CallExpr
	// bindings makes the trace context-sensitive: while stepping THROUGH a
	// call, the callee's parameters are bound to that call's actual arguments.
	//
	// Without it a parameter can only be resolved by unioning every call site
	// in the package, which is worse than imprecise — it is wrong in one
	// direction. clipSafe (tools_context.go) has dozens of callers; one
	// argument this analyzer cannot follow made clipSafe's RESULT untraceable
	// at every call site, including tools_arch.go:358 where the argument is a
	// plain local. Keyed by declaration and saved/restored around each step so
	// nested and recursive traces do not see each other's bindings.
	bindings map[*ast.FuncDecl]map[string]boundArg
}

// boundArg is one actual argument plus the scope it must be classified in —
// the caller's, not the callee's.
type boundArg struct {
	expr  ast.Expr
	scope *ast.FuncDecl
}

// bindParams pairs decl's parameters with a call's actual arguments. A
// mismatched count (variadic or a spread call) binds nothing rather than
// binding the wrong expression to a name.
func bindParams(decl *ast.FuncDecl, args []ast.Expr, scope *ast.FuncDecl) map[string]boundArg {
	out := map[string]boundArg{}
	if decl.Type == nil || decl.Type.Params == nil {
		return out
	}
	i := 0
	for _, field := range decl.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, n := range field.Names {
			if i < len(args) && n.Name != "_" {
				out[n.Name] = boundArg{expr: args[i], scope: scope}
			}
			i++
		}
	}
	return out
}

func newErrProvenanceAnalyzer(t *testing.T) *errProvenanceAnalyzer {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing package source: %v", err)
	}
	a := &errProvenanceAnalyzer{
		fset:       fset,
		funcs:      map[string][]*ast.FuncDecl{},
		pkgValues:  map[string]ast.Expr{},
		sanctioned: map[string]bool{},
		callSites:  map[string][]*ast.CallExpr{},
		bindings:   map[*ast.FuncDecl]map[string]boundArg{},
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			inHelpers := strings.HasSuffix(name, "tool_errors.go")
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					a.funcs[d.Name.Name] = append(a.funcs[d.Name.Name], d)
					if inHelpers {
						a.sanctioned[d.Name.Name] = true
					}
				case *ast.GenDecl:
					a.indexValueSpecs(d)
				}
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if name := calleeName(call); name != "" {
						a.callSites[name] = append(a.callSites[name], call)
					}
				}
				return true
			})
		}
	}
	return a
}

// indexValueSpecs records package-level const/var initialisers so a call site
// passing errMsgInvalidProjectIDUUID resolves to the string literal behind it
// instead of being judged on its name.
func (a *errProvenanceAnalyzer) indexValueSpecs(d *ast.GenDecl) {
	if d.Tok != token.CONST && d.Tok != token.VAR {
		return
	}
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Values) != len(vs.Names) {
			continue
		}
		for i, n := range vs.Names {
			a.pkgValues[n.Name] = vs.Values[i]
		}
	}
}

// calleeName returns the bare function/method name of a call, or "" when the
// callee is not a plain identifier or selector (a call through a func value).
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// traceKey identifies one in-progress trace so cycles terminate.
type traceKey struct {
	kind string
	name string
	idx  int
}

// classify answers where expr's value came from, resolving identifiers against
// fn's body and, when that is not enough, against the rest of the package.
func (a *errProvenanceAnalyzer) classify(expr ast.Expr, fn *ast.FuncDecl, seen map[traceKey]bool, depth int) provenance {
	if expr == nil {
		return provUnknown
	}
	if depth > maxProvenanceDepth {
		return provUnknown
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		return provStatic
	case *ast.Ident:
		return a.classifyIdent(e, fn, seen, depth)
	case *ast.SelectorExpr:
		// A field read. Provenance ends here (see the boundaries note above):
		// error-shaped field name → error, anything else → data.
		if errShapedName.MatchString(e.Sel.Name) {
			return provError
		}
		return provStatic
	case *ast.CallExpr:
		return a.classifyCall(e, fn, seen, depth)
	case *ast.BinaryExpr:
		return combine(a.classify(e.X, fn, seen, depth+1), a.classify(e.Y, fn, seen, depth+1))
	case *ast.ParenExpr:
		return a.classify(e.X, fn, seen, depth+1)
	case *ast.UnaryExpr:
		return a.classify(e.X, fn, seen, depth+1)
	case *ast.StarExpr:
		return a.classify(e.X, fn, seen, depth+1)
	case *ast.IndexExpr:
		return a.classify(e.X, fn, seen, depth+1)
	case *ast.SliceExpr:
		return a.classify(e.X, fn, seen, depth+1)
	case *ast.TypeAssertExpr:
		return a.classify(e.X, fn, seen, depth+1)
	case *ast.CompositeLit:
		out := provStatic
		for _, elt := range e.Elts {
			out = combine(out, a.classify(elt, fn, seen, depth+1))
		}
		return out
	case *ast.KeyValueExpr:
		return a.classify(e.Value, fn, seen, depth+1)
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.StructType,
		*ast.FuncType, *ast.InterfaceType, *ast.Ellipsis:
		// A TYPE, not a value — `make([]string, 0, n)`'s first argument. It
		// carries no runtime value at all, so it cannot carry an error; before
		// this case it fell to the default and made every slice built with
		// make() untraceable, which is how two ordinary helpers
		// (allowedTargets, boundaryMarkers) poisoned the messages built from
		// them.
		return provStatic
	default:
		return provUnknown
	}
}

// classifyIdent resolves a bare identifier: builtin, package-level value,
// local definition, or parameter.
func (a *errProvenanceAnalyzer) classifyIdent(id *ast.Ident, fn *ast.FuncDecl, seen map[traceKey]bool, depth int) provenance {
	switch id.Name {
	case "nil", "true", "false", "iota", "_":
		return provStatic
	}
	if v, ok := a.pkgValues[id.Name]; ok {
		key := traceKey{kind: "pkgvalue", name: id.Name}
		if seen[key] {
			return provStatic
		}
		seen[key] = true
		defer delete(seen, key)
		return a.classify(v, nil, seen, depth+1)
	}
	if fn == nil || fn.Body == nil {
		return a.nameVerdict(id.Name)
	}
	// Local identifiers are routinely self-referential — `out = append(out, t)`
	// and `s = strings.ReplaceAll(s, ...)` are both in this package — so the
	// walk has to break the cycle explicitly. Without this the depth limit
	// breaks it instead, and the depth limit's verdict is provUnknown, which
	// turned two ordinary accumulator loops (allowedTargets,
	// neutralizeBoundaryMarkers) into untraceable dead ends that poisoned every
	// message built from them. Re-entry contributes provStatic: the other
	// assignments to the same variable decide it.
	key := traceKey{kind: "local", name: fn.Name.Name + "." + id.Name, idx: int(fn.Pos())}
	if seen[key] {
		return provStatic
	}
	seen[key] = true
	defer delete(seen, key)
	if p, found := a.classifyLocalDefinition(id, fn, seen, depth); found {
		return p
	}
	// This function was reached THROUGH a call: use that call's actual
	// argument, classified in the caller's scope, rather than the union over
	// every call site.
	if b, ok := a.bindings[fn][id.Name]; ok {
		return a.classify(b.expr, b.scope, seen, depth+1)
	}
	if idx, typ, ok := paramInfo(fn, id.Name); ok {
		// A parameter's declared type settles most cases without walking every
		// call site: an int or a []Task cannot carry an error's text.
		if v, decisive := declaredTypeVerdict(typ); decisive {
			return v
		}
		return a.classifyParam(fn, idx, id.Name, seen, depth)
	}
	// Declared nowhere this analyzer can see (a closure variable from an
	// enclosing function literal, say). Fail closed unless the name itself
	// settles it.
	if errShapedName.MatchString(id.Name) {
		return provError
	}
	return provUnknown
}

// nameVerdict is the last-resort judgement for an identifier with no reachable
// definition.
func (a *errProvenanceAnalyzer) nameVerdict(name string) provenance {
	if errShapedName.MatchString(name) {
		return provError
	}
	return provUnknown
}

// classifyLocalDefinition scans fn's body for every place id is bound —
// `:=`/`=` assignments (including tuple returns), var/const declarations,
// range clauses and type switches — and combines their verdicts. found is
// false when id is not assigned anywhere in fn.
func (a *errProvenanceAnalyzer) classifyLocalDefinition(
	id *ast.Ident, fn *ast.FuncDecl, seen map[traceKey]bool, depth int,
) (provenance, bool) {
	out, found := provStatic, false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range stmt.Lhs {
				lid, ok := lhs.(*ast.Ident)
				if !ok || lid.Name != id.Name {
					continue
				}
				found = true
				out = combine(out, a.classifyRHS(stmt.Rhs, i, id.Name, fn, seen, depth))
			}
		case *ast.ValueSpec:
			for i, n := range stmt.Names {
				if n.Name != id.Name {
					continue
				}
				if i < len(stmt.Values) {
					found = true
					out = combine(out, a.classify(stmt.Values[i], fn, seen, depth+1))
					continue
				}
				// `var x T` with no initialiser, filled through a pointer
				// (json.Unmarshal(&x) is the shape all over this package).
				// There is no expression to follow, but the DECLARED TYPE
				// answers the only question this gate asks: `var actions
				// []session.NextAction` cannot be an error, `var err error`
				// is one. A string or bare interface stays undecided —
				// those CAN hold an error's text, which is the thing being
				// tracked — so the trace continues and ends untraceable.
				if v, decisive := declaredTypeVerdict(stmt.Type); decisive {
					found = true
					out = combine(out, v)
				}
			}
		case *ast.RangeStmt:
			// A range key or value is derived from the ranged collection —
			// classify THAT, so `for name, spec := range m` does not become an
			// untraceable dead end.
			if identIs(stmt.Key, id.Name) || identIs(stmt.Value, id.Name) {
				found = true
				out = combine(out, a.classify(stmt.X, fn, seen, depth+1))
			}
		}
		return true
	})
	return out, found
}

// classifyRHS resolves the right-hand side of an assignment for the lhsIdx'th
// left-hand identifier, which is where multi-value returns are unpacked.
func (a *errProvenanceAnalyzer) classifyRHS(
	rhs []ast.Expr, lhsIdx int, lhsName string, fn *ast.FuncDecl, seen map[traceKey]bool, depth int,
) provenance {
	if len(rhs) == 0 {
		return provUnknown
	}
	if len(rhs) > lhsIdx && len(rhs) > 1 {
		return a.classify(rhs[lhsIdx], fn, seen, depth+1)
	}
	if len(rhs) != 1 {
		return provUnknown
	}
	call, ok := rhs[0].(*ast.CallExpr)
	if !ok {
		// `x := y` / `x := <expr>` with a single lhs.
		if lhsIdx == 0 {
			return a.classify(rhs[0], fn, seen, depth+1)
		}
		// A type assertion or map index in comma-ok form: the second value is
		// a bool, never a message.
		return provStatic
	}
	return a.classifyCallResult(call, lhsIdx, lhsName, fn, seen, depth)
}

// classifyCall handles a call used directly as an expression (result 0).
func (a *errProvenanceAnalyzer) classifyCall(
	call *ast.CallExpr, fn *ast.FuncDecl, seen map[traceKey]bool, depth int,
) provenance {
	return a.classifyCallResult(call, 0, "", fn, seen, depth)
}

// classifyCallResult is the heart of the trace: it decides what the idx'th
// result of this call carries.
func (a *errProvenanceAnalyzer) classifyCallResult(
	call *ast.CallExpr, idx int, lhsName string, fn *ast.FuncDecl, seen map[traceKey]bool, depth int,
) provenance {
	name := calleeName(call)
	if name == "" {
		if isTypeConversionCallee(call.Fun) {
			// A conversion whose callee is a TYPE literal rather than an
			// identifier — `[]rune(s)`, `(*T)(x)`. It carries its operand
			// through unchanged, so classify that. truncateRunes'
			// `string(runes[:n])` chain runs through exactly this shape, and
			// treating it as an opaque call made clipSafe — and therefore
			// every message clipSafe touches — untraceable.
			out := provStatic
			for _, arg := range call.Args {
				out = combine(out, a.classify(arg, fn, seen, depth+1))
			}
			return out
		}
		// A call through a func-typed value — no declaration to follow.
		return provUnknown
	}
	// `x.Error()` turns an error into text. This is the shape [F170-07] had to
	// remove from batchAccept, and it is unambiguous.
	if name == "Error" && len(call.Args) == 0 {
		if _, isSel := call.Fun.(*ast.SelectorExpr); isSel {
			return provError
		}
	}
	if isFmtCall(call, "Errorf") {
		return provError
	}
	if isFmtCall(call, "Sprintf", "Sprint", "Sprintln") {
		out := provStatic
		for _, arg := range call.Args {
			out = combine(out, a.classify(arg, fn, seen, depth+1))
		}
		return out
	}
	if a.sanctioned[name] {
		return provStatic
	}
	if decls := a.funcs[name]; len(decls) > 0 {
		key := traceKey{kind: "func", name: name, idx: idx}
		if seen[key] {
			return provStatic // cycle: other paths decide
		}
		seen[key] = true
		defer delete(seen, key)
		out := provStatic
		for _, d := range decls {
			// Step INTO the callee with this call's arguments bound to its
			// parameters, so a parameter resolves to what THIS call passes
			// rather than to the union over every caller in the package.
			prev, had := a.bindings[d]
			a.bindings[d] = bindParams(d, call.Args, fn)
			out = combine(out, a.classifyFuncResult(d, idx, seen, depth))
			if had {
				a.bindings[d] = prev
			} else {
				delete(a.bindings, d)
			}
		}
		return out
	}
	if isBuiltinConversion(name) {
		out := provStatic
		for _, arg := range call.Args {
			out = combine(out, a.classify(arg, fn, seen, depth+1))
		}
		return out
	}
	if _, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
		// A call into another package, or a method on a type declared
		// elsewhere. Nothing syntactic remains to follow, so the RECEIVING
		// identifier's name decides: `_, err := pkg.F()` is an error;
		// `warnings := validator.CheckTaskInput(...)` is data. Used inline
		// with no receiving identifier it is data too — strings.Join(...) is
		// not an error.
		//
		// Treating this case as untraceable instead was measured, not
		// assumed: it flagged 43 call sites across 13 files, almost all of
		// them a Sprintf over a []string or a struct field, because ONE
		// unresolvable leaf poisons an entire chain. A gate that reds a
		// seventh of the package on its first run does not get fixed, it gets
		// deleted — and this is the exact judgement the pre-existing regex
		// gate already made, so nothing that passed before newly passes now.
		if lhsName != "" && errShapedName.MatchString(lhsName) {
			return provError
		}
		return provStatic
	}
	// A bare identifier this package does not declare as a function: a
	// func-typed variable or parameter. There is no declaration anywhere to
	// follow, which is the untraceable case proper — fail closed.
	return provUnknown
}

// classifyFuncResult classifies the idx'th result of every return statement in
// decl.
func (a *errProvenanceAnalyzer) classifyFuncResult(
	decl *ast.FuncDecl, idx int, seen map[traceKey]bool, depth int,
) provenance {
	if decl.Body == nil {
		return provUnknown
	}
	out, sawReturn := provStatic, false
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		sawReturn = true
		switch {
		case len(ret.Results) == 0:
			// Naked return with named results — the value was set somewhere
			// else in the body and this analyzer does not follow that.
			out = combine(out, provUnknown)
		case len(ret.Results) == 1 && idx > 0:
			// `return other(...)` passing a whole tuple through.
			if call, isCall := ret.Results[0].(*ast.CallExpr); isCall {
				out = combine(out, a.classifyCallResult(call, idx, "", decl, seen, depth+1))
			} else {
				out = combine(out, provUnknown)
			}
		case idx < len(ret.Results):
			out = combine(out, a.classify(ret.Results[idx], decl, seen, depth+1))
		default:
			out = combine(out, provUnknown)
		}
		return true
	})
	if !sawReturn {
		return provUnknown
	}
	return out
}

// classifyParam traces a parameter back to the arguments every call site
// supplies for it.
func (a *errProvenanceAnalyzer) classifyParam(
	fn *ast.FuncDecl, idx int, name string, seen map[traceKey]bool, depth int,
) provenance {
	if errShapedName.MatchString(name) {
		return provError
	}
	key := traceKey{kind: "param", name: fn.Name.Name, idx: idx}
	if seen[key] {
		return provStatic
	}
	seen[key] = true
	defer delete(seen, key)

	sites := a.callSites[fn.Name.Name]
	if len(sites) == 0 {
		// Nobody calls it (yet). Fail closed: a message whose only source is a
		// parameter no caller supplies cannot be shown to be safe.
		return provUnknown
	}
	out := provStatic
	for _, call := range sites {
		if idx >= len(call.Args) {
			out = combine(out, provUnknown) // variadic or spread call
			continue
		}
		out = combine(out, a.classify(call.Args[idx], a.enclosingFunc(call), seen, depth+1))
	}
	return out
}

// enclosingFunc finds the declaration a call sits inside, so the call's
// arguments resolve against the right local scope.
func (a *errProvenanceAnalyzer) enclosingFunc(call *ast.CallExpr) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, decls := range a.funcs {
		for _, d := range decls {
			if d.Body == nil {
				continue
			}
			if call.Pos() >= d.Body.Pos() && call.End() <= d.Body.End() {
				found = d
			}
		}
	}
	return found
}

// paramInfo returns the flat argument index and declared type of fn's
// parameter called name.
func paramInfo(fn *ast.FuncDecl, name string) (int, ast.Expr, bool) {
	if fn.Type == nil || fn.Type.Params == nil {
		return 0, nil, false
	}
	i := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			i++
			continue
		}
		for _, n := range field.Names {
			if n.Name == name {
				return i, field.Type, true
			}
			i++
		}
	}
	return 0, nil, false
}

// declaredTypeVerdict answers this gate's question from a declared type alone,
// and reports whether the answer is decisive.
//
// Undecided is reserved for the types that CAN carry an error's text — string
// and a bare interface — because that is precisely what the gate tracks;
// everything else either mentions error in its type (an error) or cannot hold
// one (data). Without this, every `var items []finishWorkEvidenceItem` filled
// by json.Unmarshal became an untraceable dead end and poisoned every message
// built from it: 10 of the 13 remaining false positives in tools_worksession.go
// alone came from that one shape.
func declaredTypeVerdict(t ast.Expr) (provenance, bool) {
	if t == nil {
		return provStatic, false
	}
	errorish := false
	ast.Inspect(t, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && errShapedName.MatchString(id.Name) {
			errorish = true
		}
		return true
	})
	if errorish {
		return provError, true
	}
	switch typ := t.(type) {
	case *ast.Ident:
		if typ.Name == "string" || typ.Name == "any" {
			return provStatic, false
		}
	case *ast.InterfaceType:
		return provStatic, false
	}
	return provStatic, true
}

func identIs(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

func isFmtCall(call *ast.CallExpr, names ...string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return false
	}
	for _, n := range names {
		if sel.Sel.Name == n {
			return true
		}
	}
	return false
}

// isTypeConversionCallee reports whether a call's callee is a type literal, so
// the call is a conversion rather than a function call.
func isTypeConversionCallee(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.StructType,
		*ast.InterfaceType, *ast.FuncType, *ast.StarExpr:
		return true
	case *ast.ParenExpr:
		return isTypeConversionCallee(t.X)
	}
	return false
}

// isBuiltinConversion reports whether name is a Go builtin or a predeclared
// type used as a conversion. These have no declaration in this package, so
// without the list they would fall through to the "func-typed variable"
// fail-closed branch and red every `string(b)` in the package.
func isBuiltinConversion(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete", "imag",
		"len", "make", "max", "min", "new", "panic", "print", "println", "real", "recover",
		"bool", "byte", "error", "float32", "float64", "int", "int8", "int16", "int32",
		"int64", "rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "any":
		return true
	}
	return false
}

// clientMessageSite is one place a string becomes something an MCP client can
// read.
type clientMessageSite struct {
	pos     string
	kind    string
	snippet string
	verdict provenance
}

// collectClientMessageSites walks every non-test file and classifies both
// surfaces U14 owns:
//
//  1. mcp.NewToolResultError(X) — the direct error surface.
//  2. Err-shaped fields of composite literals inside any function that also
//     calls jsonText(...) — the surface U13 explicitly deferred here
//     ("ErrMsg carries a store error string, U14's jurisdiction",
//     u13_stored_data_inventory_test.go's storedDataComputedExclusions) and
//     that U14's own NewToolResultError( needle could never match. Each gate
//     assumed the other held it; scanning it here is what ends that.
func (a *errProvenanceAnalyzer) collectClientMessageSites(t *testing.T) []clientMessageSite {
	t.Helper()
	var out []clientMessageSite
	for _, decls := range a.funcs {
		for _, decl := range decls {
			if decl.Body == nil || strings.HasSuffix(a.fset.Position(decl.Pos()).Filename, "tool_errors.go") {
				continue
			}
			out = append(out, a.sitesInFunc(decl)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out
}

func (a *errProvenanceAnalyzer) sitesInFunc(decl *ast.FuncDecl) []clientMessageSite {
	var out []clientMessageSite
	serialises := false
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && calleeName(call) == "jsonText" {
			serialises = true
		}
		return true
	})
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			if calleeName(node) != "NewToolResultError" || len(node.Args) == 0 {
				return true
			}
			v := a.classify(node.Args[0], decl, map[traceKey]bool{}, 0)
			out = append(out, clientMessageSite{
				pos:     a.shortPos(node.Pos()),
				kind:    "NewToolResultError",
				snippet: exprText(a.fset, node.Args[0]),
				verdict: v,
			})
		case *ast.CompositeLit:
			if !serialises {
				return true
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || !errShapedFieldName(key.Name) {
					continue
				}
				v := a.classify(kv.Value, decl, map[traceKey]bool{}, 0)
				out = append(out, clientMessageSite{
					pos:     a.shortPos(kv.Pos()),
					kind:    "jsonText field " + key.Name,
					snippet: exprText(a.fset, kv.Value),
					verdict: v,
				})
			}
		}
		return true
	})
	return out
}

// errShapedFieldName reports whether a struct field name marks the field as
// carrying error text: ErrMsg, Error, LastError, or an unexported errMsg.
//
// Case matters, which a lowercased substring check loses: "DeferredTaskIDs"
// contains the letters e-r-r ("Def-err-ed") and was flagged by exactly that
// mistake, so a plain []uuid.UUID field became a client-message site.
func errShapedFieldName(name string) bool {
	return strings.Contains(name, "Err") || strings.HasPrefix(name, "err")
}

func (a *errProvenanceAnalyzer) shortPos(p token.Pos) string {
	pos := a.fset.Position(p)
	return fmt.Sprintf("%s:%d", trimDir(pos.Filename), pos.Line)
}

func trimDir(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// exprText renders an expression back to source-ish text for the violation
// message, so a failure names the actual expression rather than only a line.
func exprText(fset *token.FileSet, e ast.Expr) string {
	start, end := fset.Position(e.Pos()), fset.Position(e.End())
	if start.Filename != end.Filename || start.Line != end.Line {
		return fmt.Sprintf("<multi-line expression at %s:%d>", trimDir(start.Filename), start.Line)
	}
	return fmt.Sprintf("<%s:%d cols %d-%d>", trimDir(start.Filename), start.Line, start.Column, end.Column)
}

// TestSEC_U14BypassViaErrMsgIndirection is [F170-08]'s gate over the real
// package.
//
// Revert any one of [F170-07]'s 27 tools_proposal.go sites back to
// fmt.Sprintf("creating goal: %v", err) and this test goes red: errMsg at
// acceptProposalPg traces into materializeFromPayloadPg's second result, into
// that Sprintf, into `err`, and `err` was assigned from a call into another
// package — an error by name. The pre-existing regex gate stays green through
// exactly that mutation, which is why this test exists.
func TestSEC_U14BypassViaErrMsgIndirection(t *testing.T) {
	a := newErrProvenanceAnalyzer(t)
	sites := a.collectClientMessageSites(t)
	if len(sites) == 0 {
		t.Fatal("provenance scan found no client message sites at all — the scan itself is broken")
	}
	var violations []string
	for _, s := range sites {
		if s.verdict == provStatic {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s  [%s]  %s %s",
			s.pos, s.verdict, s.kind, s.snippet))
	}
	if len(violations) > 0 {
		t.Errorf("%d client-visible message(s) out of %d scanned are error-derived or untraceable. "+
			"ERROR-DERIVED: route the value through storeErrorText (server-side failure) or "+
			"inputErrorText (the caller's own argument failed validation), see tool_errors.go. "+
			"UNTRACEABLE: this gate fails closed by design — make the source resolvable or route it "+
			"through a helper; NEVER relax the analyzer to make it green:",
			len(violations), len(sites))
		for _, v := range violations {
			t.Error("  " + v)
		}
	}
}

// analyzerFromSource builds an analyzer over a synthetic single-file package,
// so the unit tests below exercise the classifier on shapes that must not be
// added to the real package just to be tested.
func analyzerFromSource(t *testing.T, src string) (*errProvenanceAnalyzer, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing synthetic source: %v", err)
	}
	a := &errProvenanceAnalyzer{
		fset:       fset,
		funcs:      map[string][]*ast.FuncDecl{},
		pkgValues:  map[string]ast.Expr{},
		sanctioned: map[string]bool{"storeErrorText": true, "inputErrorText": true},
		callSites:  map[string][]*ast.CallExpr{},
		bindings:   map[*ast.FuncDecl]map[string]boundArg{},
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			a.funcs[d.Name.Name] = append(a.funcs[d.Name.Name], d)
		case *ast.GenDecl:
			a.indexValueSpecs(d)
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name := calleeName(call); name != "" {
				a.callSites[name] = append(a.callSites[name], call)
			}
		}
		return true
	})
	return a, file
}

// verdictFor classifies the argument of the single NewToolResultError call in
// a synthetic source, or the single Err-shaped composite field when kindField
// is non-empty.
func verdictFor(t *testing.T, a *errProvenanceAnalyzer, file *ast.File) provenance {
	t.Helper()
	var got *provenance
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall || calleeName(call) != "NewToolResultError" || len(call.Args) == 0 {
				return true
			}
			v := a.classify(call.Args[0], fn, map[traceKey]bool{}, 0)
			got = &v
			return false
		})
	}
	if got == nil {
		t.Fatal("synthetic source contained no NewToolResultError call to classify")
	}
	return *got
}

// TestSEC_U14ProvenanceCatchesIndirectErrMsg is the mutation the old regex gate
// cannot see, reduced to its smallest form: the error never appears inside the
// NewToolResultError call, only two frames below it.
func TestSEC_U14ProvenanceCatchesIndirectErrMsg(t *testing.T) {
	a, file := analyzerFromSource(t, `package mcp

func materialize() (any, string) {
	row, err := store.Create()
	if err != nil {
		return nil, fmt.Sprintf("creating goal: %v", err)
	}
	return row, ""
}

func handler() *mcp.CallToolResult {
	created, errMsg := materialize()
	_ = created
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg)
	}
	return nil
}
`)
	if got := verdictFor(t, a, file); got != provError {
		t.Errorf("verdict = %v, want ERROR-DERIVED — an error reaching the client through one "+
			"string-valued return is exactly the bypass [F170-08] exists to close", got)
	}
}

// TestSEC_U14ProvenanceAcceptsRoutedErrMsg is the same shape AFTER [F170-07]:
// the indirection is unchanged, only the producer routes through the helper.
// Without this the gate could be "passing" by flagging every errMsg it sees,
// which would make the fix indistinguishable from the bug.
func TestSEC_U14ProvenanceAcceptsRoutedErrMsg(t *testing.T) {
	a, file := analyzerFromSource(t, `package mcp

func materialize() (any, string) {
	row, err := store.Create()
	if err != nil {
		return nil, storeErrorText("creating goal", err)
	}
	return row, ""
}

func handler() *mcp.CallToolResult {
	created, errMsg := materialize()
	_ = created
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg)
	}
	return nil
}
`)
	if got := verdictFor(t, a, file); got != provStatic {
		t.Errorf("verdict = %v, want static — storeErrorText IS the sanctioned exit; flagging it "+
			"would leave no way to write a correct handler", got)
	}
}

// TestSEC_U14ProvenanceFailsClosedOnUntraceableSource pins the property
// dispatch item ④ names: a gate that shrugs at what it cannot follow is not a
// gate. Here the message comes from a parameter that no call site in the
// package supplies, so there is nothing to trace — and the verdict must be a
// violation, not a pass.
func TestSEC_U14ProvenanceFailsClosedOnUntraceableSource(t *testing.T) {
	a, file := analyzerFromSource(t, `package mcp

func handler(detail string) *mcp.CallToolResult {
	return mcp.NewToolResultError(detail)
}
`)
	got := verdictFor(t, a, file)
	if got == provStatic {
		t.Fatal("verdict = static for a source the analyzer cannot trace — fail-OPEN. " +
			"This is the failure mode the whole gate exists to prevent, relocated into the gate")
	}
	if got != provUnknown {
		t.Errorf("verdict = %v, want UNTRACEABLE (fail-closed but distinguishable from a "+
			"confirmed leak, so a human knows which judgement to make)", got)
	}
}

// TestSEC_U14ProvenanceFailsClosedOnFuncValueCall is the second untraceable
// shape: a call through a func-typed value has no declaration to follow.
func TestSEC_U14ProvenanceFailsClosedOnFuncValueCall(t *testing.T) {
	a, file := analyzerFromSource(t, `package mcp

func handler(render func() string) *mcp.CallToolResult {
	msg := render()
	return mcp.NewToolResultError(msg)
}
`)
	if got := verdictFor(t, a, file); got == provStatic {
		t.Error("verdict = static for a message produced by an opaque func value — fail-open")
	}
}

// TestSEC_U14ProvenanceStaticConstSitesStayGreen is the positive control the
// rejected "widen the regex" option would have failed. errMsg-PREFIXED package
// constants hold fixed validation sentences; they must resolve to their string
// literal rather than being judged on their name, or this gate needs the very
// exemption list it was designed to avoid.
func TestSEC_U14ProvenanceStaticConstSitesStayGreen(t *testing.T) {
	a, file := analyzerFromSource(t, `package mcp

const errMsgInvalidProjectIDUUID = "invalid project_id UUID"

func handler() *mcp.CallToolResult {
	return mcp.NewToolResultError(errMsgInvalidProjectIDUUID)
}
`)
	if got := verdictFor(t, a, file); got != provStatic {
		t.Errorf("verdict = %v, want static — a package const holding a literal is not an error, "+
			"and flagging it is what made the regex-widening option unusable", got)
	}
}

// TestSEC_U14ProvenanceStaticConstSitesStayGreenInRealPackage is the same
// control against the real code: the acceptance criterion says the existing
// NewToolResultError(errMsg*) sites must not turn red. It asserts on the count
// the scan actually finds rather than on a number copied from the ticket.
func TestSEC_U14ProvenanceStaticConstSitesStayGreenInRealPackage(t *testing.T) {
	a := newErrProvenanceAnalyzer(t)
	checked := 0
	for _, decls := range a.funcs {
		for _, decl := range decls {
			if decl.Body == nil {
				continue
			}
			ast.Inspect(decl.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || calleeName(call) != "NewToolResultError" || len(call.Args) == 0 {
					return true
				}
				id, ok := call.Args[0].(*ast.Ident)
				if !ok {
					return true
				}
				if _, isPkgConst := a.pkgValues[id.Name]; !isPkgConst {
					return true
				}
				checked++
				if v := a.classify(call.Args[0], decl, map[traceKey]bool{}, 0); v != provStatic {
					t.Errorf("%s: NewToolResultError(%s) classified %v, want static — this is a "+
						"package const holding a fixed string", a.shortPos(call.Pos()), id.Name, v)
				}
				return true
			})
		}
	}
	if checked == 0 {
		t.Fatal("no NewToolResultError(<package const>) sites found — the control asserts nothing")
	}
	t.Logf("package-const message sites verified green: %d", checked)
}

// TestSEC_U14ProvenanceScansJSONTextErrFields pins the second surface: an
// Err-shaped field serialised through jsonText, which no NewToolResultError
// needle can ever match. This is the tools_proposal.go batchAccept shape that
// U13 deferred to U14 and U14 could not see.
func TestSEC_U14ProvenanceScansJSONTextErrFields(t *testing.T) {
	a, file := analyzerFromSource(t, `package mcp

func batch() *mcp.CallToolResult {
	results := []Item{}
	res, err := accept()
	_ = res
	if err != nil {
		results = append(results, Item{ID: "x", ErrMsg: err.Error()})
	}
	return jsonText(Result{Results: results})
}
`)
	var decl *ast.FuncDecl
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "batch" {
			decl = fd
		}
	}
	if decl == nil {
		t.Fatal("synthetic source lost its batch function")
	}
	sites := a.sitesInFunc(decl)
	found := false
	for _, s := range sites {
		if strings.Contains(s.kind, "ErrMsg") {
			found = true
			if s.verdict != provError {
				t.Errorf("ErrMsg field verdict = %v, want ERROR-DERIVED (err.Error() is an error "+
					"turned into client-visible text)", s.verdict)
			}
		}
	}
	if !found {
		t.Error("the ErrMsg field of a composite literal in a jsonText-serialising function was " +
			"not scanned at all — U13 defers this surface to U14, so nothing else covers it")
	}
}
