// Package runner discovers `.lint` rules and applies them across a codebase.
package runner

import (
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

// RuleSet is a collection of compiled rules, each scoped to a directory subtree.
type RuleSet struct {
	rules []scopedRule
}

type scopedRule struct {
	rule  *engine.CompiledRule
	scope string // absolute directory; rule applies to files at or under it
}

// Rules returns every compiled rule (ignoring scope), sorted by id.
func (rs *RuleSet) Rules() []*engine.CompiledRule {
	out := make([]*engine.CompiledRule, 0, len(rs.rules))
	for _, sr := range rs.rules {
		out = append(out, sr.rule)
	}
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

// Load discovers `.lint/` directories from root upward (ancestors) and
// downward (descendants), compiling every `*.lint` rule it finds. If
// rulesDirOverride is set, only that directory is loaded and applied to the
// whole tree.
func Load(root, rulesDirOverride string) (*RuleSet, []LoadError, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	rs := &RuleSet{}
	var lerrs []LoadError
	seen := map[string]bool{}

	loadDir := func(lintDir, scope string) {
		if seen[lintDir] {
			return
		}
		seen[lintDir] = true
		entries, err := os.ReadDir(lintDir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".lint") {
				continue
			}
			path := filepath.Join(lintDir, e.Name())
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
				rs.rules = append(rs.rules, scopedRule{rule: cr, scope: scope})
			}
		}
	}

	if rulesDirOverride != "" {
		abs, err := filepath.Abs(rulesDirOverride)
		if err != nil {
			return nil, nil, err
		}
		// An explicit --rules directory applies to every checked file, not just
		// files under the working directory. Empty scope means "global".
		loadDir(abs, "")
		return rs, lerrs, nil
	}

	// Ancestors: every <ancestor>/.lint applies to that ancestor's subtree.
	for dir := absRoot; ; {
		ld := filepath.Join(dir, ".lint")
		if fi, err := os.Stat(ld); err == nil && fi.IsDir() {
			loadDir(ld, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Descendants: nested .lint dirs apply to their own subtree.
	filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != absRoot && ignoredDirs[d.Name()] {
				if d.Name() == ".lint" {
					loadDir(path, filepath.Dir(path))
				}
				return filepath.SkipDir
			}
			return nil
		}
		return nil
	})

	return rs, lerrs, nil
}

// Options configures a check run.
type Options struct {
	Paths       []string        // files/dirs to check (default ".")
	Concurrency int             // worker count (default GOMAXPROCS)
	LangFilter  map[string]bool // if non-empty, only these languages
}

// Result is the outcome of a check run.
type Result struct {
	Findings []engine.Finding
	Sources  map[string][]byte // file -> contents, for findings' files
	FileErrs []LoadError       // files that failed to read/parse
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

	type job struct{ path string }
	jobs := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	res := &Result{Sources: map[string][]byte{}}

	worker := func() {
		defer wg.Done()
		for j := range jobs {
			fs, src, ferr := rs.checkFile(j.path, opts.LangFilter)
			mu.Lock()
			if ferr != nil {
				res.FileErrs = append(res.FileErrs, LoadError{Path: j.path, Err: ferr})
			}
			if len(fs) > 0 {
				res.Findings = append(res.Findings, fs...)
				res.Sources[reportPath(j.path)] = src
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

func (rs *RuleSet) checkFile(path string, langFilter map[string]bool) ([]engine.Finding, []byte, error) {
	l, ok := lang.ForPath(path)
	if !ok {
		return nil, nil, nil
	}
	if len(langFilter) > 0 && !langFilter[l.Name] {
		return nil, nil, nil
	}
	absDir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, nil, err
	}
	applicable := rs.applicable(absDir, l)
	if len(applicable) == 0 {
		return nil, nil, nil
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	tree, err := engine.ParseSource(l, src)
	if err != nil {
		return nil, nil, err
	}
	rp := reportPath(path)
	var findings []engine.Finding
	for _, cr := range applicable {
		for _, f := range cr.Run(l, tree, src) {
			f.File = rp
			findings = append(findings, f)
		}
	}
	return findings, src, nil
}

func (rs *RuleSet) applicable(absDir string, l *lang.Language) []*engine.CompiledRule {
	var out []*engine.CompiledRule
	for _, sr := range rs.rules {
		if !withinScope(absDir, sr.scope) {
			continue
		}
		if rs.ruleTargets(sr.rule, l) {
			out = append(out, sr.rule)
		}
	}
	return out
}

func (rs *RuleSet) ruleTargets(cr *engine.CompiledRule, l *lang.Language) bool {
	for _, rl := range cr.Languages() {
		if rl.Name == l.Name {
			return true
		}
	}
	return false
}

func withinScope(dir, scope string) bool {
	if scope == "" || dir == scope {
		return true // empty scope = global (e.g. an explicit --rules dir)
	}
	return strings.HasPrefix(dir, scope+string(os.PathSeparator))
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
func reportPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}
