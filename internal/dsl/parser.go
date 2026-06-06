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
	p := &parser{lex: newLexer(file, src), file: file, lets: map[string]*LetDef{}}
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
	lets map[string]*LetDef // file-level `let` definitions, resolved in `where`
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
	// File-level `let` definitions may lead the file.
	if err := p.consumeLets(); err != nil {
		return nil, err
	}
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
			// `let` definitions may also appear between rule blocks.
			if t.kind == tIdent && t.val == "let" {
				if err := p.parseLet(); err != nil {
					return nil, err
				}
				continue
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

// consumeLets parses any leading `let` definitions at the top of the file.
func (p *parser) consumeLets() error {
	for {
		t, err := p.peek()
		if err != nil {
			return err
		}
		if t.kind != tIdent || t.val != "let" {
			return nil
		}
		if err := p.parseLet(); err != nil {
			return err
		}
	}
}

// parseLet parses `let NAME = [a, b, "c"]` or `let NAME = "regex"`.
func (p *parser) parseLet() error {
	p.next() // 'let'
	name, err := p.expect(tIdent)
	if err != nil {
		return err
	}
	if _, err := p.expect(tEq); err != nil {
		return err
	}
	t, err := p.peek()
	if err != nil {
		return err
	}
	var def *LetDef
	switch t.kind {
	case tLBrack:
		list, err := p.parseBracketList()
		if err != nil {
			return err
		}
		def = &LetDef{List: list, Pos: name.pos}
	case tString:
		s, err := p.parseStringValue()
		if err != nil {
			return err
		}
		def = &LetDef{IsRegex: true, Text: s, Pos: name.pos}
	default:
		return p.lex.errf(t.pos, "expected '[ ... ]' or a \"regex\" after 'let %s =', got %s", name.val, t)
	}
	p.lets[name.val] = def
	return nil
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
	case "url", "link":
		s, err := p.parseStringValue()
		if err != nil {
			return err
		}
		r.URL = s
	case "tag", "tags":
		tags, err := p.parseIdentList()
		if err != nil {
			return err
		}
		r.Tags = append(r.Tags, tags...)
	case "paths", "path":
		globs, err := p.parseStringList()
		if err != nil {
			return err
		}
		r.Paths = append(r.Paths, globs...)
	case "exclude", "excludePaths":
		globs, err := p.parseStringList()
		if err != nil {
			return err
		}
		r.ExcludePaths = append(r.ExcludePaths, globs...)
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
		// not inside / not has / not precedes / not follows /
		// not directly inside / not directly has / not <matcher>
		nt, err := p.peek()
		if err != nil {
			return err
		}
		if nt.kind == tIdent {
			switch nt.val {
			case "inside":
				p.next()
				return p.addRelation(r, RelNotInside, kw.pos)
			case "has":
				p.next()
				return p.addRelation(r, RelNotHas, kw.pos)
			case "precedes":
				p.next()
				return p.addRelation(r, RelNotPrecedes, kw.pos)
			case "follows":
				p.next()
				return p.addRelation(r, RelNotFollows, kw.pos)
			case "directly":
				p.next()
				return p.addDirectlyRelation(r, kw.pos, true)
			}
		}
		m, err := p.parseMatcher()
		if err != nil {
			return err
		}
		acc.matchers = append(acc.matchers, &Matcher{Kind: MatchNot, Sub: m, Pos: kw.pos})
	case "inside":
		return p.addRelation(r, RelInside, kw.pos)
	case "has":
		return p.addRelation(r, RelHas, kw.pos)
	case "precedes":
		return p.addRelation(r, RelPrecedes, kw.pos)
	case "follows":
		return p.addRelation(r, RelFollows, kw.pos)
	case "directly":
		return p.addDirectlyRelation(r, kw.pos, false)
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

// addRelation parses the matcher after a relation keyword and records it.
func (p *parser) addRelation(r *Rule, kind RelationKind, pos Position) error {
	m, err := p.parseMatcher()
	if err != nil {
		return err
	}
	r.Relations = append(r.Relations, Relation{Kind: kind, Matcher: m, Pos: pos})
	return nil
}

// addDirectlyRelation parses `directly inside`/`directly has` (the `directly`
// keyword has already been consumed). neg selects the negated form.
func (p *parser) addDirectlyRelation(r *Rule, pos Position, neg bool) error {
	dt, err := p.expect(tIdent)
	if err != nil {
		return err
	}
	var kind RelationKind
	switch dt.val {
	case "inside":
		kind = RelDirectlyInside
		if neg {
			kind = RelNotDirectlyInside
		}
	case "has":
		kind = RelDirectlyHas
		if neg {
			kind = RelNotDirectlyHas
		}
	default:
		return p.lex.errf(dt.pos, "expected 'inside' or 'has' after 'directly', got %q", dt.val)
	}
	return p.addRelation(r, kind, pos)
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
	var where []Constraint
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
		// A `where` clause inside the block scopes its constraint to this
		// branch (applied only to matches this combinator produces).
		if t.kind == tIdent && t.val == "where" {
			p.next()
			c, err := p.parseConstraint()
			if err != nil {
				return nil, err
			}
			where = append(where, c)
			continue
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
	return &Matcher{Kind: kind, Children: children, Where: where, Pos: kw.pos}, nil
}

// --- constraints ---------------------------------------------------------

// numConstraintKind maps a comparison token to the numeric `where $X <op> n`
// constraint kind. Only the ordering operators reach here; equality on a number
// is handled by the existing text `==`/`!=`.
func numConstraintKind(k tokKind) ConstraintKind {
	switch k {
	case tGt:
		return ConNumGt
	case tGe:
		return ConNumGe
	case tLt:
		return ConNumLt
	default: // tLe
		return ConNumLe
	}
}

// countConstraintKind maps a comparison token to the `where $X count <op> n`
// constraint kind, reporting false for a non-comparison token.
func countConstraintKind(k tokKind) (ConstraintKind, bool) {
	switch k {
	case tGt:
		return ConCountGt, true
	case tGe:
		return ConCountGe, true
	case tLt:
		return ConCountLt, true
	case tLe:
		return ConCountLe, true
	case tEqEq:
		return ConCountEq, true
	case tBangEq:
		return ConCountNe, true
	}
	return 0, false
}

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
	case tEq:
		return c, p.lex.errf(op.pos, "expected '==' (single '=' is not valid)")
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
	case tLt, tGt, tLe, tGe:
		num, err := p.expect(tNumber)
		if err != nil {
			return c, err
		}
		c.Kind, c.Text = numConstraintKind(op.kind), num.val
		return c, nil
	case tIdent:
		switch op.val {
		case "count":
			cmp, err := p.next()
			if err != nil {
				return c, err
			}
			k, ok := countConstraintKind(cmp.kind)
			if !ok {
				return c, p.lex.errf(cmp.pos, "expected a comparison (==, !=, <, <=, >, >=) after 'count', got %s", cmp)
			}
			num, err := p.expect(tNumber)
			if err != nil {
				return c, err
			}
			c.Kind, c.Text = k, num.val
		case "matches":
			s, err := p.parseRegexValue()
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
			list, err := p.parseInList()
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
				s, err := p.parseRegexValue()
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
				list, err := p.parseInList()
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

// parseInList reads the right-hand side of `in`: either a `[ ... ]` literal or a
// `@NAME` reference to a list-valued `let` definition.
func (p *parser) parseInList() ([]string, error) {
	t, err := p.peek()
	if err != nil {
		return nil, err
	}
	if t.kind == tAt {
		p.next()
		def, err := p.lookupLet(t)
		if err != nil {
			return nil, err
		}
		if def.IsRegex {
			return nil, p.lex.errf(t.pos, "let @%s is a regex; `in` needs a list", t.val)
		}
		return def.List, nil
	}
	return p.parseBracketList()
}

// parseRegexValue reads the right-hand side of `matches`: either a string or a
// `@NAME` reference to a regex-valued `let` definition.
func (p *parser) parseRegexValue() (string, error) {
	t, err := p.peek()
	if err != nil {
		return "", err
	}
	if t.kind == tAt {
		p.next()
		def, err := p.lookupLet(t)
		if err != nil {
			return "", err
		}
		if !def.IsRegex {
			return "", p.lex.errf(t.pos, "let @%s is a list; `matches` needs a regex", t.val)
		}
		return def.Text, nil
	}
	return p.parseStringValue()
}

// lookupLet resolves a @NAME reference, erroring if it is undefined.
func (p *parser) lookupLet(at token) (*LetDef, error) {
	def, ok := p.lets[at.val]
	if !ok {
		return nil, p.lex.errf(at.pos, "undefined let @%s (define it with `let %s = ...`)", at.val, at.val)
	}
	return def, nil
}

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

// parseStringList reads a list of string globs, either as a bracket list
// `["a", "b"]` or a comma-separated sequence `"a", "b"`.
func (p *parser) parseStringList() ([]string, error) {
	t, err := p.peek()
	if err != nil {
		return nil, err
	}
	if t.kind == tLBrack {
		return p.parseBracketList()
	}
	var out []string
	first, err := p.parseStringValue()
	if err != nil {
		return nil, err
	}
	out = append(out, first)
	for {
		nt, err := p.peek()
		if err != nil {
			return nil, err
		}
		if nt.kind != tComma {
			return out, nil
		}
		p.next()
		s, err := p.parseStringValue()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
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
