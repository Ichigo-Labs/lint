package runner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/engine"
)

// writeFile creates path's parent directories and writes content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// setupProject builds a fake project tree under a fresh temp dir and chdirs into
// it so that reported file paths are relative to the project root. It returns the
// absolute project root.
//
// Layout (rules live only in the project-root .lint/ and apply to the whole tree):
//
//	<root>/
//	  .lint/console.lint     rule no_console (typescript): console.log($$$)
//	  .lint/panic.lint       rule no_panic (go): panic($$$)
//	  src/
//	    app.ts                 console.log -> flagged by no_console
//	    util.go                no panic / no console -> clean
//	    notes.txt              console.log text, but .txt is not a known language
//	  pkg/
//	    core.go                panic -> flagged by no_panic
//	    widget.ts              console.log -> flagged by no_console
//	  other/
//	    helper.go              panic -> flagged by no_panic (root rules apply everywhere)
func setupProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// On macOS the temp dir is a symlink (/var -> /private/var); resolve it so
	// the chdir'd cwd and the walked absolute paths agree and reportPath can
	// produce relative paths.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	writeFile(t, filepath.Join(root, ".lint", "console.lint"), `rule no_console {
  in typescript
  message "avoid console.log"
  pattern { console.log($$$) }
}`)

	writeFile(t, filepath.Join(root, ".lint", "panic.lint"), `rule no_panic {
  in go
  message "avoid panic"
  pattern { panic($$$) }
}`)

	writeFile(t, filepath.Join(root, "src", "app.ts"), `console.log("hi");`+"\n")
	writeFile(t, filepath.Join(root, "src", "util.go"), `package main

func f() {
	x := 1
	_ = x
}
`)
	writeFile(t, filepath.Join(root, "src", "notes.txt"), `console.log("ignored");`+"\n")

	writeFile(t, filepath.Join(root, "pkg", "core.go"), `package pkg

func g() {
	panic("boom")
}
`)
	writeFile(t, filepath.Join(root, "pkg", "widget.ts"), `console.log("w");`+"\n")

	writeFile(t, filepath.Join(root, "other", "helper.go"), `package other

