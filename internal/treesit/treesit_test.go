package treesit

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
)

// parse builds a tree directly with smacker (the engine package imports
// treesit, so the test cannot import engine without a cycle).
func parse(t *testing.T, l *lang.Language, src string) *sitter.Tree {
	t.Helper()
	parser := sitter.NewParser()
	parser.SetLanguage(l.Grammar())
	tree, err := parser.ParseCtx(context.Background(), nil, []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

// snippets holds a nontrivial source per registered language. Every snippet
// embeds multibyte unicode (in strings or comments) so byte/point bookkeeping
// is exercised on non-ASCII offsets; mangle() additionally derives a
// syntactically broken variant of each to cover ERROR/MISSING nodes.
var snippets = map[string]string{
	"go": `package p

// héllo 世界 comment
func f(xs []int) (int, error) {
	total := 0
	for i, x := range xs {
		if x < 0 {
			return 0, fmt.Errorf("negative %d at %d → 世界", x, i)
		}
		total += x
	}
	return total, nil
}
`,
	"python": `# comment with ünïcode 日本語
class Greeter:
    def greet(self, name):
        if not name:
            raise ValueError("empty 名前")
        return f"héllo {name} ✓"

print(Greeter().greet("世界"))
`,
	"java": `// hällo 世界
class Greeter<T> {
    private final String name;
    Greeter(String name) { this.name = name; }
    boolean check(int a, int b) { return a < b && b > 0; }
    String greet() { return "héllo " + name + " ✓"; }
}
`,
	"csharp": `// hällo 世界
class Greeter {
    private readonly string name = "世界";
    public bool Check(int a, int b) => a < b;
    public string Greet() { return $"héllo {name} ✓"; }
}
`,
	"c": `/* hällo 世界 */
#include <stdio.h>
int sum(const int *xs, int n) {
    int total = 0;
    for (int i = 0; i < n; i++) total += xs[i];
    printf("héllo %d ✓\n", total);
    return total;
}
`,
	"cpp": `// hällo 世界
#include <vector>
template <typename T> T sum(const std::vector<T> &xs) {
    T total{};
    for (const auto &x : xs) total += x;
    return total;
}
auto s = std::string("héllo 世界 ✓");
`,
	"rust": `// hällo 世界
fn sum(xs: &[i32]) -> Result<i32, String> {
    let mut total = 0;
    for &x in xs {
        if x < 0 {
            return Err(format!("negative {} → 世界", x));
        }
        total += x;
    }
    Ok(total)
}
`,
	"kotlin": `// hällo 世界
class Greeter(private val name: String) {
    fun greet(xs: List<Int>): String {
        val total = xs.filter { it > 0 }.sum()
        return "héllo $name ✓ $total"
    }
}
`,
	"swift": `// hällo 世界
struct Greeter {
    let name: String
    func greet(xs: [Int]) -> String {
        let total = xs.filter { $0 > 0 }.reduce(0, +)
        return "héllo \(name) ✓ \(total)"
    }
}
`,
	"typescript": `// hällo 世界
interface Named { name: string }
class Greeter<T extends Named> {
  constructor(private items: T[]) {}
  greet(): string[] {
    return this.items.filter(x => x.name !== "").map(x => ` + "`héllo ${x.name} ✓`" + `);
  }
}
`,
	"tsx": `// hällo 世界
const Greeting = ({ name }: { name: string }) => (
  <div className="greeting" title="héllo">
    {name === "" ? <span>∅</span> : <b>héllo {name} ✓</b>}
  </div>
);
`,
	"html": `<!DOCTYPE html>
<html lang="ja">
  <!-- hällo 世界 -->
  <body>
    <div class="greeting" data-x="héllo">
      <p>héllo 世界 ✓</p>
      <img src="x.png" alt="絵">
    </div>
  </body>
</html>
`,
	"css": `/* hällo 世界 */
.greeting::before { content: "héllo ✓"; color: #ff0000; }
@media (max-width: 600px) {
  body > .greeting { margin: 0 1px; }
}
`,
	"scss": `/* hällo 世界 */
$base-color: #ff0000;
.greeting {
  color: $base-color;
  &:hover { content: "héllo ✓"; }
  .inner { margin: 0 1px; }
}
`,
	"json": `{
  "name": "héllo 世界",
  "ok": true,
  "n": 2.5,
  "items": [1, null, "✓", {"nested": false}]
}
`,
	"yaml": `# hällo 世界
name: héllo
items:
  - 1
  - ok: true
    note: "✓ 世界"
text: |
  multi
  line
`,
	"xml": `<?xml version="1.0" encoding="UTF-8"?>
<greeting lang="ja">
  <!-- hällo 世界 -->
  <name attr="héllo">世界</name>
  <data><![CDATA[✓ raw]]></data>
</greeting>
`,
}

// mangle derives a syntactically broken variant: truncating mid-construct
// and appending junk reliably produces ERROR and MISSING nodes in most
// grammars (HTML/YAML may still parse cleanly; equivalence must hold anyway).
func mangle(src string) string {
	return src[:2*len(src)/3] + "\n@@ %% <<({ 世"
}

// missingSnippets are sources verified to make their grammar insert MISSING
// (zero-width) nodes, which mangle() does not reliably trigger. Zero-width
// nodes are the traversal edge case most likely to diverge between the
// cursor walk and Child(i) recursion, so they get dedicated coverage.
var missingSnippets = map[string]string{
	"go":         "package p\nfunc f() {\n\tx := 1\n",
	"typescript": "function f() { return (1 + 2; }",
	"c":          "int f() { return (1 + 2; }",
	"java":       "class A { int f() { return 1 } }",
	"rust":       "fn f() -> i32 { (1 + 2 }",
	"json":       "{\"a\": [1, 2}",
	"python":     "def f(:\n    pass\n",
}

// refNode is one node of the trusted reference walk: a plain recursive
// Child(i) traversal through smacker, the order Flat claims to replicate.
type refNode struct {
	n        *sitter.Node
	parent   int
	children []int32
	end      int // index just past the subtree, for SubtreeEnd
}

func collectRef(t *testing.T, root *sitter.Node) []refNode {
	t.Helper()
	var nodes []refNode
	var rec func(n *sitter.Node, parent int) int32
	rec = func(n *sitter.Node, parent int) int32 {
		i := len(nodes)
		nodes = append(nodes, refNode{n: n, parent: parent})
		count := int(n.ChildCount())
		for j := 0; j < count; j++ {
			c := n.Child(j)
			if c == nil {
				t.Fatalf("node %d: Child(%d) is nil with ChildCount %d", i, j, count)
			}
			ci := rec(c, i)
			nodes[i].children = append(nodes[i].children, ci)
		}
		nodes[i].end = len(nodes)
		return int32(i)
	}
	rec(root, -1)
	return nodes
}

// seen accumulates which node classes the corpus exercised, so the suite
// fails if the snippets quietly stop covering error recovery paths.
type seen struct{ err, missing, extra, multibyteCol bool }

func checkEquivalence(t *testing.T, l *lang.Language, src string, sn *seen) {
	t.Helper()
	tree := parse(t, l, src)
	flat := ExtractTree(tree)
	ref := collectRef(t, tree.RootNode())
	grammar := LangPtr(l.Grammar())
	names := SymbolNames(grammar)
	canon := SymbolCanon(grammar)

	if flat.Len() != len(ref) {
		t.Fatalf("node count: flat %d, recursive walk %d", flat.Len(), len(ref))
	}
	for i, r := range ref {
		n := r.n
		if got, want := SymbolName(names, flat.Symbol[i]), n.Type(); got != want {
			t.Fatalf("node %d: type %q, want %q (symbol %d)", i, got, want, flat.Symbol[i])
		}
		if got, want := SymbolName(names, Canonical(canon, flat.Symbol[i])), n.Type(); got != want {
			t.Fatalf("node %d: canonical type %q, want %q", i, got, want)
		}
		if flat.StartByte[i] != n.StartByte() || flat.EndByte[i] != n.EndByte() {
			t.Fatalf("node %d (%s): bytes [%d,%d), want [%d,%d)",
				i, n.Type(), flat.StartByte[i], flat.EndByte[i], n.StartByte(), n.EndByte())
		}
		sp, ep := n.StartPoint(), n.EndPoint()
		if flat.StartRow[i] != sp.Row || flat.StartCol[i] != sp.Column ||
			flat.EndRow[i] != ep.Row || flat.EndCol[i] != ep.Column {
			t.Fatalf("node %d (%s): points (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
				i, n.Type(), flat.StartRow[i], flat.StartCol[i], flat.EndRow[i], flat.EndCol[i],
				sp.Row, sp.Column, ep.Row, ep.Column)
		}
		// Columns are byte offsets, so a node preceded on its own line by a
		// multibyte rune has a column that a rune-based count would get
		// wrong. Seeing one means the comparison above covered that case.
		if lineStart := int(flat.StartByte[i]) - int(flat.StartCol[i]); lineStart >= 0 {
			for _, b := range []byte(src[lineStart:flat.StartByte[i]]) {
				if b >= 0x80 {
					sn.multibyteCol = true
					break
				}
			}
		}
		if flat.IsNamed(i) != n.IsNamed() || flat.IsExtra(i) != n.IsExtra() ||
			flat.IsMissing(i) != n.IsMissing() || flat.IsError(i) != n.IsError() ||
			flat.HasError(i) != n.HasError() {
			t.Fatalf("node %d (%s): flags named=%v extra=%v missing=%v error=%v hasError=%v, want %v %v %v %v %v",
				i, n.Type(), flat.IsNamed(i), flat.IsExtra(i), flat.IsMissing(i), flat.IsError(i), flat.HasError(i),
				n.IsNamed(), n.IsExtra(), n.IsMissing(), n.IsError(), n.HasError())
		}
		if int(flat.Parent[i]) != r.parent {
			t.Fatalf("node %d (%s): parent %d, want %d", i, n.Type(), flat.Parent[i], r.parent)
		}
		if flat.NodeID[i] != n.ID() {
			t.Fatalf("node %d (%s): id %#x, want %#x", i, n.Type(), flat.NodeID[i], n.ID())
		}
		if kids := flat.Children(i); !equalInt32(kids, r.children) {
			t.Fatalf("node %d (%s): children %v, want %v", i, n.Type(), kids, r.children)
		}
		if int(flat.SubtreeEnd[i]) != r.end {
			t.Fatalf("node %d (%s): subtree end %d, want %d", i, n.Type(), flat.SubtreeEnd[i], r.end)
		}
		sn.err = sn.err || flat.IsError(i)
		sn.missing = sn.missing || flat.IsMissing(i)
		sn.extra = sn.extra || flat.IsExtra(i)
	}
}

func equalInt32(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestExtractMatchesRecursiveWalk(t *testing.T) {
	var sn seen
	for _, name := range lang.Names() {
		l, ok := lang.Get(name)
		if !ok {
			t.Fatalf("lang.Get(%q) failed", name)
		}
		src, ok := snippets[name]
		if !ok {
			t.Fatalf("no test snippet for language %q; add one", name)
		}
		t.Run(name, func(t *testing.T) {
			checkEquivalence(t, l, src, &sn)
		})
		t.Run(name+"/broken", func(t *testing.T) {
			checkEquivalence(t, l, mangle(src), &sn)
		})
		if ms, ok := missingSnippets[name]; ok {
			t.Run(name+"/missing", func(t *testing.T) {
				checkEquivalence(t, l, ms, &sn)
			})
		}
	}
	// The corpus must keep exercising the recovery paths, or the equivalence
	// claim silently narrows to clean parses.
	if !sn.err {
		t.Error("corpus produced no ERROR node")
	}
	if !sn.missing {
		t.Error("corpus produced no MISSING node")
	}
	if !sn.extra {
		t.Error("corpus produced no extra (comment) node")
	}
	if !sn.multibyteCol {
		t.Error("corpus exercised no multibyte column offsets")
	}
}

// TestSymbolCanon verifies the canonicalization invariant on the full symbol
// table of every language: same name <=> same canonical id, and the
// canonical id is the first occurrence of the name.
func TestSymbolCanon(t *testing.T) {
	for _, name := range lang.Names() {
		l, _ := lang.Get(name)
		grammar := LangPtr(l.Grammar())
		names := SymbolNames(grammar)
		canon := SymbolCanon(grammar)
		if len(names) != len(canon) {
			t.Fatalf("%s: len(names)=%d len(canon)=%d", name, len(names), len(canon))
		}
		first := map[string]uint16{}
		for s, nm := range names {
			f, ok := first[nm]
			if !ok {
				f = uint16(s)
				first[nm] = f
			}
			if canon[s] != f {
				t.Errorf("%s: canon[%d]=%d, want %d (first id named %q)", name, s, canon[s], f, nm)
			}
		}
	}
	if SymbolName(nil, ErrorSymbol) != "ERROR" {
		t.Error("SymbolName must resolve ErrorSymbol to ERROR")
	}
	if Canonical(nil, ErrorSymbol) != ErrorSymbol {
		t.Error("Canonical must map ErrorSymbol to itself")
	}
}

// TestSmackerLayouts pins the struct layouts TreePtr/LangPtr mirror. If a
// go-tree-sitter bump moves these fields, this fails before the unsafe
// pointer walks can read garbage.
func TestSmackerLayouts(t *testing.T) {
	tree := reflect.TypeOf(sitter.Tree{})
	if f := tree.Field(0); f.Name != "BaseTree" || f.Offset != 0 || f.Type.Kind() != reflect.Ptr {
		t.Fatalf("sitter.Tree field 0 is %s %v at offset %d; TreePtr assumes *BaseTree first", f.Name, f.Type, f.Offset)
	}
	base := reflect.TypeOf(sitter.BaseTree{})
	if f := base.Field(0); f.Name != "c" || f.Offset != 0 || f.Type.Kind() != reflect.Ptr {
		t.Fatalf("sitter.BaseTree field 0 is %s %v at offset %d; TreePtr assumes c *C.TSTree first", f.Name, f.Type, f.Offset)
	}
	lt := reflect.TypeOf(sitter.Language{})
	if f := lt.Field(0); f.Name != "ptr" || f.Offset != 0 || f.Type.Kind() != reflect.UnsafePointer {
		t.Fatalf("sitter.Language field 0 is %s %v at offset %d; LangPtr assumes ptr unsafe.Pointer first", f.Name, f.Type, f.Offset)
	}
	if got := unsafe.Sizeof(sitter.Point{}); got != 8 {
		t.Fatalf("sitter.Point is %d bytes; TSPoint mirror assumes 8", got)
	}
}
