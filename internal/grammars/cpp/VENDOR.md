# Vendored grammar: cpp (compressed tables)

- Upstream grammar: github.com/tree-sitter/tree-sitter-cpp v0.22.3
- Vendored from: github.com/smacker/go-tree-sitter@v0.0.0-20240827094217-dd81d9e9be82, directory cpp/
  (byte-identical grammar to what lint linked before vendoring)
- Parser ABI: LANGUAGE_VERSION 14 (smacker runtime supports 13–14)
- Generated 2026-06-10 by internal/grammars/gen. Regenerate with:

      go run ./internal/grammars/gen -sub cpp -name cpp -upstream "github.com/tree-sitter/tree-sitter-cpp v0.22.3"

What gen changed relative to the smacker copy:

- ts_parse_table, ts_small_parse_table, ts_small_parse_table_map and
  ts_parse_actions (3.0 MB, ~95% of parser.c) are removed from the C source
  and shipped as tables.bin.gz; binding.go decompresses them on first use and
  hands them to lint_cpp_set_tables(). The bytes are exactly what the
  C compiler laid out for the original arrays, captured by running a dumper
  built from the unmodified parser.c.
- Every public symbol is prefixed lint_ (lint_tree_sitter_cpp, the external scanner
  entry points) so tests can link this package and smacker's original
  side by side; the per-platform equivalence test requires both grammars to
  produce identical trees.
- scanner includes of ../alloc.h, ../array.h are rewritten to local copies.

No other modifications.
