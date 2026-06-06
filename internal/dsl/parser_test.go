package dsl_test

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lintel/internal/dsl"
)

// --- helpers -------------------------------------------------------------

func parseRules(t *testing.T, file, src string) []*dsl.Rule {
	t.Helper()
	rules, err := dsl.Parse(file, src)
	if err != nil {
		t.Fatalf("Parse(%q) returned unexpected error: %v", file, err)
	}
	return rules
}

// parseOne parses a single-rule source and asserts exactly one rule came back.
func parseOne(t *testing.T, src string) *dsl.Rule {
	t.Helper()
	rules := parseRules(t, "rule.lint", src)
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule, got %d", len(rules))
	}
	return rules[0]
}

func eqStrings(a, b []string) bool {
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

// --- multiple rule blocks -----------------------------------------------

func TestMultipleRuleBlocks(t *testing.T) {
	src := `
rule no_println {
  message "avoid Println"
  severity error
  in go
  pattern { fmt.Println($x) }
}

rule no_printf {
  message "avoid Printf"
  pattern { fmt.Printf($fmt, $args) }
}
`
	rules := parseRules(t, "stuff.lint", src)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	r0 := rules[0]
	if r0.ID != "no_println" {
		t.Errorf("rules[0].ID = %q, want %q", r0.ID, "no_println")
	}
	if r0.Message != "avoid Println" {
		t.Errorf("rules[0].Message = %q", r0.Message)
	}
	if r0.Severity != dsl.Error {
		t.Errorf("rules[0].Severity = %q, want %q", r0.Severity, dsl.Error)
	}
	if !eqStrings(r0.Languages, []string{"go"}) {
		t.Errorf("rules[0].Languages = %v", r0.Languages)
	}
	if r0.Match == nil || r0.Match.Kind != dsl.MatchPattern {
		t.Fatalf("rules[0].Match unexpected: %+v", r0.Match)
	}
	if r0.Match.Pattern != "fmt.Println($x)" {
		t.Errorf("rules[0].Match.Pattern = %q", r0.Match.Pattern)
	}

	r1 := rules[1]
	if r1.ID != "no_printf" {
		t.Errorf("rules[1].ID = %q", r1.ID)
	}
	// Severity not given => defaults to Warning.
	if r1.Severity != dsl.Warning {
		t.Errorf("rules[1].Severity = %q, want default %q", r1.Severity, dsl.Warning)
	}
	if r1.Match.Pattern != "fmt.Printf($fmt, $args)" {
		t.Errorf("rules[1].Match.Pattern = %q", r1.Match.Pattern)
	}
	// File should be propagated from Parse's first argument.
	if r0.File != "stuff.lint" || r1.File != "stuff.lint" {
		t.Errorf("File not propagated: %q / %q", r0.File, r1.File)
	}
}

// --- bare (unwrapped) rule: id derived from filename --------------------

func TestBareRuleIDFromFilename(t *testing.T) {
	src := `
message "bare message"
pattern { foo() }
`
	rules := parseRules(t, "my-rule.lint", src)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	r := rules[0]
	if r.ID != "my-rule" {
		t.Errorf("bare rule ID = %q, want %q", r.ID, "my-rule")
	}
	if r.Message != "bare message" {
		t.Errorf("Message = %q", r.Message)
	}
	if r.Match == nil || r.Match.Pattern != "foo()" {
		t.Errorf("Match = %+v", r.Match)
	}
}

func TestBareRuleIDStripsDirAndExt(t *testing.T) {
	r := parseOneFromFile(t, "/some/dir/check-this.lint", "pattern { foo() }")
	if r.ID != "check-this" {
		t.Errorf("ID = %q, want %q", r.ID, "check-this")
	}
}

func parseOneFromFile(t *testing.T, file, src string) *dsl.Rule {
	t.Helper()
	rules := parseRules(t, file, src)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	return rules[0]
}

// --- all field types -----------------------------------------------------

func TestAllFieldTypes(t *testing.T) {
	src := `
rule everything {
  message "msg here"
  note "extra note"
  severity info
  in go, python, typescript
  pattern { foo($x) }
  where $x matches "bar"
  fix "baz($x)"
  test {
    match "foo(1)"
    no_match "qux(1)"
  }
}
`
	r := parseOne(t, src)
	if r.ID != "everything" {
		t.Errorf("ID = %q", r.ID)
	}
	if r.Message != "msg here" {
		t.Errorf("Message = %q", r.Message)
	}
	if r.Note != "extra note" {
		t.Errorf("Note = %q", r.Note)
	}
	if r.Severity != dsl.Info {
		t.Errorf("Severity = %q, want %q", r.Severity, dsl.Info)
	}
	if !eqStrings(r.Languages, []string{"go", "python", "typescript"}) {
		t.Errorf("Languages = %v", r.Languages)
	}
	if r.Match == nil || r.Match.Kind != dsl.MatchPattern || r.Match.Pattern != "foo($x)" {
		t.Errorf("Match = %+v", r.Match)
	}
	if len(r.Where) != 1 {
		t.Fatalf("Where len = %d, want 1", len(r.Where))
	}
	if r.Where[0].Kind != dsl.ConRegex || r.Where[0].Var != "x" || r.Where[0].Text != "bar" {
		t.Errorf("Where[0] = %+v", r.Where[0])
	}
	if r.Fix == nil {
		t.Fatalf("Fix is nil")
	}
	if *r.Fix != "baz($x)" {
		t.Errorf("Fix = %q", *r.Fix)
	}
	if len(r.Tests) != 2 {
		t.Fatalf("Tests len = %d, want 2", len(r.Tests))
	}
	if r.Tests[0].Expect != dsl.ExpectMatch || r.Tests[0].Code != "foo(1)" || r.Tests[0].Count != -1 {
		t.Errorf("Tests[0] = %+v", r.Tests[0])
	}
	if r.Tests[1].Expect != dsl.ExpectNoMatch || r.Tests[1].Code != "qux(1)" {
		t.Errorf("Tests[1] = %+v", r.Tests[1])
	}
}

// --- field aliases -------------------------------------------------------

func TestFieldAliases(t *testing.T) {
	src := `
rule aliases {
  msg "via msg"
  help "via help"
  language go
  languages python
  pattern { foo() }
  tests {
    bad "foo()"
    ok "bar()"
  }
}
`
	r := parseOne(t, src)
	if r.Message != "via msg" {
		t.Errorf("msg alias: Message = %q", r.Message)
	}
	if r.Note != "via help" {
		t.Errorf("help alias: Note = %q", r.Note)
	}
	if !eqStrings(r.Languages, []string{"go", "python"}) {
		t.Errorf("language/languages aliases: %v", r.Languages)
	}
	if len(r.Tests) != 2 {
		t.Fatalf("Tests len = %d", len(r.Tests))
	}
	if r.Tests[0].Expect != dsl.ExpectMatch {
		t.Errorf("'bad' should be ExpectMatch, got %v", r.Tests[0].Expect)
	}
	if r.Tests[1].Expect != dsl.ExpectNoMatch {
		t.Errorf("'ok' should be ExpectNoMatch, got %v", r.Tests[1].Expect)
	}
}

// --- pattern body forms --------------------------------------------------

func TestPatternBodyForms(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"brace", `pattern { foo($x) }`},
		{"triple", `pattern """foo($x)"""`},
		{"double", `pattern "foo($x)"`},
		{"backtick", "pattern `foo($x)`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parseOne(t, tc.src)
			if r.Match == nil || r.Match.Kind != dsl.MatchPattern {
				t.Fatalf("Match = %+v", r.Match)
			}
			if r.Match.Pattern != "foo($x)" {
				t.Errorf("Pattern = %q, want %q", r.Match.Pattern, "foo($x)")
			}
		})
	}
}

