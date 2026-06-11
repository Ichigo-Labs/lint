// Command gen vendors a Tree-sitter grammar from the smacker/go-tree-sitter
// module with its parse tables split out of the C source and stored
// compressed, to be decompressed at runtime on first use of the language.
//
// The four large generated arrays (ts_parse_table, ts_small_parse_table,
// ts_small_parse_table_map, ts_parse_actions) make up ~95% of a generated
// parser.c and are referenced only through pointer fields of the TSLanguage
// struct — the grammar's own code never reads them. gen therefore:
//
//  1. compiles the original parser.c with a small dumper main() and runs it,
//     capturing the arrays' exact in-memory bytes (so C initializer parsing
//     is never reimplemented here, and struct/bitfield layout is whatever
//     the C compiler says it is);
//  2. gzips those bytes into tables.bin.gz (gzip's CRC doubles as an
//     integrity check at load time);
//  3. writes a patched parser.c where each array is a pointer assigned at
//     runtime by a lint_<name>_set_tables() function, and where every public
//     symbol gets a lint_ prefix so the vendored grammar can link alongside
//     the original smacker package (the equivalence tests need both);
//  4. writes the binding.go, VENDOR.md, and supporting headers for a
//     package under internal/grammars/<name>.
//
// Usage:
//
//	go run ./internal/grammars/gen -sub csharp -name csharp \
//	    -upstream "github.com/tree-sitter/tree-sitter-c-sharp v0.21.3"
//
// The blob bakes in little-endian byte order and the host compiler's layout
// of TSParseActionEntry; every release platform is little-endian and the
// per-grammar equivalence test (vendored vs. smacker trees must be
// identical) runs on each of them in CI, so a layout divergence cannot ship.
package main

