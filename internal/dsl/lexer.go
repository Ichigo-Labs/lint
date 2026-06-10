package dsl

import (
	"fmt"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tString // cooked string value
	tNumber
	tMeta // $name reference (in where clauses)
	tLBrace
	tRBrace
	tLBrack
	tRBrack
	tComma
	tAt // @name reference to a `let` definition (in where clauses)
	tEq // single '=' (used by `let NAME = ...`)
	tEqEq
	tBangEq
	tLt // <
	tGt // >
	tLe // <=
	tGe // >=
)

type token struct {
	kind tokKind
	val  string
	pos  Position
}

func (t token) String() string {
	switch t.kind {
	case tEOF:
		return "end of file"
	case tIdent:
		return fmt.Sprintf("%q", t.val)
	case tString:
		return "string"
	case tNumber:
		return "number"
	case tMeta:
		return "$" + t.val
	case tLBrace:
		return "{"
	case tRBrace:
		return "}"
	case tLBrack:
		return "["
	case tRBrack:
		return "]"
	case tComma:
		return ","
	case tAt:
		return "@" + t.val
	case tEq:
		return "="
	case tEqEq:
		return "=="
	case tBangEq:
		return "!="
	case tLt:
		return "<"
	case tGt:
		return ">"
	case tLe:
		return "<="
	case tGe:
		return ">="
	}
	return "?"
}

// lexer is a hand-written, restartable scanner. Because lexing is a pure
// function of position, the parser can snapshot and restore state to peek.
type lexer struct {
	src  string
	pos  int
	line int
	col  int
	file string
}

func newLexer(file, src string) *lexer {
	return &lexer{src: src, pos: 0, line: 1, col: 1, file: file}
}

type lexState struct{ pos, line, col int }

func (l *lexer) save() lexState     { return lexState{l.pos, l.line, l.col} }
func (l *lexer) restore(s lexState) { l.pos, l.line, l.col = s.pos, s.line, s.col }
func (l *lexer) at(i int) byte      { return l.src[i] }
func (l *lexer) eof() bool          { return l.pos >= len(l.src) }
func (l *lexer) cur() byte          { return l.src[l.pos] }
func (l *lexer) here() Position     { return Position{Line: l.line, Col: l.col} }

