package runner

import (
	"github.com/ichigo-labs/lintel/internal/dsl"
	"github.com/ichigo-labs/lintel/internal/engine"
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
}

// RunRuleTests evaluates a rule's inline `test { ... }` cases. Each case is run
// against every language the rule targets; the language with the most matches
// is taken as the result (a snippet only parses meaningfully in its own
// language, so others contribute zero).
func RunRuleTests(cr *engine.CompiledRule) []CaseResult {
	var results []CaseResult
	for i, tc := range cr.Raw().Tests {
		best, bestLang := 0, ""
		for _, l := range cr.Languages() {
			tree, err := engine.ParseSource(l, []byte(tc.Code))
			if err != nil {
				continue
			}
			n := len(cr.Run(l, tree, []byte(tc.Code)))
			if n > best {
				best, bestLang = n, l.Name
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
		results = append(results, CaseResult{
			RuleID: cr.ID,
			Index:  i,
			Expect: tc.Expect,
			Want:   tc.Count,
			Got:    best,
			Lang:   bestLang,
			Pass:   pass,
		})
	}
	return results
}
