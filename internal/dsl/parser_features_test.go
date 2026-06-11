package dsl_test

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// --- report ----------------------------------------------------------------

func TestReportField(t *testing.T) {
	r := parseOne(t, `rule r {
  pattern { foo($X) }
  report $X
}`)
	if r.Report != "X" {
		t.Fatalf("Report = %q, want %q", r.Report, "X")
	}
}

func TestFocusAliasForReport(t *testing.T) {
	r := parseOne(t, "pattern { foo($X) }\nfocus $X\n")
	if r.Report != "X" {
		t.Fatalf("Report = %q, want %q", r.Report, "X")
	}
}

// --- directly precedes / follows --------------------------------------------

func TestDirectlySiblingRelations(t *testing.T) {
	r := parseOne(t, `rule r {
  pattern { a() }
  directly precedes pattern { b() }
  not directly follows pattern { c() }
}`)
	if len(r.Relations) != 2 {
		t.Fatalf("Relations len = %d, want 2", len(r.Relations))
	}
	if r.Relations[0].Kind != dsl.RelDirectlyPrecedes {
		t.Errorf("Relations[0].Kind = %v, want RelDirectlyPrecedes", r.Relations[0].Kind)
	}
	if r.Relations[1].Kind != dsl.RelNotDirectlyFollows {
		t.Errorf("Relations[1].Kind = %v, want RelNotDirectlyFollows", r.Relations[1].Kind)
	}
}

// --- kind membership / length / lines ----------------------------------------

