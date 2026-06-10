package engine

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/lang"
)

// Regression tests for review findings on the markup-language/performance
// change. Each test pins a confirmed bug.

// regrRun compiles a one-rule source and runs it over code in the language.
func regrRun(t *testing.T, langName, rule, code string) []Finding {
	t.Helper()
	rules, err := dsl.Parse("regr.lint", rule)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	cr, err := Compile(rules[0])
	if err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	l, ok := lang.Get(langName)
	if !ok {
		t.Fatalf("no language %q", langName)
	}
	tree, err := ParseSource(l, []byte(code))
	if err != nil {
		t.Fatalf("parse code: %v", err)
	}
	return cr.Run(l, tree, []byte(code))
}

// Empty delimiter pairs must compare structurally, so interior whitespace and
// comments stay irrelevant (`foo()` matches `foo( )` and `foo(/* */)`).
func TestEmptyDelimiterPairsIgnoreWhitespace(t *testing.T) {
	goRule := "rule r { in go\n pattern { foo() } }"
	tests := []struct {
		name, lang, rule, code string
		want                   int
	}{
		{"go space in empty args", "go", goRule, "package p\nfunc f() { foo( ) }", 1},
		{"go comment in empty args", "go", goRule, "package p\nfunc f() { foo(/* nothing */) }", 1},
		{"go exact empty args", "go", goRule, "package p\nfunc f() { foo() }", 1},
		{"ts empty catch with newline", "typescript",
			"rule r { in typescript\n pattern { try { $$$ } catch ($E) {} } }",
			"try { x(); } catch (e) {\n}", 1},
		{"ts empty catch with comment", "typescript",
			"rule r { in typescript\n pattern { try { $$$ } catch ($E) {} } }",
			"try { x(); } catch (e) { /* ignore */ }", 1},
		// The markup text-hole semantics must survive the fix: content still
		// compares, and an empty pattern value must not match a non-empty one.
		{"xml attr value still compared", "xml",
			`rule r { in xml` + "\n" + ` pattern { <a b="x"/> } }`, `<a b="y"/>`, 0},
		{"xml empty attr does not match non-empty", "xml",
			`rule r { in xml` + "\n" + ` pattern { <a b=""/> } }`, `<a b="y"/>`, 0},
		{"xml empty attr matches empty", "xml",
			`rule r { in xml` + "\n" + ` pattern { <a b=""/> } }`, `<a b=""/>`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(regrRun(t, tt.lang, tt.rule, tt.code)); got != tt.want {
				t.Fatalf("findings = %d, want %d", got, tt.want)
			}
		})
	}
}

// Interpolated literals keep structural matching: the code inside `${…}` /
// f-string braces compares like code (whitespace-insensitively), while
// non-interpolated literals compare by full text.
func TestInterpolatedLiteralsMatchStructurally(t *testing.T) {
	tests := []struct {
		name, lang, rule, code string
		want                   int
	}{
		{"ts template substitution whitespace", "typescript",
			"rule r { in typescript\n pattern { foo(`a ${x}`) } }",
			"foo(`a ${ x }`);", 1},
		{"ts template static text still compared", "typescript",
			"rule r { in typescript\n pattern { foo(`a ${x}`) } }",
			"foo(`b ${x}`);", 0},
		{"python fstring interpolation whitespace", "python",
			`rule r { in python` + "\n" + ` pattern { print(f"hi {name}") } }`,
			`print(f"hi { name }")`, 1},
		{"python fstring static text still compared", "python",
			`rule r { in python` + "\n" + ` pattern { print(f"hi {name}") } }`,
			`print(f"bye {name}")`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(regrRun(t, tt.lang, tt.rule, tt.code)); got != tt.want {
				t.Fatalf("findings = %d, want %d", got, tt.want)
			}
		})
	}
}

// findSeq must find sequences whose statements sit under stacked transparent
// wrappers (C# top-level statements: global_statement(expression_statement)).
func TestSeqUnderStackedTransparentWrappers(t *testing.T) {
	rule := "rule r { in csharp\n pattern { foo(); bar(); } }"
	fs := regrRun(t, "csharp", rule, "foo();\nbar();\n")
	if len(fs) != 1 {
		t.Fatalf("findings = %d, want 1", len(fs))
	}
}

// computeAnchor must not harvest anchors from comment subtrees: matching
// skips comments, so their text never needs to appear in a matching file.
func TestAnchorIgnoresCommentSubtrees(t *testing.T) {
	rules, err := dsl.Parse("regr.lint", `rule r { in rust
 pattern { barcall(/** BLOCKDOCTEXT */ 1) } }`)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	cr, err := Compile(rules[0])
	if err != nil {
		t.Fatalf("compile rule: %v", err)
	}
	l, _ := lang.Get("rust")
	a := cr.AnchorFor(l)
	if strings.Contains(a, "BLOCKDOCTEXT") {
		t.Fatalf("anchor %q harvested from a comment subtree", a)
	}
	// The pattern still matches comment-free code; its anchor must occur there.
	code := "fn f() { barcall(1); }"
	if fs := regrRun(t, "rust", `rule r { in rust
 pattern { barcall(/** BLOCKDOCTEXT */ 1) } }`, code); len(fs) != 1 {
		t.Fatalf("pattern should match comment-free code, findings = %d", len(fs))
	}
	if a != "" && !strings.Contains(code, a) {
		t.Fatalf("anchor %q does not occur in matching code %q", a, code)
	}
}

// missingSentinels accounts per occurrence: a reused metavariable with one
// surviving and one swallowed occurrence must fail to compile.
func TestRepeatedMetavarPartiallySwallowedErrors(t *testing.T) {
	rules, err := dsl.Parse("regr.lint", `rule r { in xml
 pattern { <a b="$X">hello $X</a> } }`)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	_, err = Compile(rules[0])
	if err == nil {
		t.Fatalf("expected compile error for partially swallowed $X")
	}
	if !strings.Contains(err.Error(), "$X") {
		t.Fatalf("error should name $X: %v", err)
	}
}

// A variadic metavariable in a quoted-value hole is rejected with a clear
// error instead of silently binding like a single capture.
func TestVariadicInAttrValueHoleErrors(t *testing.T) {
	rules, err := dsl.Parse("regr.lint", `rule r { in xml
 pattern { <a b="$$$X"/> } }`)
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	_, err = Compile(rules[0])
	if err == nil {
		t.Fatalf("expected compile error for variadic in quoted value")
	}
	if !strings.Contains(err.Error(), "$$$X") {
		t.Fatalf("error should name $$$X: %v", err)
	}
}
