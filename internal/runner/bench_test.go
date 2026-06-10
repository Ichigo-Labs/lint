package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchProject writes a synthetic project: nGo Go files and nTS TypeScript
// files of ~moderate size, plus a ruleset. A fraction of files contain
// matches; the rest exercise the no-finding path.
func benchProject(b *testing.B, nGo, nTS int) string {
	b.Helper()
	dir := b.TempDir()
	mustWrite := func(path, content string) {
		b.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	mustWrite(filepath.Join(dir, ".lint", "rules.lint"), `
rule no-panic {
  message  "no panic"
  in       go
  pattern  { panic($$$) }
}
rule no-console {
  message  "no console"
  in       typescript, tsx
  pattern  { console.log($$$) }
}
rule no-eval {
  message  "no eval"
  in       typescript, tsx
  pattern  { eval($X) }
}
rule absent {
  message  "never fires"
  in       go, typescript, tsx
  pattern  { veryAbsentCall($$$) }
}
`)
	var goSrc strings.Builder
	goSrc.WriteString("package p\n\nimport \"fmt\"\n\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&goSrc, "func f%d(x int) {\n\tif x > %d {\n\t\tfmt.Println(\"v\", x)\n\t}\n}\n\n", i, i)
	}
	goMatch := goSrc.String() + "func boom() { panic(\"x\") }\n"
	var tsSrc strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&tsSrc, "export function g%d(x: number): number {\n  return x * %d;\n}\n\n", i, i)
	}
	tsMatch := tsSrc.String() + "console.log('dbg');\n"

	for i := 0; i < nGo; i++ {
		src := goSrc.String()
		if i%10 == 0 {
			src = goMatch
		}
		mustWrite(filepath.Join(dir, "go", fmt.Sprintf("f%03d.go", i)), src)
	}
	for i := 0; i < nTS; i++ {
		src := tsSrc.String()
		if i%10 == 0 {
			src = tsMatch
		}
		mustWrite(filepath.Join(dir, "ts", fmt.Sprintf("g%03d.ts", i)), src)
	}
	return dir
}

func benchCheck(b *testing.B, nGo, nTS int) {
	dir := benchProject(b, nGo, nTS)
	rs, lerrs, err := Load(dir, filepath.Join(dir, ".lint"))
	if err != nil {
		b.Fatal(err)
	}
	if len(lerrs) > 0 {
		b.Fatalf("load errors: %v", lerrs)
	}
	res, err := rs.Check(Options{Paths: []string{dir}})
	if err != nil {
		b.Fatal(err)
	}
	if len(res.Findings) == 0 {
		b.Fatal("expected findings")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := rs.Check(Options{Paths: []string{dir}}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheck200(b *testing.B)  { benchCheck(b, 100, 100) }
func BenchmarkCheck1000(b *testing.B) { benchCheck(b, 500, 500) }
