package engine

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
)

// wrapperKinds are nodes that a pattern snippet acquires merely from being
// parsed as a whole file. Unwrapping them lets a pattern like `foo()` match a
// call wherever it appears, ignoring statement/file scaffolding.
var wrapperKinds = map[string]bool{
	"program":          true, // js/ts
	"source_file":      true, // go, rust
	"module":           true, // python
	"translation_unit": true, // c, c++
	"compilation_unit": true, // c#, java
	"document":         true, // html, xml, yaml (one document of a stream)
	"stylesheet":       true, // css, scss
	"stream":           true, // yaml
	"block_mapping":    true, // yaml: a single-pair pattern matches the pair anywhere
	"block_sequence":   true, // yaml: a single-item pattern matches the item anywhere
}

// transparentKinds wrap a single expression as a statement (or, in C#, a
// top-level statement). Matching looks through them so `foo()` matches
// `foo();` regardless of statement context. In the markup languages they
// strip single-child wrappers around the meaningful node: an open-tag
// pattern `<img …>` parses as element(start_tag), and looking through the
// element makes it match the start tag of any matching element, closed or
// not; quoted_attribute_value unwraps so `class="x"` and `class=x` compare
// equal; XML's content wrapper unwraps so `<a>$$$</a>` absorbs any children.
var transparentKinds = map[string]bool{
	"expression_statement":   true,
	"global_statement":       true, // c# top-level statements
	"element":                true, // html, xml (single-child: an unclosed/self-closing element)
	"quoted_attribute_value": true, // html
	"content":                true, // xml element content
	"block_node":             true, // yaml: wraps a block mapping/sequence/scalar
	"flow_node":              true, // yaml: wraps any flow value
	"plain_scalar":           true, // yaml: wraps the typed scalar
}

// isTransparent reports whether matching looks through kind: the engine-wide
// wrappers plus any language-specific ones (JSON's "string").
func isTransparent(kind string, l *lang.Language) bool {
	return transparentKinds[kind] || l.IsTransparent(kind)
}

// blockKinds are the statement-list containers across the supported grammars: a
// candidate and its sibling statements share one of these as their nearest
// common parent. Used by the precedes/follows relations to scope "same block".
var blockKinds = map[string]bool{
	"block":              true, // go, python, java, rust, c#; css/scss rule bodies
	"statement_block":    true, // ts/tsx/js
	"compound_statement": true, // c, c++
	"statements":         true, // kotlin, swift
	"source_file":        true, // go, rust, kotlin, swift (top level)
	"module":             true, // python (top level)
	"program":            true, // ts/js, java (top level)
	"translation_unit":   true, // c, c++ (top level)
	"compilation_unit":   true, // c# (top level)
	"document":           true, // html, xml (top level)
	"element":            true, // html (element children)
	"content":            true, // xml (element children)
	"stylesheet":         true, // css, scss (top level)
	"block_mapping":      true, // yaml (mapping pairs as siblings)
	"block_sequence":     true, // yaml (sequence items as siblings)
}

// terminatorKinds are anonymous statement/list separators that carry no
// structural meaning for matching. Dropping them (on both pattern and target
// sides) lets `throw $X` match `throw e;`, makes block arity robust across
// languages that model terminators differently (Go uses newline tokens, others
// use `;`), and lets a variadic absorb a comma-separated run so `f($A, $$$REST)`
// matches `f(1)`. The only precision lost is the presence of empty for-clause
// slots and trailing commas.
var terminatorKinds = map[string]bool{
	";":    true,
	",":    true,
	"\n":   true,
	"\r":   true,
	"\r\n": true,
}

// paramWrapperKinds wrap a single expression/identifier as one element of a
// parameter or argument list. Variadic detection unwraps them so `f($$$)` and
// `function f($$$)` recognise the `$$$` as a list-variadic, rather than a
// single wrapped element.
var paramWrapperKinds = map[string]bool{
	"required_parameter":          true, // ts/tsx
	"optional_parameter":          true, // ts/tsx
	"parameter":                   true, // rust, c#
	"formal_parameter":            true, // java
	"spread_parameter":            true, // java
	"parameter_declaration":       true, // go, c, c++
	"typed_parameter":             true, // python
	"default_parameter":           true, // python
	"typed_default_parameter":     true, // python
	"argument":                    true, // c# call arguments
	"value_argument":              true, // kotlin, swift call arguments
	"literal_element":             true, // go composite-literal elements
	"keyed_element":               true, // go keyed composite-literal elements
	"field_initializer":           true, // rust struct-literal fields
	"shorthand_field_initializer": true, // rust struct-literal shorthand
	"attribute":                   true, // html boolean attributes (`<img $$$>`)
	"Attribute":                   true, // xml attributes (`<a $$$>` via the ="" rewrite)
	"block_sequence_item":         true, // yaml list items (`- $$$`)
}

