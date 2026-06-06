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
}

// transparentKinds wrap a single expression as a statement (or, in C#, a
// top-level statement). Matching looks through them so `foo()` matches
// `foo();` regardless of statement context.
var transparentKinds = map[string]bool{
	"expression_statement": true,
	"global_statement":     true, // c# top-level statements
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
	"literal_element":             true, // go composite-literal elements
	"keyed_element":               true, // go keyed composite-literal elements
	"field_initializer":           true, // rust struct-literal fields
	"shorthand_field_initializer": true, // rust struct-literal shorthand
}

// unwrapForVariadic descends through transparent and parameter-wrapper nodes so
// a buried variadic sentinel is recognised at a list level.
func unwrapForVariadic(n *sitter.Node, l *lang.Language) *sitter.Node {
	for transparentKinds[n.Type()] || paramWrapperKinds[n.Type()] {
		kids := namedChildren(n, l)
		if len(kids) != 1 {
			return n
		}
		n = kids[0]
	}
	return n
}

// relevantChildren returns a node's children with comments and grammar "extra"
// nodes removed. Anonymous nodes (punctuation, operators, keywords) are kept so
// that matching is precise about operators and structure.
func relevantChildren(n *sitter.Node, l *lang.Language) []*sitter.Node {
	count := int(n.ChildCount())
	out := make([]*sitter.Node, 0, count)
	for i := 0; i < count; i++ {
		c := n.Child(i)
		if c == nil || c.IsExtra() || l.IsComment(c.Type()) || terminatorKinds[c.Type()] {
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
		if c == nil || c.IsExtra() || l.IsComment(c.Type()) {
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
	for transparentKinds[n.Type()] {
		kids := namedChildren(n, l)
		if len(kids) != 1 {
			return n
		}
		n = kids[0]
	}
	return n
}

// prewarm forces smacker's lazy per-tree node cache to be fully populated
// single-threaded. smacker's Tree.cache is an unsynchronized map written on
// cache miss by Child/NamedChild/Parent; without prewarming, concurrent
// matching against a shared pattern tree would write that map concurrently and
// panic. After prewarming, concurrent matching only reads the cache, which is
// safe. Call this once per pattern tree at compile time.
func prewarm(n *sitter.Node) {
	count := int(n.ChildCount())
	for i := 0; i < count; i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		_ = c.Parent()
		prewarm(c)
	}
	named := int(n.NamedChildCount())
	for i := 0; i < named; i++ {
		_ = n.NamedChild(i)
	}
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
