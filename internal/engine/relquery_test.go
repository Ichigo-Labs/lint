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

func TestNotFollowsRelation(t *testing.T) {
	// lock() not followed by unlock() in the same block
	rule := "rule r { in go\n pattern { lock() }\n not follows pattern { unlock() } }"
	fs := run(t, "go", rule, `
package p
func good() { lock(); work(); unlock() }
func bad()  { lock(); work() }
`)
	wantN(t, fs, 1) // only bad()'s lock has no following unlock()
}

func TestFollowsRelation(t *testing.T) {
	rule := "rule r { in go\n pattern { lock() }\n follows pattern { unlock() } }"
	fs := run(t, "go", rule, `
package p
func good() { lock(); work(); unlock() }
func bad()  { lock(); work() }
`)
	wantN(t, fs, 1) // only good()'s lock is followed by unlock()
}

func TestFollowsRespectsBlockBoundary(t *testing.T) {
	// A Lock in an inner block is not "followed by" an Unlock in an outer block:
	// the search does not climb past the candidate's enclosing block.
	rule := "rule r { in go\n pattern { $MU.Lock() }\n not follows pattern { $MU.Unlock() } }"
	fs := run(t, "go", rule, `
package p
func f(c bool) {
	if c {
		mu.Lock()
	}
	mu.Unlock()
}`)
	wantN(t, fs, 1) // the lock inside the if-block has no unlock in its block
}

func TestFollowsThroughEmbeddedCall(t *testing.T) {
	// The candidate may be embedded in a larger statement (x := acquire()); a
	// later sibling statement still counts as a follower.
	rule := "rule r { in go\n pattern { acquire() }\n not follows pattern { cleanup() } }"
	fs := run(t, "go", rule, `
package p
func f() {
	x := acquire()
	_ = x
	cleanup()
}`)
	wantN(t, fs, 0) // cleanup() follows, so acquire() is not flagged
}

func TestPrecedesRelation(t *testing.T) {
	// use() with an earlier sibling that declares x
	rule := "rule r { in go\n pattern { use() }\n precedes pattern { x := $_ } }"
	fs := run(t, "go", rule, `
package p
func g() { x := 1; use(); nope() }
func h() { use() }
`)
	wantN(t, fs, 1)
}

func TestDirectlyHasRelation(t *testing.T) {
	// a + expression with y as a direct operand (depth 1)
	rule := "rule r { in go\n pattern { $A + $B }\n directly has pattern { y } }"
	fs := run(t, "go", rule, `
package p
func f() int { return y + 1 }
func g() int { return z + 1 }
`)
	wantN(t, fs, 1)
}

func TestDirectlyInsideRelation(t *testing.T) {
	// y whose immediate parent is a + expression
	rule := "rule r { in go\n pattern { y }\n directly inside pattern { $A + $B } }"
	fs := run(t, "go", rule, `
package p
func f() int { return y + 1 }
func g() int { y := 0; return y }
`)
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
