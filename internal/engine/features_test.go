package engine

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// --- kind membership ----------------------------------------------------------

func TestWhereKindIn(t *testing.T) {
	rule := "rule r { in go\n pattern { foo($A) }\n where $A kind in [int_literal, interpreted_string_literal] }"
	fs := run(t, "go", rule, `
package p
func f() {
	foo(1)
	foo("s")
	foo(x)
	foo(a.b)
}`)
	wantN(t, fs, 2)
}

func TestWhereNotKindIn(t *testing.T) {
	rule := "rule r { in go\n pattern { foo($A) }\n where $A not kind in [int_literal, interpreted_string_literal] }"
	fs := run(t, "go", rule, `
package p
func f() {
	foo(1)
	foo("s")
	foo(x)
	foo(a.b)
}`)
	wantN(t, fs, 2)
}

// --- length / lines ----------------------------------------------------------

func TestWhereLength(t *testing.T) {
	rule := "rule r { in go\n pattern { foo($A) }\n where $A length > 5 }"
	fs := run(t, "go", rule, "package p\nfunc f() { foo(abcdef); foo(ab); foo(x) }")
	wantN(t, fs, 1)
	if fs[0].Bindings["A"] != "abcdef" {
		t.Fatalf("got %q", fs[0].Bindings["A"])
	}
}

func TestWhereLines(t *testing.T) {
	rule := `rule r { in go
 query "(function_declaration) @match"
 where $match lines > 3 }`
	fs := run(t, "go", rule, `
package p
func short() { x() }
func long() {
	a()
	b()
}`)
	wantN(t, fs, 1)
	if !strings.Contains(fs[0].Snippet, "long") {
		t.Fatalf("got %q", fs[0].Snippet)
	}
}

// --- report ----------------------------------------------------------------

func TestReportNarrowsSpan(t *testing.T) {
	rule := "rule r { in go\n pattern { fmt.$M($$$) }\n report $M }"
	fs := run(t, "go", rule, "package p\nfunc f() { fmt.Println(1) }")
	wantN(t, fs, 1)
	if fs[0].Snippet != "Println" {
		t.Fatalf("reported snippet = %q, want just the capture %q", fs[0].Snippet, "Println")
	}
}

func TestReportFixReplacesCaptureOnly(t *testing.T) {
	rule := "rule r { in go\n pattern { foo($A) }\n report $A\n fix \"wrapped($A)\" }"
	code := "package p\nfunc f() { foo(bar) }"
	fs := run(t, "go", rule, code)
	wantN(t, fs, 1)
	if fs[0].Snippet != "bar" {
		t.Fatalf("snippet = %q, want %q", fs[0].Snippet, "bar")
	}
	if fs[0].Fix == nil || *fs[0].Fix != "wrapped(bar)" {
		t.Fatalf("fix = %v, want wrapped(bar)", fs[0].Fix)
	}
	// Splicing the fix at the reported span rewrites only the capture.
	out := code[:fs[0].StartByte] + *fs[0].Fix + code[fs[0].EndByte:]
	if !strings.Contains(out, "foo(wrapped(bar))") {
		t.Fatalf("after fix: %q", out)
	}
}

func TestReportVariadicFallsBackToMatch(t *testing.T) {
	rule := "rule r { in go\n pattern { foo($$$ARGS) }\n report $ARGS }"
	fs := run(t, "go", rule, "package p\nfunc f() { foo(1, 2) }")
	wantN(t, fs, 1)
	if fs[0].Snippet != "foo(1, 2)" {
		t.Fatalf("snippet = %q, want the full match", fs[0].Snippet)
	}
}

func TestReportUnknownCaptureErrors(t *testing.T) {
	rules, err := dsl.Parse("test.lint", "rule r { in go\n pattern { foo($X) }\n report $Y }")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Compile(rules[0]); err == nil || !strings.Contains(err.Error(), "report $Y") {
		t.Fatalf("Compile err = %v, want a report-$Y error", err)
	}
}

func TestReportFromRelationCapture(t *testing.T) {
	// The reported capture may be bound by a positive relation's pattern.
	rule := `rule r { in go
 pattern { os.Exit($$$) }
 inside pattern { func $F() { $$$ } }
 report $F }`
	fs := run(t, "go", rule, "package p\nfunc shutdown() { os.Exit(1) }")
	wantN(t, fs, 1)
	if fs[0].Snippet != "shutdown" {
		t.Fatalf("snippet = %q, want %q", fs[0].Snippet, "shutdown")
	}
}

// --- directly precedes / follows ------------------------------------------------

func TestDirectlyFollows(t *testing.T) {
	// Flag a() only when the immediately-next statement is b().
	rule := "rule r { in go\n pattern { a() }\n directly follows pattern { b() } }"
	fs := run(t, "go", rule, `
package p
func f() {
	a()
	b()
	a()
	c()
	a()
}`)
	wantN(t, fs, 1)
	if fs[0].StartLine != 4 {
		t.Fatalf("matched a() on line %d, want line 4", fs[0].StartLine)
	}
}

func TestDirectlyPrecedes(t *testing.T) {
	// Flag a() only when the immediately-previous statement is b().
	rule := "rule r { in go\n pattern { a() }\n directly precedes pattern { b() } }"
	fs := run(t, "go", rule, `
package p
func f() {
	b()
	a()
	c()
	a()
}`)
	wantN(t, fs, 1)
	if fs[0].StartLine != 5 {
		t.Fatalf("matched a() on line %d, want line 5", fs[0].StartLine)
	}
}

func TestNotDirectlyFollows(t *testing.T) {
	rule := "rule r { in go\n pattern { a() }\n not directly follows pattern { b() } }"
	fs := run(t, "go", rule, `
package p
func f() {
	a()
	b()
	a()
	c()
	a()
}`)
	wantN(t, fs, 2)
}

func TestDirectlyFollowsSharesBindings(t *testing.T) {
	// The adjacent sibling shares metavariable bindings with the candidate:
	// flag a redundant immediate re-assignment of the same variable.
	rule := "rule r { in go\n pattern { $X = $V }\n directly follows pattern { $X = $V } }"
	fs := run(t, "go", rule, `
package p
func f() {
	x := 0
	x = 1
	x = 1
	y := 0
	y = 1
	y = 2
	_, _ = x, y
}`)
	wantN(t, fs, 1)
	if fs[0].StartLine != 5 {
		t.Fatalf("matched on line %d, want line 5", fs[0].StartLine)
	}
}

// --- let matchers ----------------------------------------------------------------

func TestLetMatcher(t *testing.T) {
	rule := `let DEBUG = any {
  pattern { console.log($$$) }
  pattern { console.debug($$$) }
}
rule r { in typescript
  @DEBUG
}`
	fs := run(t, "typescript", rule, "console.log(1);\nconsole.debug(2);\nconsole.error(3);\n")
	wantN(t, fs, 2)
}

func TestLetMatcherInRelation(t *testing.T) {
	rule := `let TESTFN = pattern { func $_($_ *testing.T) { $$$ } }
rule r { in go
  pattern { time.Sleep($$$) }
  inside @TESTFN
}`
	fs := run(t, "go", rule, `
package p
func TestThing(t *testing.T) {
	time.Sleep(100)
}
func retry() {
	time.Sleep(100)
}`)
	wantN(t, fs, 1)
}
