package engine

import (
	"sync"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
	"github.com/ichigo-labs/lint/internal/treesit"
)

// noNode is the flat-index sentinel for "no node" (what a nil *sitter.Node
// used to convey).
const noNode int32 = -1

// langSyms holds per-language symbol metadata resolved once per process:
// the symbol-id -> kind-name table plus every kind predicate the matcher
// consults, as dense boolean tables indexed by (canonical) symbol id. With
// these, the hot path never touches a string-keyed map: kind checks are
// array loads and kind equality is uint16 comparison.
type langSyms struct {
	names []string
	canon []uint16

	// nameToCanon resolves a kind name to its canonical symbol, for
	// compile-time uses (`where $x kind "..."`). Unknown names are absent.
	nameToCanon map[string]uint16

	// Predicates indexed by symbol id. Two symbols with the same name agree
	// on every predicate (all are defined on the name), so indexing by raw
	// or canonical symbol is equivalent.
	comment      []bool
	terminator   []bool
	transparent  []bool // engine-wide transparentKinds + language-specific
	block        []bool
	paramWrapper []bool
	startTag     []bool
	element      []bool
	literal      []bool
	foldCase     []bool
	interp       []bool
}

// langSymsCache caches langSyms by *lang.Language (registry singletons). The
// key is deliberately the Language, not the grammar pointer: the predicate
// tables bake in Language-level sets (comment/literal/fold kinds), so two
// languages sharing one grammar must not share an entry.
var langSymsCache sync.Map

func symsFor(l *lang.Language) *langSyms {
	if v, ok := langSymsCache.Load(l); ok {
		return v.(*langSyms)
	}
	ptr := treesit.LangPtr(l.Grammar())
	names := treesit.SymbolNames(ptr)
	s := &langSyms{
		names:        names,
		canon:        treesit.SymbolCanon(ptr),
		nameToCanon:  make(map[string]uint16, len(names)),
		comment:      make([]bool, len(names)),
		terminator:   make([]bool, len(names)),
		transparent:  make([]bool, len(names)),
		block:        make([]bool, len(names)),
		paramWrapper: make([]bool, len(names)),
		startTag:     make([]bool, len(names)),
		element:      make([]bool, len(names)),
		literal:      make([]bool, len(names)),
		foldCase:     make([]bool, len(names)),
		interp:       make([]bool, len(names)),
	}
	for sym, name := range names {
		if _, seen := s.nameToCanon[name]; !seen {
			s.nameToCanon[name] = s.canon[sym]
		}
		s.comment[sym] = l.IsComment(name)
		s.terminator[sym] = terminatorKinds[name]
		s.transparent[sym] = isTransparent(name, l)
		s.block[sym] = blockKinds[name]
		s.paramWrapper[sym] = paramWrapperKinds[name]
		s.startTag[sym] = startTagKinds[name]
		s.element[sym] = elementKinds[name]
		s.literal[sym] = l.IsLiteral(name)
		s.foldCase[sym] = l.FoldsCase(name)
		s.interp[sym] = l.IsInterpolation(name)
	}
	v, _ := langSymsCache.LoadOrStore(l, s)
	return v.(*langSyms)
}

// is reports a predicate table entry, treating out-of-range symbols (ERROR)
// as false — the same answer the old string sets gave for "ERROR".
func is(table []bool, sym uint16) bool {
	return int(sym) < len(table) && table[sym]
}

// canonForKind resolves a kind name to the canonical symbol used in csym
// comparisons. ok=false means no node of this language can have the kind.
func (s *langSyms) canonForKind(name string) (uint16, bool) {
	if name == "ERROR" {
		return treesit.ErrorSymbol, true
	}
	c, ok := s.nameToCanon[name]
	return c, ok
}

