// Package fix applies autofix replacements from findings back to source files.
package fix

import (
	"os"
	"sort"

	"github.com/ichigo-labs/lintel/internal/engine"
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
		// Apply from the end of the file backwards so earlier offsets stay valid.
		sort.Slice(fs, func(i, j int) bool { return fs[i].StartByte > fs[j].StartByte })

		out := append([]byte(nil), src...)
		lastStart := uint32(len(src) + 1)
		applied := 0
		for _, f := range fs {
			if f.EndByte > lastStart { // overlaps a fix we already applied
				res.Skipped++
				continue
			}
			if int(f.EndByte) > len(out) {
				res.Skipped++
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
