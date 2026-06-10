package dsl_test

import (
	"strings"
	"testing"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// These tests pin down the raw-body lexer's treatment of '#': inside a
// brace-delimited body, '#' starts a line comment (for brace counting only)
// when it is followed by a space, tab, newline, or end of input. A '#' glued
// to other text — a CSS hex color `#fff`, an id selector `#main`, `#$C` — is
// ordinary pattern text and must not swallow the rest of the line.

// --- '#' glued to text is code, not a comment -----------------------------

func TestRawBodyHashGluedToText(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"css_hex_color", `pattern { color: #fff }`, "color: #fff"},
		{"id_selector_with_nested_block", `pattern { .x #main { color: red } }`, ".x #main { color: red }"},
		{"hash_metavariable", `pattern { color: #$C }`, "color: #$C"},
		{"hash_directly_before_close_brace", `pattern { x #}`, "x #"},
		{"hash_then_newline_then_close", "pattern { x #\n}", "x #"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parseOne(t, tc.src)
			if r.Match == nil || r.Match.Kind != dsl.MatchPattern {
				t.Fatalf("Match = %+v", r.Match)
			}
			if r.Match.Pattern != tc.want {
				t.Errorf("Pattern = %q, want %q", r.Match.Pattern, tc.want)
			}
		})
	}
}

// --- '# ' comments and brace counting -------------------------------------

func TestRawBodyHashCommentSkipsBraceForCounting(t *testing.T) {
	// A '}' inside a '# ' comment does not close the body; the block is
	// closed by the brace on the following line. The comment text itself
	// (including its '}') stays in the body verbatim.
	src := "pattern { foo() # not a close }\n}"
	r := parseOne(t, src)
	want := "foo() # not a close }"
	if r.Match == nil || r.Match.Kind != dsl.MatchPattern {
		t.Fatalf("Match = %+v", r.Match)
	}
	if r.Match.Pattern != want {
		t.Errorf("Pattern = %q, want %q", r.Match.Pattern, want)
	}
}

func TestRawBodyHashCommentEatsSameLineBrace(t *testing.T) {
	// Because '# ' comments run to end of line, a '}' on the same line is
	// part of the comment — with no later brace the body is unbalanced.
	rules, err := dsl.Parse("r.lint", "pattern { x # trailing }")
	if err == nil {
		t.Fatalf("Parse succeeded with %d rules, want unbalanced-brace error", len(rules))
	}
	if !strings.Contains(err.Error(), "unbalanced") {
		t.Errorf("error = %q, want it to mention 'unbalanced'", err)
	}
}

// --- multi-line python bodies with comments --------------------------------

func TestRawBodyMultilinePythonWithComments(t *testing.T) {
	src := `
rule py {
  in python
  pattern {
      if $x:
          $body  # note
  }
  test {
    match {
        if ready:
            go()  # note with a brace }
    }
  }
}
`
	r := parseOne(t, src)
	wantPat := "if $x:\n    $body  # note"
	if r.Match == nil || r.Match.Kind != dsl.MatchPattern {
		t.Fatalf("Match = %+v", r.Match)
	}
	if r.Match.Pattern != wantPat {
		t.Errorf("Pattern = %q, want %q", r.Match.Pattern, wantPat)
	}
	if len(r.Tests) != 1 {
		t.Fatalf("Tests len = %d, want 1", len(r.Tests))
	}
	wantCode := "if ready:\n    go()  # note with a brace }"
	if r.Tests[0].Expect != dsl.ExpectMatch || r.Tests[0].Code != wantCode {
		t.Errorf("Tests[0] = %+v, want Code %q", r.Tests[0], wantCode)
	}
}

// --- '#' at or near end of input -------------------------------------------

func TestRawBodyHashAtEOFDoesNotCrash(t *testing.T) {
	// A '#' with nothing after it counts as a comment opener (EOF behaves
	// like whitespace); the body is then unterminated. All of these must
	// produce a clean "unbalanced" error, not a panic.
	cases := []struct {
		name string
		src  string
	}{
		{"hash_then_eof", "pattern { x #"},
		{"hash_space_then_eof", "pattern { x # "},
		{"hash_comment_then_eof", "pattern { x # comment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := dsl.Parse("r.lint", tc.src)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded with %d rules, want error", tc.src, len(rules))
			}
			if !strings.Contains(err.Error(), "unbalanced") {
				t.Errorf("Parse(%q) error = %q, want mention of 'unbalanced'", tc.src, err)
			}
		})
	}
}

func TestHashCommentAtEOFAfterBody(t *testing.T) {
	// Outside a body, a trailing '#' comment running into EOF is harmless
	// DSL trivia.
	r := parseOne(t, "pattern { foo() } #")
	if r.Match == nil || r.Match.Pattern != "foo()" {
		t.Errorf("Match = %+v, want pattern %q", r.Match, "foo()")
	}
}
