package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
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

// Pattern is a compiled structural pattern for one language.
type Pattern struct {
	lang    *lang.Language
	src     []byte              // rewritten pattern source
	metas   map[string]metaSpec // sentinel identifier -> spec
	opMetas map[uint32]metaSpec // operator-leaf StartByte -> spec ($OP holes)
	tree    *sitter.Tree        // kept alive so nodes remain valid

	root  *sitter.Node   // single-node pattern root
	isSeq bool           // sequence pattern (match a run of sibling statements)
	seq   []*sitter.Node // sequence elements (named children)

	// anchor is the longest concrete token a matching file must contain
	// byte-for-byte; "" when no such token exists. Used to skip files (and
	// their parses) that cannot match.
	anchor string

	// Per-node lookups precomputed once at compile time, keyed by node ID,
	// so the hot matching path never crosses cgo for pattern-side kinds,
	// texts, or child lists and never re-allocates a sentinel string for
	// meta lookups.
	kindByID  map[uintptr]string
	textByID  map[uintptr][]byte
	metaByID  map[uintptr]metaSpec
	innerByID map[uintptr]metaSpec
	relByID   map[uintptr][]*sitter.Node
	namedByID map[uintptr][]*sitter.Node
}

// precompute fills the per-node lookup tables. Call after prewarm, once the
// pattern's shape (root/seq) is final.
func (p *Pattern) precompute() {
	p.kindByID = map[uintptr]string{}
	p.textByID = map[uintptr][]byte{}
	p.metaByID = map[uintptr]metaSpec{}
	p.innerByID = map[uintptr]metaSpec{}
	p.relByID = map[uintptr][]*sitter.Node{}
	p.namedByID = map[uintptr][]*sitter.Node{}
	walk(p.tree.RootNode(), func(n *sitter.Node) {
		id := n.ID()
		p.kindByID[id] = n.Type()
		p.textByID[id] = p.src[n.StartByte():n.EndByte()]
		p.relByID[id] = relevantChildren(n, p.lang)
		p.namedByID[id] = namedChildren(n, p.lang)
		if n.ChildCount() == 0 {
			if s, ok := p.metas[n.Content(p.src)]; ok {
				p.metaByID[id] = s
			} else if s, ok := p.opMetas[n.StartByte()]; ok {
				p.metaByID[id] = s
			}
		} else if s, e, ok := innerSpan(n); ok && s != e {
			if spec, found := p.metas[string(p.src[s:e])]; found {
				p.innerByID[id] = spec
			}
		}
	})
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
		// Populate smacker's lazy node cache single-threaded so concurrent
		// matching against this shared pattern tree never writes the cache.
		prewarm(root)
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
			p.anchor = p.computeAnchor(l)
			p.precompute()
			return p, nil
		}
		p.isSeq = true
		p.seq = nodes
		p.root = nodes[0].Parent()
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
	roots := p.seq
	if !p.isSeq {
		roots = []*sitter.Node{p.root}
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
			if _, isMeta := p.metaOf(n); isMeta {
				return
			}
			consider(p.text(n))
			return
		}
		// Full-text-compared literal without a metavariable hole or
		// interpolation: its entire text appears verbatim in any match.
		if l.IsLiteral(n.Type()) {
			if _, ok := p.metaOfInner(n); !ok {
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
	if p.metaByID != nil {
		s, ok := p.metaByID[n.ID()]
		return s, ok
	}
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

// metaOfInner reports the metavariable spec carried in a node's text hole
// (see innerSpan), for sentinels that parse between delimiter tokens rather
// than as nodes of their own: `attr="$X"` in XML puts the sentinel inside
// AttValue's quotes, which the grammar exposes only as part of the parent's
// span.
func (p *Pattern) metaOfInner(n *sitter.Node) (metaSpec, bool) {
	if p.innerByID != nil {
		s, ok := p.innerByID[n.ID()]
		return s, ok
	}
	s, e, ok := innerSpan(n)
	if !ok || s == e {
		return metaSpec{}, false
	}
	spec, found := p.metas[string(p.src[s:e])]
	return spec, found
}

// kind returns a pattern node's type without crossing cgo on the hot path.
func (p *Pattern) kind(n *sitter.Node) string {
	if k, ok := p.kindByID[n.ID()]; ok {
		return k
	}
	return n.Type()
}

// textBytes returns a pattern node's source bytes without allocating.
func (p *Pattern) textBytes(n *sitter.Node) []byte {
	if t, ok := p.textByID[n.ID()]; ok {
		return t
	}
	return p.src[n.StartByte():n.EndByte()]
}

// relevant returns a pattern node's relevant children from the precomputed
// table (falling back to a live computation for safety).
func (p *Pattern) relevant(n *sitter.Node) []*sitter.Node {
	if kids, ok := p.relByID[n.ID()]; ok {
		return kids
	}
	return relevantChildren(n, p.lang)
}

// named returns a pattern node's named children from the precomputed table.
func (p *Pattern) named(n *sitter.Node) []*sitter.Node {
	if kids, ok := p.namedByID[n.ID()]; ok {
		return kids
	}
	return namedChildren(n, p.lang)
}

// unwrapForVariadic descends through transparent and parameter-wrapper
// pattern nodes so a buried variadic sentinel is recognised at a list level,
// using only precomputed lookups.
func (p *Pattern) unwrapForVariadic(n *sitter.Node) *sitter.Node {
	for isTransparent(p.kind(n), p.lang) || paramWrapperKinds[p.kind(n)] {
		kids := p.named(n)
		if len(kids) != 1 {
			// XML's grammar cannot parse a bare attribute name, so a variadic
			// in attribute position is rewritten to `$$$X=""` and parses as
			// Attribute(Name, AttValue); the Name child carries the sentinel.
			if p.kind(n) == "Attribute" && len(kids) > 1 {
				return kids[0]
			}
			return n
		}
		n = kids[0]
	}
	return n
}

// text returns a pattern node's source text.
func (p *Pattern) text(n *sitter.Node) string { return n.Content(p.src) }
