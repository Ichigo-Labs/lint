package engine

import "testing"

// jsonRule wraps a pattern body in a minimal json rule.
func jsonRule(pattern string) string {
	return "rule r { in json\n pattern { " + pattern + " } }"
}

// yamlRule wraps a pattern body in a minimal yaml rule.
func yamlRule(pattern string) string {
	return "rule r { in yaml\n pattern { " + pattern + " } }"
}

// --- json -----------------------------------------------------------------

func TestJSONPairValueMetavariable(t *testing.T) {
	// A bare metavariable in value position is auto-quoted by the rewriter
	// (JSON has no bare tokens) and the string wrapper is transparent, so $V
	// binds values of ANY kind: string values bind their content WITHOUT the
	// quotes, numbers bind their raw text. Pair patterns match at any depth.
	rule := jsonRule(`"version": $V`)
	fs := run(t, "json", rule, `{"version": "1.0", "nested": {"version": 2}}`)
	wantN(t, fs, 2)
	if fs[0].Bindings["V"] != "1.0" {
		t.Fatalf("string value $V = %q, want %q (content sans quotes)", fs[0].Bindings["V"], "1.0")
	}
	if fs[1].Bindings["V"] != "2" {
		t.Fatalf("number value $V = %q, want %q", fs[1].Bindings["V"], "2")
	}
}

func TestJSONKeyValueMetavariablesWithWhere(t *testing.T) {
	// "$K" in key position binds the key without quotes; a where-regex on $V
	// filters by the bound value text.
	rule := `rule r { in json
 pattern { "$K": "$V" }
 where $V matches "^app" }`
	fs := run(t, "json", rule, `{"name": "app", "count": 3}`)
	wantN(t, fs, 1)
	if fs[0].Bindings["K"] != "name" || fs[0].Bindings["V"] != "app" {
		t.Fatalf("bindings = K:%q V:%q, want K:name V:app", fs[0].Bindings["K"], fs[0].Bindings["V"])
	}
}

