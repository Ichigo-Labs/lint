package engine

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
	"github.com/ichigo-labs/lint/internal/treesit"
)

type metaKind int

const (
	mvSingle metaKind = iota
	mvVariadic
	mvOperator // a binary/comparison operator hole ($OP); binds the operator token
)

// metaSpec describes a metavariable discovered in a pattern.
type metaSpec struct {
	name string // capture name (empty for anonymous)
	kind metaKind
	anon bool
	// count is how many times rewrite emitted this sentinel (a named
	// metavariable can be reused); every occurrence must survive parsing.
	count int
}

// Pattern is a compiled structural pattern for one language. After compile
// the matcher works exclusively on the flat form (f and the dense per-node
// tables below); the smacker nodes are kept only for DebugPattern.
type Pattern struct {
	lang    *lang.Language
	src     []byte              // rewritten pattern source
	metas   map[string]metaSpec // sentinel identifier -> spec
	opMetas map[uint32]metaSpec // operator-leaf StartByte -> spec ($OP holes)
	tree    *sitter.Tree        // backs sroot/sseq (debug only)

	syms *langSyms
	f    *treesit.Flat
	csym []uint16 // canonical symbol per flat node

	root  int32   // flat index of the single-node pattern root (or seq host)
	isSeq bool    // sequence pattern (match a run of sibling statements)
	seq   []int32 // flat indices of the sequence elements

	sroot *sitter.Node // smacker twins of root/seq, for DebugPattern only
	sseq  []*sitter.Node

	// anchor is the longest concrete token a matching file must contain
	// byte-for-byte; "" when no such token exists. Used to skip files (and
	// their parses) that cannot match.
	anchor string

	// Dense per-node tables (flat index -> value) precomputed once at
	// compile time, so the hot matching path is pure array access: no maps,
	// no cgo, no string hashing.
	meta        []metaSpec
	hasMeta     []bool
	inner       []metaSpec // metavariable in a node's text hole
	hasInner    []bool
	uwTrans     []int32 // transparent-unwrap fixpoint per node
	uwVar       []int32 // unwrapForVariadic fixpoint per node
	holeContent []bool  // innerSpan exists and holds non-whitespace bytes
	interp      []bool  // node directly contains an interpolation child
	namedRel    []bool  // any relevant child is named

	relStart, relList     []int32 // relevant-children CSR
	namedStart, namedList []int32 // named-children CSR
}

