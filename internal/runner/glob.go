package runner

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/ichigo-labs/lint/internal/engine"
)

// pathMatches reports whether a rule applies to the file at report path rp,
// honoring the rule's `paths` (include) and `exclude` globs. A rule with no
// `paths` applies everywhere except where an `exclude` glob matches.
func pathMatches(cr *engine.CompiledRule, rp string) bool {
	rp = filepath.ToSlash(rp)
	for _, g := range cr.ExcludePaths {
		if globMatch(g, rp) {
			return false
		}
	}
	if len(cr.Paths) == 0 {
		return true
	}
	for _, g := range cr.Paths {
		if globMatch(g, rp) {
			return true
		}
	}
	return false
}

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

// globMatch reports whether path matches a glob supporting `*` (any run of
// non-separator chars), `?` (one non-separator char), and `**` (any run of
// chars, crossing directory separators).
func globMatch(glob, path string) bool {
	re := compileGlob(glob)
	if re == nil {
		return false
	}
	return re.MatchString(path)
}

func compileGlob(glob string) *regexp.Regexp {
	glob = filepath.ToSlash(glob)
	globMu.Lock()
	defer globMu.Unlock()
	if re, ok := globCache[glob]; ok {
		return re
	}
	re := regexp.MustCompile("^" + globToRegexp(glob) + "$")
	globCache[glob] = re
	return re
}

func globToRegexp(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch c {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				// `**` (optionally followed by `/`) crosses directories.
				i++
				if i+1 < len(glob) && glob[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	return b.String()
}
