package engine

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/lang"
)

// htmlRule wraps a pattern body in a minimal html rule.
func htmlRule(pattern string) string {
	return "rule r { in html\n pattern { " + pattern + " } }"
}

func TestHTMLOpenTagPattern(t *testing.T) {
	// An open-tag pattern parses as element(start_tag, MISSING end_tag); the
	// phantom end tag is filtered, so the pattern matches the start tag of any
	// div carrying a style attribute, closed or not.
	rule := htmlRule(`<div $$$ style=$_ $$$>`)
	tests := []struct {
		name string
		code string
		want int
	}{
		{"style only", `<div style="x">y</div>`, 1},
		{"style among other attributes", `<div class="a" style="b">y</div>`, 1},
		{"case-folded tag and attribute names", `<DIV STYLE="x">`, 1},
		{"no style attribute", `<div class="a">y</div>`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantN(t, run(t, "html", rule, tt.code), tt.want)
		})
	}
}

func TestHTMLImgVariadicAttrs(t *testing.T) {
	// <img $$$> matches img start tags of any arity (including none); sibling
	// void elements with a different tag name (<br>) are not matched.
	fs := run(t, "html", htmlRule(`<img $$$>`), `<p><img src="a.png"><br><img></p>`)
	wantN(t, fs, 2)
	if fs[0].Snippet != `<img src="a.png">` {
		t.Fatalf("first snippet = %q", fs[0].Snippet)
	}
	if fs[1].Snippet != `<img>` {
		t.Fatalf("second snippet = %q", fs[1].Snippet)
	}
}

func TestHTMLAttrValueMetavariable(t *testing.T) {
	// $X works inside quotes (MetaInStrings) and binds the inner value without
	// the quote characters; quoted and unquoted forms cross-match because
	// quoted_attribute_value is transparent.
	tests := []struct {
		name    string
		pattern string
		code    string
		wantS   string
	}{
		{"quoted pattern, quoted target", `<img src="$S">`, `<img src="a.png">`, "a.png"},
		{"unquoted pattern, unquoted target", `<img src=$S>`, `<img src=x>`, "x"},
		{"unquoted pattern, quoted target", `<img src=$S>`, `<img src="y">`, "y"},
		{"quoted pattern, unquoted target", `<img src="$S">`, `<img src=x>`, "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "html", htmlRule(tt.pattern), tt.code)
			wantN(t, fs, 1)
			if got := fs[0].Bindings["S"]; got != tt.wantS {
				t.Fatalf("$S = %q, want %q", got, tt.wantS)
			}
		})
	}
}

func TestHTMLTagNameMetavariable(t *testing.T) {
	rule := htmlRule(`<$T style=$_>`)
	fs := run(t, "html", rule, `<span style="color:red">hi</span><b>q</b>`)
	wantN(t, fs, 1)
	if fs[0].Bindings["T"] != "span" {
		t.Fatalf("$T = %q, want %q", fs[0].Bindings["T"], "span")
	}
}

func TestHTMLTagNameBackref(t *testing.T) {
	// Reusing $T as outer and inner tag names requires the same tag in both
	// positions (binding equality on reuse).
	rule := htmlRule(`<$T><$T>$_</$T></$T>`)
	tests := []struct {
		name string
		code string
		want int
	}{
		{"same nested tag", `<div><div>x</div></div>`, 1},
		{"different nested tag", `<div><span>x</span></div>`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "html", rule, tt.code)
			wantN(t, fs, tt.want)
			if tt.want == 1 && fs[0].Bindings["T"] != "div" {
				t.Fatalf("$T = %q, want %q", fs[0].Bindings["T"], "div")
			}
		})
	}
}

func TestHTMLVariadicChildren(t *testing.T) {
	rule := htmlRule(`<ul>$$$ITEMS</ul>`)
	fs := run(t, "html", rule, `<ul><li>a</li><li>b</li></ul>`)
	wantN(t, fs, 1)
	if got := fs[0].Bindings["ITEMS"]; got != `<li>a</li><li>b</li>` {
		t.Fatalf("$ITEMS = %q", got)
	}
}

func TestHTMLElementPattern(t *testing.T) {
	// A full element pattern (with end tag) matches each closed element; $X in
	// content position binds the text child.
	rule := htmlRule(`<li>$X</li>`)
	fs := run(t, "html", rule, `<ul><li>a</li><li>b</li></ul>`)
	wantN(t, fs, 2)
	if fs[0].Snippet != `<li>a</li>` {
		t.Fatalf("first snippet = %q", fs[0].Snippet)
	}
	if fs[0].Bindings["X"] != "a" || fs[1].Bindings["X"] != "b" {
		t.Fatalf("$X bindings = %q, %q; want a, b", fs[0].Bindings["X"], fs[1].Bindings["X"])
	}
}

