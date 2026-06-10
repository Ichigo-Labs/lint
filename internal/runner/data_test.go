package runner

import (
	"path/filepath"
	"sort"
	"testing"
)

// TestDataExtensionMapping checks that the data-language extensions map to
// their languages (.json/.jsonc -> json, .yaml/.yml -> yaml) and that
// identical content under an unknown extension (.txt) is ignored.
func TestDataExtensionMapping(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "json.lint"), `rule no_version {
  in json
  pattern { "version": $V }
}`)
	writeFile(t, filepath.Join(root, ".lint", "yaml.lint"), `rule no_replicas {
  in yaml
  pattern { replicas: $V }
}`)

	writeFile(t, filepath.Join(root, "app.json"), `{"version": "1.0"}`+"\n")
	writeFile(t, filepath.Join(root, "config.jsonc"), "// settings\n{\"version\": \"2.0\"}\n")
	writeFile(t, filepath.Join(root, "deploy.yaml"), "replicas: 3\n")
	writeFile(t, filepath.Join(root, "ci.yml"), "replicas: 2\n")
	// Identical content to app.json, but .txt maps to no language.
	writeFile(t, filepath.Join(root, "notes.txt"), `{"version": "1.0"}`+"\n")

	rs := mustLoad(t)
	res := mustCheck(t, rs, Options{})

	got := collectKeys(res.Findings)
	want := []string{
		"app.json|no_version",
		"ci.yml|no_replicas",
		"config.jsonc|no_version",
		"deploy.yaml|no_replicas",
	}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("findings = %v, want %v", got, want)
	}
}

// TestDataExplicitOnly checks that a rule without an `in` clause never
// compiles for the explicit-only data languages: its Languages() exclude
// json/yaml, and it produces no findings on data files even when the pattern
// text appears verbatim in them.
func TestDataExplicitOnly(t *testing.T) {
	root := newMarkupRoot(t)

	writeFile(t, filepath.Join(root, ".lint", "generic.lint"), `rule generic {
  pattern { fmt.Println($$$) }
}`)
	// The pattern text appears inside each data file.
	writeFile(t, filepath.Join(root, "data.json"), `{"cmd": "fmt.Println(1)"}`+"\n")
	writeFile(t, filepath.Join(root, "data.yaml"), `cmd: fmt.Println(1)`+"\n")

	rs := mustLoad(t)
	if rs.Len() != 1 {
		t.Fatalf("expected 1 rule, got %d", rs.Len())
	}

	// `lint list`-level: the compiled in-less rule targets some languages, but
	// never the explicit-only data ones.
	langs := rs.Rules()[0].Languages()
	if len(langs) == 0 {
		t.Fatalf("in-less rule compiled for no languages at all")
	}
	for _, l := range langs {
		switch l.Name {
		case "json", "yaml":
			t.Errorf("in-less rule compiled for explicit-only language %q", l.Name)
		}
	}

	res := mustCheck(t, rs, Options{})
	if len(res.Findings) != 0 {
		t.Fatalf("in-less rule fired on data files: %v", collectKeys(res.Findings))
	}
}

// TestDataInlineSuppression checks the suppression-comment forms of each data
// language: yaml # line comments (ignore-next-line with a rule id) and jsonc
// // comments (same-line ignore with a rule id). The jsonc case documents
// actual behavior: the json language registers the jsonc "comment" kind, so
// suppression directives in .jsonc comments are honored.
func TestDataInlineSuppression(t *testing.T) {
	cases := []struct {
		name      string
		rule      string
		file      string
		content   string
		wantLines []int // StartLine of findings that survive suppression
	}{
		{
			name: "yaml line-comment ignore-next-line by id",
			rule: `rule no_replicas {
  in yaml
  pattern { replicas: $V }
}`,
			file: "deploy.yaml",
			content: `a:
  # lint:ignore-next-line no_replicas
  replicas: 1
b:
  replicas: 2
`,
			wantLines: []int{5},
		},
		{
			name: "jsonc line-comment same-line ignore by id",
			rule: `rule no_version {
  in json
  pattern { "version": $V }
}`,
			file: "config.jsonc",
			content: `{
  "version": "1.0", // lint:ignore no_version pinned on purpose
  "tool": {"version": "2.0"}
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