func TestPatternBraceMultilineDedent(t *testing.T) {
	src := `pattern {
    if $x != nil {
        return $x
    }
}`
	r := parseOne(t, src)
	want := "if $x != nil {\n    return $x\n}"
	if r.Match.Pattern != want {
		t.Errorf("Pattern = %q, want %q", r.Match.Pattern, want)
	}
}

func TestMessageStringEscapes(t *testing.T) {
	// Double-quoted strings process \n, \t, \" and \\ escapes.
	src := `
message "use \"foo\" not\nbar\ttab\\end"
pattern { x() }
`
	r := parseOne(t, src)
	want := "use \"foo\" not\nbar\ttab\\end"
	if r.Message != want {
		t.Errorf("Message = %q, want %q", r.Message, want)
	}
}

func TestBacktickAndTripleAreRaw(t *testing.T) {
	// Backtick strings do not process escapes.
	r := parseOne(t, "message `raw \\n stays`\npattern { x() }")
	if r.Message != `raw \n stays` {
		t.Errorf("backtick Message = %q", r.Message)
	}
}

// --- query matcher -------------------------------------------------------

func TestQueryMatcher(t *testing.T) {
	src := `
rule q {
  query "(call_expression function: (identifier) @match)"
}
`
	r := parseOne(t, src)
	if r.Match == nil || r.Match.Kind != dsl.MatchQuery {
		t.Fatalf("Match = %+v", r.Match)
	}
	if r.Match.Query != "(call_expression function: (identifier) @match)" {
		t.Errorf("Query = %q", r.Match.Query)
	}
}

