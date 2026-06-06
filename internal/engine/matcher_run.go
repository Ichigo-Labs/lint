package engine

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// Index is a per-file, pre-order node index shared across all rules checking
// one parsed tree. Building it once (one walk) lets every single-pattern rule
// scan only the nodes of its root kind instead of re-walking the whole tree.
type Index struct {
	byType map[string][]*sitter.Node // node kind -> nodes, in pre-order
	all    []*sitter.Node            // every node, in pre-order
}

// BuildIndex walks a parsed tree once and records its nodes by kind. The
// pre-order traversal exactly matches walk(), so iterating the index is
// equivalent to (but cheaper than) re-walking and filtering per rule.
func BuildIndex(tree *sitter.Tree) *Index {
	idx := &Index{byType: map[string][]*sitter.Node{}}
	walk(tree.RootNode(), func(n *sitter.Node) {
		idx.byType[n.Type()] = append(idx.byType[n.Type()], n)
		idx.all = append(idx.all, n)
	})
	return idx
}

// find produces all candidate matches for a matcher across a tree. Only
// "positive" matchers (pattern/query/any/all) can generate candidates.
func (cm *compiledMatcher) find(ctx *matchCtx, root *sitter.Node) []Match {
	return cm.filterWhere(ctx, cm.findRaw(ctx, root))
}

