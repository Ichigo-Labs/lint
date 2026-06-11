// Package grammartest compares two Tree-sitter grammars by demanding they
// produce identical syntax trees. The vendored compressed-table grammars use
// it to prove byte-for-byte equivalence with the smacker originals they were
// generated from; it is imported only by tests.
package grammartest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
)

// EqualOnTestdata parses every file in the package's testdata directory with
// both grammars and fails the test on the first differing node.
func EqualOnTestdata(t *testing.T, vendored, original *sitter.Language) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("testdata", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata samples found (err=%v)", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(filepath.Base(f), func(t *testing.T) {
			Equal(t, vendored, original, src)
		})
	}
}

// Equal parses src with both grammars and fails the test unless the two
// trees match node-for-node (kind, byte range, namedness, error/missing
// flags, field names).
func Equal(t *testing.T, vendored, original *sitter.Language, src []byte) {
	t.Helper()
	a := parse(t, vendored, src)
	defer a.Close()
	b := parse(t, original, src)
	defer b.Close()
	compare(t, a.RootNode(), b.RootNode(), 0)
}

func parse(t *testing.T, l *sitter.Language, src []byte) *sitter.Tree {
	t.Helper()
	p := sitter.NewParser()
	defer p.Close()
	p.SetLanguage(l)
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

func compare(t *testing.T, a, b *sitter.Node, depth int) {
	t.Helper()
	if desc(a) != desc(b) {
		t.Fatalf("trees diverge at depth %d:\n  vendored: %s\n  original: %s", depth, desc(a), desc(b))
	}
	for i := 0; i < int(a.ChildCount()); i++ {
		compare(t, a.Child(i), b.Child(i), depth+1)
	}
}

func desc(n *sitter.Node) string {
	if n == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s [%d-%d] named=%t error=%t missing=%t extra=%t children=%d",
		n.Type(), n.StartByte(), n.EndByte(), n.IsNamed(), n.IsError(), n.IsMissing(), n.IsExtra(), n.ChildCount())
}