// --- where operators -----------------------------------------------------

func TestWhereOperators(t *testing.T) {
	cases := []struct {
		name   string
		clause string
		check  func(t *testing.T, c dsl.Constraint)
	}{
		{"matches", `where $x matches "foo"`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConRegex || c.Var != "x" || c.Text != "foo" {
				t.Errorf("got %+v", c)
			}
		}},
		{"not_matches", `where $x not matches "foo"`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNotRegex || c.Text != "foo" {
				t.Errorf("got %+v", c)
			}
		}},
		{"kind", `where $x kind identifier`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConKind || c.Text != "identifier" {
				t.Errorf("got %+v", c)
			}
		}},
		{"not_kind", `where $x not kind call_expression`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNotKind || c.Text != "call_expression" {
				t.Errorf("got %+v", c)
			}
		}},
		{"in", `where $x in [a, b, "c"]`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConIn || !eqStrings(c.List, []string{"a", "b", "c"}) {
				t.Errorf("got %+v", c)
			}
		}},
		{"not_in", `where $x not in [a, b]`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNotIn || !eqStrings(c.List, []string{"a", "b"}) {
				t.Errorf("got %+v", c)
			}
		}},
		{"eq_var", `where $x == $y`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConEqVar || c.Other != "y" {
				t.Errorf("got %+v", c)
			}
		}},
		{"neq_var", `where $x != $y`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNeqVar || c.Other != "y" {
				t.Errorf("got %+v", c)
			}
		}},
		{"eq_text", `where $x == "lit"`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConEqText || c.Text != "lit" {
				t.Errorf("got %+v", c)
			}
		}},
		{"neq_text", `where $x != "lit"`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNeqText || c.Text != "lit" {
				t.Errorf("got %+v", c)
			}
		}},
		{"is_pattern", `where $x is pattern { bar($x) }`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConPattern {
				t.Fatalf("Kind = %v", c.Kind)
			}
			if c.Matcher == nil || c.Matcher.Kind != dsl.MatchPattern || c.Matcher.Pattern != "bar($x)" {
				t.Errorf("Matcher = %+v", c.Matcher)
			}
		}},
		{"is_not_pattern", `where $x is not pattern { bar() }`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNotPattern {
				t.Fatalf("Kind = %v", c.Kind)
			}
			if c.Matcher == nil || c.Matcher.Kind != dsl.MatchPattern || c.Matcher.Pattern != "bar()" {
				t.Errorf("Matcher = %+v", c.Matcher)
			}
		}},
		{"is_query", `where $x is query "(identifier) @x"`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConPattern {
				t.Fatalf("Kind = %v", c.Kind)
			}
			if c.Matcher == nil || c.Matcher.Kind != dsl.MatchQuery || c.Matcher.Query != "(identifier) @x" {
				t.Errorf("Matcher = %+v", c.Matcher)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "pattern { foo($x) }\n" + tc.clause + "\n"
			r := parseOne(t, src)
			if len(r.Where) != 1 {
				t.Fatalf("Where len = %d, want 1", len(r.Where))
			}
			tc.check(t, r.Where[0])
		})
	}
}

