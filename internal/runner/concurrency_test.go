package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestConcurrentCheckNoRace exercises many files against shared compiled rules
// (shared pattern trees) under high concurrency. Run with -race. It guards the
// pattern-tree prewarm that prevents concurrent writes to smacker's lazy node
// cache.
func TestConcurrentCheckNoRace(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".lint"), 0o755)
	rules := `
rule r1 { in go
  pattern { if $ERR == nil { return $ERR } } }
rule r2 { in go
  pattern { fmt.Println($$$) } }
rule r3 { in go
  pattern { []int{$$$} } }
`
	os.WriteFile(filepath.Join(dir, ".lint", "r.lint"), []byte(rules), 0o644)
	for i := 0; i < 120; i++ {
		src := fmt.Sprintf(`package p
import "fmt"
func f%d(err error) error {
	fmt.Println("x", %d)
	xs := []int{1, 2, 3}
	_ = xs
	if err == nil {
		return err
	}
	return nil
}
`, i, i)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), []byte(src), 0o644)
	}
	rs, lerrs, err := Load(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lerrs) > 0 {
		t.Fatalf("load errors: %v", lerrs)
	}
	var prev int
	for iter := 0; iter < 8; iter++ {
		res, err := rs.Check(Options{Paths: []string{dir}, Concurrency: 16})
		if err != nil {
			t.Fatal(err)
		}
		if iter == 0 {
			prev = len(res.Findings)
			if prev == 0 {
				t.Fatal("expected findings")
			}
		} else if len(res.Findings) != prev {
			t.Fatalf("nondeterministic findings: %d vs %d", len(res.Findings), prev)
		}
	}
}