// relevantChildren returns a node's children with comments and grammar "extra"
// nodes removed. Anonymous nodes (punctuation, operators, keywords) are kept so
// that matching is precise about operators and structure.
func relevantChildren(n *sitter.Node, l *lang.Language) []*sitter.Node {
	count := int(n.ChildCount())
	out := make([]*sitter.Node, 0, count)
	for i := 0; i < count; i++ {
		c := n.Child(i)
		if c == nil || c.IsExtra() || c.IsMissing() || l.IsComment(c.Type()) || terminatorKinds[c.Type()] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// namedChildren returns a node's named children minus comments/extras.
func namedChildren(n *sitter.Node, l *lang.Language) []*sitter.Node {
	count := int(n.NamedChildCount())
	out := make([]*sitter.Node, 0, count)
	for i := 0; i < count; i++ {
		c := n.NamedChild(i)
		if c == nil || c.IsExtra() || c.IsMissing() || l.IsComment(c.Type()) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// unwrap descends through file-level wrapper nodes while they have exactly one
// named child, returning the meaningful root of a pattern.
func unwrap(n *sitter.Node) *sitter.Node {
	for {
		if !wrapperKinds[n.Type()] || n.NamedChildCount() != 1 {
			return n
		}
		nc := n.NamedChild(0)
		if nc == nil {
			return n
		}
		n = nc
	}
}

// unwrapTransparent strips expression-statement style wrappers from a single
// node so equivalent expressions compare equal regardless of statement context.
func unwrapTransparent(n *sitter.Node, l *lang.Language) *sitter.Node {
	for isTransparent(n.Type(), l) {
		kids := namedChildren(n, l)
		if len(kids) != 1 {
			return n
		}
		n = kids[0]
	}
	return n
}

// startTagKinds are the open-tag nodes an open-tag pattern compiles to, and
// elementKinds the element wrappers that own them. Relation matching uses the
// pair so `inside { <form $$$> }` accepts the form element (the ancestor)
// whose start tag matches.
var startTagKinds = map[string]bool{
	"start_tag":    true, // html
	"STag":         true, // xml
	"EmptyElemTag": true, // xml self-closing
}

var elementKinds = map[string]bool{
	"element": true, // html, xml
}

// innerSpan returns the byte span of n's textual content: the hole between
// its anonymous delimiter tokens. Several grammars store a literal's content
// only as part of the parent's span — an XML AttValue holds just its two
// quote tokens, a CSS color_value just its '#' — so the content is the text
// between the leading and trailing runs of children. Returns ok=false when n
// has named children (real structure) or children that don't hug the edges.
// An empty hole (e.g. attr="") is valid and returns start == end.
func innerSpan(n *sitter.Node) (start, end uint32, ok bool) {
	count := int(n.ChildCount())
	if count == 0 || n.NamedChildCount() != 0 {
		return 0, 0, false
	}
	start, end = n.StartByte(), n.EndByte()
	i := 0
	for i < count {
		c := n.Child(i)
		if c == nil || c.StartByte() != start {
			break
		}
		start = c.EndByte()
		i++
	}
	j := count - 1
	for j >= i {
		c := n.Child(j)
		if c == nil || c.EndByte() != end {
			break
		}
		end = c.StartByte()
		j--
	}
	if j >= i || start > end {
		return 0, 0, false // children in the middle: not a pure text hole
	}
	return start, end, true
}

// walk visits every node in the tree rooted at n in pre-order.
func walk(n *sitter.Node, fn func(*sitter.Node)) {
	fn(n)
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		c := n.Child(i)
		if c != nil {
			walk(c, fn)
		}
	}
}