func (cm *compiledMatcher) findRaw(ctx *matchCtx, root *sitter.Node) []Match {
	switch cm.kind {
	case dsl.MatchPattern:
		if cm.pattern.isSeq {
			return cm.findSeq(ctx, root)
		}
		return cm.findSingle(ctx, root)
	case dsl.MatchQuery:
		return runQuery(ctx, cm.query, root)
	case dsl.MatchAny:
		var out []Match
		for _, ch := range cm.children {
			out = append(out, ch.find(ctx, root)...)
		}
		return out
	case dsl.MatchAll:
		gen := cm.firstPositive()
		if gen == nil {
			return nil
		}
		var out []Match
		for _, m := range gen.find(ctx, root) {
			b := m.Bindings
			ok := true
			for _, ch := range cm.children {
				if ch == gen {
					continue
				}
				nb, matched := ch.matchesNode(ctx, m.Node, b)
				if !matched {
					ok = false
					break
				}
				b.merge(nb)
			}
			if ok {
				m.Bindings = b
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// filterWhere drops matches that fail this matcher's branch-scoped `where`
// predicates (those declared directly inside an `any { ... }` / `all { ... }`).
func (cm *compiledMatcher) filterWhere(ctx *matchCtx, matches []Match) []Match {
	if len(cm.where) == 0 {
		return matches
	}
	out := make([]Match, 0, len(matches))
	for _, m := range matches {
		if cm.passesWhereMatch(ctx, m) {
			out = append(out, m)
		}
	}
	return out
}

func (cm *compiledMatcher) passesWhereMatch(ctx *matchCtx, m Match) bool {
	b := m.Bindings
	if b == nil {
		b = Bindings{}
	}
	bm := b.clone()
	bindMatch(bm, m, ctx.tsrc)
	return cm.passesWhereBindings(ctx, bm)
}

func (cm *compiledMatcher) passesWhereBindings(ctx *matchCtx, b Bindings) bool {
	for _, c := range cm.where {
		if !c.eval(ctx, b) {
			return false
		}
	}
	return true
}

// passesWhereAt checks branch-scoped `where` predicates for a matcher accepted
// at a specific node, exposing $match for that node.
func (cm *compiledMatcher) passesWhereAt(ctx *matchCtx, n *sitter.Node, b Bindings) bool {
	if len(cm.where) == 0 {
		return true
	}
	bm := b.clone()
	bindMatch(bm, mkMatch(n, b), ctx.tsrc)
	return cm.passesWhereBindings(ctx, bm)
}

func (cm *compiledMatcher) firstPositive() *compiledMatcher {
	for _, ch := range cm.children {
		if canGenerate(ch) {
			return ch
		}
	}
	return nil
}

func (cm *compiledMatcher) findSingle(ctx *matchCtx, root *sitter.Node) []Match {
	ctx.pat = cm.pattern
	want := cm.pattern.root.Type()
	_, rootIsMeta := cm.pattern.metaOf(cm.pattern.root)
	var out []Match
	visit := func(n *sitter.Node) {
		if !rootIsMeta && n.Type() != want {
			return
		}
		b := Bindings{}
		if ctx.matchNode(cm.pattern.root, n, b) {
			out = append(out, mkMatch(n, b))
		}
	}
	// A single-kind pattern (the common case) scans only its bucket; otherwise
	// fall back to a full walk (identical pre-order, so identical results).
	if ctx.index != nil && !rootIsMeta {
		for _, n := range ctx.index.byType[want] {
			visit(n)
		}
	} else {
		walk(root, visit)
	}
	return out
}

func (cm *compiledMatcher) findSeq(ctx *matchCtx, root *sitter.Node) []Match {
	ctx.pat = cm.pattern
	var out []Match
	visit := func(n *sitter.Node) {
		if n.NamedChildCount() == 0 {
			return
		}
		kids := namedChildren(n, ctx.l)
		for s := 0; s < len(kids); s++ {
			b := Bindings{}
			consumed, ok := ctx.matchPrefix(cm.pattern.seq, kids[s:], b)
			if ok && consumed > 0 {
				first := kids[s]
				last := kids[s+consumed-1]
				out = append(out, Match{
					Node:       n,
					StartByte:  first.StartByte(),
					EndByte:    last.EndByte(),
					StartPoint: first.StartPoint(),
					EndPoint:   last.EndPoint(),
					Bindings:   b,
				})
			}
		}
	}
	if ctx.index != nil {
		for _, n := range ctx.index.all {
			visit(n)
		}
	} else {
		walk(root, visit)
	}
	return out
}

// matchesNode reports whether the matcher accepts a specific node, seeded with
// existing bindings (for cross-pattern back-references).
func (cm *compiledMatcher) matchesNode(ctx *matchCtx, n *sitter.Node, seed Bindings) (Bindings, bool) {
	switch cm.kind {
	case dsl.MatchPattern:
		ctx.pat = cm.pattern
		if cm.pattern.isSeq {
			kids := namedChildren(n, ctx.l)
			for s := 0; s < len(kids); s++ {
				b := seed.clone()
				if consumed, ok := ctx.matchPrefix(cm.pattern.seq, kids[s:], b); ok && consumed > 0 {
					return b, true
				}
			}
			return nil, false
		}
		b := seed.clone()
		if ctx.matchNode(cm.pattern.root, n, b) {
			return b, true
		}
		return nil, false
	case dsl.MatchQuery:
		// The query "matches at" n when its matched construct (the span over
		// all captures) begins at n and is contained in n. This lets a query
		// like (call_expression function:(identifier)@match) anchor on the call
		// node even though @match captures the inner identifier.
		for _, m := range runQuery(ctx, cm.query, n) {
			if m.hasCover && m.coverStart == n.StartByte() && m.coverEnd <= n.EndByte() {
				b := seed.clone()
				b.merge(m.Bindings)
				return b, true
			}
		}
		return nil, false
	case dsl.MatchAll:
		b := seed.clone()
		for _, ch := range cm.children {
			nb, ok := ch.matchesNode(ctx, n, b)
			if !ok {
				return nil, false
			}
			b.merge(nb)
		}
		if !cm.passesWhereAt(ctx, n, b) {
			return nil, false
		}
		return b, true
	case dsl.MatchAny:
		for _, ch := range cm.children {
			if nb, ok := ch.matchesNode(ctx, n, seed); ok {
				if !cm.passesWhereAt(ctx, n, nb) {
					continue
				}
				return nb, true
			}
		}
		return nil, false
	case dsl.MatchNot:
		if _, ok := cm.sub.matchesNode(ctx, n, seed); ok {
			return nil, false
		}
		return seed.clone(), true
	}
	return nil, false
}

func mkMatch(n *sitter.Node, b Bindings) Match {
	return Match{
		Node:       n,
		StartByte:  n.StartByte(),
		EndByte:    n.EndByte(),
		StartPoint: n.StartPoint(),
		EndPoint:   n.EndPoint(),
		Bindings:   b,
	}
}

// runQuery executes a raw Tree-sitter query over a node's subtree.
func runQuery(ctx *matchCtx, q *sitter.Query, node *sitter.Node) []Match {
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(q, node)
	var out []Match
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, ctx.tsrc)
		if len(m.Captures) == 0 {
			continue
		}
		anchor, binds, cs, ce := queryAnchor(ctx, q, m)
		if anchor == nil {
			continue
		}
		mm := mkMatch(anchor, binds)
		mm.coverStart, mm.coverEnd, mm.hasCover = cs, ce, true
		out = append(out, mm)
	}
	return out
}

// queryAnchor picks the reported node for a query match (a capture named
// "match"/"target" if present, otherwise the widest capture), collects all
// captures as bindings, and returns the cover span over every captured node.
func queryAnchor(ctx *matchCtx, q *sitter.Query, m *sitter.QueryMatch) (*sitter.Node, Bindings, uint32, uint32) {
	binds := Bindings{}
	var anchor *sitter.Node
	var widest *sitter.Node
	var widestSpan uint32
	var coverStart, coverEnd uint32
	first := true
	for _, cap := range m.Captures {
		name := q.CaptureNameForId(cap.Index)
		binds[name] = Binding{Node: cap.Node, Text: cap.Node.Content(ctx.tsrc)}
		if name == "match" || name == "target" {
			anchor = cap.Node
		}
		span := cap.Node.EndByte() - cap.Node.StartByte()
		if widest == nil || span > widestSpan {
			widest, widestSpan = cap.Node, span
		}
		if first || cap.Node.StartByte() < coverStart {
			coverStart = cap.Node.StartByte()
		}
		if first || cap.Node.EndByte() > coverEnd {
			coverEnd = cap.Node.EndByte()
		}
		first = false
	}
	if anchor == nil {
		anchor = widest
	}
	return anchor, binds, coverStart, coverEnd
}
