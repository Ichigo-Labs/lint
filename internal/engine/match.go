package engine

import (
	"bytes"
	"strings"

	"github.com/ichigo-labs/lint/internal/lang"
)

// Binding is the value captured by a metavariable.
//
// Node is a flat index into the file's Index; -1 (noNode) means the capture
// has no single node (variadics, text holes). Note the zero value is index 0
// — the file root — so constructions must always set Node explicitly.
type Binding struct {
	Node     int32
	Text     string // captured source text
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
// pattern/target pair. Pattern nodes and target nodes are both flat indices —
// into ctx.pat's arrays and ctx.ix's arrays respectively.
type matchCtx struct {
	pat  *Pattern
	tsrc []byte
	l    *lang.Language
	ix   *Index

	undo []undoEntry

	// scratch is the candidate-attempt bindings map findSingle/findSeq
	// reuse: most candidates fail without binding anything, so allocating a
	// map per candidate would waste ~90% of the maps. Successful matches
	// clone it; the journal then rolls it back to empty.
	scratch Bindings
}

// scratchBindings returns the reusable per-find bindings map.
func (c *matchCtx) scratchBindings() Bindings {
	if c.scratch == nil {
		c.scratch = Bindings{}
	}
	return c.scratch
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

// tbytes returns a target node's source bytes (no copy).
func (c *matchCtx) tbytes(n int32) []byte {
	return c.tsrc[c.ix.f.StartByte[n]:c.ix.f.EndByte[n]]
}

// matchNode tests whether pattern node p matches target node t, recording any
// metavariable bindings into b through the undo journal. On a false return, b
// is unchanged (every failing branch rolls back its own writes).
func (c *matchCtx) matchNode(p, t int32, b Bindings) bool {
	// Single metavariable: binds any one node (with equality on reuse). The
	// target is bound through its transparent wrappers, so `$X` captures the
	// expression rather than the statement (and an attribute value rather
	// than its quoting wrapper).
	if spec, ok := c.pat.metaAt(p); ok {
		if spec.kind == mvVariadic {
			return true // a lone variadic in node position matches anything
		}
		if spec.anon {
			return true
		}
		tu := c.ix.unwrapTransparentT(t)
		txt := c.tbytes(tu)
		if prev, exists := b[spec.name]; exists {
			return prev.Text == string(txt)
		}
		c.bind(b, spec.name, Binding{Node: tu, Text: string(txt)})
		return true
	}

	p = c.pat.uwTrans[p]
	t = c.ix.unwrapTransparentT(t)

	// p may have become a metavariable after unwrapping a statement wrapper.
	if spec, ok := c.pat.metaAt(p); ok && spec.kind == mvSingle {
		if spec.anon {
			return true
		}
		txt := c.tbytes(t)
		if prev, exists := b[spec.name]; exists {
			return prev.Text == string(txt)
		}
		c.bind(b, spec.name, Binding{Node: t, Text: string(txt)})
		return true
	}

	pSym := c.pat.csym[p]
	if pSym != c.ix.csym[t] {
		return false
	}

	// A metavariable in a node's text hole (`attr="$X"` in XML): the content
	// is not a child node, so bind the target's hole text.
	if spec, ok := c.pat.innerAt(p); ok {
		ts, te, tok := c.ix.innerSpan(t)
		if !tok {
			return false
		}
		if spec.anon {
			return true
		}
		txt := c.tsrc[ts:te]
		if prev, exists := b[spec.name]; exists {
			return prev.Text == string(txt)
		}
		c.bind(b, spec.name, Binding{Node: noNode, Text: string(txt)})
		return true
	}

	// Literal nodes compare by their full source text: many grammars do not
	// expose a literal's content as a child node (a Go string holds only its
	// quote tokens, a CSS color only its '#'), so comparing children would
	// ignore the content entirely. Interpolated literals (template strings,
	// f-strings) keep structural matching so the code inside `${…}` compares
	// like code, not like text.
	if is(c.pat.syms.literal, pSym) && !c.pat.interp[p] {
		return bytes.Equal(c.pat.textBytes(p), c.tbytes(t))
	}

	pk := c.pat.rel(p)
	tk := c.ix.rel(t)
	if len(pk) == 0 && len(tk) == 0 {
		// Leaf tokens of the same kind: identifiers, literals, operators.
		pb, tb := c.pat.textBytes(p), c.tbytes(t)
		if is(c.pat.syms.foldCase, pSym) {
			return strings.EqualFold(string(pb), string(tb))
		}
		return bytes.Equal(pb, tb)
	}
	// Nodes with no named structure on either side (only delimiter tokens)
	// compare textually when either side has real content between the tokens
	// (an XML attribute value, say). When both holes are empty or whitespace
	// (`()` vs `( )`), the delimiters compare structurally so that
	// whitespace and comments inside stay irrelevant, as everywhere else.
	if !c.pat.namedRel[p] && !c.hasNamedT(tk) {
		if c.pat.holeContent[p] || c.holeHasContentT(t) {
			return bytes.Equal(c.pat.textBytes(p), c.tbytes(t))
		}
		return c.matchExact(pk, tk, b)
	}
	return c.matchExact(pk, tk, b)
}

// hasNamedT reports whether any target node in the list is a named node.
func (c *matchCtx) hasNamedT(nodes []int32) bool {
	for _, n := range nodes {
		if c.ix.f.IsNamed(int(n)) {
			return true
		}
	}
	return false
}

// holeHasContentT reports whether a target node's inner span (the text
// between its delimiter tokens) contains non-whitespace bytes.
func (c *matchCtx) holeHasContentT(n int32) bool {
	s, e, ok := c.ix.innerSpan(n)
	return ok && len(bytes.TrimSpace(c.tsrc[s:e])) > 0
}

// matchExact matches pattern nodes against target nodes requiring that every
// target node is consumed. Unlike matchPrefix it never leaves a tail, so a
// trailing variadic absorbs all remaining nodes. On a false return, b is
// unchanged.
func (c *matchCtx) matchExact(pats, tgts []int32, b Bindings) bool {
	if len(pats) == 0 {
		return len(tgts) == 0
	}
	p := pats[0]
	pu := c.pat.uwVar[p]
	if spec, ok := c.pat.metaAt(pu); ok && spec.kind == mvVariadic {
		for k := 0; k <= len(tgts); k++ {
			mark := c.mark()
			if !spec.anon {
				txt := c.spanBytes(tgts[:k])
				if prev, exists := b[spec.name]; exists {
					if prev.Text != string(txt) {
						continue
					}
				} else {
					c.bind(b, spec.name, Binding{Node: noNode, Text: string(txt), Variadic: true, Count: k})
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
func (c *matchCtx) matchPrefix(pats, tgts []int32, b Bindings) (int, bool) {
	if len(pats) == 0 {
		return 0, true
	}
	p := pats[0]
	// A variadic applies at this list level when, after unwrapping transparent
	// and parameter-wrapper nodes, it is a variadic leaf. A variadic sentinel
	// buried inside a semantic node (e.g. Go's expression_list) is instead
	// matched by recursing into that node.
	pu := c.pat.uwVar[p]
	if spec, ok := c.pat.metaAt(pu); ok && spec.kind == mvVariadic {
		for k := 0; k <= len(tgts); k++ {
			mark := c.mark()
			if !spec.anon {
				txt := c.spanBytes(tgts[:k])
				if prev, exists := b[spec.name]; exists {
					if prev.Text != string(txt) {
						continue
					}
				} else {
					c.bind(b, spec.name, Binding{Node: noNode, Text: string(txt), Variadic: true, Count: k})
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

// spanBytes returns the source bytes spanning a run of sibling target nodes.
func (c *matchCtx) spanBytes(nodes []int32) []byte {
	if len(nodes) == 0 {
		return nil
	}
	start := c.ix.f.StartByte[nodes[0]]
	end := c.ix.f.EndByte[nodes[len(nodes)-1]]
	return c.tsrc[start:end]
}
