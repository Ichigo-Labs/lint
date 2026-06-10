package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/lang"
)

// benchSource synthesizes a Go file of roughly n function blocks with a mix
// of matching and non-matching code for the benchmark rules.
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

const benchRules = `
rule no-panic {
  message  "no panic"
  in       go
  pattern  { panic($$$) }
}
rule errorf-negative {
  message  "found $V"
  in       go
  pattern  { fmt.Errorf("negative: %d", $V) }
}
rule self-compare {
  message  "self compare"
  in       go
  pattern  { $X == $X }
}
rule absent-call {
  message  "never matches"
  in       go
  pattern  { definitelyNotPresent($$$ARGS) }
}
`

func compileBenchRules(b *testing.B) []*CompiledRule {
	b.Helper()
	rules, err := dsl.Parse("bench.lint", benchRules)
	if err != nil {
		b.Fatal(err)
	}
	var out []*CompiledRule
	for _, r := range rules {
		cr, err := Compile(r)
		if err != nil {
			b.Fatal(err)
		}
		out = append(out, cr)
	}
	return out
}

func BenchmarkParseSource(b *testing.B) {
	l, _ := lang.Get("go")
	src := benchSource(200)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree, err := ParseSource(l, src)
		if err != nil {
			b.Fatal(err)
		}
		_ = tree
	}
}

func BenchmarkBuildIndex(b *testing.B) {
	l, _ := lang.Get("go")
	src := benchSource(200)
	tree, err := ParseSource(l, src)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildIndex(l, tree)
	}
}

func BenchmarkRunIndexed(b *testing.B) {
	l, _ := lang.Get("go")
	src := benchSource(200)
	tree, err := ParseSource(l, src)
	if err != nil {
		b.Fatal(err)
	}
	idx := BuildIndex(l, tree)
	crs := compileBenchRules(b)
	// Sanity: the matching rules must actually fire.
	if n := len(crs[0].RunIndexed(l, tree, src, idx)); n == 0 {
		b.Fatal("no-panic rule found nothing")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, cr := range crs {
			cr.RunIndexed(l, tree, src, idx)
		}
	}
}

func BenchmarkCompileImplicitRule(b *testing.B) {
	rules, err := dsl.Parse("bench.lint", `
rule implicit {
  message "m"
  pattern { exec($A, $B) }
}
`)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(rules[0]); err != nil {
			b.Fatal(err)
		}
	}
}