// precompute extracts the pattern tree to its flat form and fills the dense
// per-node tables, once the pattern's shape (sroot/sseq) is final.
func (p *Pattern) precompute() {
	p.syms = symsFor(p.lang)
	p.f = treesit.ExtractTree(p.tree)
	f, st := p.f, p.syms
	n := f.Len()

	p.csym = make([]uint16, n)
	for i := 0; i < n; i++ {
		p.csym[i] = treesit.Canonical(st.canon, f.Symbol[i])
	}
	p.relStart, p.relList, p.namedStart, p.namedList = buildChildCSR(f, st, p.csym)

	// Resolve the smacker root/seq nodes into flat indices by ts_node id.
	byID := make(map[uintptr]int32, n)
	for i, id := range f.NodeID {
		byID[id] = int32(i)
	}
	if p.sroot != nil {
		p.root = byID[p.sroot.ID()]
	}
	for _, sn := range p.sseq {
		p.seq = append(p.seq, byID[sn.ID()])
	}

	p.meta = make([]metaSpec, n)
	p.hasMeta = make([]bool, n)
	p.inner = make([]metaSpec, n)
	p.hasInner = make([]bool, n)
	p.holeContent = make([]bool, n)
	p.interp = make([]bool, n)
	p.namedRel = make([]bool, n)
	for i := 0; i < n; i++ {
		if len(f.Children(i)) == 0 {
			if s, ok := p.metas[string(p.src[f.StartByte[i]:f.EndByte[i]])]; ok {
				p.meta[i], p.hasMeta[i] = s, true
			} else if s, ok := p.opMetas[f.StartByte[i]]; ok {
				p.meta[i], p.hasMeta[i] = s, true
			}
		} else if s, e, ok := flatInnerSpan(f, int32(i)); ok {
			if s != e {
				if spec, found := p.metas[string(p.src[s:e])]; found {
					p.inner[i], p.hasInner[i] = spec, true
				}
			}
			p.holeContent[i] = len(bytes.TrimSpace(p.src[s:e])) > 0
		}
		for _, k := range p.named(int32(i)) {
			if is(st.interp, p.csym[k]) {
				p.interp[i] = true
				break
			}
		}
		for _, k := range p.rel(int32(i)) {
			if f.IsNamed(int(k)) {
				p.namedRel[i] = true
				break
			}
		}
	}

	// Unwrap fixpoints: both unwraps descend into a node's single named
	// child, which always carries a larger pre-order index, so a reverse
	// pass sees every child's fixpoint before its parent needs it.
	p.uwTrans = make([]int32, n)
	p.uwVar = make([]int32, n)
	attrSym, attrOK := st.canonForKind("Attribute")
	for i := n - 1; i >= 0; i-- {
		ni := int32(i)
		p.uwTrans[i] = ni
		if is(st.transparent, p.csym[i]) {
			if kids := p.named(ni); len(kids) == 1 {
				p.uwTrans[i] = p.uwTrans[kids[0]]
			}
		}
		p.uwVar[i] = ni
		if is(st.transparent, p.csym[i]) || is(st.paramWrapper, p.csym[i]) {
			kids := p.named(ni)
			switch {
			case len(kids) == 1:
				p.uwVar[i] = p.uwVar[kids[0]]
			case len(kids) > 1 && attrOK && p.csym[i] == attrSym:
				// XML's grammar cannot parse a bare attribute name, so a
				// variadic in attribute position is rewritten to `$$$X=""`
				// and parses as Attribute(Name, AttValue); the Name child
				// carries the sentinel.
				p.uwVar[i] = kids[0]
			}
		}
	}
}

// opInsertion records where a $OP operator placeholder was emitted in the
// rewritten source so the parsed operator leaf can be tagged after parsing.
type opInsertion struct {
	offset uint32 // byte offset of the placeholder within the rewritten string
	name   string // metavariable name (OP, OP1, ...)
}

