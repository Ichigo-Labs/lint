// Package fix applies autofix replacements from findings back to source files.
package fix

import (
	"os"
	"sort"

	"github.com/ichigo-labs/lint/internal/engine"
)

// Result summarizes an apply pass.
type Result struct {
	FilesChanged int
	FixesApplied int
	Skipped      int // fixes dropped due to overlap with another fix
}

// Apply rewrites files in place using the Fix field of findings. Findings
// without a fix are ignored. Fixes are applied from the end of the file
// backwards so earlier byte offsets stay valid; when two fixes overlap, the
// later (innermost, higher-offset) one wins and the conflicting outer fix is
// skipped and counted in Skipped.
func Apply(findings []engine.Finding, sources map[string][]byte) (Result, error) {
	var res Result
	byFile := map[string][]engine.Finding{}
	for _, f := range findings {
		if f.Fix == nil {
			continue
		}
		byFile[f.File] = append(byFile[f.File], f)
	}

	for file, fs := range byFile {
		src, ok := sources[file]
		if !ok {
			b, err := os.ReadFile(file)
			if err != nil {
				return res, err
			}
			src = b
		}
		out, applied, skipped := ApplyBytes(src, fs)
		res.Skipped += skipped
		if applied > 0 {
			info, err := os.Stat(file)
			mode := os.FileMode(0644)
			if err == nil {
				mode = info.Mode()
			}
			if err := os.WriteFile(file, out, mode); err != nil {
				return res, err
			}
			res.FilesChanged++
			res.FixesApplied += applied
		}
	}
	return res, nil
}

// ApplyBytes applies the fixes of one file's findings to its source bytes,
// returning the rewritten bytes plus how many fixes were applied and how many
// were skipped (overlap with an already-applied fix). Findings without a fix
// are ignored. The input slice's order is not preserved.
func ApplyBytes(src []byte, fs []engine.Finding) ([]byte, int, int) {
	// Apply from the end of the file backwards so earlier offsets stay valid.
	sort.Slice(fs, func(i, j int) bool { return fs[i].StartByte > fs[j].StartByte })

	out := append([]byte(nil), src...)
	lastStart := uint32(len(src) + 1)
	applied, skipped := 0, 0
	for _, f := range fs {
		if f.Fix == nil {
			continue
		}
		if f.EndByte > lastStart { // overlaps a fix we already applied
			skipped++
			continue
		}
		if int(f.EndByte) > len(out) {
			skipped++
			continue
		}
		repl := []byte(*f.Fix)
		newOut := make([]byte, 0, len(out)-int(f.EndByte-f.StartByte)+len(repl))
		newOut = append(newOut, out[:f.StartByte]...)
		newOut = append(newOut, repl...)
		newOut = append(newOut, out[f.EndByte:]...)
		out = newOut
		lastStart = f.StartByte
		applied++
	}
	return out, applied, skipped
}