func h() {
	panic("x")
}
`)

	t.Chdir(root)
	return root
}

// findingKey identifies a finding by its reported file and the rule that fired.
func findingKey(f engine.Finding) string { return f.File + "|" + f.RuleID }

func collectKeys(fs []engine.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, findingKey(f))
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
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

// TestLoadDiscoversRules checks that Load finds every rule in the project-root
// .lint directory.
func TestLoadDiscoversRules(t *testing.T) {
	setupProject(t)

	rs, lerrs, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(lerrs) != 0 {
		t.Fatalf("unexpected load errors: %v", lerrs)
	}
	if rs.Len() != 2 {
		t.Fatalf("expected 2 rules discovered, got %d", rs.Len())
	}

	var ids []string
	for _, r := range rs.Rules() {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	want := []string{"no_console", "no_panic"}
	if !equalStrings(ids, want) {
		t.Fatalf("rule ids = %v, want %v", ids, want)
	}
}

// TestCheckAppliesAcrossTree verifies that:
//   - root .lint rules apply across the whole tree (src/, pkg/, and other/),
//   - findings carry file paths relative to the working directory,
//   - files with unknown extensions (.txt) are not selected.
func TestCheckAppliesAcrossTree(t *testing.T) {
	setupProject(t)

	rs, lerrs, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(lerrs) != 0 {
		t.Fatalf("unexpected load errors: %v", lerrs)
	}

	res, err := rs.Check(Options{Paths: []string{"."}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.FileErrs) != 0 {
		t.Fatalf("unexpected file errors: %v", res.FileErrs)
	}

	got := collectKeys(res.Findings)
	want := []string{
		filepath.Join("other", "helper.go") + "|no_panic", // root rule reaches every dir
		filepath.Join("pkg", "core.go") + "|no_panic",
		filepath.Join("pkg", "widget.ts") + "|no_console",
		filepath.Join("src", "app.ts") + "|no_console",
	}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}

	// Files with unknown extensions (.txt) are never selected.
	for _, f := range res.Findings {
		if filepath.Ext(f.File) == ".txt" {
			t.Fatalf("unexpected finding in non-source file %s", f.File)
		}
	}

	// Sources are keyed by the same relative path as the findings.
	for _, f := range res.Findings {
		if _, ok := res.Sources[f.File]; !ok {
			t.Fatalf("missing source contents for flagged file %s", f.File)
		}
	}
}

// TestLanguageFiltering checks that Options.LangFilter restricts which files are
// analyzed by language.
func TestLanguageFiltering(t *testing.T) {
	setupProject(t)

	rs, _, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	goRes, err := rs.Check(Options{Paths: []string{"."}, LangFilter: map[string]bool{"go": true}})
	if err != nil {
		t.Fatalf("Check go: %v", err)
	}
	gotGo := collectKeys(goRes.Findings)
	wantGo := []string{
		filepath.Join("other", "helper.go") + "|no_panic",
		filepath.Join("pkg", "core.go") + "|no_panic",
	}
	sort.Strings(wantGo)
	if !equalStrings(gotGo, wantGo) {
		t.Fatalf("go-filtered findings = %v, want %v", gotGo, wantGo)
	}

	tsRes, err := rs.Check(Options{Paths: []string{"."}, LangFilter: map[string]bool{"typescript": true}})
	if err != nil {
		t.Fatalf("Check ts: %v", err)
	}
	gotTS := collectKeys(tsRes.Findings)
	wantTS := []string{
		filepath.Join("pkg", "widget.ts") + "|no_console",
		filepath.Join("src", "app.ts") + "|no_console",
	}
	sort.Strings(wantTS)
	if !equalStrings(gotTS, wantTS) {
		t.Fatalf("ts-filtered findings = %v, want %v", gotTS, wantTS)
	}
}

// TestExtensionMappingSelectsFiles isolates the lang.ForPath behavior: two files
// with identical content differ only by extension; only the one whose extension
// maps to a known language is analyzed.
func TestExtensionMappingSelectsFiles(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule no_console {
  in typescript
  pattern { console.log($$$) }
}`)
	body := `console.log("x");` + "\n"
	writeFile(t, filepath.Join(root, "good.ts"), body)  // .ts -> typescript
	writeFile(t, filepath.Join(root, "skip.txt"), body) // .txt -> no language
	writeFile(t, filepath.Join(root, "skip.md"), body)  // .md  -> no language
	t.Chdir(root)

	rs, _, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res, err := rs.Check(Options{Paths: []string{"."}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := collectKeys(res.Findings)
	want := []string{"good.ts|no_console"}
	if !equalStrings(got, want) {
		t.Fatalf("findings = %v, want %v (only the .ts file should be selected)", got, want)
	}
}

// TestRulesDirOverride checks that an explicit --rules directory replaces
// discovery (the project-root .lint/ is ignored) and applies to every file.
func TestRulesDirOverride(t *testing.T) {
	root := setupProject(t)

	// A rules directory outside the discovered .lint/, holding only no_panic.
	override := filepath.Join(root, "altrules")
	writeFile(t, filepath.Join(override, "p.lint"), `rule no_panic {
  in go
  message "avoid panic"
  pattern { panic($$$) }
}`)

	rs, lerrs, err := Load(".", override)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(lerrs) != 0 {
		t.Fatalf("unexpected load errors: %v", lerrs)
	}
	if rs.Len() != 1 {
		t.Fatalf("expected only the overridden rule, got %d", rs.Len())
	}

	res, err := rs.Check(Options{Paths: []string{"."}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Only no_panic ran (root's no_console was not discovered), and it fired
	// across the whole tree.
	got := collectKeys(res.Findings)
	want := []string{
		filepath.Join("other", "helper.go") + "|no_panic",
		filepath.Join("pkg", "core.go") + "|no_panic",
	}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("override findings = %v, want %v", got, want)
	}
}

// TestInlineSuppression checks that lint:ignore / lint:ignore-next-line comments
// silence findings on the targeted line, by rule id or for all rules.
func TestInlineSuppression(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule no_panic {
  in go
  pattern { panic($$$) }
}`)
	writeFile(t, filepath.Join(root, ".lint", "todo.lint"), `rule no_todo {
  in go
  pattern { TODO($$$) }
}`)
	writeFile(t, filepath.Join(root, "a.go"), `package p
func f() {
	panic(1) // lint:ignore no_panic
	panic(2)
	// lint:ignore-next-line
	panic(3)
	panic(4) // lint:ignore
	panic(5) // lint:ignore other_rule
	panic(6); TODO() // lint:ignore no_panic , no_todo because legacy
}
`)
	t.Chdir(root)
	rs, _, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res, err := rs.Check(Options{Paths: []string{"."}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Suppressed: panic(1) by id, panic(3) by next-line, panic(4) by bare ignore,
	// and both panic(6)+TODO() by a spaced, comma-separated list with a trailing
	// explanation. Not suppressed: panic(2), and panic(5) (different rule named).
	var lines []int
	for _, f := range res.Findings {
		lines = append(lines, f.StartLine)
	}
	sort.Ints(lines)
	if len(lines) != 2 || lines[0] != 4 || lines[1] != 8 {
		t.Fatalf("remaining finding lines = %v, want [4 8]", lines)
	}
}

// TestProjectConfig checks that .lint.toml disables a rule and overrides another
// rule's severity.
func TestProjectConfig(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule no_panic {
  in go
  severity error
  pattern { panic($$$) }
}
rule no_todo {
  in go
  pattern { TODO($$$) }
}`)
	writeFile(t, filepath.Join(root, ".lint.toml"), `[project]
disable = ["no_todo"]

[rules.no_panic]
severity = "warning"
`)
	writeFile(t, filepath.Join(root, "a.go"), `package p
func f() {
	panic(1)
	TODO()
}
`)
	t.Chdir(root)
	rs, _, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res, err := rs.Check(Options{Paths: []string{"."}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %d, want 1 (no_todo disabled): %v", len(res.Findings), collectKeys(res.Findings))
	}
	f := res.Findings[0]
	if f.RuleID != "no_panic" {
		t.Fatalf("RuleID = %q, want no_panic", f.RuleID)
	}
	if f.Severity != dsl.Warning {
		t.Fatalf("Severity = %q, want overridden warning", f.Severity)
	}
}

// TestTagFilter checks that Options.TagFilter restricts which rules run.
func TestTagFilter(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule no_panic {
  in go
  tags safety
  pattern { panic($$$) }
}
rule no_todo {
  in go
  tags style
  pattern { TODO($$$) }
}`)
	writeFile(t, filepath.Join(root, "a.go"), "package p\nfunc f() { panic(1); TODO() }\n")
	t.Chdir(root)
	rs, _, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res, err := rs.Check(Options{Paths: []string{"."}, TagFilter: map[string]bool{"safety": true}})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	got := collectKeys(res.Findings)
	want := []string{"a.go|no_panic"}
	if !equalStrings(got, want) {
		t.Fatalf("tag-filtered findings = %v, want %v", got, want)
	}
}

// TestRunRuleTests checks that inline test { ... } cases are evaluated, covering
// match, no_match, an exact `count`, and a deliberately failing case.
func TestRunRuleTests(t *testing.T) {
	src := `rule sample {
  in typescript
  pattern { console.log($$$) }
  test {
    match { console.log(1); }
    no_match { console.error(1); }
    match { console.log(1); console.log(2); } count 2
    no_match { console.log(99); }
  }
}`
	rules, err := dsl.Parse("sample.lint", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	cr, err := engine.Compile(rules[0])
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	results := RunRuleTests(cr)
	if len(results) != 4 {
		t.Fatalf("expected 4 case results, got %d", len(results))
	}

	type expect struct {
		pass bool
		got  int
	}
	wants := []expect{
		{pass: true, got: 1},  // match console.log(1)
		{pass: true, got: 0},  // no_match console.error(1)
		{pass: true, got: 2},  // match ... count 2
		{pass: false, got: 1}, // no_match but it actually matches -> fail
	}
	for i, w := range wants {
		r := results[i]
		if r.RuleID != "sample" {
			t.Errorf("case %d: RuleID = %q, want %q", i, r.RuleID, "sample")
		}
		if r.Index != i {
			t.Errorf("case %d: Index = %d, want %d", i, r.Index, i)
		}
		if r.Pass != w.pass {
			t.Errorf("case %d: Pass = %v, want %v (got=%d)", i, r.Pass, w.pass, r.Got)
		}
		if r.Got != w.got {
			t.Errorf("case %d: Got = %d, want %d", i, r.Got, w.got)
		}
	}
}
