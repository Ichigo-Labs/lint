package treesit

import (
	"context"
	"fmt"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/lang"
)

// benchSource replicates engine's benchmark corpus (benchSource(200) in
// internal/engine/bench_test.go) so Extract is measured on the same tree the
// engine benchmarks use.
func benchSource(n int) []byte {
	var b strings.Builder
	b.WriteString("package bench\n\nimport \"fmt\"\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `
func handler%d(x int) error {
	v := compute(x, %d)
	if v < 0 {
		return fmt.Errorf("negative: %%d", v)
	}
	for j := 0; j < v; j++ {
		process(j, "item")
	}
	if x == %d {
		panic("boom")
	}
	return nil
}
`, i, i, i)
	}
	return []byte(b.String())
}

// BenchmarkExtract measures the full flat serialization: one cgo walk plus
// the pure-Go CSR and SubtreeEnd builds. This is the whole per-file cost of
// what used to be a cgo-call-per-node index build.
func BenchmarkExtract(b *testing.B) {
	l, _ := lang.Get("go")
	parser := sitter.NewParser()
	parser.SetLanguage(l.Grammar())
	tree, err := parser.ParseCtx(context.Background(), nil, benchSource(200))
	if err != nil {
		b.Fatal(err)
	}
	nodes := ExtractTree(tree).Len()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractTree(tree)
	}
	b.ReportMetric(float64(nodes), "nodes")
}