import (
	"compress/gzip"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const smackerVersion = "v0.0.0-20240827094217-dd81d9e9be82"

// arrays lists the generated tables to extract, in blob section order.
var arrays = []struct {
	ident   string // array identifier
	def     string // exact definition line (anchor)
	pointer string // pointer declaration that replaces the definition
	field   string // TSLanguage struct initializer line to remove
	assign  string // runtime assignment injected before `return &language;`
	castTo  string // cast applied in the setter
}{
	{
		ident:   "ts_parse_table",
		def:     "static const uint16_t ts_parse_table[LARGE_STATE_COUNT][SYMBOL_COUNT] = {",
		pointer: "static const uint16_t (*ts_parse_table)[SYMBOL_COUNT];",
		field:   "    .parse_table = &ts_parse_table[0][0],\n",
		assign:  "  language.parse_table = &ts_parse_table[0][0];\n",
		castTo:  "(const uint16_t (*)[SYMBOL_COUNT])",
	},
	{
		ident:   "ts_small_parse_table",
		def:     "static const uint16_t ts_small_parse_table[] = {",
		pointer: "static const uint16_t *ts_small_parse_table;",
		field:   "    .small_parse_table = ts_small_parse_table,\n",
		assign:  "  language.small_parse_table = ts_small_parse_table;\n",
		castTo:  "(const uint16_t *)",
	},
	{
		ident:   "ts_small_parse_table_map",
		def:     "static const uint32_t ts_small_parse_table_map[] = {",
		pointer: "static const uint32_t *ts_small_parse_table_map;",
		field:   "    .small_parse_table_map = ts_small_parse_table_map,\n",
		assign:  "  language.small_parse_table_map = ts_small_parse_table_map;\n",
		castTo:  "(const uint32_t *)",
	},
	{
		ident:   "ts_parse_actions",
		def:     "static const TSParseActionEntry ts_parse_actions[] = {",
		pointer: "static const TSParseActionEntry *ts_parse_actions;",
		field:   "    .parse_actions = ts_parse_actions,\n",
		assign:  "  language.parse_actions = ts_parse_actions;\n",
		castTo:  "(const TSParseActionEntry *)",
	},
}

func main() {
	var (
		sub      = flag.String("sub", "", `grammar directory inside the smacker module (e.g. "csharp", "typescript/tsx")`)
		name     = flag.String("name", "", `vendored package name (e.g. "csharp", "tsx")`)
		upstream = flag.String("upstream", "", `upstream provenance line for VENDOR.md (e.g. "github.com/tree-sitter/tree-sitter-c-sharp v0.21.3")`)
		out      = flag.String("out", "", `output directory (default internal/grammars/<name>)`)
	)
	flag.Parse()
	if *sub == "" || *name == "" || *upstream == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *out == "" {
		*out = filepath.Join("internal", "grammars", *name)
	}
	if err := run(*sub, *name, *upstream, *out); err != nil {
		fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}
}

func run(sub, name, upstream, out string) error {
	modRoot, err := smackerRoot()
	if err != nil {
		return err
	}
	srcDir := filepath.Join(modRoot, filepath.FromSlash(sub))
	parserSrc, err := os.ReadFile(filepath.Join(srcDir, "parser.c"))
	if err != nil {
		return err
	}
	src := string(parserSrc)

	cfunc, err := entryFunc(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	blob, sizes, err := dumpTables(src, srcDir, cfunc)
	if err != nil {
		return fmt.Errorf("dumping tables: %w", err)
	}
	gzSize, err := writeBlob(filepath.Join(out, "tables.bin.gz"), blob)
	if err != nil {
		return err
	}

	patched, err := patchParser(src, name, cfunc)
	if err != nil {
		return fmt.Errorf("patching parser.c: %w", err)
	}
	if err := os.WriteFile(filepath.Join(out, "parser.c"), []byte(patched), 0o644); err != nil {
		return err
	}

	if err := copyHeaders(srcDir, modRoot, out, cfunc, name); err != nil {
		return err
	}
	if err := writeBinding(out, name, cfunc); err != nil {
		return err
	}
	if err := writeVendorDoc(out, name, sub, upstream, cfunc, sizes); err != nil {
		return err
	}

	var total uint64
	for _, s := range sizes {
		total += s
	}
	fmt.Printf("%s: extracted %.1f MB of tables -> %.2f MB compressed (parser.c %.1f MB -> %.1f MB)\n",
		name, float64(total)/1e6, float64(gzSize)/1e6, float64(len(src))/1e6, float64(len(patched))/1e6)
	return nil
}

func smackerRoot() (string, error) {
	outb, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMODCACHE: %w", err)
	}
	root := filepath.Join(strings.TrimSpace(string(outb)), "github.com", "smacker", "go-tree-sitter@"+smackerVersion)
	if _, err := os.Stat(root); err != nil {
		return "", fmt.Errorf("smacker module not in module cache (run `go mod download`): %w", err)
	}
	return root, nil
}

func entryFunc(src string) (string, error) {
	m := regexp.MustCompile(`TS_PUBLIC const TSLanguage \*(tree_sitter_\w+)\(void\)`).FindStringSubmatch(src)
	if m == nil {
		return "", fmt.Errorf("entry function not found in parser.c")
	}
	return m[1], nil
}

// mustOnce replaces old with new in s, failing unless old occurs exactly once.
func mustOnce(s, old, new string) (string, error) {
	if n := strings.Count(s, old); n != 1 {
		return "", fmt.Errorf("anchor occurs %d times, want 1: %.80q", n, old)
	}
	return strings.Replace(s, old, new, 1), nil
}

// dumpTables compiles the original parser.c plus a dumper main() and runs it,
// returning the concatenated little-endian blob (header + sections) and the
// individual section sizes.
func dumpTables(src, srcDir, cfunc string) ([]byte, []uint64, error) {
	tmp, err := os.MkdirTemp("", "lint-grammar-gen-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)

	var b strings.Builder
	b.WriteString(src)
	// The TSLanguage struct references the external scanner functions from
	// scanner.c; the dumper never parses, so stubs satisfy the linker. (For
	// a grammar without an external scanner these are merely unused.)
	fmt.Fprintf(&b, "\nvoid *%[1]s_external_scanner_create(void) { return 0; }\n", cfunc)
	fmt.Fprintf(&b, "void %[1]s_external_scanner_destroy(void *p) { (void)p; }\n", cfunc)
	fmt.Fprintf(&b, "bool %[1]s_external_scanner_scan(void *p, TSLexer *l, const bool *v) { return false; }\n", cfunc)
	fmt.Fprintf(&b, "unsigned %[1]s_external_scanner_serialize(void *p, char *buf) { return 0; }\n", cfunc)
	fmt.Fprintf(&b, "void %[1]s_external_scanner_deserialize(void *p, const char *buf, unsigned n) {}\n", cfunc)
	b.WriteString("\n#include <stdio.h>\nint main(int argc, char **argv) {\n")
	b.WriteString("  if (argc != 2) return 2;\n  FILE *f = fopen(argv[1], \"wb\");\n  if (!f) return 1;\n")
	for _, a := range arrays {
		fmt.Fprintf(&b, "  { uint64_t n = sizeof(%s); if (fwrite(&n, 8, 1, f) != 1) return 1; }\n", a.ident)
	}
	for _, a := range arrays {
		fmt.Fprintf(&b, "  if (fwrite(%s, sizeof(%s), 1, f) != 1) return 1;\n", a.ident, a.ident)
	}
	b.WriteString("  return fclose(f) == 0 ? 0 : 1;\n}\n")

	dumpC := filepath.Join(tmp, "dump.c")
	if err := os.WriteFile(dumpC, []byte(b.String()), 0o644); err != nil {
		return nil, nil, err
	}
	dumpBin := filepath.Join(tmp, "dump")
	cc := exec.Command("cc", "-O0", "-w", "-I", srcDir, "-o", dumpBin, dumpC)
	if outb, err := cc.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("cc: %v\n%s", err, outb)
	}
	rawPath := filepath.Join(tmp, "tables.bin")
	if outb, err := exec.Command(dumpBin, rawPath).CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("dump run: %v\n%s", err, outb)
	}
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		return nil, nil, err
	}

	n := len(arrays)
	if len(raw) < 8*n {
		return nil, nil, fmt.Errorf("dump output truncated (%d bytes)", len(raw))
	}
	sizes := make([]uint64, n)
	var total uint64
	for i := range sizes {
		sizes[i] = binary.LittleEndian.Uint64(raw[8*i:])
		total += sizes[i]
	}
	if uint64(len(raw)) != uint64(8*n)+total {
		return nil, nil, fmt.Errorf("dump output size mismatch: %d bytes, sections claim %d", len(raw), uint64(8*n)+total)
	}

	// Blob layout (before gzip): "LTB1" | u32 section count | u64 sizes... | data...
	blob := make([]byte, 0, 8+len(raw))
	blob = append(blob, 'L', 'T', 'B', '1')
	blob = binary.LittleEndian.AppendUint32(blob, uint32(n))
	blob = append(blob, raw...)
	return blob, sizes, nil
}

