// Package report renders findings for humans and machines.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/eriklanning/lintel/internal/dsl"
	"github.com/eriklanning/lintel/internal/engine"
)

// Style controls colorized output.
type Style struct {
	Color bool
}

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	gray   = "\033[90m"
)

func (s Style) c(code, text string) string {
	if !s.Color {
		return text
	}
	return code + text + reset
}

func sevColor(sev dsl.Severity) string {
	switch sev {
	case dsl.Error:
		return red
	case dsl.Warning:
		return yellow
	default:
		return cyan
	}
}

// Counts summarizes findings by severity.
type Counts struct {
	Errors, Warnings, Infos int
}

// Tally counts findings by severity.
func Tally(findings []engine.Finding) Counts {
	var c Counts
	for _, f := range findings {
		switch f.Severity {
		case dsl.Error:
			c.Errors++
		case dsl.Warning:
			c.Warnings++
		default:
			c.Infos++
		}
	}
	return c
}

// Pretty writes findings grouped by file with a source excerpt and underline.
func Pretty(w io.Writer, findings []engine.Finding, sources map[string][]byte, style Style) {
	if len(findings) == 0 {
		return
	}
	byFile := map[string][]engine.Finding{}
	var files []string
	for _, f := range findings {
		if _, ok := byFile[f.File]; !ok {
			files = append(files, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	sort.Strings(files)

	for _, file := range files {
		fs := byFile[file]
		sort.Slice(fs, func(i, j int) bool {
			if fs[i].StartLine != fs[j].StartLine {
				return fs[i].StartLine < fs[j].StartLine
			}
			return fs[i].StartCol < fs[j].StartCol
		})
		fmt.Fprintln(w, style.c(bold, file))
		src := sources[file]
		for _, f := range fs {
			loc := fmt.Sprintf("%d:%d", f.StartLine, f.StartCol)
			sev := style.c(sevColor(f.Severity), string(f.Severity))
			rule := style.c(gray, "["+f.RuleID+"]")
			fmt.Fprintf(w, "  %s  %s %s %s\n",
				style.c(dim, loc), sev, f.Message, rule)
			writeExcerpt(w, src, f, style)
			if f.Note != "" {
				fmt.Fprintf(w, "      %s %s\n", style.c(gray, "note:"), f.Note)
			}
		}
		fmt.Fprintln(w)
	}
	writeSummary(w, Tally(findings), style)
}

// writeExcerpt prints the matched line(s) with a caret underline under the
// matched span on its first line.
func writeExcerpt(w io.Writer, src []byte, f engine.Finding, style Style) {
	if src == nil {
		return
	}
	lines := splitLines(src)
	if f.StartLine < 1 || f.StartLine > len(lines) {
		return
	}
	line := lines[f.StartLine-1]
	gutter := fmt.Sprintf("%6d | ", f.StartLine)
	fmt.Fprintf(w, "%s%s\n", style.c(gray, gutter), expandTabs(line))

	// Underline from StartCol to the end of the match on the first line.
	startCol := f.StartCol
	endCol := len(line) + 1
	if f.EndLine == f.StartLine {
		endCol = f.EndCol
	}
	if startCol < 1 {
		startCol = 1
	}
	pad := underlinePad(line, startCol-1)
	width := underlinePad(line, endCol-1) - pad
	if width < 1 {
		width = 1
	}
	caretGutter := strings.Repeat(" ", 6) + " | "
	carets := strings.Repeat(" ", pad) + strings.Repeat("^", width)
	fmt.Fprintf(w, "%s%s\n", style.c(gray, caretGutter), style.c(sevColor(f.Severity), carets))
}

// underlinePad computes display columns up to byte column n, expanding tabs.
func underlinePad(line string, n int) int {
	if n > len(line) {
		n = len(line)
	}
	w := 0
	for i := 0; i < n; i++ {
		if line[i] == '\t' {
			w += 4
		} else {
			w++
		}
	}
	return w
}

func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

func splitLines(src []byte) []string {
	return strings.Split(string(src), "\n")
}

func writeSummary(w io.Writer, c Counts, style Style) {
	parts := []string{}
	if c.Errors > 0 {
		parts = append(parts, style.c(red, fmt.Sprintf("%d error(s)", c.Errors)))
	}
	if c.Warnings > 0 {
		parts = append(parts, style.c(yellow, fmt.Sprintf("%d warning(s)", c.Warnings)))
	}
	if c.Infos > 0 {
		parts = append(parts, style.c(cyan, fmt.Sprintf("%d info", c.Infos)))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(w, "%s %s\n", style.c(bold, "✖"), strings.Join(parts, ", "))
}

// jsonFinding is the stable JSON shape.
type jsonFinding struct {
	RuleID    string            `json:"rule"`
	Severity  string            `json:"severity"`
	Message   string            `json:"message"`
	Note      string            `json:"note,omitempty"`
	File      string            `json:"file"`
	StartLine int               `json:"startLine"`
	StartCol  int               `json:"startColumn"`
	EndLine   int               `json:"endLine"`
	EndCol    int               `json:"endColumn"`
	Snippet   string            `json:"snippet"`
	Bindings  map[string]string `json:"bindings,omitempty"`
	Fix       *string           `json:"fix,omitempty"`
}

// JSON writes findings as a JSON array.
func JSON(w io.Writer, findings []engine.Finding) error {
	out := make([]jsonFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, jsonFinding{
			RuleID:    f.RuleID,
			Severity:  string(f.Severity),
			Message:   f.Message,
			Note:      f.Note,
			File:      f.File,
			StartLine: f.StartLine,
			StartCol:  f.StartCol,
			EndLine:   f.EndLine,
			EndCol:    f.EndCol,
			Snippet:   f.Snippet,
			Bindings:  f.Bindings,
			Fix:       f.Fix,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