func (l *lexer) advance() byte {
	c := l.src[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

// errf builds a positional error.
func (l *lexer) errf(pos Position, format string, args ...any) error {
	return fmt.Errorf("%s:%d:%d: %s", l.file, pos.Line, pos.Col, fmt.Sprintf(format, args...))
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '-'
}
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// skipTrivia consumes whitespace and DSL comments (# line, // line, /* block */).
func (l *lexer) skipTrivia() {
	for !l.eof() {
		c := l.cur()
		switch {
		case isSpace(c):
			l.advance()
		case c == '#':
			for !l.eof() && l.cur() != '\n' {
				l.advance()
			}
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/':
			for !l.eof() && l.cur() != '\n' {
				l.advance()
			}
		case c == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*':
			l.advance()
			l.advance()
			for !l.eof() && !(l.cur() == '*' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/') {
				l.advance()
			}
			if !l.eof() {
				l.advance() // *
				l.advance() // /
			}
		default:
			return
		}
	}
}

// next returns the next structural token.
func (l *lexer) next() (token, error) {
	l.skipTrivia()
	pos := l.here()
	if l.eof() {
		return token{kind: tEOF, pos: pos}, nil
	}
	c := l.cur()
	switch {
	case c == '{':
		l.advance()
		return token{kind: tLBrace, pos: pos}, nil
	case c == '}':
		l.advance()
		return token{kind: tRBrace, pos: pos}, nil
	case c == '[':
		l.advance()
		return token{kind: tLBrack, pos: pos}, nil
	case c == ']':
		l.advance()
		return token{kind: tRBrack, pos: pos}, nil
	case c == ',':
		l.advance()
		return token{kind: tComma, pos: pos}, nil
	case c == '=':
		l.advance()
		if !l.eof() && l.cur() == '=' {
			l.advance()
			return token{kind: tEqEq, pos: pos}, nil
		}
		// A single '=' is only meaningful in `let NAME = ...`; in a `where`
		// comparison the parser rejects it with a "use ==" hint.
		return token{kind: tEq, pos: pos}, nil
	case c == '!':
		l.advance()
		if l.eof() || l.cur() != '=' {
			return token{}, l.errf(pos, "expected '!='")
		}
		l.advance()
		return token{kind: tBangEq, pos: pos}, nil
	case c == '<':
		l.advance()
		if !l.eof() && l.cur() == '=' {
			l.advance()
			return token{kind: tLe, pos: pos}, nil
		}
		return token{kind: tLt, pos: pos}, nil
	case c == '>':
		l.advance()
		if !l.eof() && l.cur() == '=' {
			l.advance()
			return token{kind: tGe, pos: pos}, nil
		}
		return token{kind: tGt, pos: pos}, nil
	case c == '@':
		l.advance()
		if l.eof() || !isIdentStart(l.cur()) {
			return token{}, l.errf(pos, "expected a name after '@'")
		}
		start := l.pos
		for !l.eof() && isIdentCont(l.cur()) {
			l.advance()
		}
		return token{kind: tAt, val: l.src[start:l.pos], pos: pos}, nil
	case c == '"':
		s, err := l.readDoubleString()
		if err != nil {
			return token{}, err
		}
		return token{kind: tString, val: s, pos: pos}, nil
	case c == '`':
		s, err := l.readBacktickString()
		if err != nil {
			return token{}, err
		}
		return token{kind: tString, val: s, pos: pos}, nil
	case c == '$':
		l.advance()
		if l.eof() || !isIdentStart(l.cur()) {
			return token{}, l.errf(pos, "expected a metavariable name after '$'")
		}
		start := l.pos
		for !l.eof() && isIdentCont(l.cur()) {
			l.advance()
		}
		return token{kind: tMeta, val: l.src[start:l.pos], pos: pos}, nil
	case isDigit(c):
		start := l.pos
		for !l.eof() && isDigit(l.cur()) {
			l.advance()
		}
		// Optional fractional part, e.g. 3.5 (a trailing dot with no digit, as in
		// a range or method call, is left for the next token).
		if !l.eof() && l.cur() == '.' && l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1]) {
			l.advance() // '.'
			for !l.eof() && isDigit(l.cur()) {
				l.advance()
			}
		}
		return token{kind: tNumber, val: l.src[start:l.pos], pos: pos}, nil
	case isIdentStart(c):
		start := l.pos
		for !l.eof() && isIdentCont(l.cur()) {
			l.advance()
		}
		return token{kind: tIdent, val: l.src[start:l.pos], pos: pos}, nil
	}
	return token{}, l.errf(pos, "unexpected character %q", string(c))
}

// peek returns the next token without consuming input.
func (l *lexer) peek() (token, error) {
	s := l.save()
	t, err := l.next()
	l.restore(s)
	return t, err
}

// readDoubleString reads a "..." or """...""" literal. Double-quoted strings
// process \n \t \" \\ escapes; triple-quoted strings are raw.
func (l *lexer) readDoubleString() (string, error) {
	pos := l.here()
	// Triple-quoted?
	if strings.HasPrefix(l.src[l.pos:], `"""`) {
		l.advance()
		l.advance()
		l.advance()
		start := l.pos
		for !l.eof() && !strings.HasPrefix(l.src[l.pos:], `"""`) {
			l.advance()
		}
		if l.eof() {
			return "", l.errf(pos, "unterminated \"\"\" string")
		}
		body := l.src[start:l.pos]
		l.advance()
		l.advance()
		l.advance()
		return trimRawBody(body), nil
	}
	l.advance() // opening "
	var b strings.Builder
	for {
		if l.eof() {
			return "", l.errf(pos, "unterminated string")
		}
		c := l.advance()
		if c == '"' {
			return b.String(), nil
		}
		if c == '\\' {
			if l.eof() {
				return "", l.errf(pos, "unterminated string escape")
			}
			e := l.advance()
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			default:
				b.WriteByte('\\')
				b.WriteByte(e)
			}
			continue
		}
		b.WriteByte(c)
	}
}

// readBacktickString reads a `...` raw string (no escapes).
func (l *lexer) readBacktickString() (string, error) {
	pos := l.here()
	l.advance() // opening `
	start := l.pos
	for !l.eof() && l.cur() != '`' {
		l.advance()
	}
	if l.eof() {
		return "", l.errf(pos, "unterminated `...` string")
	}
	body := l.src[start:l.pos]
	l.advance() // closing `
	return strings.TrimSpace(body), nil
}