func writeBlob(path string, blob []byte) (int64, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	if _, err := zw.Write(blob); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

func patchParser(src, name, cfunc string) (string, error) {
	var err error
	for _, a := range arrays {
		start := strings.Index(src, "\n"+a.def+"\n")
		if start < 0 || strings.Count(src, "\n"+a.def+"\n") != 1 {
			return "", fmt.Errorf("array definition anchor not found exactly once: %q", a.def)
		}
		end := strings.Index(src[start:], "\n};\n")
		if end < 0 {
			return "", fmt.Errorf("no closing `};` after %q", a.def)
		}
		src = src[:start] + "\n" + a.pointer + "\n" + src[start+end+4:]

		if src, err = mustOnce(src, a.field, ""); err != nil {
			return "", err
		}
	}

	if src, err = mustOnce(src, "  static const TSLanguage language = {", "  static TSLanguage language = {"); err != nil {
		return "", err
	}

	var assigns strings.Builder
	assigns.WriteString("  };\n")
	for _, a := range arrays {
		assigns.WriteString(a.assign)
	}
	assigns.WriteString("  return &language;")
	if src, err = mustOnce(src, "  };\n  return &language;", assigns.String()); err != nil {
		return "", err
	}

	var setter strings.Builder
	setter.WriteString("\n// Added by internal/grammars/gen: receives the parse tables that the\n")
	setter.WriteString("// vendoring step extracted from this file and compressed into\n")
	setter.WriteString("// tables.bin.gz. binding.go decompresses them and calls this exactly once\n")
	fmt.Fprintf(&setter, "// before the first lint_%s() call.\n", cfunc)
	fmt.Fprintf(&setter, "void lint_%s_set_tables(const void *parse_table,\n", name)
	pad := strings.Repeat(" ", len("void lint_"+name+"_set_tables("))
	fmt.Fprintf(&setter, "%sconst void *small_parse_table,\n", pad)
	fmt.Fprintf(&setter, "%sconst void *small_parse_table_map,\n", pad)
	fmt.Fprintf(&setter, "%sconst void *parse_actions) {\n", pad)
	for i, a := range arrays {
		param := []string{"parse_table", "small_parse_table", "small_parse_table_map", "parse_actions"}[i]
		fmt.Fprintf(&setter, "  %s = %s%s;\n", a.ident, a.castTo, param)
	}
	setter.WriteString("}\n")
	src += setter.String()

	// Prefix every public symbol (the entry function and the five external
	// scanner functions all share the cfunc prefix) so the vendored grammar
	// can be linked together with the original smacker package in tests.
	src = strings.ReplaceAll(src, cfunc, "lint_"+cfunc)
	return src, nil
}

func copyHeaders(srcDir, modRoot, out, cfunc, name string) error {
	relUp := regexp.MustCompile(`#include "\.\./(\w+\.h)"`)
	relLocal := regexp.MustCompile(`#include "(?:\./)?(\w+\.h)"`)
	var queue []string
	copied := map[string]bool{}

	copyFile := func(from, to string, rename bool) error {
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		s := relUp.ReplaceAllString(string(data), `#include "$1"`)
		// Headers referenced from the smacker module root must come along
		// (transitively: array.h itself includes "./alloc.h").
		for _, m := range relUp.FindAllStringSubmatch(string(data), -1) {
			queue = append(queue, m[1])
		}
		if rename {
			s = strings.ReplaceAll(s, cfunc, "lint_"+cfunc)
		}
		return os.WriteFile(to, []byte(s), 0o644)
	}

	if err := copyFile(filepath.Join(srcDir, "parser.h"), filepath.Join(out, "parser.h"), false); err != nil {
		return err
	}
	for _, h := range []string{"scanner.c", "scanner.h"} {
		from := filepath.Join(srcDir, h)
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if err := copyFile(from, filepath.Join(out, h), true); err != nil {
			return err
		}
	}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if copied[h] || h == "parser.h" {
			continue
		}
		copied[h] = true
		data, err := os.ReadFile(filepath.Join(modRoot, h))
		if err != nil {
			return err
		}
		for _, m := range relLocal.FindAllStringSubmatch(string(data), -1) {
			queue = append(queue, m[1])
		}
		if err := os.WriteFile(filepath.Join(out, h), data, 0o644); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(modRoot, "LICENSE"), filepath.Join(out, "LICENSE"), false); err != nil {
		return err
	}
	return prefixScannerGlobals(out, name)
}

