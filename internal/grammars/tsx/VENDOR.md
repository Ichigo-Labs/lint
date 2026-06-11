# Vendored grammar: tsx (compressed tables)

- Upstream grammar: github.com/tree-sitter/tree-sitter-typescript v0.21.2
- Vendored from: github.com/smacker/go-tree-sitter@v0.0.0-20240827094217-dd81d9e9be82, directory typescript/tsx/
  (byte-identical grammar to what lint linked before vendoring)
- Parser ABI: LANGUAGE_VERSION 14 (smacker runtime supports 13–14)
- Generated 2026-06-10 by internal/grammars/gen. Regenerate with:

      go run ./internal/grammars/gen -sub typescript/tsx -name tsx -upstream "github.com/tree-sitter/tree-sitter-typescript v0.21.2"

What gen changed relative to the smacker copy:

- ts_parse_table, ts_small_parse_table, ts_small_parse_table_map and
  ts_parse_actions (1.4 MB, ~95% of parser.c) are removed from the C source
  and shipped as tables.bin.gz; binding.go decompresses them on first use and
  hands them to lint_tsx_set_tables(). The bytes are exactly what the
  C compiler laid out for the original arrays, captured by running a dumper
  built from the unmodified parser.c.
- Every public symbol is prefixed lint_ (lint_tree_sitter_tsx, the external scanner
  entry points) so tests can link this package and smacker's original
  side by side; the per-platform equivalence test requires both grammars to
  produce identical trees.
- scanner includes of ../alloc.h, ../array.h are rewritten to local copies.

No other modifications.
