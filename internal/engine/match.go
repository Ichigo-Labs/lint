package engine

import (
	"bytes"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
)

// Binding is the value captured by a metavariable.
type Binding struct {
	Node     *sitter.Node // captured node (nil for variadic captures)
	Text     string       // captured source text
	Variadic bool
	Count    int // number of nodes a variadic captured (0 for single captures)
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

// matchCtx carries the immutable inputs of a single match attempt, plus the
// binding undo journal: the matching kernel mutates one Bindings map in place
// and rolls failed branches back, instead of cloning the map at every
// pattern/target pair.
type matchCtx struct {
	pat   *Pattern
	tsrc  []byte
	l     *lang.Language
	index *Index // optional per-file node index (shared across a file's rules)

	undo []undoEntry
}

// undoEntry records one binding write so a failed branch can be rolled back.
type undoEntry struct {
	key     string
	prev    Binding
	existed bool
}

func (c *matchCtx) mark() int { return len(c.undo) }

// bind writes a binding through the journal.
func (c *matchCtx) bind(b Bindings, key string, v Binding) {
	prev, existed := b[key]
	c.undo = append(c.undo, undoEntry{key: key, prev: prev, existed: existed})
	b[key] = v
}

// rollback restores b to its state at mark.
func (c *matchCtx) rollback(b Bindings, mark int) {
	for i := len(c.undo) - 1; i >= mark; i-- {
		e := c.undo[i]
		if e.existed {
			b[e.key] = e.prev
		} else {
			delete(b, e.key)
		}
	}
	c.undo = c.undo[:mark]
}

func (c *matchCtx) ttext(n *sitter.Node) string {
	return string(c.tsrc[n.StartByte():n.EndByte()])
}

// tkind returns a target node's kind, via the per-file symbol cache when one
// is available.
func (c *matchCtx) tkind(n *sitter.Node) string {
	if c.index != nil {
		return c.index.kind(n)
	}
	return n.Type()
}

// trel returns a target node's relevant children, memoized per file.
func (c *matchCtx) trel(n *sitter.Node) []*sitter.Node {
	if c.index != nil {
		return c.index.relevant(n, c.l)
	}
	return relevantChildren(n, c.l)
}

// tnamed returns a target node's named children, memoized per file.
func (c *matchCtx) tnamed(n *sitter.Node) []*sitter.Node {
	if c.index != nil {
		return c.index.named(n, c.l)
	}
	return namedChildren(n, c.l)
}

// tUnwrapTransparent is unwrapTransparent for target nodes, using the
// memoized kind/children lookups.
func (c *matchCtx) tUnwrapTransparent(n *sitter.Node) *sitter.Node {
	for isTransparent(c.tkind(n), c.l) {
		kids := c.tnamed(n)
		if len(kids) != 1 {
			return n
		}
		n = kids[0]
	}
	return n
}

// pUnwrapTransparent is unwrapTransparent for pattern nodes, using the
// pattern's precomputed tables.
func (c *matchCtx) pUnwrapTransparent(n *sitter.Node) *sitter.Node {
	for isTransparent(c.pat.kind(n), c.pat.lang) {
		kids := c.pat.named(n)
		if len(kids) != 1 {
			return n
		}
		n = kids[0]
	}
	return n
}

// matchNode tests whether pattern node p matches target node t, recording any
// metavariable bindings into b through the undo journal. On a false return, b
// is unchanged (every failing branch rolls back its own writes).
func (c *matchCtx) matchNode(p, t *sitter.Node, b Bindings) bool {
	// Single metavariable: binds any one node (with equality on reuse). The
	// target is bound through its transparent wrappers, so `$X` captures the
	// expression rather than the statement (and an attribute value rather
	// than its quoting wrapper).
	if spec, ok := c.pat.metaOf(p); ok {
		if spec.kind == mvVariadic {
			return true // a lone variadic in node position matches anything
		}
		if spec.anon {
			return true
		}
		tu := c.tUnwrapTransparent(t)
		txt := c.ttext(tu)
		if prev, exists := b[spec.name]; exists {
			return prev.Text == txt
		}
		c.bind(b, spec.name, Binding{Node: tu, Text: txt})
		return true
	}

	p = c.pUnwrapTransparent(p)
	t = c.tUnwrapTransparent(t)

	// p may have become a metavariable after unwrapping a statement wrapper.
	if spec, ok := c.pat.metaOf(p); ok && spec.kind == mvSingle {
		if spec.anon {
			return true
		}
		txt := c.ttext(t)
		if prev, exists := b[spec.name]; exists {
			return prev.Text == txt
		}
		c.bind(b, spec.name, Binding{Node: t, Text: txt})
		return true
	}

	pKind := c.pat.kind(p)
	if pKind != c.tkind(t) {
		return false
	}

	// A metavariable in a node's text hole (`attr="$X"` in XML): the content
	// is not a child node, so bind the target's hole text.
	if spec, ok := c.pat.metaOfInner(p); ok {
		ts, te, tok := innerSpan(t)
		if !tok {
			return false
		}
		if spec.anon {
			return true
		}
		txt := string(c.tsrc[ts:te])
		if prev, exists := b[spec.name]; exists {
			return prev.Text == txt
		}
		c.bind(b, spec.name, Binding{Text: txt})
		return true
	}

	// Literal nodes compare by their full source text: many grammars do not
	// expose a literal's content as a child node (a Go string holds only its
	// quote tokens, a CSS color only its '#'), so comparing children would
	// ignore the content entirely. Interpolated literals (template strings,
	// f-strings) keep structural matching so the code inside `${…}` compares
	// like code, not like text.
	if c.pat.lang.IsLiteral(pKind) && !c.hasInterpolation(p) {
		return bytes.Equal(c.pat.textBytes(p), c.tsrc[t.StartByte():t.EndByte()])
	}

	pk := c.pat.relevant(p)
	tk := c.trel(t)
	if len(pk) == 0 && len(tk) == 0 {
		// Leaf tokens of the same kind: identifiers, literals, operators.
		pb, tb := c.pat.textBytes(p), c.tsrc[t.StartByte():t.EndByte()]
		if c.l.FoldsCase(pKind) {
			return strings.EqualFold(string(pb), string(tb))
		}
		return bytes.Equal(pb, tb)
	}
	// Nodes with no named structure on either side (only delimiter tokens)
	// compare textually when either side has real content between the tokens
	// (an XML attribute value, say). When both holes are empty or whitespace
	// (`()` vs `( )`), the delimiters compare structurally so that
	// whitespace and comments inside stay irrelevant, as everywhere else.
	if !hasNamed(pk) && !hasNamed(tk) {
		if holeHasContent(p, c.pat.src) || holeHasContent(t, c.tsrc) {
			return bytes.Equal(c.pat.textBytes(p), c.tsrc[t.StartByte():t.EndByte()])
		}
		return c.matchExact(pk, tk, b)
	}
	return c.matchExact(pk, tk, b)
}

// hasNamed reports whether any node in the list is a named grammar node.
func hasNamed(nodes []*sitter.Node) bool {
	for _, n := range nodes {
		if n.IsNamed() {
			return true
		}
	}
	return false
}

// holeHasContent reports whether a node's inner span (the text between its
// delimiter tokens) contains non-whitespace bytes.
func holeHasContent(n *sitter.Node, src []byte) bool {
	s, e, ok := innerSpan(n)
	return ok && len(bytes.TrimSpace(src[s:e])) > 0
}

// hasInterpolation reports whether a pattern literal directly contains
// interpolated code (`${…}` in a template string, `{…}` in an f-string).
func (c *matchCtx) hasInterpolation(p *sitter.Node) bool {
	for _, k := range c.pat.named(p) {
		if c.pat.lang.IsInterpolation(c.pat.kind(k)) {
			return true
		}
	}
	return false
}

// matchExact matches pattern nodes against target nodes requiring that every
// target node is consumed. Unlike matchPrefix it never leaves a tail, so a
// trailing variadic absorbs all remaining nodes. On a false return, b is
// unchanged.
func (c *matchCtx) matchExact(pats, tgts []*sitter.Node, b Bindings) bool {
	if len(pats) == 0 {
		return len(tgts) == 0
	}
	p := pats[0]
	pu := c.pat.unwrapForVariadic(p)
	if spec, ok := c.pat.metaOf(pu); ok && spec.kind == mvVariadic {
		for k := 0; k <= len(tgts); k++ {
			mark := c.mark()
			if !spec.anon {
				txt := spanText(tgts[:k], c.tsrc)
				if prev, exists := b[spec.name]; exists {
					if prev.Text != txt {
						continue
					}
				} else {
					c.bind(b, spec.name, Binding{Text: txt, Variadic: true, Count: k})
				}
			}
			if c.matchExact(pats[1:], tgts[k:], b) {
				return true
			}
			c.rollback(b, mark)
		}
		return false
	}
	if len(tgts) == 0 {
		return false
	}
	mark := c.mark()
	if c.matchNode(p, tgts[0], b) && c.matchExact(pats[1:], tgts[1:], b) {
		return true
	}
	c.rollback(b, mark)
	return false
}

// matchPrefix matches pattern nodes against a prefix of target nodes, returning
// how many target nodes were consumed. Variadic pattern nodes absorb a run of
// target nodes (with backtracking). On a false return, b is unchanged.
func (c *matchCtx) matchPrefix(pats, tgts []*sitter.Node, b Bindings) (int, bool) {
	if len(pats) == 0 {
		return 0, true
	}
	p := pats[0]
	// A variadic applies at this list level when, after unwrapping transparent
	// and parameter-wrapper nodes, it is a variadic leaf. A variadic sentinel
	// buried inside a semantic node (e.g. Go's expression_list) is instead
	// matched by recursing into that node.
	pu := c.pat.unwrapForVariadic(p)
	if spec, ok := c.pat.metaOf(pu); ok && spec.kind == mvVariadic {
		for k := 0; k <= len(tgts); k++ {
			mark := c.mark()
			if !spec.anon {
				txt := spanText(tgts[:k], c.tsrc)
				if prev, exists := b[spec.name]; exists {
					if prev.Text != txt {
						continue
					}
				} else {
					c.bind(b, spec.name, Binding{Text: txt, Variadic: true, Count: k})
				}
			}
			if rest, ok := c.matchPrefix(pats[1:], tgts[k:], b); ok {
				return k + rest, true
			}
			c.rollback(b, mark)
		}
		return 0, false
	}

	if len(tgts) == 0 {
		return 0, false
	}
	mark := c.mark()
	if c.matchNode(p, tgts[0], b) {
		if rest, ok := c.matchPrefix(pats[1:], tgts[1:], b); ok {
			return 1 + rest, true
		}
	}
	c.rollback(b, mark)
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
