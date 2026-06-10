package engine

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// find produces all candidate matches for a matcher across a tree. Only
// "positive" matchers (pattern/query/any/all) can generate candidates.
// root is a flat node index (the file root, 0, from RunIndexed).
func (cm *compiledMatcher) find(ctx *matchCtx, root int32) []Match {
	return cm.filterWhere(ctx, cm.findRaw(ctx, root))
}

func (cm *compiledMatcher) findRaw(ctx *matchCtx, root int32) []Match {
	switch cm.kind {
	case dsl.MatchPattern:
		if cm.pattern.isSeq {
			return cm.findSeq(ctx)
		}
		return cm.findSingle(ctx)
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
	bindMatch(ctx.ix, bm, m, ctx.tsrc)
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
func (cm *compiledMatcher) passesWhereAt(ctx *matchCtx, n int32, b Bindings) bool {
	if len(cm.where) == 0 {
		return true
	}
	bm := b.clone()
	bindMatch(ctx.ix, bm, mkMatch(ctx.ix, n, b), ctx.tsrc)
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

func (cm *compiledMatcher) findSingle(ctx *matchCtx) []Match {
	ctx.pat = cm.pattern
	want := cm.pattern.csym[cm.pattern.root]
	rootIsMeta := cm.pattern.hasMeta[cm.pattern.root]
	var out []Match
	b := ctx.scratchBindings()
	visit := func(n int32) {
		if !rootIsMeta && ctx.ix.csym[n] != want {
			return
		}
		// Each candidate starts a fresh top-level match attempt against the
		// shared scratch map; the journal rolls every attempt (matched or
		// not) back to empty, and successful matches keep a clone.
		ctx.undo = ctx.undo[:0]
		if ctx.matchNode(cm.pattern.root, n, b) {
			out = append(out, mkMatch(ctx.ix, n, b.clone()))
		}
		ctx.rollback(b, 0)
	}
	// A single-kind pattern (the common case) scans only its bucket; a
	// metavariable root (or the test-only linearScan toggle) scans every
	// node, in the same pre-order.
	if !rootIsMeta && !ctx.ix.linearScan {
		if int(want) < len(ctx.ix.byType) {
			for _, n := range ctx.ix.byType[want] {
				visit(n)
			}
		}
	} else {
		for i, count := int32(0), int32(ctx.ix.f.Len()); i < count; i++ {
			visit(i)
		}
	}
	return out
}

func (cm *compiledMatcher) findSeq(ctx *matchCtx) []Match {
	ctx.pat = cm.pattern
	ix := ctx.ix
	var out []Match
	b := ctx.scratchBindings()
	visit := func(n int32) {
		kids := ix.named(n)
		for s := 0; s < len(kids); s++ {
			ctx.undo = ctx.undo[:0] // fresh top-level attempt; see findSingle
			consumed, ok := ctx.matchPrefix(cm.pattern.seq, kids[s:], b)
			if ok && consumed > 0 {
				first := kids[s]
				last := kids[s+consumed-1]
				out = append(out, Match{
					Node:       n,
					StartByte:  ix.f.StartByte[first],
					EndByte:    ix.f.EndByte[last],
					StartPoint: ix.startPoint(first),
					EndPoint:   ix.endPoint(last),
					Bindings:   b.clone(),
				})
			}
			ctx.rollback(b, 0)
		}
	}
	// Only nodes that parent an instance of the first element's kind can
	// host the sequence, so scan those instead of every node. When the
	// first element is (or unwraps from) a transparent wrapper, the
	// matching target child may carry the wrapper or not, so both the
	// parent and the transparent grandparent are candidates.
	first := cm.pattern.uwVar[cm.pattern.seq[0]]
	if !cm.pattern.hasMeta[first] && !ix.linearScan {
		core := cm.pattern.uwTrans[cm.pattern.seq[0]]
		want := cm.pattern.csym[core]
		seenParents := map[int32]bool{}
		addHost := func(h int32) {
			if seenParents[h] {
				return
			}
			seenParents[h] = true
			visit(h)
		}
		if int(want) < len(ix.byType) {
			for _, n := range ix.byType[want] {
				// Ascend through every transparent ancestor: the sequence host
				// is the first non-transparent one, and wrappers can stack
				// (C# wraps top-level statements as
				// global_statement(expression_statement(…))).
				for p := ix.f.Parent[n]; p != -1; p = ix.f.Parent[p] {
					addHost(p)
					if !is(ix.st.transparent, ix.csym[p]) {
						break
					}
				}
			}
		}
		return out
	}
	for i, count := int32(0), int32(ix.f.Len()); i < count; i++ {
		visit(i)
	}
	return out
}

// matchesNode reports whether the matcher accepts a specific node, seeded with
// existing bindings (for cross-pattern back-references).
func (cm *compiledMatcher) matchesNode(ctx *matchCtx, n int32, seed Bindings) (Bindings, bool) {
	switch cm.kind {
	case dsl.MatchPattern:
		ctx.pat = cm.pattern
		if cm.pattern.isSeq {
			kids := ctx.ix.named(n)
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
		// An open-tag pattern compiles to the start tag, but relation targets
		// (ancestors, parents) are elements; accept an element whose start
		// tag matches, so `inside { <form $$$> }` behaves as written.
		if is(cm.pattern.syms.startTag, cm.pattern.csym[cm.pattern.root]) && is(ctx.ix.st.element, ctx.ix.csym[n]) {
			if kids := ctx.ix.rel(n); len(kids) > 0 {
				b2 := seed.clone()
				if ctx.matchNode(cm.pattern.root, kids[0], b2) {
					return b2, true
				}
			}
		}
		return nil, false
	case dsl.MatchQuery:
		// The query "matches at" n when its matched construct (the span over
		// all captures) begins at n and is contained in n. This lets a query
		// like (call_expression function:(identifier)@match) anchor on the call
		// node even though @match captures the inner identifier.
		for _, m := range runQuery(ctx, cm.query, n) {
			if m.hasCover && m.coverStart == ctx.ix.f.StartByte[n] && m.coverEnd <= ctx.ix.f.EndByte[n] {
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

func mkMatch(ix *Index, n int32, b Bindings) Match {
	return Match{
		Node:       n,
		StartByte:  ix.f.StartByte[n],
		EndByte:    ix.f.EndByte[n],
		StartPoint: ix.startPoint(n),
		EndPoint:   ix.endPoint(n),
		Bindings:   b,
	}
}

// runQuery executes a raw Tree-sitter query over a node's subtree. Queries
// are the one place matching still needs live smacker nodes: the scoped node
// is rebuilt by child-ordinal descent (cgo, but confined to query rules) and
// captures are translated back to flat indices by ts_node id.
func runQuery(ctx *matchCtx, q *sitter.Query, n int32) []Match {
	var node *sitter.Node
	if n == 0 {
		node = ctx.ix.tree.RootNode()
	} else {
		node = ctx.ix.sitterNode(n)
	}
	if node == nil {
		return nil
	}
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
		if anchor == noNode {
			continue
		}
		mm := mkMatch(ctx.ix, anchor, binds)
		mm.coverStart, mm.coverEnd, mm.hasCover = cs, ce, true
		out = append(out, mm)
	}
	return out
}

// queryAnchor picks the reported node for a query match (a capture named
// "match"/"target" if present, otherwise the widest capture), collects all
// captures as bindings, and returns the cover span over every captured node.
func queryAnchor(ctx *matchCtx, q *sitter.Query, m *sitter.QueryMatch) (int32, Bindings, uint32, uint32) {
	binds := Bindings{}
	anchor := noNode
	widest := noNode
	var widestSpan uint32
	var coverStart, coverEnd uint32
	first := true
	for _, cap := range m.Captures {
		ci := ctx.ix.indexOfID(cap.Node.ID())
		var sb, eb uint32
		if ci != noNode {
			sb, eb = ctx.ix.f.StartByte[ci], ctx.ix.f.EndByte[ci]
		} else {
			// Defensive: every tree node was extracted, so this should not
			// happen; fall back to the live node's span.
			sb, eb = cap.Node.StartByte(), cap.Node.EndByte()
		}
		name := q.CaptureNameForId(cap.Index)
		binds[name] = Binding{Node: ci, Text: string(ctx.tsrc[sb:eb])}
		if (name == "match" || name == "target") && ci != noNode {
			anchor = ci
		}
		span := eb - sb
		if widest == noNode || span > widestSpan {
			if ci != noNode {
				widest, widestSpan = ci, span
			}
		}
		if first || sb < coverStart {
			coverStart = sb
		}
		if first || eb > coverEnd {
			coverEnd = eb
		}
		first = false
	}
	if anchor == noNode {
		anchor = widest
	}
	return anchor, binds, coverStart, coverEnd
}