func TestWhereKindLengthLinesOperators(t *testing.T) {
	cases := []struct {
		name   string
		clause string
		check  func(t *testing.T, c dsl.Constraint)
	}{
		{"kind_in", `where $x kind in [identifier, call_expression]`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConKindIn || !eqStrings(c.List, []string{"identifier", "call_expression"}) {
				t.Errorf("got %+v", c)
			}
		}},
		{"not_kind_in", `where $x not kind in [identifier]`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConNotKindIn || !eqStrings(c.List, []string{"identifier"}) {
				t.Errorf("got %+v", c)
			}
		}},
		{"kind_named_in", "where $x kind in\n", func(t *testing.T, c dsl.Constraint) {
			// No bracket after `in`: it is the kind name itself.
			if c.Kind != dsl.ConKind || c.Text != "in" {
				t.Errorf("got %+v", c)
			}
		}},
		{"length_gt", `where $x length > 80`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConLenGt || c.Text != "80" {
				t.Errorf("got %+v", c)
			}
		}},
		{"length_eq", `where $x length == 0`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConLenEq || c.Text != "0" {
				t.Errorf("got %+v", c)
			}
		}},
		{"lines_le", `where $x lines <= 3`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConLinesLe || c.Text != "3" {
				t.Errorf("got %+v", c)
			}
		}},
		{"lines_ne", `where $x lines != 1`, func(t *testing.T, c dsl.Constraint) {
			if c.Kind != dsl.ConLinesNe || c.Text != "1" {
				t.Errorf("got %+v", c)
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

func TestWhereKindInLetList(t *testing.T) {
	r := parseOne(t, "let K = [identifier, call_expression]\nrule r {\n  pattern { foo($x) }\n  where $x kind in @K\n}")
	c := r.Where[0]
	if c.Kind != dsl.ConKindIn || !eqStrings(c.List, []string{"identifier", "call_expression"}) {
		t.Fatalf("got %+v", c)
	}
}

// --- let matchers -------------------------------------------------------------

func TestLetMatcherAtRuleLevel(t *testing.T) {
	src := `let CALLS = any {
  pattern { foo($$$) }
  pattern { bar($$$) }
}

rule a {
  @CALLS
}

rule b {
  @CALLS
  where $match matches "x"
}`
	rules := parseRules(t, "rules.lint", src)
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	for _, r := range rules {
		if r.Match.Kind != dsl.MatchAny || len(r.Match.Children) != 2 {
			t.Fatalf("rule %s: Match = %+v, want the let's any{2 patterns}", r.ID, r.Match)
		}
	}
}

func TestLetMatcherInRelationAndIs(t *testing.T) {
	src := `let FN = pattern { func $_($$$) { $$$ } }
rule r {
  pattern { foo($X) }
  inside @FN
  where $X is @FN
}`
	r := parseOne(t, src)
	if len(r.Relations) != 1 || r.Relations[0].Matcher.Kind != dsl.MatchPattern {
		t.Fatalf("Relations = %+v", r.Relations)
	}
	if len(r.Where) != 1 || r.Where[0].Kind != dsl.ConPattern || r.Where[0].Matcher.Kind != dsl.MatchPattern {
		t.Fatalf("Where = %+v", r.Where)
	}
}

func TestLetMatcherInsideCombinator(t *testing.T) {
	src := `let A = pattern { a() }
rule r {
  any {
    @A
    pattern { b() }
  }
}`
	r := parseOne(t, src)
	if r.Match.Kind != dsl.MatchAny || len(r.Match.Children) != 2 {
		t.Fatalf("Match = %+v", r.Match)
	}
	if r.Match.Children[0].Kind != dsl.MatchPattern || r.Match.Children[0].Pattern != "a()" {
		t.Fatalf("Children[0] = %+v", r.Match.Children[0])
	}
}

// --- test fix assertions --------------------------------------------------------

func TestTestFixAssertion(t *testing.T) {
	src := `rule r {
  pattern { foo($X) }
  fix "bar($X)"
  test {
    match "foo(1)" fix "bar(1)"
    match "foo(2)" count 1 fix "bar(2)"
    no_match "bar(3)"
  }
}`
	r := parseOne(t, src)
	if len(r.Tests) != 3 {
		t.Fatalf("Tests len = %d, want 3", len(r.Tests))
	}
	if r.Tests[0].Fixed == nil || *r.Tests[0].Fixed != "bar(1)" {
		t.Errorf("Tests[0].Fixed = %v", r.Tests[0].Fixed)
	}
	if r.Tests[1].Count != 1 || r.Tests[1].Fixed == nil || *r.Tests[1].Fixed != "bar(2)" {
		t.Errorf("Tests[1] = %+v", r.Tests[1])
	}
	if r.Tests[2].Fixed != nil {
		t.Errorf("Tests[2].Fixed = %v, want nil", *r.Tests[2].Fixed)
	}
}

// --- errors -----------------------------------------------------------------------

func TestNewFeatureErrors(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		contains string
	}{
		{
			name:     "fix_assert_on_no_match",
			src:      `rule r { pattern { foo() } fix "x" test { no_match "y" fix "z" } }`,
			contains: "only applies to match cases",
		},
		{
			name:     "fix_assert_without_fix_template",
			src:      `rule r { pattern { foo() } test { match "foo()" fix "bar()" } }`,
			contains: "no fix template",
		},
		{
			name:     "let_matcher_used_as_list",
			src:      "let M = pattern { foo() }\nrule r { pattern { f($x) } where $x in @M }",
			contains: "is a matcher",
		},
		{
			name:     "let_list_used_as_matcher",
			src:      "let L = [a]\nrule r { @L }",
			contains: "is a list",
		},
		{
			name:     "undefined_let_matcher",
			src:      `rule r { @NOPE }`,
			contains: "undefined let",
		},
		{
			name:     "report_needs_metavariable",
			src:      `rule r { pattern { foo() } report X }`,
			contains: "$metavariable",
		},
		{
			name:     "directly_unknown_relation",
			src:      `rule r { pattern { foo() } directly bogus pattern { b() } }`,
			contains: "after 'directly'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := dsl.Parse("r.lint", tc.src)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded (got %d rules), want error", tc.src, len(rules))
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.contains)
			}
		})
	}
}
