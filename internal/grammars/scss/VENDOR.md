# Vendored grammar: tree-sitter-scss

- Upstream: https://github.com/tree-sitter-grammars/tree-sitter-scss
- Commit: 2ef6d42e3ad7a8208900f9346f4529806ae0f9f9 (src/ identical to tag
  v1.0.0; pinned to this commit because the LICENSE file landed after the tag)
- License: MIT (see LICENSE)
- Parser ABI: LANGUAGE_VERSION 14 (compatible with the smacker/go-tree-sitter
  runtime, which supports 13–14)

Files and their upstream paths:

| Local | Upstream |
| --- | --- |
| `parser.c` | `src/parser.c` |
| `scanner.c` | `src/scanner.c` |
| `tree_sitter/parser.h` | `src/tree_sitter/parser.h` |

No local modifications.

The grammar extends tree-sitter-css (`grammar.js` requires
`tree-sitter-css/grammar`), so plain CSS parses under it as well.
