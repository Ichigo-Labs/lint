# Vendored grammar: csharp (compressed tables)

- Upstream grammar: github.com/tree-sitter/tree-sitter-c-sharp v0.21.3
- Vendored from: github.com/smacker/go-tree-sitter@v0.0.0-20240827094217-dd81d9e9be82, directory csharp/
  (byte-identical grammar to what lint linked before vendoring)
- Parser ABI: LANGUAGE_VERSION 14 (smacker runtime supports 13–14)
- Generated 2026-06-10 by internal/grammars/gen. Regenerate with:

      go run ./internal/grammars/gen -sub csharp -name csharp -upstream "github.com/tree-sitter/tree-sitter-c-sharp v0.21.3"

What gen changed relative to the smacker copy:

- ts_parse_table, ts_small_parse_table, ts_small_parse_table_map and
  ts_parse_actions (5.5 MB, ~95% of parser.c) are removed from the C source
  and shipped as tables.bin.gz; binding.go decompresses them on first use and
  hands them to lint_csharp_set_tables(). The bytes are exactly what the
  C compiler laid out for the original arrays, captured by running a dumper
  built from the unmodified parser.c.
- Every public symbol is prefixed lint_ (lint_tree_sitter_c_sharp, the external scanner
  entry points) so tests can link this package and smacker's original
  side by side; the per-platform equivalence test requires both grammars to
  produce identical trees.
- scanner includes of ../alloc.h, ../array.h are rewritten to local copies.

No other modifications.
