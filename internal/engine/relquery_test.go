package engine

import "testing"

func TestHasRelation(t *testing.T) {
	// functions that contain a console.log call
	rule := "rule r { in typescript\n pattern { function $N($$$) { $$$ } }\n has pattern { console.log($$$) } }"
	fs := run(t, "typescript", rule, `
function a() { console.log(1); }
function b() { return 2; }
`)
	wantN(t, fs, 1)
	if fs[0].Bindings["N"] != "a" {
		t.Fatalf("got %q", fs[0].Bindings["N"])
	}
}

func TestNotHasRelation(t *testing.T) {
	rule := "rule r { in typescript\n pattern { function $N($$$) { $$$ } }\n not has pattern { return $$$ } }"
	fs := run(t, "typescript", rule, `
function a() { console.log(1); }
function b() { return 2; }
`)
	wantN(t, fs, 1)
	if fs[0].Bindings["N"] != "a" {
		t.Fatalf("got %q", fs[0].Bindings["N"])
	}
}

func TestInsideRelation(t *testing.T) {
	// await expressions inside a while loop
	rule := "rule r { in typescript\n pattern { await $X }\n inside pattern { while ($COND) { $$$ } } }"
	fs := run(t, "typescript", rule, `
async function f() {
	while (going) { await g(); }
	await h();
}`)
	wantN(t, fs, 1)
}

func TestRawQuery(t *testing.T) {
	rule := "rule r { in go\n query \"(call_expression function: (identifier) @match (#eq? @match \\\"panic\\\"))\" }"
	fs := run(t, "go", rule, `
package main
func f() { panic("x"); foo() }
`)
	wantN(t, fs, 1)
	if fs[0].Snippet != "panic" {
		t.Fatalf("got %q", fs[0].Snippet)
	}
}

func TestWhereKind(t *testing.T) {
	rule := "rule r { in go\n pattern { $X = $Y }\n where $Y kind int_literal }"
	fs := run(t, "go", rule, `
package main
func f() {
	a := 0
	a = 5
	a = b
}`)
	wantN(t, fs, 1)
}
