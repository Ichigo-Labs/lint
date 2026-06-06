package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ichigo-labs/lint/internal/dsl"
)

// projectConfig is the optional, project-level rule configuration loaded from
// `.lint.toml` (or `lint.toml`) at the root. It disables rules and overrides
// their severity without editing the rule files themselves.
type projectConfig struct {
	disabled map[string]bool         // rule id -> disabled
	severity map[string]dsl.Severity // rule id -> overridden severity
}

func newProjectConfig() *projectConfig {
	return &projectConfig{disabled: map[string]bool{}, severity: map[string]dsl.Severity{}}
}

// isDisabled reports whether a rule is turned off by config.
func (c *projectConfig) isDisabled(id string) bool {
	return c != nil && c.disabled[id]
}

// severityFor returns the configured severity override for a rule, if any.
func (c *projectConfig) severityFor(id string) (dsl.Severity, bool) {
	if c == nil {
		return "", false
	}
	s, ok := c.severity[id]
	return s, ok
}

// loadProjectConfig reads `.lint.toml` / `lint.toml` from dir if present. A
// missing file yields an empty (no-op) config and no error.
func loadProjectConfig(dir string) (*projectConfig, error) {
	for _, name := range []string{".lint.toml", "lint.toml"} {
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		return parseProjectConfig(string(b)), nil
	}
	return newProjectConfig(), nil
}

// parseProjectConfig parses the supported TOML subset:
//
//	disable = ["a", "b"]          # top-level or under [project]
//
//	[rules.some-rule]
//	enabled  = false
//	severity = "error"
//
// It is intentionally lenient: unknown keys/sections and malformed lines are
// skipped rather than erroring, so a config never blocks a run.
func parseProjectConfig(content string) *projectConfig {
	cfg := newProjectConfig()
	section := ""
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch {
		case key == "disable" && (section == "" || section == "project"):
			for _, id := range parseTOMLStringArray(val) {
				cfg.disabled[id] = true
			}
		case strings.HasPrefix(section, "rules."):
			id := strings.TrimSpace(strings.TrimPrefix(section, "rules."))
			if id == "" {
				continue
			}
			switch key {
			case "enabled":
				if val == "false" {
					cfg.disabled[id] = true
				}
			case "severity":
				if sev, ok := parseSeverity(unquote(val)); ok {
					cfg.severity[id] = sev
				}
			}
		}
	}
	return cfg
}

// parseSeverity validates a severity string from config.
func parseSeverity(s string) (dsl.Severity, bool) {
	switch s {
	case "error":
		return dsl.Error, true
	case "warning", "warn":
		return dsl.Warning, true
	case "info":
		return dsl.Info, true
	}
	return "", false
}

// parseTOMLStringArray parses `["a", "b"]` into its string items.
func parseTOMLStringArray(val string) []string {
	val = strings.TrimSpace(val)
	if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
		return nil
	}
	var out []string
	for _, item := range strings.Split(val[1:len(val)-1], ",") {
		if item = unquote(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// unquote strips matching single or double quotes from a TOML scalar.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
