package runner

import (
	"path/filepath"
	"sort"
	"testing"
)

// newMarkupRoot creates an empty project root, resolves symlinks (macOS temp
// dirs), and chdirs into it so reported paths are relative.
func newMarkupRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	t.Chdir(root)
	return root
}

// mustLoad loads the ruleset from the cwd and fails the test on any error.
func mustLoad(t *testing.T) *RuleSet {
	t.Helper()
	rs, lerrs, err := Load(".", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(lerrs) != 0 {
		t.Fatalf("unexpected load errors: %v", lerrs)
	}
	return rs
}

// mustCheck runs Check over the whole tree and fails the test on any error.
func mustCheck(t *testing.T, rs *RuleSet, opts Options) *Result {
	t.Helper()
	if len(opts.Paths) == 0 {
		opts.Paths = []string{"."}
	}
	res, err := rs.Check(opts)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.FileErrs) != 0 {
		t.Fatalf("unexpected file errors: %v", res.FileErrs)
	}
	return res
}

// TestMarkupExtensionMapping checks that each new extension maps to its
// language (.html/.htm -> html, .css -> css, .scss -> scss, .xml/.svg -> xml)
// and that identical content under an unknown extension (.txt) is ignored.
func TestMarkupExtensionMapping(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "html.lint"), `rule no_marquee {
  in html
  pattern { <marquee $$$> }
}`)
	writeFile(t, filepath.Join(root, ".lint", "css.lint"), `rule no_float_css {
  in css
  pattern { float: $V }
}`)
	writeFile(t, filepath.Join(root, ".lint", "scss.lint"), `rule no_float_scss {
  in scss
  pattern { float: $V }
}`)
	writeFile(t, filepath.Join(root, ".lint", "xml.lint"), `rule no_version {
  in xml
  pattern { <version>$V</version> }
}`)

	writeFile(t, filepath.Join(root, "index.html"), "<marquee>hi</marquee>\n")
	writeFile(t, filepath.Join(root, "page.htm"), "<marquee>hi</marquee>\n")
	writeFile(t, filepath.Join(root, "style.css"), ".a { float: left; }\n")
	writeFile(t, filepath.Join(root, "theme.scss"), ".a { float: left; }\n")
	writeFile(t, filepath.Join(root, "pom.xml"), "<project><version>1.0</version></project>\n")
	writeFile(t, filepath.Join(root, "icon.svg"), "<svg><version>9</version></svg>\n")
	// Identical content to index.html, but .txt maps to no language.
	writeFile(t, filepath.Join(root, "notes.txt"), "<marquee>hi</marquee>\n")

	rs := mustLoad(t)
	res := mustCheck(t, rs, Options{})

	got := collectKeys(res.Findings)
	want := []string{
		"icon.svg|no_version",
		"index.html|no_marquee",
		"page.htm|no_marquee",
		"pom.xml|no_version",
		"style.css|no_float_css",
		"theme.scss|no_float_scss",
	}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
}

// TestMarkupExplicitOnly checks that a rule without an `in` clause never
// compiles for the explicit-only markup languages: its Languages() exclude
// html/css/scss/xml, and it produces no findings on markup files even when
// the pattern text appears verbatim in them.
func TestMarkupExplicitOnly(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "generic.lint"), `rule generic {
  pattern { fmt.Println($$$) }
}`)
	// The pattern text appears as plain text/content in each markup file.
	writeFile(t, filepath.Join(root, "page.html"), `<p>fmt.Println("hello")</p>`+"\n")
	writeFile(t, filepath.Join(root, "style.css"), "/* fmt.Println(1) */ .a { color: red; }\n")
	writeFile(t, filepath.Join(root, "theme.scss"), "// fmt.Println(1)\n.a { color: red; }\n")
	writeFile(t, filepath.Join(root, "data.xml"), "<doc>fmt.Println(1)</doc>\n")

	rs := mustLoad(t)
	if rs.Len() != 1 {
		t.Fatalf("expected 1 rule, got %d", rs.Len())
	}

	// `lint list`-level: the compiled in-less rule targets some languages, but
	// never the explicit-only ones.
	langs := rs.Rules()[0].Languages()
	if len(langs) == 0 {
		t.Fatalf("in-less rule compiled for no languages at all")
	}
	for _, l := range langs {
		switch l.Name {
		case "html", "css", "scss", "xml":
			t.Errorf("in-less rule compiled for explicit-only language %q", l.Name)
		}
	}

	res := mustCheck(t, rs, Options{})
	if len(res.Findings) != 0 {
		t.Fatalf("in-less rule fired on markup files: %v", collectKeys(res.Findings))
	}
}

