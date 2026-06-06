package engine

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// Statement patterns match regardless of a trailing terminator (`;` / newline).
func TestTerminatorTrailingSemicolon(t *testing.T) {
	rule := "rule r { in typescript\n pattern { throw $X } }"
	fs := run(t, "typescript", rule, "function f(){ throw err; }\nfunction g(){ throw boom; }")
	wantN(t, fs, 2)
}

// A multi-line Go block matches the same pattern as a single-line one.
func TestGoMultilineBlock(t *testing.T) {
	rule := "rule r { in go\n pattern { if $ERR == nil { return $ERR } } }"
	multi := `
package main
func f(err error) error {
	if err == nil {
		return err
	}
	return nil
}`
	fs := run(t, "go", rule, multi)
	wantN(t, fs, 1)
}

// Variadics work inside value-list literals (Go composite literals).
func TestGoCompositeVariadic(t *testing.T) {
	rule := "rule r { in go\n pattern { []int{$$$} } }"
	code := `
package main
func f() {
	a := []int{1, 2, 3}
	b := []int{}
}`
	fs := run(t, "go", rule, code)
	wantN(t, fs, 2)
}

// A query used in a relation anchors on the matched construct even when its
// capture is a sub-node.
func TestQueryRelationCover(t *testing.T) {
	rule := "rule r { in typescript\n pattern { function $N($$$) { $$$ } }\n has query \"(call_expression function: (identifier) @c)\" }"
	code := `
function a() { foo(); }
function b() { return 2; }`
	fs := run(t, "typescript", rule, code)
	wantN(t, fs, 1)
	if fs[0].Bindings["N"] != "a" {
		t.Fatalf("got %q", fs[0].Bindings["N"])
	}
}

// `$` inside a string literal in a pattern is literal, not a metavariable.
func TestMetavarInStringIsLiteral(t *testing.T) {
	rule := "rule r { in python\n pattern { log(\"hi $name\") } }"
	fs := run(t, "python", rule, "log(\"hi $name\")\nlog(\"hi bob\")\n")
	wantN(t, fs, 1)
	if fs[0].Snippet != `log("hi $name")` {
		t.Fatalf("got %q", fs[0].Snippet)
	}
}

// `where $X != $Y` does not fire when $Y is unbound (a typo'd metavariable).
func TestNeqUnboundNoMatch(t *testing.T) {
	rule := "rule r { in go\n pattern { $X = $Y }\n where $X != $TYPO }"
	fs := run(t, "go", rule, "package main\nfunc f() {\n a := 0\n a = 5\n}")
	wantN(t, fs, 0)
}

// A purely-negative rule is rejected at compile time.
func TestPurelyNegativeRejected(t *testing.T) {
	rules, err := dsl.Parse("t.lint", "rule r { in go\n not pattern { panic($$$) } }")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(rules[0]); err == nil {
		t.Fatal("expected compile error for purely-negative rule")
	} else if !strings.Contains(err.Error(), "positive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A capture-free query is rejected at compile time.
func TestQueryNoCaptureRejected(t *testing.T) {
	rules, err := dsl.Parse("t.lint", "rule r { in go\n query \"(call_expression)\" }")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(rules[0]); err == nil {
		t.Fatal("expected compile error for capture-free query")
	}
}

var _ = dsl.Error

// A positional metavar followed by a trailing variadic matches a single-arg
// call (the separating comma is optional when the variadic is empty).
func TestVariadicAfterPositional(t *testing.T) {
	rule := "rule r { in go\n pattern { foo($A, $$$REST) } }"
	code := `
package main
func g() {
	foo(1)
	foo(1, 2, 3)
	bar(9)
}`
	fs := run(t, "go", rule, code)
	wantN(t, fs, 2)
}
