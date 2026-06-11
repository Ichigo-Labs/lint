package runner

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/engine"
)

// compileRule parses and compiles a single-rule source.
func compileRule(t *testing.T, src string) *engine.CompiledRule {
	t.Helper()
	rules, err := dsl.Parse("r.lint", src)
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
	return cr
}

func TestRunRuleTestsFixAssertion(t *testing.T) {
	cr := compileRule(t, `rule errf {
  in go
  pattern { errors.New(fmt.Sprintf($$$A)) }
  fix     "fmt.Errorf($$$A)"
  test {
    match """
package p
func f() error { return errors.New(fmt.Sprintf("bad: %s", n)) }
""" fix """
package p
func f() error { return fmt.Errorf("bad: %s", n) }
"""
  }
}`)
	results := RunRuleTests(cr)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Pass || results[0].FixMismatch {
		t.Fatalf("case failed: %+v", results[0])
	}
}

func TestRunRuleTestsFixAssertionMismatch(t *testing.T) {
	cr := compileRule(t, `rule errf {
  in go
  pattern { foo($X) }
  fix     "bar($X)"
  test {
    match "package p\nfunc f() { foo(1) }" fix "package p\nfunc f() { qux(1) }"
  }
}`)
	results := RunRuleTests(cr)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Pass || !r.FixMismatch {
		t.Fatalf("expected a fix mismatch failure, got %+v", r)
	}
	if !strings.Contains(r.FixGot, "bar(1)") || !strings.Contains(r.FixWant, "qux(1)") {
		t.Fatalf("FixGot = %q, FixWant = %q", r.FixGot, r.FixWant)
	}
}

func TestRunRuleTestsFixAssertionMultipleFindings(t *testing.T) {
	// All findings' fixes apply before comparing.
	cr := compileRule(t, `rule nolog {
  in typescript
  pattern { console.log($$$A) }
  fix     "logger.info($$$A)"
  test {
    match "console.log(1);\nconsole.log(2);" count 2 fix "logger.info(1);\nlogger.info(2);"
  }
}`)
	results := RunRuleTests(cr)
	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("expected a passing case, got %+v", results)
	}
}
