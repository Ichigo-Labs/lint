package dsl

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Parse parses the text of a `.lint` file into zero or more rules. A file may
// contain explicit `rule <id> { ... }` blocks, or a single bare rule whose
// fields appear at top level (its id defaults to the file's base name).
func Parse(file, src string) ([]*Rule, error) {
	p := &parser{lex: newLexer(file, src), file: file}
	return p.parseFile()
}

// ParseFile reads and parses a `.lint` file from disk.
func ParseFile(path string) ([]*Rule, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, string(b))
}

type parser struct {
	lex  *lexer
	file string
}

func (p *parser) defaultID() string {
	base := filepath.Base(p.file)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// --- token helpers -------------------------------------------------------

func (p *parser) peek() (token, error) { return p.lex.peek() }
func (p *parser) next() (token, error) { return p.lex.next() }

func (p *parser) expect(k tokKind) (token, error) {
	t, err := p.next()
	if err != nil {
		return t, err
	}
	if t.kind != k {
		return t, p.lex.errf(t.pos, "expected %s, got %s", tokName(k), t)
	}
	return t, nil
}

func tokName(k tokKind) string {
	switch k {
	case tLBrace:
		return "'{'"
	case tRBrace:
		return "'}'"
	case tLBrack:
		return "'['"
	case tRBrack:
		return "']'"
	case tComma:
		return "','"
	case tString:
		return "a string"
	case tIdent:
		return "an identifier"
	case tMeta:
		return "a $metavariable"
	}
	return "token"
}

// --- top level -----------------------------------------------------------

func (p *parser) parseFile() ([]*Rule, error) {
	t, err := p.peek()
	if err != nil {
		return nil, err
	}
	if t.kind == tEOF {
		return nil, nil
	}
	if t.kind == tIdent && t.val == "rule" {
		var rules []*Rule
		for {
			t, err := p.peek()
			if err != nil {
				return nil, err
			}
			if t.kind == tEOF {
				return rules, nil
			}
			if t.kind != tIdent || t.val != "rule" {
				return nil, p.lex.errf(t.pos, "expected another 'rule' block or end of file, got %s", t)
			}
			r, err := p.parseRuleBlock()
			if err != nil {
				return nil, err
			}
			rules = append(rules, r)
		}
	}
	// Bare rule: fields at top level until EOF.
	r := &Rule{ID: p.defaultID(), File: p.file, Pos: t.pos}
	if err := p.parseFields(r, true); err != nil {
		return nil, err
	}
	if err := p.finishRule(r); err != nil {
		return nil, err
	}
	return []*Rule{r}, nil
}

func (p *parser) parseRuleBlock() (*Rule, error) {
	kw, _ := p.next() // 'rule'
	id, err := p.expect(tIdent)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tLBrace); err != nil {
		return nil, err
	}
	r := &Rule{ID: id.val, File: p.file, Pos: kw.pos}
	if err := p.parseFields(r, false); err != nil {
		return nil, err
	}
	if _, err := p.expect(tRBrace); err != nil {
		return nil, err
	}
	if err := p.finishRule(r); err != nil {
		return nil, err
	}
	return r, nil
}

// matchers collected at rule level are implicitly ANDed.
type ruleAcc struct {
	matchers []*Matcher
}

func (p *parser) parseFields(r *Rule, topLevel bool) error {
	acc := &ruleAcc{}
	for {
		t, err := p.peek()
		if err != nil {
			return err
		}
		if t.kind == tEOF || (!topLevel && t.kind == tRBrace) {
			break
		}
		if t.kind != tIdent {
			return p.lex.errf(t.pos, "expected a field name, got %s", t)
		}
		if err := p.parseField(r, acc, t.val); err != nil {
			return err
		}
	}
	// Combine accumulated matchers into r.Match.
	switch len(acc.matchers) {
	case 0:
		// finishRule validates this.
	case 1:
		r.Match = acc.matchers[0]
	default:
		r.Match = &Matcher{Kind: MatchAll, Children: acc.matchers, Pos: acc.matchers[0].Pos}
	}
	return nil
}

