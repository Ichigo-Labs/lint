# Vendored grammar: tree-sitter-xml

- Upstream: https://github.com/tree-sitter-grammars/tree-sitter-xml
- Tag: v0.7.0 (commit 4b64dd3a03ec002258d6268d712fd93716d6ab57)
- License: MIT (see LICENSE)
- Parser ABI: LANGUAGE_VERSION 14 (compatible with the smacker/go-tree-sitter
  runtime, which supports 13–14)

Files and their upstream paths:

| Local | Upstream |
| --- | --- |
| `parser.c` | `xml/src/parser.c` |
| `scanner.c` | `xml/src/scanner.c` |
| `scanner-common.h` | `common/scanner.h` |
| `tree_sitter/parser.h` | `xml/src/tree_sitter/parser.h` |
| `tree_sitter/array.h` | `xml/src/tree_sitter/array.h` |
| `tree_sitter/alloc.h` | `xml/src/tree_sitter/alloc.h` |

Local modifications:

- `scanner.c`: `#include "../../common/scanner.h"` rewritten to
  `#include "scanner-common.h"` (the file is vendored flat next to it).

The companion DTD grammar from the same repo is not vendored; only
`common/scanner.h` is shared and the `TS_XML` define selects the XML tokens.
