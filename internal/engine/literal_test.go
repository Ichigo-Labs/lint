package engine

import "testing"

// Regression tests for literal matching: nodes whose kind is in a language's
// literalKinds (and nodes with no named structure on either side) compare by
// full source text. Previously a Go pattern fmt.Println("foo") matched
// fmt.Println("bar"), because a string's content is not a tree-sitter child
// node (only the quote tokens are), so child-wise comparison saw two equal
// empty structures.
func TestLiteralTextMatching(t *testing.T) {
	cases := []struct {
		name    string
		lang    string
		rule    string
		code    string
		want    int
		snippet string // exact snippet of the single expected finding ("" to skip)
	}{
		{
			// The original bug: "foo" must not match "bar".
			name:    "go interpreted string discriminates",
			lang:    "go",
			rule:    "rule r { in go\n pattern { fmt.Println(\"foo\") } }",
			code:    "package main\nfunc f() {\n\tfmt.Println(\"foo\")\n\tfmt.Println(\"bar\")\n}",
			want:    1,
			snippet: `fmt.Println("foo")`,
		},
		{
			// No false negatives: an identical string still matches.
			name:    "go interpreted string identical matches",
			lang:    "go",
			rule:    "rule r { in go\n pattern { g(\"foo\") } }",
			code:    "package main\nfunc f() {\n\tg(\"foo\")\n}",
			want:    1,
			snippet: `g("foo")`,
		},
		{
			name:    "go raw string discriminates",
			lang:    "go",
			rule:    "rule r { in go\n pattern { g(`foo`) } }",
			code:    "package main\nfunc f() {\n\tg(`foo`)\n\tg(`bar`)\n}",
			want:    1,
			snippet: "g(`foo`)",
		},
		{
			// Backslash escapes inside the literal are part of its text.
			name:    "go escaped string discriminates",
			lang:    "go",
			rule:    "rule r { in go\n pattern { g(\"a\\\"b\") } }",
			code:    "package main\nfunc f() {\n\tg(\"a\\\"b\")\n\tg(\"a\\\"c\")\n}",
			want:    1,
			snippet: `g("a\"b")`,
		},
		{
			name:    "typescript string discriminates",
			lang:    "typescript",
			rule:    "rule r { in typescript\n pattern { f(\"x\") } }",
			code:    "f(\"x\");\nf(\"y\");\n",
			want:    1,
			snippet: `f("x")`,
		},
		{
			name:    "typescript template string discriminates",
			lang:    "typescript",
			rule:    "rule r { in typescript\n pattern { f(`a`) } }",
			code:    "f(`a`);\nf(`b`);\n",
			want:    1,
			snippet: "f(`a`)",
		},
		{
			// Python exposes string content as a named child, so this guards
			// the literal-kind text comparison rather than the no-named-
			// children fallback.
			name:    "python string discriminates",
			lang:    "python",
			rule:    "rule r { in python\n pattern { print(\"x\") } }",
			code:    "print(\"x\")\nprint(\"y\")\n",
			want:    1,
			snippet: `print("x")`,
		},
		{
			name:    "rust string discriminates",
			lang:    "rust",
			rule:    "rule r { in rust\n pattern { foo(\"a\") } }",
			code:    "fn main() {\n\tfoo(\"a\");\n\tfoo(\"b\");\n}",
			want:    1,
			snippet: `foo("a")`,
		},
		{
			name:    "java string discriminates",
			lang:    "java",
			rule:    "rule r { in java\n pattern { log(\"a\") } }",
			code:    "class T {\n\tvoid f() {\n\t\tlog(\"a\");\n\t\tlog(\"b\");\n\t}\n}",
			want:    1,
			snippet: `log("a")`,
		},
		{
			name:    "c string discriminates",
			lang:    "c",
			rule:    "rule r { in c\n pattern { printf(\"a\") } }",
			code:    "void f(void) {\n\tprintf(\"a\");\n\tprintf(\"b\");\n}",
			want:    1,
			snippet: `printf("a")`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := run(t, tc.lang, tc.rule, tc.code)
			wantN(t, fs, tc.want)
			if tc.snippet != "" && fs[0].Snippet != tc.snippet {
				t.Fatalf("snippet = %q, want %q", fs[0].Snippet, tc.snippet)
			}
		})
	}
}

// A metavariable on a string argument must keep binding the whole literal node,
// quotes included; the literal text comparison only applies to literal pattern
// nodes, never to metavariable holes.
func TestLiteralMetavarBindsWholeLiteral(t *testing.T) {
	rule := "rule r { in go\n pattern { fmt.Println($X) } }"
	fs := run(t, "go", rule, "package main\nfunc f() {\n\tfmt.Println(\"bar\")\n}")
	wantN(t, fs, 1)
	if fs[0].Bindings["X"] != `"bar"` {
		t.Fatalf("$X = %q, want %q (quotes included)", fs[0].Bindings["X"], `"bar"`)
	}
}

// A bare $X used as a statement in a sequence pattern binds through the
// expression_statement wrapper: the pattern side unwraps to the sentinel and
// the target side unwraps to the inner expression. The literal fix must not
// disturb this path (expression_statement is not a literal kind).
func TestLiteralSeqStatementMetavarStillBinds(t *testing.T) {
	rule := "rule r { in typescript\n pattern { lock();\n $X } }"
	fs := run(t, "typescript", rule, "lock();\nwork(\"a\");\n")
	wantN(t, fs, 1)
	if fs[0].Bindings["X"] != `work("a")` {
		t.Fatalf("$X = %q, want %q", fs[0].Bindings["X"], `work("a")`)
	}
}

// Inside a statement-sequence pattern the same literal-text rule applies: a
// run whose second statement assigns a different string must not match.
// Before the fix the y pair below also matched, because "a" compared equal
// to "b" (no named children on either side of the string node).
func TestLiteralSeqStringDiscriminates(t *testing.T) {
	rule := "rule r { in go\n pattern { $Y = \"a\"\n $Y = \"a\" } }"
	fs := run(t, "go", rule, "package p\nfunc f() {\n\tx = \"a\"\n\tx = \"a\"\n\ty = \"a\"\n\ty = \"b\"\n}")
	wantN(t, fs, 1)
	if fs[0].Bindings["Y"] != "x" {
		t.Fatalf("$Y = %q, want %q", fs[0].Bindings["Y"], "x")
	}
}