// Index is a per-file flat form of one parsed tree, shared across all rules
// checking that file. It is built with a single cgo call (treesit.Extract);
// every lookup afterwards — kinds, spans, children, ancestors, descendants —
// is a plain Go array read, which is what makes matching cgo-free. An Index
// is confined to the goroutine checking its file.
type Index struct {
	f    *treesit.Flat
	st   *langSyms
	tree *sitter.Tree // kept only so query rules can execute against it

	// csym is each node's canonical symbol: print-equal kinds (tree-sitter
	// aliases can give one name several symbol ids) compare integer-equal.
	csym []uint16

	// byType buckets node indices by canonical symbol, in pre-order, so a
	// single-kind pattern scans only its bucket. ERROR nodes are unbucketed
	// (no pattern compiles to kind ERROR).
	byType [][]int32

	// relStart/relList and namedStart/namedList are CSR layouts of each
	// node's "relevant" children (comments, extras, missing nodes and
	// statement terminators dropped) and named children (minus comments and
	// extras) — the two child views the matching kernel uses.
	relStart, relList     []int32
	namedStart, namedList []int32

	// idToIdx lazily maps ts_node ids to flat indices; only query rules
	// need it (to translate query captures back into flat nodes).
	idToIdx map[uintptr]int32

	// linearScan forces findSingle/findSeq to scan every node instead of
	// using byType buckets; tests use it to verify bucket-path equivalence.
	linearScan bool
}

// BuildIndex extracts a parsed tree into its flat per-file index. The tree
// handle stays referenced by the Index (queries run against it); callers
// still own closing it after the file's rules have run.
func BuildIndex(l *lang.Language, tree *sitter.Tree) *Index {
	f := treesit.ExtractTree(tree)
	st := symsFor(l)
	n := f.Len()

	ix := &Index{f: f, st: st, tree: tree}

	csym := make([]uint16, n)
	for i := 0; i < n; i++ {
		csym[i] = treesit.Canonical(st.canon, f.Symbol[i])
	}
	ix.csym = csym

	// Bucket nodes by canonical symbol with a counting pass + fill pass;
	// ascending fill preserves pre-order within each bucket.
	counts := make([]int32, len(st.names))
	for i := 0; i < n; i++ {
		if s := csym[i]; int(s) < len(counts) {
			counts[s]++
		}
	}
	backing := make([]int32, n)
	byType := make([][]int32, len(st.names))
	off := 0
	for s, c := range counts {
		if c > 0 {
			byType[s] = backing[off : off : off+int(c)]
			off += int(c)
		}
	}
	for i := 0; i < n; i++ {
		if s := csym[i]; int(s) < len(byType) {
			byType[s] = append(byType[s], int32(i))
		}
	}
	ix.byType = byType

	ix.buildChildViews()
	return ix
}

// buildChildViews precomputes the relevant- and named-children CSR arrays.
func (ix *Index) buildChildViews() {
	ix.relStart, ix.relList, ix.namedStart, ix.namedList = buildChildCSR(ix.f, ix.st, ix.csym)
}

// buildChildCSR computes, in one pass over the full child list, the two
// filtered child views matching uses: "relevant" children (comments, extras,
// missing nodes and statement terminators dropped) and named children (minus
// comments and extras). Shared by per-file indexes and compiled patterns.
func buildChildCSR(f *treesit.Flat, st *langSyms, csym []uint16) (relStart, relList, namedStart, namedList []int32) {
	n := f.Len()
	relStart = make([]int32, n+1)
	namedStart = make([]int32, n+1)
	relList = make([]int32, 0, len(f.ChildList))
	namedList = make([]int32, 0, len(f.ChildList))
	for i := 0; i < n; i++ {
		for _, c := range f.Children(i) {
			if f.IsExtra(int(c)) || f.IsMissing(int(c)) {
				continue
			}
			sym := csym[c]
			if is(st.comment, sym) {
				continue
			}
			if !is(st.terminator, sym) {
				relList = append(relList, c)
			}
			if f.IsNamed(int(c)) {
				namedList = append(namedList, c)
			}
		}
		relStart[i+1] = int32(len(relList))
		namedStart[i+1] = int32(len(namedList))
	}
	return relStart, relList, namedStart, namedList
}

// rel returns a node's relevant children (see buildChildViews).
func (ix *Index) rel(n int32) []int32 { return ix.relList[ix.relStart[n]:ix.relStart[n+1]] }

// named returns a node's named children minus comments/extras.
func (ix *Index) named(n int32) []int32 { return ix.namedList[ix.namedStart[n]:ix.namedStart[n+1]] }

// kindName resolves a node's kind name (for constraint errors and debug).
func (ix *Index) kindName(n int32) string {
	return treesit.SymbolName(ix.st.names, ix.f.Symbol[n])
}

