package engine

import "testing"

// TestXMLPatternCounts is a table of xml patterns vs. sources, checking only
// the number of findings (bindings are asserted in the dedicated tests below).
func TestXMLPatternCounts(t *testing.T) {
	tests := []struct {
		name string
		rule string
		code string
		n    int
	}{
		{
			name: "element inner text metavariable",
			rule: "rule r { in xml\n pattern { <version>$V</version> } }",
			code: `<deps><version>1.2-SNAPSHOT</version></deps>`,
			n:    1,
		},
		{
			name: "attribute metavariable requires the attribute name",
			rule: "rule r { in xml\n pattern { <a b=\"$X\"/> } }",
			code: `<r><a b="hello"/><a c="hello"/></r>`,
			n:    1, // <a c="hello"/> has no b attribute
		},
		{
			name: "attribute value literal compares by text", // regression: AttValue content is not a child node
			rule: "rule r { in xml\n pattern { <a b=\"x\"/> } }",
			code: `<r><a b="x"/><a b="y"/></r>`,
			n:    1,
		},
		{
			name: "variadic attributes and children",
			rule: "rule r { in xml\n pattern { <dependency $$$A>$$$C</dependency> } }",
			code: `<deps><dependency scope="test"><version>1</version></dependency><dependency></dependency></deps>`,
			n:    2, // with attrs+children and with neither
		},
		{
			name: "empty-element pattern does not match open/close element",
			rule: "rule r { in xml\n pattern { <a $$$/> } }",
			code: `<r><a x="1"/><a x="1"></a></r>`,
			n:    1, // EmptyElemTag and STag are distinct node kinds
		},
		{
			name: "tag names are case-sensitive",
			rule: "rule r { in xml\n pattern { <Version>$V</Version> } }",
			code: `<r><version>x</version><Version>y</Version></r>`,
			n:    1,
		},
		{
			name: "where regex on bound inner text",
			rule: "rule r { in xml\n pattern { <version>$V</version> }\n where $V matches \"-SNAPSHOT$\" }",
			code: `<r><version>1.2-SNAPSHOT</version><version>1.2</version></r>`,
			n:    1,
		},
		{
			name: "where numeric on bound inner text",
			rule: "rule r { in xml\n pattern { <timeout>$N</timeout> }\n where $N > 1000 }",
			code: `<cfg><timeout>500</timeout><timeout>2000</timeout><timeout>1000</timeout><timeout>abc</timeout></cfg>`,
			n:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := run(t, "xml", tt.rule, tt.code)
			wantN(t, fs, tt.n)
		})
	}
}

func TestXMLInnerTextBinding(t *testing.T) {
	rule := "rule r { in xml\n pattern { <version>$V</version> } }"
	fs := run(t, "xml", rule, `<deps><version>1.2-SNAPSHOT</version></deps>`)
	wantN(t, fs, 1)
	if fs[0].Bindings["V"] != "1.2-SNAPSHOT" {
		t.Fatalf("$V = %q, want %q", fs[0].Bindings["V"], "1.2-SNAPSHOT")
	}
}

func TestXMLAttributeBindingStripsQuotes(t *testing.T) {
	// MetaInStrings: $X inside a quoted attribute value binds the inner text.
	rule := "rule r { in xml\n pattern { <a b=\"$X\"/> } }"
	fs := run(t, "xml", rule, `<r><a b="hello"/></r>`)
	wantN(t, fs, 1)
	if fs[0].Bindings["X"] != "hello" {
		t.Fatalf("$X = %q, want %q (quotes must be stripped)", fs[0].Bindings["X"], "hello")
	}
}

func TestXMLEmptyElemSnippet(t *testing.T) {
	rule := "rule r { in xml\n pattern { <a $$$/> } }"
	fs := run(t, "xml", rule, `<r><a x="1"/><a x="1"></a></r>`)
	wantN(t, fs, 1)
	if fs[0].Snippet != `<a x="1"/>` {
		t.Fatalf("got snippet %q, want %q", fs[0].Snippet, `<a x="1"/>`)
	}
}

func TestXMLTagBackref(t *testing.T) {
	// <$T>$_</$T>: $T binds the tag name; the close-tag occurrence is a
	// back-reference, so both must agree (always true in well-formed XML) and
	// the binding is available to where clauses.
	rule := "rule r { in xml\n pattern { <$T>$_</$T> }\n where $T matches \"^v\" }"
	fs := run(t, "xml", rule, `<r><version>1</version><name>x</name></r>`)
	wantN(t, fs, 1)
	if fs[0].Bindings["T"] != "version" {
		t.Fatalf("$T = %q, want %q", fs[0].Bindings["T"], "version")
	}
}

func TestXMLSiblingSequence(t *testing.T) {
	// A multi-element pattern compiles as a sequence (scaffolded under a single
	// root) and matches consecutive sibling elements only.
	rule := "rule r { in xml\n pattern { <a/><b/> } }"
	fs := run(t, "xml", rule, `<r><a/><b/></r>`)
	wantN(t, fs, 1)

	// Non-consecutive siblings do not match.
	fs = run(t, "xml", rule, `<r><a/><c/><b/></r>`)
	wantN(t, fs, 0)
}
