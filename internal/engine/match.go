package engine

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/eriklanning/lintel/internal/lang"
)

// Binding is the value captured by a metavariable.
type Binding struct {
	Node     *sitter.Node // captured node (nil for variadic captures)
	Text     string       // captured source text
	Variadic bool
}

// Bindings maps metavariable names to their captures.
type Bindings map[string]Binding

func (b Bindings) clone() Bindings {
	out := make(Bindings, len(b))
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (b Bindings) merge(other Bindings) {
	for k, v := range other {
		b[k] = v
	}
}

// matchCtx carries the immutable inputs of a single match attempt.
type matchCtx struct {
	pat  *Pattern
	tsrc []byte
	l    *lang.Language
}

func (c *matchCtx) ttext(n *sitter.Node) string { return n.Content(c.tsrc) }

// matchNode tests whether pattern node p matches target node t, recording any
// metavariable bindings into b (mutated only on the success path of callers).
func (c *matchCtx) matchNode(p, t *sitter.Node, b Bindings) bool {
	// Single metavariable: binds any one node (with equality on reuse).
	if spec, ok := c.pat.metaOf(p); ok {
		if spec.kind == mvVariadic {
			return true // a lone variadic in node position matches anything
		}
		if spec.anon {
			return true
		}
		txt := c.ttext(t)
		if prev, exists := b[spec.name]; exists {
			return prev.Text == txt
		}
		b[spec.name] = Binding{Node: t, Text: txt}
		return true
	}

	p = unwrapTransparent(p, c.pat.lang)
	t = unwrapTransparent(t, c.l)

	// p may have become a metavariable after unwrapping a statement wrapper.
	if spec, ok := c.pat.metaOf(p); ok && spec.kind == mvSingle {
		if spec.anon {
			return true
		}
		txt := c.ttext(t)
		if prev, exists := b[spec.name]; exists {
			return prev.Text == txt
		}
		b[spec.name] = Binding{Node: t, Text: txt}
		return true
	}

	if p.Type() != t.Type() {
		return false
	}

	pk := relevantChildren(p, c.pat.lang)
	tk := relevantChildren(t, c.l)
	if len(pk) == 0 && len(tk) == 0 {
		// Leaf tokens of the same kind: identifiers, literals, operators.
		return c.pat.text(p) == c.ttext(t)
	}
	return c.matchExact(pk, tk, b)
}

// matchExact matches pattern nodes against target nodes requiring that every
// target node is consumed. Unlike matchPrefix it never leaves a tail, so a
// trailing variadic absorbs all remaining nodes.
func (c *matchCtx) matchExact(pats, tgts []*sitter.Node, b Bindings) bool {
	if len(pats) == 0 {
		return len(tgts) == 0
	}
	p := pats[0]
	pu := unwrapForVariadic(p, c.pat.lang)
	if spec, ok := c.pat.metaOf(pu); ok && spec.kind == mvVariadic {
		for k := 0; k <= len(tgts); k++ {
			b2 := b.clone()
			if !spec.anon {
				txt := spanText(tgts[:k], c.tsrc)
				if prev, exists := b2[spec.name]; exists {
					if prev.Text != txt {
						continue
					}
				} else {
					b2[spec.name] = Binding{Text: txt, Variadic: true}
				}
			}
			if c.matchExact(pats[1:], tgts[k:], b2) {
				b.merge(b2)
				return true
			}
		}
		return false
	}
	if len(tgts) == 0 {
		return false
	}
	b2 := b.clone()
	if c.matchNode(p, tgts[0], b2) && c.matchExact(pats[1:], tgts[1:], b2) {
		b.merge(b2)
		return true
	}
	return false
}

// matchPrefix matches pattern nodes against a prefix of target nodes, returning
// how many target nodes were consumed. Variadic pattern nodes absorb a run of
// target nodes (with backtracking).
func (c *matchCtx) matchPrefix(pats, tgts []*sitter.Node, b Bindings) (int, bool) {
	if len(pats) == 0 {
		return 0, true
	}
	p := pats[0]
	// A variadic applies at this list level when, after unwrapping transparent
	// and parameter-wrapper nodes, it is a variadic leaf. A variadic sentinel
	// buried inside a semantic node (e.g. Go's expression_list) is instead
	// matched by recursing into that node.
	pu := unwrapForVariadic(p, c.pat.lang)
	if spec, ok := c.pat.metaOf(pu); ok && spec.kind == mvVariadic {
		for k := 0; k <= len(tgts); k++ {
			b2 := b.clone()
			if !spec.anon {
				txt := spanText(tgts[:k], c.tsrc)
				if prev, exists := b2[spec.name]; exists {
					if prev.Text != txt {
						continue
					}
				} else {
					b2[spec.name] = Binding{Text: txt, Variadic: true}
				}
			}
			if rest, ok := c.matchPrefix(pats[1:], tgts[k:], b2); ok {
				b.merge(b2)
				return k + rest, true
			}
		}
		return 0, false
	}

	if len(tgts) == 0 {
		return 0, false
	}
	b2 := b.clone()
	if c.matchNode(p, tgts[0], b2) {
		if rest, ok := c.matchPrefix(pats[1:], tgts[1:], b2); ok {
			b.merge(b2)
			return 1 + rest, true
		}
	}
	return 0, false
}

// spanText returns the source text spanning a run of sibling nodes.
func spanText(nodes []*sitter.Node, src []byte) string {
	if len(nodes) == 0 {
		return ""
	}
	start := nodes[0].StartByte()
	end := nodes[len(nodes)-1].EndByte()
	return string(src[start:end])
}
