// Package runner discovers `.lint` rules and applies them across a codebase.
package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/engine"
	"github.com/ichigo-labs/lint/internal/lang"
)

// LoadError records a rule file that failed to parse or compile.
type LoadError struct {
	Path string
	Err  error
}

func (e LoadError) Error() string { return fmt.Sprintf("%s: %v", e.Path, e.Err) }

// RuleSet is the collection of compiled rules loaded from a project's `.lint`
// directory.
type RuleSet struct {
	rules  []*engine.CompiledRule
	config *projectConfig // project-level disable/severity overrides (never nil after Load)
}

// Rules returns every compiled rule, sorted by id.
func (rs *RuleSet) Rules() []*engine.CompiledRule {
	out := make([]*engine.CompiledRule, len(rs.rules))
	copy(out, rs.rules)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Len reports how many rules were loaded.
func (rs *RuleSet) Len() int { return len(rs.rules) }

var ignoredDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "out": true, "target": true, "bin": true, "obj": true,
	".idea": true, ".vscode": true, "__pycache__": true, ".lint": true,
}

// Load compiles every `*.lint` rule in the project's `.lint/` directory at root.
// If rulesDirOverride is set, that directory's rules are loaded instead. Rules
// apply to every file checked.
func Load(root, rulesDirOverride string) (*RuleSet, []LoadError, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	rs := &RuleSet{}
	cfg, err := loadProjectConfig(absRoot)
	if err != nil {
		return nil, nil, err
	}
	rs.config = cfg

	rulesDir := filepath.Join(absRoot, ".lint")
	if rulesDirOverride != "" {
		abs, err := filepath.Abs(rulesDirOverride)
		if err != nil {
			return nil, nil, err
		}
		rulesDir = abs
	}

	var lerrs []LoadError
	entries, err := os.ReadDir(rulesDir)
	if err != nil {
		return rs, lerrs, nil // no rules directory: an empty ruleset, not an error
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".lint") {
			continue
		}
		path := filepath.Join(rulesDir, e.Name())
		rules, err := dsl.ParseFile(path)
		if err != nil {
			lerrs = append(lerrs, LoadError{Path: path, Err: err})
			continue
		}
		for _, r := range rules {
			cr, err := engine.Compile(r)
			if err != nil {
				lerrs = append(lerrs, LoadError{Path: path, Err: err})
				continue
			}
			rs.rules = append(rs.rules, cr)
		}
	}
	return rs, lerrs, nil
}

// Options configures a check run.
type Options struct {
	Paths       []string        // files/dirs to check (default ".")
	Concurrency int             // worker count (default GOMAXPROCS)
	LangFilter  map[string]bool // if non-empty, only these languages
	TagFilter   map[string]bool // if non-empty, only rules carrying one of these tags
}

// Result is the outcome of a check run.
type Result struct {
	Findings []engine.Finding
	Sources  map[string][]byte // file -> contents, for findings' files
	FileErrs []LoadError       // files that failed to read/parse
}

// ruleRun is a rule pre-resolved for one run: project-config disables, tag
// filters, and severity overrides are applied once per Check instead of once
// per file.
type ruleRun struct {
	cr          *engine.CompiledRule
	sevOverride dsl.Severity
	hasOverride bool
}

// Check applies the ruleset to the given paths.
func (rs *RuleSet) Check(opts Options) (*Result, error) {
	if len(opts.Paths) == 0 {
		opts.Paths = []string{"."}
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = runtime.GOMAXPROCS(0)
	}

	files, err := collectFiles(opts.Paths)
	if err != nil {
		return nil, err
	}

	// Resolve the per-language rule list (with config and tag filters
	// applied) once per run; checkFile then just looks its language up.
	byLang := map[string][]ruleRun{}
	for _, cr := range rs.rules {
		if rs.config.isDisabled(cr.ID) {
			continue
		}
		if len(opts.TagFilter) > 0 && !anyTag(cr, opts.TagFilter) {
			continue
		}
		sev, hasSev := rs.config.severityFor(cr.ID)
		for _, l := range cr.Languages() {
			if len(opts.LangFilter) > 0 && !opts.LangFilter[l.Name] {
				continue
			}
			byLang[l.Name] = append(byLang[l.Name], ruleRun{cr: cr, sevOverride: sev, hasOverride: hasSev})
		}
	}
	cwd, _ := os.Getwd()

	type job struct{ path string }
	jobs := make(chan job, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	res := &Result{Sources: map[string][]byte{}}

	worker := func() {
		defer wg.Done()
		for j := range jobs {
			fs, src, ferr := checkFile(j.path, byLang, cwd)
			mu.Lock()
			if ferr != nil {
				res.FileErrs = append(res.FileErrs, LoadError{Path: j.path, Err: ferr})
			}
			if len(fs) > 0 {
				res.Findings = append(res.Findings, fs...)
				res.Sources[reportPath(j.path, cwd)] = src
			}
			mu.Unlock()
		}
	}
	wg.Add(conc)
	for i := 0; i < conc; i++ {
		go worker()
	}
	for _, f := range files {
		jobs <- job{path: f}
	}
	close(jobs)
	wg.Wait()

	sort.Slice(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		if a.StartCol != b.StartCol {
			return a.StartCol < b.StartCol
		}
		return a.RuleID < b.RuleID
	})
	return res, nil
}