func TestMultipleWhereAccumulate(t *testing.T) {
	src := `
rule w {
  pattern { foo($x) }
  where $x matches "a"
  where $x not matches "b"
}
`
	r := parseOne(t, src)
	// where clauses must not turn the single pattern into an AND combinator.
	if r.Match.Kind != dsl.MatchPattern {
		t.Errorf("Match.Kind = %v, want MatchPattern", r.Match.Kind)
	}
	if len(r.Where) != 2 {
		t.Fatalf("Where len = %d, want 2", len(r.Where))
	}
	if r.Where[0].Kind != dsl.ConRegex || r.Where[1].Kind != dsl.ConNotRegex {
		t.Errorf("Where kinds = %v, %v", r.Where[0].Kind, r.Where[1].Kind)
	}
}

// --- relations: inside / has and negations ------------------------------

func TestRelationsInsideHas(t *testing.T) {
	src := `
rule rel {
  pattern { foo($x) }
  inside pattern { func $f() $body }
  has pattern { bar() }
  not inside pattern { test() }
  not has pattern { baz() }
}
`
	r := parseOne(t, src)
	if r.Match.Kind != dsl.MatchPattern {
		t.Errorf("Match.Kind = %v", r.Match.Kind)
	}
	if len(r.Relations) != 4 {
		t.Fatalf("Relations len = %d, want 4", len(r.Relations))
	}
	wantKinds := []dsl.RelationKind{dsl.RelInside, dsl.RelHas, dsl.RelNotInside, dsl.RelNotHas}
	wantPats := []string{"func $f() $body", "bar()", "test()", "baz()"}
	for i, rel := range r.Relations {
		if rel.Kind != wantKinds[i] {
			t.Errorf("Relations[%d].Kind = %v, want %v", i, rel.Kind, wantKinds[i])
		}
		if rel.Matcher == nil || rel.Matcher.Kind != dsl.MatchPattern {
			t.Fatalf("Relations[%d].Matcher = %+v", i, rel.Matcher)
		}
		if rel.Matcher.Pattern != wantPats[i] {
			t.Errorf("Relations[%d].Matcher.Pattern = %q, want %q", i, rel.Matcher.Pattern, wantPats[i])
		}
	}
}

// --- any / all / not -----------------------------------------------------

func TestAnyCombinator(t *testing.T) {
	src := `
rule a {
  any {
    pattern { foo() }
    pattern { bar() }
  }
}
`
	r := parseOne(t, src)
	if r.Match.Kind != dsl.MatchAny {
		t.Fatalf("Match.Kind = %v, want MatchAny", r.Match.Kind)
	}
	if len(r.Match.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(r.Match.Children))
	}
	if r.Match.Children[0].Pattern != "foo()" || r.Match.Children[1].Pattern != "bar()" {
		t.Errorf("children patterns = %q, %q", r.Match.Children[0].Pattern, r.Match.Children[1].Pattern)
	}
}

func TestAllCombinator(t *testing.T) {
	src := `
rule a {
  all {
    pattern { foo() }
    pattern { bar() }
  }
}
`
	r := parseOne(t, src)
	if r.Match.Kind != dsl.MatchAll {
		t.Fatalf("Match.Kind = %v, want MatchAll", r.Match.Kind)
	}
	if len(r.Match.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(r.Match.Children))
	}
}

func TestNotMatcherCombinesWithPattern(t *testing.T) {
	src := `
rule n {
  pattern { foo($x) }
  not pattern { bar() }
}
`
	r := parseOne(t, src)
	// Two matchers at rule level => implicit AND.
	if r.Match.Kind != dsl.MatchAll {
		t.Fatalf("Match.Kind = %v, want MatchAll", r.Match.Kind)
	}
	if len(r.Match.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(r.Match.Children))
	}
	if r.Match.Children[0].Kind != dsl.MatchPattern {
		t.Errorf("child[0].Kind = %v", r.Match.Children[0].Kind)
	}
	not := r.Match.Children[1]
	if not.Kind != dsl.MatchNot {
		t.Fatalf("child[1].Kind = %v, want MatchNot", not.Kind)
	}
	if not.Sub == nil || not.Sub.Kind != dsl.MatchPattern || not.Sub.Pattern != "bar()" {
		t.Errorf("not.Sub = %+v", not.Sub)
	}
}