// prefixScannerGlobals renames every global symbol an external scanner
// defines beyond the already-prefixed tree_sitter_* entry points (some
// scanners declare helper data without `static`, e.g. swift's OP_SYMBOLS),
// so the vendored package can link next to the original smacker package in
// the equivalence tests. The globals are discovered by compiling the scanner
// and reading the object's symbol table rather than by parsing C.
func prefixScannerGlobals(out, name string) error {
	scanner := filepath.Join(out, "scanner.c")
	if _, err := os.Stat(scanner); err != nil {
		return nil
	}
	tmp, err := os.MkdirTemp("", "lint-grammar-scan-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	obj := filepath.Join(tmp, "scanner.o")
	if outb, err := exec.Command("cc", "-c", "-O0", "-w", "-I", out, "-o", obj, scanner).CombinedOutput(); err != nil {
		return fmt.Errorf("cc scanner.c: %v\n%s", err, outb)
	}
	nmOut, err := exec.Command("nm", "-g", "--defined-only", obj).Output()
	if err != nil {
		return fmt.Errorf("nm: %w", err)
	}

	var defines strings.Builder
	for _, line := range strings.Split(string(nmOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sym := fields[len(fields)-1]
		if strings.HasPrefix(sym, "lint_") {
			continue
		}
		fmt.Fprintf(&defines, "#define %s lint_%s_%s\n", sym, name, sym)
	}
	if defines.Len() == 0 {
		return nil
	}

	data, err := os.ReadFile(scanner)
	if err != nil {
		return err
	}
	hdr := "// Added by internal/grammars/gen: prefix the scanner's non-static\n" +
		"// globals so this package can link alongside the smacker original in\n" +
		"// the equivalence tests.\n" + defines.String() + "\n"
	return os.WriteFile(scanner, append([]byte(hdr), data...), 0o644)
}

func writeBinding(out, name, cfunc string) error {
	display := name
	var b strings.Builder
	fmt.Fprintf(&b, `// Package %s exposes the vendored %s grammar. Its parse tables are
// stored gzip-compressed (tables.bin.gz) and decompressed into C memory on
// the first GetLanguage call; see VENDOR.md for provenance and regeneration.
package %s

/*
#include "parser.h"
const TSLanguage *lint_%s(void);
void lint_%s_set_tables(const void *parse_table,
                        const void *small_parse_table,
                        const void *small_parse_table_map,
                        const void *parse_actions);
*/
import "C"

import (
	_ "embed"
	"sync"
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/grammars/tables"
)

//go:embed tables.bin.gz
var tablesGz []byte

var once sync.Once

// GetLanguage returns the Tree-sitter grammar for %s. The first call
// decompresses the parse tables into C-allocated memory that lives for the
// rest of the process (the grammar reads it on every parse).
func GetLanguage() *sitter.Language {
	once.Do(func() {
		s := tables.Sections(tablesGz, 4)
		C.lint_%s_set_tables(C.CBytes(s[0]), C.CBytes(s[1]), C.CBytes(s[2]), C.CBytes(s[3]))
	})
	return sitter.NewLanguage(unsafe.Pointer(C.lint_%s()))
}
`, name, display, name, cfunc, name, display, name, cfunc)
	return os.WriteFile(filepath.Join(out, "binding.go"), []byte(b.String()), 0o644)
}

func writeVendorDoc(out, name, sub, upstream, cfunc string, sizes []uint64) error {
	var total uint64
	for _, s := range sizes {
		total += s
	}
	doc := fmt.Sprintf(`# Vendored grammar: %s (compressed tables)

- Upstream grammar: %s
- Vendored from: github.com/smacker/go-tree-sitter@%s, directory %s/
  (byte-identical grammar to what lint linked before vendoring)
- Parser ABI: LANGUAGE_VERSION 14 (smacker runtime supports 13–14)
- Generated %s by internal/grammars/gen. Regenerate with:

      go run ./internal/grammars/gen -sub %s -name %s -upstream "%s"

What gen changed relative to the smacker copy:

- ts_parse_table, ts_small_parse_table, ts_small_parse_table_map and
  ts_parse_actions (%.1f MB, ~95%% of parser.c) are removed from the C source
  and shipped as tables.bin.gz; binding.go decompresses them on first use and
  hands them to lint_%s_set_tables(). The bytes are exactly what the
  C compiler laid out for the original arrays, captured by running a dumper
  built from the unmodified parser.c.
- Every public symbol is prefixed lint_ (lint_%s, the external scanner
  entry points) so tests can link this package and smacker's original
  side by side; the per-platform equivalence test requires both grammars to
  produce identical trees.
- scanner includes of ../alloc.h, ../array.h are rewritten to local copies.

No other modifications.
`, name, upstream, smackerVersion, sub, time.Now().Format("2006-01-02"), sub, name, upstream,
		float64(total)/1e6, name, cfunc)
	return os.WriteFile(filepath.Join(out, "VENDOR.md"), []byte(doc), 0o644)
}