func TestJSONLiteralValueCompare(t *testing.T) {
	// Literal values compare exactly: "a": 1 does not match "a": 2, and
	// "a": "x" does not match "a": "y".
	tests := []struct {
		name    string
		pattern string
		code    string
		want    int
	}{
		{"number literal matches itself", `"a": 1`, `{"a": 1}`, 1},
		{"number literal rejects other number", `"a": 1`, `{"a": 2}`, 0},
		{"string literal matches itself", `"a": "x"`, `{"a": "x"}`, 1},
		{"string literal rejects other string", `"a": "x"`, `{"a": "y"}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantN(t, run(t, "json", jsonRule(tt.pattern), tt.code), tt.want)
		})
	}
}

func TestJSONExactArrayPattern(t *testing.T) {
	// An exact-shape array pattern matches only the array with the same
	// elements in the same order.
	rule := jsonRule(`[1, 2]`)
	fs := run(t, "json", rule, `{"a": [1, 2], "b": [1, 2, 3], "c": [2, 1]}`)
	wantN(t, fs, 1)
	if fs[0].Snippet != `[1, 2]` {
		t.Fatalf("snippet = %q, want [1, 2]", fs[0].Snippet)
	}
}

func TestJSONArrayVariadic(t *testing.T) {
	// [$$$] matches arrays of any length, INCLUDING the empty array.
	rule := jsonRule(`[$$$]`)
	fs := run(t, "json", rule, `{"a": [], "b": [1], "c": [1, 2, 3]}`)
	wantN(t, fs, 3)
	if fs[0].Snippet != `[]` {
		t.Fatalf("first snippet = %q, want the empty array", fs[0].Snippet)
	}
}

func TestJSONCCommentDoesNotBreakMatching(t *testing.T) {
	// // comments parse (jsonc grammar), so a commented target still matches.
	rule := jsonRule(`"version": $V`)
	fs := run(t, "json", rule, "{\n// a comment\n\"version\": \"1.0\"\n}")
	wantN(t, fs, 1)
	if fs[0].Bindings["V"] != "1.0" {
		t.Fatalf("$V = %q, want %q", fs[0].Bindings["V"], "1.0")
	}
}

func TestJSONKeyValueBackref(t *testing.T) {
	// Reusing $K in key and value positions requires key == value.
	rule := jsonRule(`"$K": "$K"`)
	fs := run(t, "json", rule, `{"same": "same", "diff": "other"}`)
	wantN(t, fs, 1)
	if fs[0].Bindings["K"] != "same" {
		t.Fatalf("$K = %q, want %q", fs[0].Bindings["K"], "same")
	}
}

// --- yaml -----------------------------------------------------------------

func TestYAMLPairValueMetavariable(t *testing.T) {
	// key: $V matches block mapping pairs at any depth (here inside sequence
	// items) and binds the scalar value text.
	rule := yamlRule(`image: $V`)
	fs := run(t, "yaml", rule, "services:\n  - name: web\n    image: nginx:latest\n  - name: db\n    image: postgres\n")
	wantN(t, fs, 2)
	if fs[0].Bindings["V"] != "nginx:latest" || fs[1].Bindings["V"] != "postgres" {
		t.Fatalf("$V bindings = %q, %q; want nginx:latest, postgres", fs[0].Bindings["V"], fs[1].Bindings["V"])
	}
}

func TestYAMLBooleanLiteralCompare(t *testing.T) {
	// privileged: true matches only the plain boolean true: not false, and
	// (actual behavior) not the quoted scalar "true" — double_quote_scalar is
	// a different node kind than the pattern's plain boolean_scalar.
	rule := yamlRule(`privileged: true`)
	fs := run(t, "yaml", rule, "a:\n  privileged: true\nb:\n  privileged: false\nc:\n  privileged: \"true\"\n")
	wantN(t, fs, 1)
	if fs[0].StartLine != 2 {
		t.Fatalf("finding line = %d, want 2 (the plain `true`)", fs[0].StartLine)
	}
}

func TestYAMLWhereNumeric(t *testing.T) {
	rule := "rule r { in yaml\n pattern { replicas: $N }\n where $N > 3 }"
	fs := run(t, "yaml", rule, "a:\n  replicas: 5\nb:\n  replicas: 2\n")
	wantN(t, fs, 1)
	if fs[0].Bindings["N"] != "5" {
		t.Fatalf("$N = %q, want %q", fs[0].Bindings["N"], "5")
	}
}

func TestYAMLMultiPairSequencePattern(t *testing.T) {
	// A multi-pair pattern compiles to a sequence pattern that matches
	// CONSECUTIVE pairs only; an intervening pair breaks the match. The
	// pattern lines must share indentation (the dsl dedents the body), so the
	// rule is written with the body on its own lines at column 0.
	rule := "rule r { in yaml\n pattern {\na: 1\nb: 2\n} }"
	fs := run(t, "yaml", rule, "x:\n  a: 1\n  b: 2\ny:\n  a: 1\n  c: 9\n  b: 2\n")
	wantN(t, fs, 1)
	if fs[0].StartLine != 2 {
		t.Fatalf("finding line = %d, want 2 (the consecutive pairs under x)", fs[0].StartLine)
	}
}

func TestYAMLSequenceItemMetavariable(t *testing.T) {
	// - $X matches each block sequence item and binds its scalar.
	rule := yamlRule(`- $X`)
	fs := run(t, "yaml", rule, "items:\n  - alpha\n  - beta\n")
	wantN(t, fs, 2)
	if fs[0].Bindings["X"] != "alpha" || fs[1].Bindings["X"] != "beta" {
		t.Fatalf("$X bindings = %q, %q; want alpha, beta", fs[0].Bindings["X"], fs[1].Bindings["X"])
	}
}

func TestYAMLKeyValueBackref(t *testing.T) {
	// Reusing $K as key and value requires key == value.
	rule := yamlRule(`$K: $K`)
	fs := run(t, "yaml", rule, "a:\n  same: same\n  diff: other\n")
	wantN(t, fs, 1)
	if fs[0].Bindings["K"] != "same" {
		t.Fatalf("$K = %q, want %q", fs[0].Bindings["K"], "same")
	}
}

func TestYAMLQuotedVsPlainScalarKinds(t *testing.T) {
	// Actual behavior: yaml literals are quote-SENSITIVE in both directions.
	// A pattern with a double-quoted scalar (name: "x") matches only a
	// double-quoted target, and a plain-scalar pattern (name: x) matches only
	// a plain target — double_quote_scalar and plain string_scalar are
	// distinct node kinds, and yaml has no transparent quote wrapper.
	code := "a:\n  name: \"x\"\nb:\n  name: x\n"

	fs := run(t, "yaml", yamlRule(`name: "x"`), code)
	wantN(t, fs, 1)
	if fs[0].StartLine != 2 {
		t.Fatalf("quoted pattern matched line %d, want 2 (the quoted target)", fs[0].StartLine)
	}

	fs = run(t, "yaml", yamlRule(`name: x`), code)
	wantN(t, fs, 1)
	if fs[0].StartLine != 4 {
		t.Fatalf("plain pattern matched line %d, want 4 (the plain target)", fs[0].StartLine)
	}
}