func TestNestedCombinators(t *testing.T) {
	src := `
rule nested {
  any {
    pattern { a() }
    not pattern { b() }
    all {
      pattern { c() }
      pattern { d() }
    }
  }
}
`
	r := parseOne(t, src)
	if r.Match.Kind != dsl.MatchAny {
		t.Fatalf("Match.Kind = %v", r.Match.Kind)
	}
	if len(r.Match.Children) != 3 {
		t.Fatalf("children = %d, want 3", len(r.Match.Children))
	}
	if r.Match.Children[0].Kind != dsl.MatchPattern {
		t.Errorf("child[0] = %v", r.Match.Children[0].Kind)
	}
	if r.Match.Children[1].Kind != dsl.MatchNot {
		t.Errorf("child[1] = %v", r.Match.Children[1].Kind)
	}
	inner := r.Match.Children[2]
	if inner.Kind != dsl.MatchAll {
		t.Fatalf("child[2].Kind = %v, want MatchAll", inner.Kind)
	}
	if len(inner.Children) != 2 {
		t.Errorf("inner children = %d, want 2", len(inner.Children))
	}
}

func TestMultiplePatternsImplicitAnd(t *testing.T) {
	src := `
rule multi {
  pattern { foo($x) }
  pattern { bar($x) }
}
`
	r := parseOne(t, src)
	if r.Match.Kind != dsl.MatchAll {
		t.Fatalf("Match.Kind = %v, want MatchAll", r.Match.Kind)
	}
	if len(r.Match.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(r.Match.Children))
	}
	for i, c := range r.Match.Children {
		if c.Kind != dsl.MatchPattern {
			t.Errorf("child[%d].Kind = %v", i, c.Kind)
		}
	}
}

// --- test blocks ---------------------------------------------------------

func TestTestBlockMatchNoMatchAndCount(t *testing.T) {
	src := `
rule t {
  pattern { foo() }
  test {
    match "foo()"
    match "foo(); foo()" count 2
    no_match "bar()"
  }
}
`
	r := parseOne(t, src)
	if len(r.Tests) != 3 {
		t.Fatalf("Tests len = %d, want 3", len(r.Tests))
	}
	if r.Tests[0].Expect != dsl.ExpectMatch || r.Tests[0].Code != "foo()" || r.Tests[0].Count != -1 {
		t.Errorf("Tests[0] = %+v", r.Tests[0])
	}
	if r.Tests[1].Expect != dsl.ExpectMatch || r.Tests[1].Code != "foo(); foo()" || r.Tests[1].Count != 2 {
		t.Errorf("Tests[1] = %+v", r.Tests[1])
	}
	if r.Tests[2].Expect != dsl.ExpectNoMatch || r.Tests[2].Code != "bar()" || r.Tests[2].Count != -1 {
		t.Errorf("Tests[2] = %+v", r.Tests[2])
	}
}

func TestTestEntryAliases(t *testing.T) {
	src := `
rule t {
  pattern { foo() }
  test {
    flag "foo()"
    good "bar()"
    clean "baz()"
  }
}
`
	r := parseOne(t, src)
	if len(r.Tests) != 3 {
		t.Fatalf("Tests len = %d", len(r.Tests))
	}
	if r.Tests[0].Expect != dsl.ExpectMatch {
		t.Errorf("flag => %v", r.Tests[0].Expect)
	}
	if r.Tests[1].Expect != dsl.ExpectNoMatch || r.Tests[2].Expect != dsl.ExpectNoMatch {
		t.Errorf("good/clean => %v, %v", r.Tests[1].Expect, r.Tests[2].Expect)
	}
}

// --- severity defaults and aliases --------------------------------------

func TestSeverityValues(t *testing.T) {
	cases := []struct {
		clause string
		want   dsl.Severity
	}{
		{"", dsl.Warning}, // default
		{"severity error", dsl.Error},
		{"severity warning", dsl.Warning},
		{"severity warn", dsl.Warning},
		{"severity info", dsl.Info},
	}
	for _, tc := range cases {
		name := tc.clause
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			src := tc.clause + "\npattern { foo() }\n"
			r := parseOne(t, src)
			if r.Severity != tc.want {
				t.Errorf("Severity = %q, want %q", r.Severity, tc.want)
			}
		})
	}
}