func TestHTMLWhereRegexConstraint(t *testing.T) {
	rule := `rule r { in html
 pattern { <a href="$U">$_</a> }
 where $U matches "^https://" }`
	fs := run(t, "html", rule, `<a href="https://x.com">x</a><a href="http://y.com">y</a>`)
	wantN(t, fs, 1)
	if fs[0].Bindings["U"] != "https://x.com" {
		t.Fatalf("$U = %q", fs[0].Bindings["U"])
	}
}

func TestHTMLHasRelation(t *testing.T) {
	// forms that contain an input
	rule := "rule r { in html\n pattern { <form>$$$</form> }\n has pattern { <input $$$> } }"
	fs := run(t, "html", rule, `<form><input type="text"></form><form><p>x</p></form>`)
	wantN(t, fs, 1)
}

func TestHTMLInsideRelation(t *testing.T) {
	// inputs inside a form: the relation pattern must be a full element
	// (<form>$$$</form>) so it matches the form ELEMENT node, which is an
	// ancestor of the input.
	rule := "rule r { in html\n pattern { <input $$$> }\n inside pattern { <form>$$$</form> } }"
	fs := run(t, "html", rule, `<form><input type="text"></form><input type="hidden">`)
	wantN(t, fs, 1)
	if fs[0].Snippet != `<input type="text">` {
		t.Fatalf("snippet = %q", fs[0].Snippet)
	}
}

func TestHTMLInsideRelationOpenTagPattern(t *testing.T) {
	// An OPEN-TAG relation pattern (<form $$$>) compiles to the form's
	// start_tag node, but ancestors are element nodes; relation matching
	// accepts an element whose start tag matches, so `inside` works as
	// written for both open-tag and full-element relation patterns.
	rule := "rule r { in html\n pattern { <input $$$> }\n inside pattern { <form $$$> } }"
	fs := run(t, "html", rule, `<form><input type="text"></form>`)
	wantN(t, fs, 1)

	fs = run(t, "html", rule, `<div><input type="text"></div>`)
	wantN(t, fs, 0)
}

func TestHTMLMetavarSwallowedIntoText(t *testing.T) {
	// A metavariable embedded in larger text CONTENT does not survive parsing
	// as its own node (HTML lumps free text into one token), so the pattern
	// fails to compile with a clear error. Note the element wrapper: a bare
	// `hello $X` does NOT error — the html attribute scaffold (<lntl-x …>)
	// rescues it as a sequence of two attributes (see test below).
	rules, err := dsl.Parse("test.lint", htmlRule(`<p>hello $X</p>`))
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	_, err = Compile(rules[0])
	if err == nil {
		t.Fatalf("expected compile error for metavariable swallowed into text")
	}
	if !strings.Contains(err.Error(), "do not parse as standalone nodes") {
		t.Fatalf("error = %v, want it to mention \"do not parse as standalone nodes\"", err)
	}
}

func TestHTMLBareTextMetavarCompilesAsAttributes(t *testing.T) {
	// Documents actual behavior: as raw text `hello Lntlmv-X` is one swallowed
	// text token, but compilePattern then tries the html scaffold (<lntl-x …>),
	// where the fragment parses as two boolean attributes and $X survives as an
	// attribute_name. The pattern therefore compiles — as an attribute-sequence
	// pattern, not a text pattern.
	rules, err := dsl.Parse("test.lint", htmlRule(`hello $X`))
	if err != nil {
		t.Fatalf("parse rule: %v", err)
	}
	if _, err := Compile(rules[0]); err != nil {
		t.Fatalf("expected `hello $X` to compile via the attribute scaffold, got: %v", err)
	}
	l, ok := lang.Get("html")
	if !ok {
		t.Fatal("html language not registered")
	}
	dbg, err := DebugPattern(l, `hello $X`)
	if err != nil {
		t.Fatalf("DebugPattern: %v", err)
	}
	if !strings.Contains(dbg, "sequence of 2 nodes") || !strings.Contains(dbg, "attribute_name") {
		t.Fatalf("debug = %q, want an attribute sequence of 2 nodes", dbg)
	}
}
