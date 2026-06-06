package engine

import (
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/lang"
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

func TestOperatorMetavar(t *testing.T) {
	// $OP captures the operator; back-reference on $A finds identical operands.
	rule := "rule r { in go\n pattern { $A $OP $A } }"
	fs := run(t, "go", rule, `
package p
func f(a, b int) {
	_ = a - a
	_ = a + b
	_ = b & b
}`)
	wantN(t, fs, 2) // a-a and b&b, not a+b
	if fs[0].Bindings["OP"] != "-" {
		t.Fatalf("first $OP = %q, want -", fs[0].Bindings["OP"])
	}
	if fs[1].Bindings["OP"] != "&" {
		t.Fatalf("second $OP = %q, want &", fs[1].Bindings["OP"])
	}
}

func TestOperatorMetavarInFilter(t *testing.T) {
	// constrain the captured operator to a set (quoted, since == is not an ident)
	rule := "rule r { in go\n pattern { $X $OP $Y }\n where $OP in [\"==\", \"!=\"] }"
	fs := run(t, "go", rule, `
package p
func f(a, b int) bool {
	if a == b {
		return true
	}
	if a < b {
		return false
	}
	return a != b
}`)
	wantN(t, fs, 2) // == and !=, not <
}

func TestMatchBindingPattern(t *testing.T) {
	// $match exposes the whole matched span to where and message.
	rule := "rule r { in go\n pattern { panic($$$) }\n where $match matches \"boom\" }"
	fs := run(t, "go", rule, `
package main
func f() {
	panic("boom")
	panic("ok")
}`)
	wantN(t, fs, 1)
	if fs[0].Bindings["match"] != `panic("boom")` {
		t.Fatalf("$match = %q, want %q", fs[0].Bindings["match"], `panic("boom")`)
	}
}

func TestMatchBindingDoesNotClobberQueryMatch(t *testing.T) {
	// A query's own @match capture must not be overwritten by the implicit one.
	rule := "rule r { in go\n query \"(call_expression function: (identifier) @match (#eq? @match \\\"panic\\\"))\" }"
	fs := run(t, "go", rule, "package main\nfunc f() { panic(\"x\") }")
	wantN(t, fs, 1)
	if fs[0].Bindings["match"] != "panic" {
		t.Fatalf("query @match = %q, want %q (implicit $match must not clobber it)", fs[0].Bindings["match"], "panic")
	}
}

func TestWhereCount(t *testing.T) {
	// calls passing more than two arguments
	rule := "rule r { in go\n pattern { foo($$$ARGS) }\n where $ARGS count > 2 }"
	fs := run(t, "go", rule, `
package main
func f() {
	foo(1, 2)
	foo(1, 2, 3)
	foo(1, 2, 3, 4)
	foo()
}`)
	wantN(t, fs, 2)
}

func TestWhereCountZero(t *testing.T) {
	// empty argument lists
	rule := "rule r { in go\n pattern { foo($$$ARGS) }\n where $ARGS count == 0 }"
	fs := run(t, "go", rule, "package main\nfunc f() { foo(); foo(1) }")
	wantN(t, fs, 1)
	if fs[0].Snippet != "foo()" {
		t.Fatalf("got %q", fs[0].Snippet)
	}
}

func TestWhereNumeric(t *testing.T) {
	// numeric literal above a threshold; a non-numeric capture never matches
	rule := "rule r { in go\n pattern { wait($N) }\n where $N > 1000 }"
	fs := run(t, "go", rule, `
package main
func f() {
	wait(500)
	wait(2000)
	wait(1000)
	wait(x)
}`)
	wantN(t, fs, 1)
	if fs[0].Snippet != "wait(2000)" {
		t.Fatalf("got %q", fs[0].Snippet)
	}
}

func TestIndexedMatchesWalk(t *testing.T) {
	// The shared-index path must produce byte-identical findings to the full-walk
	// fallback for single patterns, operator patterns, and statement sequences.
	code := `
package p
func f(a, b int) {
	panic("x")
	a = a
	c := a + b
	d := a - b
	e := 0
	e = 1
	e = 2
	_ = c
	_ = d
	_ = e
}`
	rules := []string{
		"rule r { in go\n pattern { panic($$$) } }",
		"rule r { in go\n pattern { $X = $X } }",
		"rule r { in go\n pattern { $A $OP $B } }",
		"rule r { in go\n pattern { $Y = $_\n $Y = $_ } }",
	}
	l, _ := lang.Get("go")
	for _, src := range rules {
		rs, err := dsl.Parse("t.lint", src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		cr, err := Compile(rs[0])
		if err != nil {
			t.Fatalf("compile %q: %v", src, err)
		}
		tree, err := ParseSource(l, []byte(code))
		if err != nil {
			t.Fatal(err)
		}
		withIdx := cr.RunIndexed(l, tree, []byte(code), BuildIndex(tree))
		noIdx := cr.RunIndexed(l, tree, []byte(code), nil) // nil index -> walk fallback
		if !sameSpans(withIdx, noIdx) {
			t.Errorf("indexed vs walk differ for %q:\n indexed=%v\n walk=%v", src, snippets(withIdx), snippets(noIdx))
		}
	}
}

func sameSpans(a, b []Finding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].StartByte != b[i].StartByte || a[i].EndByte != b[i].EndByte {
			return false
		}
	}
	return true
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

func TestLetResolution(t *testing.T) {
	// A `let` list referenced via @NAME behaves like an inline `in [...]`.
	rule := "let M = [log, debug]\nrule r { in typescript\n pattern { console.$X($$$) }\n where $X in @M }"
	fs := run(t, "typescript", rule, "console.log(1);\nconsole.debug(2);\nconsole.error(3);\n")
	wantN(t, fs, 2)
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

func TestKotlinVariadic(t *testing.T) {
	// Kotlin wraps each call argument in a value_argument node; the variadic must
	// span those wrappers so println($$$) matches calls of any arity (incl. zero).
	rule := "rule r { in kotlin\n pattern { println($$$) } }"
	fs := run(t, "kotlin", rule, `
fun f() {
	println("a", 1)
	println()
	log("nope")
}`)
	wantN(t, fs, 2)
}

func TestKotlinNotNullAssertion(t *testing.T) {
	// The !! operator is an anonymous token kept by relevantChildren, so $X!!
	// matches a force-unwrap but not an ordinary reference or an increment.
	rule := "rule r { in kotlin\n pattern { $X!! }\n }"
	fs := run(t, "kotlin", rule, `
fun f() {
	val a = x!!
	val b = y++
	val c = z
}`)
	wantN(t, fs, 1)
}

func TestSwiftVariadic(t *testing.T) {
	// Swift also wraps call arguments in value_argument nodes.
	rule := "rule r { in swift\n pattern { print($$$) } }"
	fs := run(t, "swift", rule, `
func f() {
	print("a", 1)
	print()
	log("nope")
}`)
	wantN(t, fs, 2)
}

func TestSwiftForceTryDiscriminates(t *testing.T) {
	// try! must not match try? or a plain try (the try_operator token differs).
	rule := "rule r { in swift\n pattern { try! $X }\n }"
	fs := run(t, "swift", rule, `
func f() {
	let a = try! g()
	let b = try? g()
}`)
	wantN(t, fs, 1)
}
