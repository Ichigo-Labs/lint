package engine

import (
	"context"
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/eriklanning/lintel/internal/lang"
)

type metaKind int

const (
	mvSingle metaKind = iota
	mvVariadic
)

// metaSpec describes a metavariable discovered in a pattern.
type metaSpec struct {
	name string // capture name (empty for anonymous)
	kind metaKind
	anon bool
}

// Pattern is a compiled structural pattern for one language.
type Pattern struct {
	lang  *lang.Language
	src   []byte              // rewritten pattern source
	metas map[string]metaSpec // sentinel identifier -> spec
	tree  *sitter.Tree        // kept alive so nodes remain valid

	root  *sitter.Node   // single-node pattern root
	isSeq bool           // sequence pattern (match a run of sibling statements)
	seq   []*sitter.Node // sequence elements (named children)
}

func isIdentStartByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentContByte(c byte) bool {
	return isIdentStartByte(c) || (c >= '0' && c <= '9')
}

// rewrite replaces metavariable tokens in a raw pattern with sentinel
// identifiers that parse cleanly in every target grammar, returning the
// rewritten source and a map from sentinel text to metavariable spec.
//
//	$NAME   -> Lntlmv_NAME      (single, named; reuse implies equality)
//	$_      -> Lntlanon_<n>     (single, anonymous wildcard)
//	$$$NAME -> Lntlvr_NAME      (variadic, named)
//	$$$     -> Lntlvanon_<n>    (variadic, anonymous)
func rewrite(pattern string) (string, map[string]metaSpec) {
	var b strings.Builder
	metas := map[string]metaSpec{}
	anon := 0
	i, n := 0, len(pattern)
	for i < n {
		c := pattern[i]
		// Do not interpret '$' inside the target language's string/char
		// literals: copy the literal verbatim so e.g. print("$msg") matches a
		// literal string rather than binding a metavariable inside it.
		if c == '"' || c == '\'' || c == '`' {
			b.WriteByte(c)
			i++
			for i < n {
				d := pattern[i]
				b.WriteByte(d)
				i++
				if d == '\\' && c != '`' && i < n {
					b.WriteByte(pattern[i])
					i++
					continue
				}
				if d == c {
					break
				}
			}
			continue
		}
		if c != '$' {
			b.WriteByte(pattern[i])
			i++
			continue
		}
		dollars := 0
		for i < n && pattern[i] == '$' {
			dollars++
			i++
		}
		name := ""
		if i < n && isIdentStartByte(pattern[i]) {
			start := i
			for i < n && isIdentContByte(pattern[i]) {
				i++
			}
			name = pattern[start:i]
		}
		switch {
		case dollars >= 2: // variadic ($$ or $$$)
			if name == "" || name == "_" {
				anon++
				sent := fmt.Sprintf("Lntlvanon_%d", anon)
				metas[sent] = metaSpec{kind: mvVariadic, anon: true}
				b.WriteString(sent)
			} else {
				sent := "Lntlvr_" + name
				metas[sent] = metaSpec{name: name, kind: mvVariadic}
				b.WriteString(sent)
			}
		case name == "": // lone '$' with no name: treat as literal text
			b.WriteByte('$')
		case name == "_": // anonymous single wildcard
			anon++
			sent := fmt.Sprintf("Lntlanon_%d", anon)
			metas[sent] = metaSpec{kind: mvSingle, anon: true}
			b.WriteString(sent)
		default: // named single
			sent := "Lntlmv_" + name
			metas[sent] = metaSpec{name: name, kind: mvSingle}
			b.WriteString(sent)
		}
	}
	return b.String(), metas
}

// scaffold wraps a pattern in surrounding code so that expression and
// statement fragments parse in languages with a strict top level.
type scaffold struct{ prefix, suffix string }

// scaffolds lists, per language, the contexts in which to try parsing a
// pattern. The empty (raw) context is always tried first by compilePattern.
var scaffolds = map[string][]scaffold{
	"go": {
		{"package p\nfunc _() {\n", "\n}"},
		{"package p\nvar _ = ", ""},
	},
	"rust": {
		{"fn _() {\n", "\n}"},
		{"fn _() { let _ = ", "; }"},
	},
	"java": {
		{"class _ {\n", "\n}"},
		{"class _ { void _() {\n", "\n} }"},
		{"class _ { Object _ = ", "; }"},
	},
	"csharp": {
		{"class _ {\n", "\n}"},
		{"class _ { void _() {\n", "\n} }"},
		{"class _ { object _ = ", "; }"},
	},
	"c": {
		{"void _() {\n", "\n}"},
		{"int _ = ", ";"},
	},
	"cpp": {
		{"void _() {\n", "\n}"},
		{"auto _ = ", ";"},
	},
	"python": {
		{"_ = ", ""},
	},
	"typescript": {
		{"function _() {\n", "\n}"},
		{"let _ = ", ""},
	},
	"tsx": {
		{"function _() {\n", "\n}"},
		{"let _ = ", ""},
	},
}

