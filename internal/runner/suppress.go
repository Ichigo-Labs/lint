package runner

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/engine"
	"github.com/ichigo-labs/lint/internal/lang"
)

// suppressions records which rules are silenced on each 1-based source line by
// `lint:ignore` / `lint:ignore-next-line` comments.
type suppressions map[int]*lineSuppress

// lineSuppress is the set of rules silenced on one line. all=true means every
// rule (a bare `lint:ignore` with no ids); otherwise only ids in the set.
type lineSuppress struct {
	all bool
	ids map[string]bool
}

func (ls *lineSuppress) add(ids []string) {
	if len(ids) == 0 {
		ls.all = true
		return
	}
	if ls.ids == nil {
		ls.ids = map[string]bool{}
	}
	for _, id := range ids {
		ls.ids[id] = true
	}
}

// suppresses reports whether a finding from ruleID is silenced on this line.
func (ls *lineSuppress) suppresses(ruleID string) bool {
	return ls.all || (ls.ids != nil && ls.ids[ruleID])
}

// collectSuppressions walks a parsed tree for suppression directives in comments.
// `lint:ignore [ids]` silences the comment's own line; `lint:ignore-next-line
// [ids]` silences the following line. With no ids, all rules on the target line
// are silenced. ids may be comma-separated; text after the first whitespace-run
// is treated as an explanation and ignored.
func collectSuppressions(l *lang.Language, tree *sitter.Tree, src []byte) suppressions {
	sup := suppressions{}
	add := func(line int, ids []string) {
		ls := sup[line]
		if ls == nil {
			ls = &lineSuppress{}
			sup[line] = ls
		}
		ls.add(ids)
	}
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		if l.IsComment(n.Type()) {
			line := int(n.StartPoint().Row) + 1
			if ids, ok := parseDirective(n.Content(src), "lint:ignore-next-line"); ok {
				add(line+1, ids)
			} else if ids, ok := parseDirective(n.Content(src), "lint:ignore"); ok {
				add(line, ids)
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if c := n.Child(i); c != nil {
				visit(c)
			}
		}
	}
	visit(tree.RootNode())
	return sup
}

// parseDirective tests whether a comment carries the given directive and, if so,
// returns the rule ids it names (nil = all rules). It strips //, #, and /* */
// delimiters and requires the directive to be the first token.
func parseDirective(comment, keyword string) ([]string, bool) {
	text := strings.TrimSpace(stripCommentDelims(comment))
	if !strings.HasPrefix(text, keyword) {
		return nil, false
	}
	rest := text[len(keyword):]
	// The keyword must be a whole token (end of comment or followed by space),
	// so `lint:ignore` does not match `lint:ignore-next-line` and vice versa.
	if rest != "" && !isSpaceByte(rest[0]) {
		return nil, false
	}
	if len(strings.Fields(rest)) == 0 {
		return nil, true // bare directive: all rules
	}
	// Rule ids are comma-separated; within each segment the first whitespace-
	// delimited token is the id and any trailing words are an explanation. This
	// tolerates spaces around the commas (`a , b`) and a trailing reason
	// (`a,b because ...`).
	var ids []string
	for _, seg := range strings.Split(rest, ",") {
		if f := strings.Fields(seg); len(f) > 0 {
			ids = append(ids, f[0])
		}
	}
	return ids, true
}

func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' }

// stripCommentDelims removes leading //, #, /* and trailing */ from comment text.
func stripCommentDelims(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "//"):
		s = s[2:]
	case strings.HasPrefix(s, "/*"):
		s = strings.TrimSuffix(strings.TrimSpace(s[2:]), "*/")
	case strings.HasPrefix(s, "#"):
		s = s[1:]
	}
	return s
}

// filterSuppressed drops findings silenced by suppression comments.
func filterSuppressed(findings []engine.Finding, sup suppressions) []engine.Finding {
	if len(sup) == 0 {
		return findings
	}
	out := findings[:0]
	for _, f := range findings {
		if ls := sup[f.StartLine]; ls != nil && ls.suppresses(f.RuleID) {
			continue
		}
		out = append(out, f)
	}
	return out
}
