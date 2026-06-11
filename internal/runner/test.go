package runner

import (
	"strings"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/engine"
	"github.com/ichigo-labs/lint/internal/fix"
)

// CaseResult is the outcome of one inline rule test case.
type CaseResult struct {
	RuleID string
	Index  int
	Expect dsl.TestExpectation
	Want   int // expected count; -1 means "one or more"
	Got    int
	Lang   string // language that produced Got
	Pass   bool

	// FixMismatch is set when the case asserts a `fix` output and applying
	// the rule's fixes produced something else; FixWant/FixGot hold the two
	// texts (whitespace-trimmed) for reporting.
	FixMismatch bool
	FixWant     string
	FixGot      string
}

// RunRuleTests evaluates a rule's inline `test { ... }` cases. Each case is run
// against every language the rule targets; the language with the most matches
// is taken as the result (a snippet only parses meaningfully in its own
// language, so others contribute zero).
func RunRuleTests(cr *engine.CompiledRule) []CaseResult {
	var results []CaseResult
	for i, tc := range cr.Raw().Tests {
		best, bestLang := 0, ""
		var bestFindings []engine.Finding
		for _, l := range cr.Languages() {
			tree, err := engine.ParseSource(l, []byte(tc.Code))
			if err != nil {
				continue
			}
			fs := cr.Run(l, tree, []byte(tc.Code))
			if len(fs) > best {
				best, bestLang, bestFindings = len(fs), l.Name, fs
			}
		}
		pass := false
		switch tc.Expect {
		case dsl.ExpectMatch:
			if tc.Count >= 0 {
				pass = best == tc.Count
			} else {
				pass = best >= 1
			}
		case dsl.ExpectNoMatch:
			pass = best == 0
		}
		res := CaseResult{
			RuleID: cr.ID,
			Index:  i,
			Expect: tc.Expect,
			Want:   tc.Count,
			Got:    best,
			Lang:   bestLang,
			Pass:   pass,
		}
		// A `fix` assertion: apply the rule's fixes to the snippet and compare
		// (ignoring surrounding whitespace).
		if pass && tc.Fixed != nil {
			out, _, _ := fix.ApplyBytes([]byte(tc.Code), bestFindings)
			got := strings.TrimSpace(string(out))
			want := strings.TrimSpace(*tc.Fixed)
			if got != want {
				res.Pass = false
				res.FixMismatch = true
				res.FixWant, res.FixGot = want, got
			}
		}
		results = append(results, res)
	}
	return results
}
