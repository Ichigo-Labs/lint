package engine

import (
	"testing"

	"github.com/ichigo-labs/lintel/internal/dsl"
	"github.com/ichigo-labs/lintel/internal/lang"
)

func run(t *testing.T, langName, ruleSrc, code string) []Finding {
	t.Helper()
	rules, err := dsl.Parse("test.lint", ruleSrc)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	cr, err := Compile(rules[0])
	if err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	l, ok := lang.Get(langName)
	if !ok {
		t.Fatalf("unknown lang %q", langName)
	}
	tree, err := ParseSource(l, []byte(code))
	if err != nil {
		t.Fatalf("parse code: %v", err)
	}
	return cr.Run(l, tree, []byte(code))
}

func wantN(t *testing.T, fs []Finding, n int) {
	t.Helper()
	if len(fs) != n {
		t.Fatalf("expected %d findings, got %d: %+v", n, len(fs), snippets(fs))
	}
}

func snippets(fs []Finding) []string {
	var s []string
	for _, f := range fs {
		s = append(s, f.Snippet)
	}
	return s
}

func TestSimpleCall(t *testing.T) {
	rule := "rule r { in typescript\n pattern { console.log($$$) } }"
	fs := run(t, "typescript", rule, `
function f() {
	console.log("a", 1);
	console.error("nope");
	console.log();
}`)
	wantN(t, fs, 2)
}

func TestMetavarBackref(t *testing.T) {
	// x = x (self assignment)
	rule := "rule r { in go\n pattern { $X = $X } }"
	fs := run(t, "go", rule, `
package main
func f() {
	a := 1
	a = a
	b := 2
	a = b
}`)
	wantN(t, fs, 1)
	if fs[0].Snippet != "a = a" {
		t.Fatalf("got snippet %q", fs[0].Snippet)
	}
}

func TestWhereIn(t *testing.T) {
	rule := "rule r { in typescript\n pattern { console.$M($$$) }\n where $M in [log, debug] }"
	fs := run(t, "typescript", rule, `
console.log(1);
console.debug(2);
console.error(3);
console.warn(4);`)
	wantN(t, fs, 2)
}

func TestWhereRegex(t *testing.T) {
	rule := "rule r { in go\n pattern { var $NAME = $$$ }\n where $NAME matches \"^[A-Z]\" }"
	fs := run(t, "go", rule, `
package main
var Public = 1
var private = 2
`)
	wantN(t, fs, 1)
	if fs[0].Bindings["NAME"] != "Public" {
		t.Fatalf("got %q", fs[0].Bindings["NAME"])
	}
}

func TestOperatorSensitivity(t *testing.T) {
	rule := "rule r { in go\n pattern { $A + $B } }"
	fs := run(t, "go", rule, `
package main
func f() int {
	x := 1 + 2
	y := 3 - 4
	return x + y
}`)
	// 1+2 and x+y, not 3-4
	wantN(t, fs, 2)
}

func TestMultiLangAll(t *testing.T) {
	// No `in` clause: should compile for languages that can parse it.
	rule := "rule r { pattern { foo($$$) } }"
	rules, err := dsl.Parse("test.lint", rule)
	if err != nil {
		t.Fatal(err)
	}
	cr, err := Compile(rules[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(cr.Languages()) < 5 {
		t.Fatalf("expected the call pattern to compile for many languages, got %d", len(cr.Languages()))
	}
}

func TestPython(t *testing.T) {
	rule := "rule r { in python\n pattern { print($$$) } }"
	fs := run(t, "python", rule, "print('hi')\nx = 1\nprint(x, 2)\n")
	wantN(t, fs, 2)
}

func TestRustNotInside(t *testing.T) {
	rule := "rule r { in rust\n pattern { unwrap() }\n }"
	fs := run(t, "rust", rule, `
fn main() {
	let x = foo.unwrap();
}`)
	// member call unwrap() — pattern unwrap() is a call_expression with fn=identifier unwrap
	// foo.unwrap() function is a field_expression, not identifier, so it won't match.
	wantN(t, fs, 0)
}