func (p *parser) parseField(r *Rule, acc *ruleAcc, name string) error {
	kw, _ := p.next() // consume the field-name ident
	switch name {
	case "message", "msg":
		s, err := p.parseStringValue()
		if err != nil {
			return err
		}
		r.Message = s
	case "note", "help":
		s, err := p.parseStringValue()
		if err != nil {
			return err
		}
		r.Note = s
	case "severity":
		id, err := p.expect(tIdent)
		if err != nil {
			return err
		}
		switch id.val {
		case "error", "warning", "info":
			r.Severity = Severity(id.val)
		case "warn":
			r.Severity = Warning
		default:
			return p.lex.errf(id.pos, "unknown severity %q (use error, warning, or info)", id.val)
		}
	case "in", "language", "languages":
		langs, err := p.parseIdentList()
		if err != nil {
			return err
		}
		r.Languages = append(r.Languages, langs...)
	case "pattern":
		body, err := p.lex.readRawBody()
		if err != nil {
			return err
		}
		acc.matchers = append(acc.matchers, &Matcher{Kind: MatchPattern, Pattern: body, Pos: kw.pos})
	case "query":
		body, err := p.lex.readRawBody()
		if err != nil {
			return err
		}
		acc.matchers = append(acc.matchers, &Matcher{Kind: MatchQuery, Query: body, Pos: kw.pos})
	case "any", "all":
		m, err := p.parseCombinator(kw, name)
		if err != nil {
			return err
		}
		acc.matchers = append(acc.matchers, m)
	case "not":
		// not inside / not has / not <matcher>
		nt, err := p.peek()
		if err != nil {
			return err
		}
		if nt.kind == tIdent && nt.val == "inside" {
			p.next()
			m, err := p.parseMatcher()
			if err != nil {
				return err
			}
			r.Relations = append(r.Relations, Relation{Kind: RelNotInside, Matcher: m, Pos: kw.pos})
		} else if nt.kind == tIdent && nt.val == "has" {
			p.next()
			m, err := p.parseMatcher()
			if err != nil {
				return err
			}
			r.Relations = append(r.Relations, Relation{Kind: RelNotHas, Matcher: m, Pos: kw.pos})
		} else {
			m, err := p.parseMatcher()
			if err != nil {
				return err
			}
			acc.matchers = append(acc.matchers, &Matcher{Kind: MatchNot, Sub: m, Pos: kw.pos})
		}
	case "inside":
		m, err := p.parseMatcher()
		if err != nil {
			return err
		}
		r.Relations = append(r.Relations, Relation{Kind: RelInside, Matcher: m, Pos: kw.pos})
	case "has":
		m, err := p.parseMatcher()
		if err != nil {
			return err
		}
		r.Relations = append(r.Relations, Relation{Kind: RelHas, Matcher: m, Pos: kw.pos})
	case "where":
		c, err := p.parseConstraint()
		if err != nil {
			return err
		}
		r.Where = append(r.Where, c)
	case "fix":
		body, err := p.lex.readRawBody()
		if err != nil {
			return err
		}
		r.Fix = &body
	case "test", "tests":
		if err := p.parseTestBlock(r); err != nil {
			return err
		}
	default:
		return p.lex.errf(kw.pos, "unknown field %q", name)
	}
	return nil
}

func (p *parser) finishRule(r *Rule) error {
	if r.Match == nil {
		return p.lex.errf(r.Pos, "rule %q has no pattern/query to match", r.ID)
	}
	if r.Severity == "" {
		r.Severity = Warning
	}
	if r.Message == "" {
		r.Message = r.ID
	}
	return nil
}

// --- matchers ------------------------------------------------------------