func checkFile(path string, byLang map[string][]ruleRun, cwd string) ([]engine.Finding, []byte, error) {
	l, ok := lang.ForPath(path)
	if !ok {
		return nil, nil, nil
	}
	applicable := byLang[l.Name]
	if len(applicable) == 0 {
		return nil, nil, nil
	}
	rp := reportPath(path, cwd)
	// Path-scoped rules (`paths` / `exclude` globs) only apply to matching
	// files; drop the rest before touching the file at all.
	applicable = pruneByPath(applicable, rp)
	if len(applicable) == 0 {
		return nil, nil, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	// Skip rules whose anchor literal can't appear in this file; if none
	// remain the file needs no parse at all.
	live := applicable
	if pruned := pruneByAnchor(applicable, l, src); len(pruned) != len(applicable) {
		if len(pruned) == 0 {
			return nil, nil, nil
		}
		live = pruned
	}
	tree, err := engine.ParseSource(l, src)
	if err != nil {
		return nil, nil, err
	}
	// Findings carry only extracted strings, so the C-side tree can be
	// released as soon as this file is done instead of waiting for the GC.
	defer tree.Close()
	// Index the tree once and share it across every rule for this file.
	idx := engine.BuildIndex(tree)
	var findings []engine.Finding
	for _, rr := range live {
		for _, f := range rr.cr.RunIndexed(l, tree, src, idx) {
			f.File = rp
			if rr.hasOverride {
				f.Severity = rr.sevOverride
			}
			findings = append(findings, f)
		}
	}
	// Drop findings silenced by inline `lint:ignore` comments. The tree walk
	// is only worth it when something fired and the directive text appears.
	if len(findings) > 0 && bytes.Contains(src, []byte("lint:ignore")) {
		findings = filterSuppressed(findings, collectSuppressions(l, tree, src))
	}
	return findings, src, nil
}

// pruneByPath drops rules whose path globs exclude the file (copy-on-write,
// like pruneByAnchor).
func pruneByPath(rules []ruleRun, rp string) []ruleRun {
	out := rules
	copied := false
	for i, rr := range rules {
		if pathMatches(rr.cr, rp) {
			if copied {
				out = append(out, rr)
			}
			continue
		}
		if !copied {
			out = make([]ruleRun, 0, len(rules)-1)
			out = append(out, rules[:i]...)
			copied = true
		}
	}
	return out
}

// pruneByAnchor drops rules whose required anchor literal does not occur in
// the file's bytes. A rule with no anchor is always kept.
func pruneByAnchor(rules []ruleRun, l *lang.Language, src []byte) []ruleRun {
	out := rules
	copied := false
	for i, rr := range rules {
		a := rr.cr.AnchorFor(l)
		if a == "" || bytes.Contains(src, []byte(a)) {
			if copied {
				out = append(out, rr)
			}
			continue
		}
		if !copied {
			out = make([]ruleRun, 0, len(rules)-1)
			out = append(out, rules[:i]...)
			copied = true
		}
	}
	return out
}

// anyTag reports whether a rule carries at least one of the filter tags.
func anyTag(cr *engine.CompiledRule, tagFilter map[string]bool) bool {
	for _, t := range cr.Tags {
		if tagFilter[t] {
			return true
		}
	}
	return false
}

func collectFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != p && (ignoredDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") && d.Name() != ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if _, ok := lang.ForPath(path); ok {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// reportPath makes a path relative to the working directory when possible.
func reportPath(path, cwd string) string {
	abs, err := filepath.Abs(path)
	if err != nil || cwd == "" {
		return path
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil {
		return path
	}
	slash := filepath.ToSlash(rel)
	if slash == ".." || strings.HasPrefix(slash, "../") {
		return path
	}
	return rel
}