// text returns a node's source bytes (no copy).
func (ix *Index) text(n int32, src []byte) []byte {
	return src[ix.f.StartByte[n]:ix.f.EndByte[n]]
}

// point converts a node's start/end positions to sitter Points.
func (ix *Index) startPoint(n int32) sitter.Point {
	return sitter.Point{Row: ix.f.StartRow[n], Column: ix.f.StartCol[n]}
}
func (ix *Index) endPoint(n int32) sitter.Point {
	return sitter.Point{Row: ix.f.EndRow[n], Column: ix.f.EndCol[n]}
}

// unwrapTransparentT strips transparent wrappers from a target node (the flat
// twin of unwrapTransparent).
func (ix *Index) unwrapTransparentT(n int32) int32 {
	for is(ix.st.transparent, ix.csym[n]) {
		kids := ix.named(n)
		if len(kids) != 1 {
			return n
		}
		n = kids[0]
	}
	return n
}

// innerSpan is the flat twin of innerSpan in node.go: the byte span of n's
// textual content between its delimiter tokens.
func (ix *Index) innerSpan(n int32) (start, end uint32, ok bool) {
	return flatInnerSpan(ix.f, n)
}

// flatInnerSpan computes a node's text hole on the flat form; see innerSpan
// in node.go for the semantics it mirrors.
func flatInnerSpan(f *treesit.Flat, n int32) (start, end uint32, ok bool) {
	kids := f.Children(int(n))
	if len(kids) == 0 {
		return 0, 0, false
	}
	for _, c := range kids {
		if f.IsNamed(int(c)) {
			return 0, 0, false // real structure: not a pure text hole
		}
	}
	start, end = f.StartByte[n], f.EndByte[n]
	i := 0
	for i < len(kids) {
		c := kids[i]
		if f.StartByte[c] != start {
			break
		}
		start = f.EndByte[c]
		i++
	}
	j := len(kids) - 1
	for j >= i {
		c := kids[j]
		if f.EndByte[c] != end {
			break
		}
		end = f.StartByte[c]
		j--
	}
	if j >= i || start > end {
		return 0, 0, false // children in the middle: not a pure text hole
	}
	return start, end, true
}

// indexOfID translates a ts_node id (from a query capture) to its flat
// index. The map is built lazily: only files actually running query rules
// pay for it.
func (ix *Index) indexOfID(id uintptr) int32 {
	if ix.idToIdx == nil {
		m := make(map[uintptr]int32, ix.f.Len())
		for i, nid := range ix.f.NodeID {
			m[nid] = int32(i)
		}
		ix.idToIdx = m
	}
	if i, ok := ix.idToIdx[id]; ok {
		return i
	}
	return noNode
}

// sitterNode rebuilds the live *sitter.Node for a flat index by descending
// child ordinals from the root. Only the scoped-query path needs real nodes,
// so this cgo cost is confined to rules embedding `query { ... }` inside
// any/all/relations.
func (ix *Index) sitterNode(n int32) *sitter.Node {
	// Collect the path of child ordinals root -> n.
	var path []int
	f := ix.f
	for cur := n; f.Parent[cur] != -1; cur = f.Parent[cur] {
		p := f.Parent[cur]
		kids := f.Children(int(p))
		ord := -1
		for i, c := range kids {
			if c == cur {
				ord = i
				break
			}
		}
		path = append(path, ord)
	}
	node := ix.tree.RootNode()
	for i := len(path) - 1; i >= 0 && node != nil; i-- {
		node = node.Child(path[i])
	}
	return node
}

// CommentSpan locates one comment node for suppression scanning.
type CommentSpan struct {
	Row                int // 0-based start row
	StartByte, EndByte uint32
}

// Comments returns every comment node in the file, in pre-order. The runner
// uses this for `lint:ignore` directives instead of re-walking the tree.
func (ix *Index) Comments() []CommentSpan {
	var out []CommentSpan
	for i, n := 0, ix.f.Len(); i < n; i++ {
		if is(ix.st.comment, ix.csym[i]) {
			out = append(out, CommentSpan{
				Row:       int(ix.f.StartRow[i]),
				StartByte: ix.f.StartByte[i],
				EndByte:   ix.f.EndByte[i],
			})
		}
	}
	return out
}