// parseMatcher parses a single matcher expression: pattern, query, any, all,
// or not <matcher>.
func (p *parser) parseMatcher() (*Matcher, error) {
	t, err := p.peek()
	if err != nil {
		return nil, err
	}
	if t.kind != tIdent {
		return nil, p.lex.errf(t.pos, "expected a matcher (pattern, query, any, all, not), got %s", t)
	}
	switch t.val {
	case "pattern":
		kw, _ := p.next()
		body, err := p.lex.readRawBody()
		if err != nil {
			return nil, err
		}
		return &Matcher{Kind: MatchPattern, Pattern: body, Pos: kw.pos}, nil
	case "query":
		kw, _ := p.next()
		body, err := p.lex.readRawBody()
		if err != nil {
			return nil, err
		}
		return &Matcher{Kind: MatchQuery, Query: body, Pos: kw.pos}, nil
	case "any", "all":
		kw, _ := p.next()
		return p.parseCombinator(kw, t.val)
	case "not":
		kw, _ := p.next()
		sub, err := p.parseMatcher()
		if err != nil {
			return nil, err
		}
		return &Matcher{Kind: MatchNot, Sub: sub, Pos: kw.pos}, nil
	}
	return nil, p.lex.errf(t.pos, "expected a matcher, got %q", t.val)
}

func (p *parser) parseCombinator(kw token, name string) (*Matcher, error) {
	if _, err := p.expect(tLBrace); err != nil {
		return nil, err
	}
	var children []*Matcher
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind == tRBrace {
			p.next()
			break
		}
		if t.kind == tEOF {
			return nil, p.lex.errf(kw.pos, "unterminated %s { ... }", name)
		}
		m, err := p.parseMatcher()
		if err != nil {
			return nil, err
		}
		children = append(children, m)
	}
	if len(children) == 0 {
		return nil, p.lex.errf(kw.pos, "%s { } needs at least one matcher", name)
	}
	kind := MatchAny
	if name == "all" {
		kind = MatchAll
	}
	return &Matcher{Kind: kind, Children: children, Pos: kw.pos}, nil
}

// --- constraints ---------------------------------------------------------

func (p *parser) parseConstraint() (Constraint, error) {
	mv, err := p.expect(tMeta)
	if err != nil {
		return Constraint{}, err
	}
	c := Constraint{Var: mv.val, Pos: mv.pos}
	op, err := p.next()
	if err != nil {
		return c, err
	}
	switch op.kind {
	case tEqEq, tBangEq:
		rhs, err := p.next()
		if err != nil {
			return c, err
		}
		switch rhs.kind {
		case tMeta:
			if op.kind == tEqEq {
				c.Kind, c.Other = ConEqVar, rhs.val
			} else {
				c.Kind, c.Other = ConNeqVar, rhs.val
			}
		case tString:
			if op.kind == tEqEq {
				c.Kind, c.Text = ConEqText, rhs.val
			} else {
				c.Kind, c.Text = ConNeqText, rhs.val
			}
		default:
			return c, p.lex.errf(rhs.pos, "expected $var or string after comparison, got %s", rhs)
		}
		return c, nil
	case tIdent:
		switch op.val {
		case "matches":
			s, err := p.parseStringValue()
			if err != nil {
				return c, err
			}
			c.Kind, c.Text = ConRegex, s
		case "kind":
			id, err := p.expect(tIdent)
			if err != nil {
				return c, err
			}
			c.Kind, c.Text = ConKind, id.val
		case "in":
			list, err := p.parseBracketList()
			if err != nil {
				return c, err
			}
			c.Kind, c.List = ConIn, list
		case "is":
			neg := false
			nt, _ := p.peek()
			if nt.kind == tIdent && nt.val == "not" {
				p.next()
				neg = true
			}
			m, err := p.parseMatcher()
			if err != nil {
				return c, err
			}
			c.Matcher = m
			if neg {
				c.Kind = ConNotPattern
			} else {
				c.Kind = ConPattern
			}
		case "not":
			nt, err := p.next()
			if err != nil {
				return c, err
			}
			if nt.kind != tIdent {
				return c, p.lex.errf(nt.pos, "expected matches/kind/in after 'not', got %s", nt)
			}
			switch nt.val {
			case "matches":
				s, err := p.parseStringValue()
				if err != nil {
					return c, err
				}
				c.Kind, c.Text = ConNotRegex, s
			case "kind":
				id, err := p.expect(tIdent)
				if err != nil {
					return c, err
				}
				c.Kind, c.Text = ConNotKind, id.val
			case "in":
				list, err := p.parseBracketList()
				if err != nil {
					return c, err
				}
				c.Kind, c.List = ConNotIn, list
			default:
				return c, p.lex.errf(nt.pos, "unknown constraint 'not %s'", nt.val)
			}
		default:
			return c, p.lex.errf(op.pos, "unknown constraint operator %q", op.val)
		}
		return c, nil
	}
	return c, p.lex.errf(op.pos, "expected a constraint operator after $%s, got %s", c.Var, op)
}