func TestMessageDefaultsToID(t *testing.T) {
	src := `
rule only_pattern {
  pattern { foo() }
}
`
	r := parseOne(t, src)
	if r.Message != "only_pattern" {
		t.Errorf("Message = %q, want default of ID %q", r.Message, "only_pattern")
	}
	if r.Severity != dsl.Warning {
		t.Errorf("Severity = %q, want default Warning", r.Severity)
	}
}

// --- in clause accumulation ---------------------------------------------

func TestLanguagesAccumulate(t *testing.T) {
	src := `
rule langs {
  in go, python
  in rust
  pattern { foo() }
}
`
	r := parseOne(t, src)
	if !eqStrings(r.Languages, []string{"go", "python", "rust"}) {
		t.Errorf("Languages = %v", r.Languages)
	}
}

// --- comments and empty input -------------------------------------------

func TestCommentsAreSkipped(t *testing.T) {
	src := `
rule c {
  # a hash line comment
  message "hi" // a trailing slash comment
  /* a block
     comment */ pattern { foo() }
}
`
	r := parseOne(t, src)
	if r.Message != "hi" {
		t.Errorf("Message = %q", r.Message)
	}
	if r.Match == nil || r.Match.Pattern != "foo()" {
		t.Errorf("Match = %+v", r.Match)
	}
}

func TestEmptyInputs(t *testing.T) {
	for _, src := range []string{"", "   \n\t  ", "# only a comment\n", "/* block only */"} {
		rules, err := dsl.Parse("empty.lint", src)
		if err != nil {
			t.Errorf("Parse(%q) error = %v, want nil", src, err)
		}
		if len(rules) != 0 {
			t.Errorf("Parse(%q) returned %d rules, want 0", src, len(rules))
		}
	}
}

// --- malformed inputs (must error) --------------------------------------

func TestMalformedInputs(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		src      string
		contains string
	}{
		{
			name:     "unknown_field",
			src:      `rule r { bogus "x" pattern { foo() } }`,
			contains: "unknown field",
		},
		{
			name:     "missing_pattern",
			src:      `rule r { message "hi" }`,
			contains: "no pattern/query",
		},
		{
			name:     "bad_severity",
			src:      `rule r { severity loud pattern { foo() } }`,
			contains: "unknown severity",
		},
		{
			name:     "unterminated_double_string",
			src:      "rule r {\n  message \"oops\n  pattern { foo() }\n}",
			contains: "unterminated",
		},
		{
			name:     "unterminated_triple_string",
			src:      `pattern """foo($x)`,
			contains: "unterminated",
		},
		{
			name:     "unterminated_backtick_string",
			src:      "pattern `foo($x)",
			contains: "unterminated",
		},
		{
			name:     "unbalanced_braces_in_body",
			src:      "pattern { if x {",
			contains: "unbalanced",
		},
		{
			name:     "unbalanced_rule_block",
			src:      "rule r {\n  pattern { foo() }",
			contains: "expected '}'",
		},
		{
			name:     "empty_any",
			src:      `rule r { any { } }`,
			contains: "needs at least one matcher",
		},
		{
			name:     "single_equals",
			src:      `rule r { pattern { foo($x) } where $x = "y" }`,
			contains: "expected '=='",
		},
		{
			name:     "bad_matcher_after_inside",
			src:      `rule r { pattern { foo() } inside bogus }`,
			contains: "expected a matcher",
		},
		{
			name:     "metavar_missing_name",
			src:      `rule r { pattern { foo() } where $ matches "y" }`,
			contains: "metavariable name",
		},
		{
			name:     "unknown_test_entry",
			src:      `rule r { pattern { foo() } test { bogus "x" } }`,
			contains: "unknown test entry",
		},
		{
			name:     "unknown_constraint_operator",
			src:      `rule r { pattern { foo($x) } where $x frobnicate "y" }`,
			contains: "unknown constraint operator",
		},
		{
			name:     "rule_missing_id",
			src:      `rule { pattern { foo() } }`,
			contains: "expected an identifier",
		},
		{
			name:     "second_block_not_rule",
			src:      "rule a { pattern { foo() } }\nbogus b { pattern { bar() } }",
			contains: "expected another 'rule' block",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := tc.file
			if file == "" {
				file = "r.lint"
			}
			rules, err := dsl.Parse(file, tc.src)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded (got %d rules), want error", tc.src, len(rules))
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.contains)
			}
		})
	}
}