// compilePattern parses a raw pattern under a language grammar and resolves its
// structural shape (single node vs. statement sequence). It tries the raw
// pattern first, then wraps it in scaffolding contexts until one parses.
func compilePattern(l *lang.Language, raw string) (*Pattern, error) {
	rewritten, metas := rewrite(raw)
	parser := sitter.NewParser()
	parser.SetLanguage(l.Grammar())

	attempts := append([]scaffold{{"", ""}}, scaffolds[l.Name]...)
	for _, sc := range attempts {
		full := sc.prefix + rewritten + sc.suffix
		tree, err := parser.ParseCtx(context.Background(), nil, []byte(full))
		if err != nil {
			continue
		}
		root := tree.RootNode()
		if root.HasError() || root.IsMissing() {
			continue
		}
		// Populate smacker's lazy node cache single-threaded so concurrent
		// matching against this shared pattern tree never writes the cache.
		prewarm(root)
		bs := uint32(len(sc.prefix))
		be := uint32(len(sc.prefix) + len(rewritten))
		nodes := topNodesInRange(root, bs, be, l)
		if len(nodes) == 0 {
			continue
		}
		p := &Pattern{lang: l, src: []byte(full), metas: metas, tree: tree}
		if len(nodes) == 1 {
			core := unwrapTransparent(unwrap(nodes[0]), l)
			if wrapperKinds[core.Type()] && core.NamedChildCount() > 1 {
				p.isSeq = true
				p.root = core
				p.seq = namedChildren(core, l)
			} else {
				p.root = core
			}
			// A pattern that is nothing but a single metavariable matches
			// everything; reject it to avoid runaway findings.
			if _, isMeta := p.metaOf(p.root); isMeta && !p.isSeq {
				return nil, fmt.Errorf("pattern %q is a bare metavariable and would match every node", raw)
			}
			return p, nil
		}
		p.isSeq = true
		p.seq = nodes
		p.root = nodes[0].Parent()
		return p, nil
	}
	return nil, fmt.Errorf("pattern is not valid %s:\n  %s", l.Name, strings.ReplaceAll(raw, "\n", "\n  "))
}

// topNodesInRange returns the shallowest nodes whose byte span lies fully
// within [start,end), skipping comments/extras. For a raw pattern the whole
// root qualifies; for a scaffolded pattern the inserted fragment's nodes do.
func topNodesInRange(root *sitter.Node, start, end uint32, l *lang.Language) []*sitter.Node {
	if root.StartByte() >= start && root.EndByte() <= end {
		return []*sitter.Node{root}
	}
	var out []*sitter.Node
	var rec func(n *sitter.Node)
	rec = func(n *sitter.Node) {
		count := int(n.ChildCount())
		for i := 0; i < count; i++ {
			c := n.Child(i)
			if c == nil || c.IsExtra() || l.IsComment(c.Type()) {
				continue
			}
			if c.StartByte() >= start && c.EndByte() <= end {
				out = append(out, c)
			} else if c.EndByte() > start && c.StartByte() < end {
				rec(c)
			}
		}
	}
	rec(root)
	return out
}

// DebugPattern compiles a raw pattern and returns a human-readable description
// of its resolved structure. Used by `lintel parse --pattern`.
func DebugPattern(l *lang.Language, raw string) (string, error) {
	p, err := compilePattern(l, raw)
	if err != nil {
		return "", err
	}
	if p.isSeq {
		var b strings.Builder
		fmt.Fprintf(&b, "sequence of %d nodes:\n", len(p.seq))
		for _, n := range p.seq {
			fmt.Fprintf(&b, "  %s\n", n.String())
		}
		return b.String(), nil
	}
	return fmt.Sprintf("kind: %s\n%s", p.root.Type(), p.root.String()), nil
}

// metaOf reports the metavariable spec for a pattern node, if the node IS a
// sentinel leaf. It deliberately ignores larger nodes whose text merely equals
// a sentinel (e.g. a grammar wrapper holding only the sentinel), so matching
// recurses into the wrapper and binds the tightest node.
func (p *Pattern) metaOf(n *sitter.Node) (metaSpec, bool) {
	if n.ChildCount() != 0 {
		return metaSpec{}, false
	}
	s, ok := p.metas[n.Content(p.src)]
	return s, ok
}

// text returns a pattern node's source text.
func (p *Pattern) text(n *sitter.Node) string { return n.Content(p.src) }
