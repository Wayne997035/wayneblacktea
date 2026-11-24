//go:build !integration

package mcp

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
)

// [F1124-13] warningsForWire: validator warnings leave the MCP layer through
// one sanitiser.

func TestWarningsForWire_NeutralizesAndBounds(t *testing.T) {
	t.Parallel()

	t.Run("marker_neutralized", func(t *testing.T) {
		t.Parallel()
		in := []string{`kind "` + storedContextMarkerEnd + `" bad`}
		got := warningsForWire(in)
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1: %v", len(got), got)
		}
		assertMarkerNeutralized(t, got[0], storedContextMarkerEnd)
	})

	t.Run("bounded", func(t *testing.T) {
		t.Parallel()
		in := []string{strings.Repeat("x", 5000), "short"}
		got := warningsForWire(in)
		// clipRunes appends a clip marker after the cap; allow a small margin
		// for it but nowhere near the 5000 input.
		for i, w := range got {
			if n := utf8.RuneCountInString(w); n > mcpWarningMaxRunes+64 {
				t.Errorf("warning[%d] has %d runes, want <= %d (+ clip marker)", i, n, mcpWarningMaxRunes)
			}
		}
		if got[1] != "short" {
			t.Errorf("short warning altered: %q", got[1])
		}
	})

	t.Run("does_not_mutate_input", func(t *testing.T) {
		t.Parallel()
		in := []string{storedContextMarkerEnd}
		_ = warningsForWire(in)
		if in[0] != storedContextMarkerEnd {
			t.Errorf("input slice was mutated: %q", in[0])
		}
	})
}

func TestWarningsForWire_PreservesNilAndEmpty(t *testing.T) {
	t.Parallel()
	if got := warningsForWire(nil); got != nil {
		t.Errorf("nil in -> %#v, want nil", got)
	}
	got := warningsForWire([]string{})
	if got == nil || len(got) != 0 {
		t.Errorf("empty in -> %#v, want non-nil empty slice", got)
	}
}

// TestAddTask_WarningsPassThroughUnchanged proves the sanitiser does not
// over-sanitise: the real add_task response carries exactly the validator's
// fixed text.
func TestAddTask_WarningsPassThroughUnchanged(t *testing.T) {
	// Not parallel: pins WBT_STRICT_VAGUENESS off via t.Setenv.
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	s := newTestWorkSessionServer(t)

	const desc = "fix it"
	want := validator.CheckTaskInput(desc, validator.KindGeneral)
	if len(want) == 0 {
		t.Fatal("fixture invalid: validator produced no warning for a vague description")
	}

	r := callAddTask(t, s, map[string]any{
		"title":       "warnings passthrough probe",
		"description": desc,
		"due_date":    "2026-12-31T00:00:00Z",
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &body); err != nil {
		t.Fatalf("parse: %v (%s)", err, resultText(r))
	}
	if !reflect.DeepEqual(body.Warnings, want) {
		t.Errorf("warnings = %q, want validator output %q", body.Warnings, want)
	}
}

// ---- source-scan gate ----

// warningsExit is one place a validator warnings value leaves the handler.
type warningsExit struct {
	pos string // file:line
	ok  bool   // value expression is wrapped by warningsForWire
}

// containsWireCall reports whether the expression tree has a call to
// warningsForWire.
func containsWireCall(e ast.Node) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "warningsForWire" {
				found = true
			}
		}
		return !found
	})
	return found
}

func mentionsIdent(e ast.Node, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// scanWarningsExits finds every warnings exit in src: a composite-literal key
// "warnings", and every fmt.Sprintf/Errorf call that formats allWarnings.
func scanWarningsExits(t *testing.T, filename string, src []byte) []warningsExit {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var exits []warningsExit
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.KeyValueExpr:
			if lit, ok := x.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"warnings"` {
				exits = append(exits, warningsExit{
					pos: fset.Position(x.Pos()).String(),
					ok:  containsWireCall(x.Value),
				})
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" || (sel.Sel.Name != "Sprintf" && sel.Sel.Name != "Errorf") {
				return true
			}
			mentioned, wrapped := false, true
			for _, a := range x.Args {
				if mentionsIdent(a, "allWarnings") {
					mentioned = true
					if !containsWireCall(a) {
						wrapped = false
					}
				}
			}
			if mentioned {
				exits = append(exits, warningsExit{pos: fset.Position(x.Pos()).String(), ok: wrapped})
			}
		}
		return true
	})
	return exits
}

func TestWarningsExitsAreSanitized(t *testing.T) {
	t.Parallel()

	// Scanner self-check on synthetic source: raw exits MUST be flagged and
	// wrapped ones MUST pass, otherwise a scanner that matches nothing (or
	// everything) would leave the real assertion below vacuous.
	t.Run("scanner_self_check", func(t *testing.T) {
		t.Parallel()
		bad := `package p
import "fmt"
func f(allWarnings []string) {
	_ = map[string]any{"warnings": allWarnings}
	_ = fmt.Sprintf("x %v", allWarnings)
}`
		ex := scanWarningsExits(t, "bad.go", []byte(bad))
		if len(ex) != 2 {
			t.Fatalf("scanner found %d exits in synthetic source, want 2: %+v", len(ex), ex)
		}
		for _, e := range ex {
			if e.ok {
				t.Errorf("scanner failed to flag raw exit %s", e.pos)
			}
		}
		good := `package p
import "fmt"
func f(allWarnings []string) {
	_ = map[string]any{"warnings": warningsForWire(allWarnings)}
	_ = fmt.Sprintf("x %v", warningsForWire(allWarnings))
}`
		ex = scanWarningsExits(t, "good.go", []byte(good))
		if len(ex) != 2 {
			t.Fatalf("scanner found %d exits in wrapped synthetic source, want 2", len(ex))
		}
		for _, e := range ex {
			if !e.ok {
				t.Errorf("scanner flagged wrapped exit %s", e.pos)
			}
		}
	})

	// Scan scope: exactly tools_gtd.go and tools_vision.go (the only files
	// that return validator warnings to an MCP caller). tools_proposal.go
	// sanitises its own "warnings" field via neutralizeTaskWarningsField.
	files := []string{"tools_gtd.go", "tools_vision.go"}
	const wantExits = 3 // gtd JSON exit + gtd strict-mode text exit + vision JSON exit
	var all []warningsExit
	perFile := map[string]int{}
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		ex := scanWarningsExits(t, name, src)
		perFile[name] = len(ex)
		all = append(all, ex...)
	}

	t.Run("finds_exactly_the_known_exits", func(t *testing.T) {
		if len(all) != wantExits {
			t.Fatalf("scanner found %d warnings exits (%v), want exactly %d — a new exit "+
				"must be wrapped in warningsForWire and this count updated deliberately",
				len(all), perFile, wantExits)
		}
		if perFile["tools_gtd.go"] != 2 || perFile["tools_vision.go"] != 1 {
			t.Errorf("exit distribution = %v, want tools_gtd.go:2 tools_vision.go:1", perFile)
		}
	})

	t.Run("strict-mode-exit_and_json_exits_wrapped", func(t *testing.T) {
		for _, e := range all {
			if !e.ok {
				t.Errorf("%s: warnings exit is not wrapped in warningsForWire", e.pos)
			}
		}
	})
}