// --- tests ---------------------------------------------------------------

func (p *parser) parseTestBlock(r *Rule) error {
	if _, err := p.expect(tLBrace); err != nil {
		return err
	}
	for {
		t, err := p.peek()
		if err != nil {
			return err
		}
		if t.kind == tRBrace {
			p.next()
			return nil
		}
		if t.kind != tIdent {
			return p.lex.errf(t.pos, "expected match/no_match in test block, got %s", t)
		}
		kw, _ := p.next()
		tc := TestCase{Count: -1, Pos: kw.pos}
		switch kw.val {
		case "match", "bad", "flag":
			tc.Expect = ExpectMatch
		case "no_match", "ok", "good", "clean":
			tc.Expect = ExpectNoMatch
		default:
			return p.lex.errf(kw.pos, "unknown test entry %q (use match or no_match)", kw.val)
		}
		body, err := p.lex.readRawBody()
		if err != nil {
			return err
		}
		tc.Code = body
		// Optional `count N` modifier for match cases.
		nt, _ := p.peek()
		if nt.kind == tIdent && nt.val == "count" {
			p.next()
			num, err := p.expect(tNumber)
			if err != nil {
				return err
			}
			n, _ := strconv.Atoi(num.val)
			tc.Count = n
		}
		r.Tests = append(r.Tests, tc)
	}
}

// --- small value parsers -------------------------------------------------

func (p *parser) parseStringValue() (string, error) {
	t, err := p.next()
	if err != nil {
		return "", err
	}
	if t.kind != tString {
		return "", p.lex.errf(t.pos, "expected a string, got %s", t)
	}
	return t.val, nil
}

// parseIdentList reads `a, b, c` of identifiers (commas required between).
func (p *parser) parseIdentList() ([]string, error) {
	var out []string
	first, err := p.expect(tIdent)
	if err != nil {
		return nil, err
	}
	out = append(out, first.val)
	for {
		t, err := p.peek()
		if err != nil {
			return nil, err
		}
		if t.kind != tComma {
			return out, nil
		}
		p.next()
		id, err := p.expect(tIdent)
		if err != nil {
			return nil, err
		}
		out = append(out, id.val)
	}
}

// parseBracketList reads `[ a, "b", c ]` of identifiers and/or strings.
func (p *parser) parseBracketList() ([]string, error) {
	if _, err := p.expect(tLBrack); err != nil {
		return nil, err
	}
	var out []string
	for {
		t, err := p.next()
		if err != nil {
			return nil, err
		}
		switch t.kind {
		case tRBrack:
			return out, nil
		case tComma:
			continue
		case tIdent, tString, tNumber:
			out = append(out, t.val)
		default:
			return nil, p.lex.errf(t.pos, "expected an item or ']' in list, got %s", t)
		}
	}
}

var _ = fmt.Sprintf