// readRawBody reads a pattern/fix/query/test body: a string literal in any of
// the three forms, or a brace-balanced `{ ... }` block of target-language code.
func (l *lexer) readRawBody() (string, error) {
	l.skipTrivia()
	pos := l.here()
	if l.eof() {
		return "", l.errf(pos, "expected a body ({ ... }, \"...\", or `...`)")
	}
	switch l.cur() {
	case '"':
		return l.readDoubleString()
	case '`':
		return l.readBacktickString()
	case '{':
		return l.readBraceBlock()
	}
	return "", l.errf(pos, "expected a body: { ... }, \"...\", \"\"\"...\"\"\", or `...`")
}

// readBraceBlock captures everything inside a balanced { ... }, treating the
// contents as opaque target-language code. It tracks string/char/comment
// context so that braces inside literals or comments do not affect nesting.
func (l *lexer) readBraceBlock() (string, error) {
	pos := l.here()
	l.advance() // opening {
	start := l.pos
	depth := 1
	for !l.eof() {
		c := l.cur()
		switch c {
		case '{':
			depth++
			l.advance()
		case '}':
			depth--
			if depth == 0 {
				body := l.src[start:l.pos]
				l.advance() // closing }
				return trimRawBody(body), nil
			}
			l.advance()
		case '"', '\'', '`':
			l.skipTargetString(c)
		case '/':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
				for !l.eof() && l.cur() != '\n' {
					l.advance()
				}
			} else if l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
				l.advance()
				l.advance()
				for !l.eof() && !(l.cur() == '*' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/') {
					l.advance()
				}
				if !l.eof() {
					l.advance()
					l.advance()
				}
			} else {
				l.advance()
			}
		case '#':
			// Line comment in Python/shell-ish targets, but only when followed
			// by whitespace: '#' glued to text is code (a CSS color `#fff`, an
			// id selector `#main`), and skipping to EOL there would miscount a
			// closing brace on the same line.
			if l.pos+1 >= len(l.src) || l.src[l.pos+1] == ' ' || l.src[l.pos+1] == '\t' || l.src[l.pos+1] == '\n' {
				for !l.eof() && l.cur() != '\n' {
					l.advance()
				}
			} else {
				l.advance()
			}
		default:
			l.advance()
		}
	}
	return "", l.errf(pos, "unbalanced { } in body")
}

// skipTargetString consumes a quoted run in target code, respecting backslash
// escapes (for " and '); backtick strings are taken verbatim to the next tick.
// A single quote that is not closed before a newline or brace is treated as an
// ordinary character (e.g. a Rust lifetime `'a`), not a char-literal opener.
func (l *lexer) skipTargetString(quote byte) {
	start := l.save()
	l.advance() // opening quote
	for !l.eof() {
		c := l.cur()
		if c == '\\' && quote != '`' {
			l.advance()
			if !l.eof() {
				l.advance()
			}
			continue
		}
		if c == quote {
			l.advance()
			return
		}
		if quote == '\'' && (c == '\n' || c == '{' || c == '}' || c == ';') {
			// Unterminated single-quote run: not a char literal. Rewind so we
			// consumed only the lone apostrophe.
			l.restore(start)
			l.advance()
			return
		}
		l.advance()
	}
}

// trimRawBody removes a leading newline and trailing blank space so that
//
//	pattern {
//	    foo()
//	}
//
// yields "foo()" rather than "\n    foo()\n".
func trimRawBody(s string) string {
	s = strings.TrimRight(s, " \t\r\n")
	s = strings.TrimLeft(s, "\r\n")
	// Strip common leading indentation.
	return dedent(s)
}

// dedent removes the longest common leading whitespace from every non-blank
// line, so brace-block patterns read naturally regardless of nesting.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	indent := -1
	for _, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		if trimmed == "" {
			continue
		}
		n := len(ln) - len(trimmed)
		if indent == -1 || n < indent {
			indent = n
		}
	}
	if indent <= 0 {
		return s
	}
	for i, ln := range lines {
		if len(ln) >= indent {
			lines[i] = ln[indent:]
		} else {
			lines[i] = strings.TrimLeft(ln, " \t")
		}
	}
	return strings.Join(lines, "\n")
}