// TestMarkupInlineSuppression checks the suppression-comment forms of each
// markup language: html/xml <!-- --> comments (with and without rule ids,
// same-line and next-line), scss // line comments, and css /* */ comments.
func TestMarkupInlineSuppression(t *testing.T) {
	cases := []struct {
		name      string
		rule      string
		file      string
		content   string
		wantLines []int // StartLine of findings that survive suppression
	}{
		{
			name: "html ignore-next-line by id and bare same-line ignore",
			rule: `rule no_marquee {
  in html
  pattern { <marquee $$$> }
}`,
			file: "page.html",
			content: `<!-- lint:ignore-next-line no_marquee -->
<marquee>a</marquee>
<marquee>b</marquee> <!-- lint:ignore -->
<marquee>c</marquee>
`,
			wantLines: []int{4},
		},
		{
			name: "scss line-comment ignore-next-line",
			rule: `rule no_float {
  in scss
  pattern { float: $V }
}`,
			file: "theme.scss",
			content: `.a {
  // lint:ignore-next-line
  float: left;
  float: right;
}
`,
			wantLines: []int{4},
		},
		{
			name: "xml same-line ignore with id and trailing reason",
			rule: `rule no_version {
  in xml
  pattern { <version>$V</version> }
}`,
			file: "pom.xml",
			content: `<project>
  <version>1</version> <!-- lint:ignore no_version because pinned -->
  <version>2</version>
</project>
`,
			wantLines: []int{3},
		},
		{
			name: "css block-comment bare ignore",
			rule: `rule no_float {
  in css
  pattern { float: $V }
}`,
			file: "style.css",
			content: `.a {
  float: left; /* lint:ignore */
  float: right;
}
`,
			wantLines: []int{3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := newMarkupRoot(t)
			writeFile(t, filepath.Join(root, ".lint", "r.lint"), tc.rule)
			writeFile(t, filepath.Join(root, tc.file), tc.content)

			rs := mustLoad(t)
			res := mustCheck(t, rs, Options{})

			var lines []int
			for _, f := range res.Findings {
				lines = append(lines, f.StartLine)
			}
			sort.Ints(lines)
			if len(lines) != len(tc.wantLines) {
				t.Fatalf("finding lines = %v, want %v", lines, tc.wantLines)
			}
			for i := range lines {
				if lines[i] != tc.wantLines[i] {
					t.Fatalf("finding lines = %v, want %v", lines, tc.wantLines)
				}
			}
		})
	}
}

// TestMarkupMultiLanguageRule checks that a single rule with `in css, scss`
// compiles for both languages and fires on .css and .scss files alike.
func TestMarkupMultiLanguageRule(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule no_float {
  in css, scss
  pattern { float: $V }
}`)
	writeFile(t, filepath.Join(root, "a.css"), ".x { float: left; }\n")
	writeFile(t, filepath.Join(root, "b.scss"), ".x { float: right; }\n")

	rs := mustLoad(t)
	if rs.Len() != 1 {
		t.Fatalf("expected 1 rule, got %d", rs.Len())
	}

	var langNames []string
	for _, l := range rs.Rules()[0].Languages() {
		langNames = append(langNames, l.Name)
	}
	sort.Strings(langNames)
	if !equalStrings(langNames, []string{"css", "scss"}) {
		t.Fatalf("rule languages = %v, want [css scss]", langNames)
	}

	res := mustCheck(t, rs, Options{})
	got := collectKeys(res.Findings)
	want := []string{"a.css|no_float", "b.scss|no_float"}
	if !equalStrings(got, want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
}

// TestMarkupLangFilter checks that Options.LangFilter limited to html only
// checks .html files, skipping other markup files whose rules would fire.
func TestMarkupLangFilter(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule no_marquee {
  in html
  pattern { <marquee $$$> }
}
rule no_float {
  in css
  pattern { float: $V }
}`)
	writeFile(t, filepath.Join(root, "index.html"), "<marquee>hi</marquee>\n")
	writeFile(t, filepath.Join(root, "style.css"), ".a { float: left; }\n")

	rs := mustLoad(t)

	// Without a filter both rules fire, proving the filter below does the work.
	all := mustCheck(t, rs, Options{})
	gotAll := collectKeys(all.Findings)
	wantAll := []string{"index.html|no_marquee", "style.css|no_float"}
	if !equalStrings(gotAll, wantAll) {
		t.Fatalf("unfiltered findings = %v, want %v", gotAll, wantAll)
	}

	res := mustCheck(t, rs, Options{LangFilter: map[string]bool{"html": true}})
	got := collectKeys(res.Findings)
	want := []string{"index.html|no_marquee"}
	if !equalStrings(got, want) {
		t.Fatalf("html-filtered findings = %v, want %v", got, want)
	}
}

// TestMarkupFix checks that a css rule with a `fix` template attaches an
// expanded Fix string (metavariable bindings substituted) to its findings.
func TestMarkupFix(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "r.lint"), `rule css_no_float {
  in css
  pattern { float: $V }
  fix "display: flex /* was $V */"
}`)
	writeFile(t, filepath.Join(root, "style.css"), ".a { float: left; }\n")

	rs := mustLoad(t)
	res := mustCheck(t, rs, Options{})

	if len(res.Findings) != 1 {
		t.Fatalf("findings = %v, want exactly 1", collectKeys(res.Findings))
	}
	f := res.Findings[0]
	if f.RuleID != "css_no_float" {
		t.Fatalf("RuleID = %q, want css_no_float", f.RuleID)
	}
	if f.Bindings["V"] != "left" {
		t.Fatalf("Bindings[V] = %q, want %q", f.Bindings["V"], "left")
	}
	if f.Fix == nil {
		t.Fatalf("Fix = nil, want expanded fix template")
	}
	if want := "display: flex /* was left */"; *f.Fix != want {
		t.Fatalf("Fix = %q, want %q", *f.Fix, want)
	}
}