// isOperatorName reports whether a metavariable name denotes an operator hole:
// exactly OP, or OP followed by digits (OP1, OP2, ...). These names are reserved
// for operator metavariables; use a different name to capture a value node.
func isOperatorName(name string) bool {
	if !strings.HasPrefix(name, "OP") {
		return false
	}
	for _, c := range name[2:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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
//	$OP     -> ==               (operator hole; the leaf is tagged after parsing)
//
// Per-language behaviour (from the lang registry):
//   - Without MetaInStrings, '$' inside the language's string/char literals
//     is copied verbatim so e.g. print("$msg") matches a literal string.
//     With it (markup languages), $X is a metavariable even between quotes,
//     because attribute values must be quoted (`<a href="$URL">`).
//   - With MetaUppercaseOnly (SCSS), $name with a lowercase initial is the
//     language's own variable syntax and stays literal; only $NAME binds.
//   - With MetaVariadicTagAttrs (XML), a variadic between < and > is emitted
//     as `SENT=""` because the grammar cannot parse a bare attribute name.
//   - With MetaQuoteBare (JSON), a metavariable outside quotes is emitted as
//     `"SENT"`, because the grammar has no bare-token form.
func rewrite(pattern string, l *lang.Language) (string, map[string]metaSpec, []opInsertion) {
	var b strings.Builder
	metas := map[string]metaSpec{}
	var ops []opInsertion
	anon := 0
	sep := l.SentinelSep()
	inTag := false // between < and >, for MetaVariadicTagAttrs
	var quote byte // open quote inside a tag, for MetaVariadicTagAttrs
	inStr := false // inside a double-quoted string, for MetaQuoteBare
	i, n := 0, len(pattern)
	for i < n {
		c := pattern[i]
		if !l.MetaInStrings && (c == '"' || c == '\'' || c == '`') {
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
			if l.MetaVariadicTagAttrs {
				switch {
				case quote != 0:
					if c == quote {
						quote = 0
					}
				case inTag && (c == '"' || c == '\''):
					quote = c
				case c == '<':
					inTag = true
				case c == '>':
					inTag = false
				}
			}
			if l.MetaQuoteBare {
				if c == '\\' && inStr && i+1 < n {
					b.WriteByte(c)
					b.WriteByte(pattern[i+1])
					i += 2
					continue
				}
				if c == '"' {
					inStr = !inStr
				}
			}
			b.WriteByte(c)
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
		// The language's own $vars (SCSS) stay literal: only uppercase-initial
		// names are metavariables there. $_ and $$$ wildcards always apply.
		if l.MetaUppercaseOnly && name != "" && name != "_" && !(name[0] >= 'A' && name[0] <= 'Z') {
			b.WriteString(strings.Repeat("$", dollars))
			b.WriteString(name)
			continue
		}
		// addMeta records one emission of a sentinel, counting reuses.
		addMeta := func(sent string, spec metaSpec) {
			spec.count = metas[sent].count + 1
			metas[sent] = spec
		}
		// writeSent emits a sentinel, quoted when the grammar has no bare
		// token form and we are not already inside quotes (JSON).
		writeSent := func(sent string) {
			if l.MetaQuoteBare && !inStr {
				b.WriteByte('"')
				b.WriteString(sent)
				b.WriteByte('"')
				return
			}
			b.WriteString(sent)
		}
		switch {
		case dollars >= 2: // variadic ($$ or $$$)
			var sent string
			if name == "" || name == "_" {
				anon++
				sent = fmt.Sprintf("Lntlvanon%s%d", sep, anon)
				addMeta(sent, metaSpec{kind: mvVariadic, anon: true})
			} else {
				sent = "Lntlvr" + sep + name
				addMeta(sent, metaSpec{name: name, kind: mvVariadic})
			}
			writeSent(sent)
			if l.MetaVariadicTagAttrs && inTag && quote == 0 {
				b.WriteString(`=""`)
			}
		case name == "": // lone '$' with no name: treat as literal text
			b.WriteByte('$')
		case name == "_": // anonymous single wildcard
			anon++
			sent := fmt.Sprintf("Lntlanon%s%d", sep, anon)
			addMeta(sent, metaSpec{kind: mvSingle, anon: true})
			writeSent(sent)
		case isOperatorName(name): // operator hole: $OP, $OP1, ...
			ops = append(ops, opInsertion{offset: uint32(b.Len()), name: name})
			b.WriteString("==") // binary placeholder; bound to the real operator at match time
		default: // named single
			sent := "Lntlmv" + sep + name
			addMeta(sent, metaSpec{name: name, kind: mvSingle})
			writeSent(sent)
		}
	}
	return b.String(), metas, ops
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
	"kotlin": {
		{"fun _() {\n", "\n}"},
		{"val _ = ", ""},
	},
	"swift": {
		{"func _() {\n", "\n}"},
		{"let _ = ", ""},
	},
	"typescript": {
		{"function _() {\n", "\n}"},
		{"let _ = ", ""},
	},
	"tsx": {
		{"function _() {\n", "\n}"},
		{"let _ = ", ""},
	},
	// Markup/style fragments. The css/scss block context compiles nested
	// fragments (`&:hover { … }` works raw under scss but not css). The
	// html/xml tag contexts compile attribute fragments (`alt=$_`), which
	// otherwise parse as flat text (html) or not at all (xml); the xml
	// element context compiles multi-element sequences, which need a single
	// document root to parse.
	"css": {
		{"a {\n", "\n}"},
	},
	"scss": {
		{"a {\n", "\n}"},
	},
	"html": {
		{"<lntl-x ", ">"},
	},
	"xml": {
		{"<lntl-x ", "/>"},
		{"<lntl-x>", "</lntl-x>"},
	},
	// A bare pair (`"key": $V`) is not a JSON document; wrap it in an object.
	// YAML gets no scaffolds: it is indentation-sensitive, and fragments
	// already parse at the top level.
	"json": {
		{"{", "}"},
	},
}

// compilePattern parses a raw pattern under a language grammar and resolves its
// structural shape (single node vs. statement sequence). It tries the raw
// pattern first, then wraps it in scaffolding contexts until one parses.
func compilePattern(l *lang.Language, raw string) (*Pattern, error) {
	rewritten, metas, ops := rewrite(raw, l)
	parser := sitter.NewParser()
	parser.SetLanguage(l.Grammar())

	attempts := append([]scaffold{{"", ""}}, scaffolds[l.Name]...)
	var swallowed []string
	for _, sc := range attempts {
		full := sc.prefix + rewritten + sc.suffix
		tree, err := parser.ParseCtx(context.Background(), nil, []byte(full))
		if err != nil {
			continue
		}
		root := tree.RootNode()
		if hasParseError(root, l) {
			continue
		}
		bs := uint32(len(sc.prefix))
		be := uint32(len(sc.prefix) + len(rewritten))
		nodes := topNodesInRange(root, bs, be, l)
		if len(nodes) == 0 {
			continue
		}
		// Every metavariable must survive parsing as a recognisable node (an
		// isolated leaf or a delimiter-wrapped text hole). Grammars that lump
		// free text into one token (HTML text nodes) can swallow a sentinel
		// into a larger leaf, making the metavariable unmatchable; treat that
		// parse as failed and try the next scaffold.
		if miss := missingSentinels(nodes, metas, []byte(full)); len(miss) > 0 {
			swallowed = miss
			continue
		}
		// A text hole binds exactly one span, so a variadic there has no
		// meaning; reject it outright rather than matching it like $X.
		if bad := variadicHole(nodes, metas, []byte(full)); bad != "" {
			return nil, fmt.Errorf("variadic metavariable %s cannot appear inside a quoted value in %s pattern %q; use a single metavariable like $X", bad, l.Name, raw)
		}
		p := &Pattern{lang: l, src: []byte(full), metas: metas, tree: tree}
		if len(ops) > 0 {
			p.opMetas = map[uint32]metaSpec{}
			for _, ins := range ops {
				if leaf := leafAt(root, bs+ins.offset); leaf != nil {
					p.opMetas[leaf.StartByte()] = metaSpec{name: ins.name, kind: mvOperator}
				} else {
					return nil, fmt.Errorf("operator metavariable $%s is not in operator position in pattern %q", ins.name, raw)
				}
			}
		}
		if len(nodes) == 1 {
			// Iterate to a fixpoint: wrapper and transparent kinds can
			// alternate (YAML: document → block_node → block_mapping →
			// pair), and a single round would stop midway.
			core := nodes[0]
			for {
				prev := core
				core = unwrapTransparent(unwrap(core), l)
				if core == prev {
					break
				}
			}
			if wrapperKinds[core.Type()] && core.NamedChildCount() > 1 {
				p.isSeq = true
				p.sroot = core
				p.sseq = namedChildren(core, l)
			} else {
				p.sroot = core
			}
			// A pattern that is nothing but a single metavariable matches
			// everything; reject it to avoid runaway findings.
			if _, isMeta := p.nodeMeta(p.sroot); isMeta && !p.isSeq {
				return nil, fmt.Errorf("pattern %q is a bare metavariable and would match every node", raw)
			}
			p.anchor = p.computeAnchor(l)
			p.precompute()
			return p, nil
		}
		p.isSeq = true
		p.sseq = nodes
		p.sroot = nodes[0].Parent()
		p.anchor = p.computeAnchor(l)
		p.precompute()
		return p, nil
	}
	if len(swallowed) > 0 {
		return nil, fmt.Errorf("metavariable(s) %s do not parse as standalone nodes in %s pattern %q; a metavariable must cover a whole value, name, or text node", strings.Join(swallowed, ", "), l.Name, raw)
	}
	return nil, fmt.Errorf("pattern is not valid %s:\n  %s", l.Name, strings.ReplaceAll(raw, "\n", "\n  "))
}

// computeAnchor finds the longest concrete token that any match of this
// pattern must contain byte-for-byte: a non-metavariable leaf (identifiers,
// keywords) or a full-text-compared literal node. Comments and extras are
// skipped (matching skips them), as are languages with case-insensitive
// kinds, where a byte-exact prefilter would be unsound.
func (p *Pattern) computeAnchor(l *lang.Language) string {
	if l.HasCaseFolding() {
		return ""
	}
	roots := p.sseq
	if !p.isSeq {
		roots = []*sitter.Node{p.sroot}
	}
	best := ""
	consider := func(txt string) {
		if len(txt) > len(best) && anchorable(txt) {
			best = txt
		}
	}
	// Matching drops comment/extra/missing subtrees entirely, so nothing
	// inside one may anchor: descending into a doc comment's children would
	// harvest comment text the target never needs to contain. Prune, don't
	// just skip.
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if n.IsExtra() || n.IsMissing() || l.IsComment(n.Type()) {
			return
		}
		if n.ChildCount() == 0 {
			if _, isMeta := p.nodeMeta(n); isMeta {
				return
			}
			consider(p.text(n))
			return
		}
		// Full-text-compared literal without a metavariable hole or
		// interpolation: its entire text appears verbatim in any match.
		if l.IsLiteral(n.Type()) {
			if _, ok := p.nodeMetaInner(n); !ok {
				hasInterp := false
				for _, k := range namedChildren(n, l) {
					if l.IsInterpolation(k.Type()) {
						hasInterp = true
						break
					}
				}
				if !hasInterp {
					consider(p.text(n))
					return
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if c := n.Child(i); c != nil {
				visit(c)
			}
		}
	}
	for _, r := range roots {
		visit(r)
	}
	return best
}

// anchorable reports whether a token is a useful byte-exact prefilter: long
// enough to be selective and carrying at least one alphanumeric byte.
func anchorable(s string) bool {
	if len(s) < 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return true
		}
	}
	return false
}

// variadicHole returns the display name of a variadic metavariable sitting in
// a delimiter-wrapped text hole (`<a b="$$$X"/>`), or "" when there is none.
func variadicHole(nodes []*sitter.Node, metas map[string]metaSpec, src []byte) string {
	bad := ""
	for _, root := range nodes {
		walk(root, func(n *sitter.Node) {
			if bad != "" || n.ChildCount() == 0 {
				return
			}
			if s, e, ok := innerSpan(n); ok {
				if spec, found := metas[string(src[s:e])]; found && spec.kind == mvVariadic {
					if spec.anon {
						bad = "$$$"
					} else {
						bad = "$$$" + spec.name
					}
				}
			}
		})
	}
	return bad
}

// hasParseError reports whether a pattern parse is unusable: it contains an
// ERROR node, or a MISSING node in a language that does not tolerate them.
// HTML tolerates MISSING nodes so an open-tag pattern (`<div …>`, parsed as
// element(start_tag, MISSING end_tag)) compiles; the phantom node is filtered
// out of matching by relevantChildren/namedChildren.
func hasParseError(root *sitter.Node, l *lang.Language) bool {
	if !root.HasError() && !root.IsMissing() {
		return false
	}
	if !l.AllowMissingNodes {
		return true
	}
	bad := false
	walk(root, func(n *sitter.Node) {
		if n.IsError() {
			bad = true
		}
	})
	return bad
}

// missingSentinels returns the display names of metavariables with at least
// one occurrence that did not survive parsing as an isolated leaf or as the
// text hole of a delimiter-wrapped node (see innerSpan). Occurrences are
// counted by distinct byte offset, so a reused metavariable must survive at
// every position rewrite emitted it.
func missingSentinels(nodes []*sitter.Node, metas map[string]metaSpec, src []byte) []string {
	if len(metas) == 0 {
		return nil
	}
	found := map[string]map[uint32]bool{}
	mark := func(s, e uint32) {
		sent := string(src[s:e])
		if _, ok := metas[sent]; !ok {
			return
		}
		if found[sent] == nil {
			found[sent] = map[uint32]bool{}
		}
		found[sent][s] = true
	}
	for _, root := range nodes {
		walk(root, func(n *sitter.Node) {
			if n.ChildCount() == 0 {
				mark(n.StartByte(), n.EndByte())
			} else if s, e, ok := innerSpan(n); ok {
				mark(s, e)
			}
		})
	}
	var missing []string
	for sent, spec := range metas {
		if len(found[sent]) >= spec.count {
			continue
		}
		switch {
		case spec.anon && spec.kind == mvVariadic:
			missing = append(missing, "$$$")
		case spec.anon:
			missing = append(missing, "$_")
		case spec.kind == mvVariadic:
			missing = append(missing, "$$$"+spec.name)
		default:
			missing = append(missing, "$"+spec.name)
		}
	}
	sort.Strings(missing)
	return missing
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

// leafAt returns the leaf node (no children) starting exactly at byte offset off,
// used to locate an operator placeholder after parsing.
func leafAt(root *sitter.Node, off uint32) *sitter.Node {
	var found *sitter.Node
	walk(root, func(n *sitter.Node) {
		if found == nil && n.ChildCount() == 0 && n.StartByte() == off {
			found = n
		}
	})
	return found
}

// DebugPattern compiles a raw pattern and returns a human-readable description
// of its resolved structure. Used by `lint parse --pattern`.
func DebugPattern(l *lang.Language, raw string) (string, error) {
	p, err := compilePattern(l, raw)
	if err != nil {
		return "", err
	}
	if p.isSeq {
		var b strings.Builder
		fmt.Fprintf(&b, "sequence of %d nodes:\n", len(p.sseq))
		for _, n := range p.sseq {
			fmt.Fprintf(&b, "  %s\n", n.String())
		}
		return b.String(), nil
	}
	return fmt.Sprintf("kind: %s\n%s", p.sroot.Type(), p.sroot.String()), nil
}

// nodeMeta is the compile-time (smacker-side) metavariable lookup, used by
// compilePattern and computeAnchor before the flat tables exist. It reports
// the spec only when the node IS a sentinel leaf, deliberately ignoring
// larger nodes whose text merely equals a sentinel (a grammar wrapper
// holding only the sentinel), so matching recurses into the wrapper and
// binds the tightest node.
func (p *Pattern) nodeMeta(n *sitter.Node) (metaSpec, bool) {
	if n.ChildCount() != 0 {
		return metaSpec{}, false
	}
	if s, ok := p.metas[n.Content(p.src)]; ok {
		return s, true
	}
	if s, ok := p.opMetas[n.StartByte()]; ok {
		return s, true
	}
	return metaSpec{}, false
}

// nodeMetaInner is the compile-time twin of innerAt: the metavariable spec
// carried in a node's text hole (see innerSpan), for sentinels that parse
// between delimiter tokens rather than as nodes of their own: `attr="$X"`
// in XML puts the sentinel inside AttValue's quotes, which the grammar
// exposes only as part of the parent's span.
func (p *Pattern) nodeMetaInner(n *sitter.Node) (metaSpec, bool) {
	s, e, ok := innerSpan(n)
	if !ok || s == e {
		return metaSpec{}, false
	}
	spec, found := p.metas[string(p.src[s:e])]
	return spec, found
}

// metaAt reports the metavariable spec for a pattern node.
func (p *Pattern) metaAt(i int32) (metaSpec, bool) {
	if p.hasMeta[i] {
		return p.meta[i], true
	}
	return metaSpec{}, false
}

// innerAt reports the metavariable spec carried in a pattern node's text hole.
func (p *Pattern) innerAt(i int32) (metaSpec, bool) {
	if p.hasInner[i] {
		return p.inner[i], true
	}
	return metaSpec{}, false
}

// textBytes returns a pattern node's source bytes without allocating.
func (p *Pattern) textBytes(i int32) []byte {
	return p.src[p.f.StartByte[i]:p.f.EndByte[i]]
}

// rel returns a pattern node's relevant children.
func (p *Pattern) rel(i int32) []int32 { return p.relList[p.relStart[i]:p.relStart[i+1]] }

// named returns a pattern node's named children minus comments/extras.
func (p *Pattern) named(i int32) []int32 { return p.namedList[p.namedStart[i]:p.namedStart[i+1]] }

// text returns a pattern node's source text (compile-time helper).
func (p *Pattern) text(n *sitter.Node) string { return n.Content(p.src) }
