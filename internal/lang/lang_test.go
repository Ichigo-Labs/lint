package lang_test

import (
	"testing"

	"github.com/ichigo-labs/lint/internal/lang"
)

// --- extension mapping for the markup/style languages ----------------------

func TestForExtNewLanguages(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{".html", "html"},
		{".htm", "html"},
		{".css", "css"},
		{".scss", "scss"},
		{".xml", "xml"},
		{".svg", "xml"},
		{".xsd", "xml"},
		{".xsl", "xml"},
		{".xslt", "xml"},
		// Without a leading dot and with different casing.
		{"html", "html"},
		{".SCSS", "scss"},
		{".SVG", "xml"},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			l, ok := lang.ForExt(tc.ext)
			if !ok {
				t.Fatalf("ForExt(%q) not found", tc.ext)
			}
			if l.Name != tc.want {
				t.Errorf("ForExt(%q).Name = %q, want %q", tc.ext, l.Name, tc.want)
			}
		})
	}
}

func TestForPathNewLanguages(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"index.html", "html"},
		{"old/page.htm", "html"},
		{"assets/style.css", "css"},
		{"src/theme.scss", "scss"},
		{"pom.xml", "xml"},
		{"icons/logo.svg", "xml"},
		{"schema/types.xsd", "xml"},
		{"transform.xsl", "xml"},
		{"transform.xslt", "xml"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			l, ok := lang.ForPath(tc.path)
			if !ok {
				t.Fatalf("ForPath(%q) not found", tc.path)
			}
			if l.Name != tc.want {
				t.Errorf("ForPath(%q).Name = %q, want %q", tc.path, l.Name, tc.want)
			}
		})
	}
}

// --- name resolution --------------------------------------------------------

func TestGetNewNames(t *testing.T) {
	for _, name := range []string{"html", "css", "scss", "xml"} {
		t.Run(name, func(t *testing.T) {
			l, ok := lang.Get(name)
			if !ok {
				t.Fatalf("Get(%q) not found", name)
			}
			if l.Name != name {
				t.Errorf("Get(%q).Name = %q, want %q", name, l.Name, name)
			}
		})
	}
}

// --- per-language metadata flags ---------------------------------------------

func TestMarkupLanguageFlags(t *testing.T) {
	cases := []struct {
		name              string
		explicitOnly      bool
		metaInStrings     bool
		metaUppercaseOnly bool
	}{
		{"html", true, true, false},
		{"css", true, true, false},
		{"scss", true, true, true},
		{"xml", true, true, false},
		// Code languages keep all of these off.
		{"go", false, false, false},
		{"typescript", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, ok := lang.Get(tc.name)
			if !ok {
				t.Fatalf("Get(%q) not found", tc.name)
			}
			if l.ExplicitOnly != tc.explicitOnly {
				t.Errorf("%s.ExplicitOnly = %v, want %v", tc.name, l.ExplicitOnly, tc.explicitOnly)
			}
			if l.MetaInStrings != tc.metaInStrings {
				t.Errorf("%s.MetaInStrings = %v, want %v", tc.name, l.MetaInStrings, tc.metaInStrings)
			}
			if l.MetaUppercaseOnly != tc.metaUppercaseOnly {
				t.Errorf("%s.MetaUppercaseOnly = %v, want %v", tc.name, l.MetaUppercaseOnly, tc.metaUppercaseOnly)
			}
		})
	}
}

func TestSentinelSep(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"html", "-"}, // html tag names cannot contain '_'
		{"css", "_"},
		{"scss", "_"},
		{"xml", "_"},
		{"go", "_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, ok := lang.Get(tc.name)
			if !ok {
				t.Fatalf("Get(%q) not found", tc.name)
			}
			if got := l.SentinelSep(); got != tc.want {
				t.Errorf("%s.SentinelSep() = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

// --- registry completeness ----------------------------------------------------

func TestNamesIncludesAllLanguages(t *testing.T) {
	want := []string{
		"c", "cpp", "csharp", "css", "go", "html", "java", "json", "kotlin",
		"python", "rust", "scss", "swift", "tsx", "typescript", "xml", "yaml",
	}
	got := lang.Names()
	if len(got) != len(want) {
		t.Fatalf("Names() returned %d names %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names()[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// --- existing languages unchanged ----------------------------------------------

func TestExistingLanguageExtensionsUnchanged(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{".go", "go"},
		{".ts", "typescript"},
		{".mts", "typescript"},
		{".cts", "typescript"},
		{".tsx", "tsx"},
		{".js", "tsx"},
		{".jsx", "tsx"},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			l, ok := lang.ForExt(tc.ext)
			if !ok {
				t.Fatalf("ForExt(%q) not found", tc.ext)
			}
			if l.Name != tc.want {
				t.Errorf("ForExt(%q).Name = %q, want %q", tc.ext, l.Name, tc.want)
			}
		})
	}
	// Aliases still resolve to the canonical language.
	if l, ok := lang.Get("golang"); !ok || l.Name != "go" {
		t.Errorf("Get(\"golang\") = %v, %v; want the go language", l, ok)
	}
	if l, ok := lang.Get("javascript"); !ok || l.Name != "tsx" {
		t.Errorf("Get(\"javascript\") = %v, %v; want the tsx language", l, ok)
	}
}
