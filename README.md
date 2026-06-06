# lintel

**lintel** finds bugs and bad patterns in your code by matching its *syntax
tree*, not its text. You write small rules in a focused DSL, and lintel runs
them across C, C++, C#, Go, Java, Python, Rust, and TypeScript/JavaScript — one
rule language for every codebase.

It is a modern, multi-language, AST-based successor to
[checkr](https://github.com/eriklanning/checkr): same "write a project lint rule
in a minute" spirit, but matching is structural (over a Tree-sitter parse tree)
instead of line-by-line regex.

```
$ lintel check
main.go
  6:2  warning use slog instead of fmt.Println [no-println]
     6 |     fmt.Println("hello")
       |     ^^^^^^^^^^^^^^^^^^^^

✖ 1 warning(s)
```

## Why structural matching?

A grep/regex rule like `fmt\.Println` can't tell `fmt.Println(x)` from
`myfmt.Println` in a comment, a string, or `fmt.Sprintln`. lintel matches the
*shape* of the code:

- **Formatting doesn't matter.** `panic( "x" )`, `panic("x")`, and a `panic`
  split across lines are all the same pattern.
- **Operators *do* matter.** `$A + $B` matches `+`, never `-`.
- **Comments and strings are not code.** A pattern only matches real syntax.
- **Metavariables capture and back-reference.** `$X = append($Y, ...)` with
  `where $X != $Y` finds copy/paste slice bugs that no regex can express.

Compared to per-language linters (ESLint, golangci-lint, Roslyn analyzers),
lintel trades depth for reach: you get *one* tiny rule language and a fast feedback
loop (`lintel test`) for project-specific rules across every language in your
repo, without writing a plugin in each ecosystem.

## Install / build

lintel embeds Tree-sitter grammars, so building it needs **Go and a C compiler
with CGO enabled**.

```bash
git clone https://github.com/eriklanning/lintel
cd lintel
CGO_ENABLED=1 go build -o lintel ./cmd/lintel
```

That produces a `lintel` binary in the current directory. Put it on your `PATH`
(or run `make install` to install it into `$GOBIN`). `make` and `make test` are
also available.

> Building without CGO (`CGO_ENABLED=0`) will fail — the grammars are C and are
> compiled into the binary.

## Quickstart

```bash
# 1. Create a rules directory.
mkdir .lintel

# 2. Write a rule. (A single-rule file may omit the rule{} wrapper.)
cat > .lintel/no-println.lint <<'EOF'
rule no-println {
  message  "use slog instead of fmt.Println"
  severity warning
  in       go
  pattern  { fmt.Println($$$ARGS) }
}
EOF

# 3. Check the current directory.
lintel check
```

Useful commands while authoring:

```bash
lintel list                          # show discovered rules
lintel test                          # run rules' inline test blocks
lintel parse --lang go --pattern 'fmt.Println($$$)'   # see how a pattern compiles
lintel parse main.go                 # dump a file's syntax tree (find node kinds)
lintel new my-rule --lang go         # scaffold a new rule
lintel langs                         # list supported languages
```

### How rules are discovered

Running `lintel` (or `lintel check`) with no `--rules` flag discovers every
`.lintel/` directory **from the current directory up through its ancestors and
down through its descendants**, and loads every `*.lint` file in them. Ancestor
rules apply to the whole tree; a nested `.lintel/` applies only to its own
subtree. This lets a monorepo keep global rules at the root and package-specific
rules deeper down.

Pass `--rules <dir>` to load rules from exactly one directory instead (the files
directly inside it — discovery is not recursive into that directory).

## Feature overview

| Feature | What it gives you |
| --- | --- |
| Structural matching | Patterns match the AST, so whitespace/comments don't, but operators do |
| One DSL, many languages | The same `.lint` syntax targets all supported languages |
| Metavariables | `$NAME` (named, back-referencing), `$_` (wildcard), `$$$NAME` (variadic) |
| `where` predicates | Constrain captures by regex, node kind, membership, equality, or a sub-pattern |
| Structural context | `inside` / `has` (and `not inside` / `not has`) filter by surrounding code |
| `any` / `all` / `not` | Combine matchers with boolean logic |
| Raw queries | Drop to a Tree-sitter s-expression `query "..."` when patterns aren't enough |
| Autofix | `fix "..."` templates rewritten by `lintel check --fix` |
| Inline tests | `test { match ... no_match ... }` run by `lintel test` |
| Severities | `error`, `warning`, `info`; `--quiet`, `--error-on-warning` control exit code |
| JSON output | `--json` for editor/CI integration |
| Parallel + scoped | `-j` workers; `.lintel/` discovery scopes rules to subtrees |
| Polyglot rules | Omit `in` and a rule compiles for every language whose grammar accepts it |

## Supported languages

| Language | `in` name (aliases) | Extensions |
| --- | --- | --- |
| C | `c` | `.c .h` |
| C++ | `cpp` (`c++`, `cc`) | `.cpp .cxx .cc .hpp .hxx .hh` |
| C# | `csharp` (`cs`, `c#`) | `.cs` |
| Go | `go` (`golang`) | `.go` |
| Java | `java` | `.java` |
| Python | `python` (`py`) | `.py .pyi` |
| Rust | `rust` (`rs`) | `.rs` |
| TypeScript | `typescript` (`ts`) | `.ts .mts .cts` |
| TSX / JS | `tsx` (`javascript`, `js`, `jsx`) | `.tsx .js .jsx .mjs .cjs` |

(Run `lintel langs` to print this list.)

## Example rules

These are real, tested rules from [`examples/`](examples/). Every rule there
carries an inline `test` block; run `lintel test --rules examples/go` (etc.) to
see them pass.

**Go — flag `panic` in library code** ([`examples/go/panic-in-library.lint`](examples/go/panic-in-library.lint)):

```
rule panic-in-library {
  message  "avoid panic in library code; return an error instead"
  severity warning
  note     "Panicking crashes the caller's program. Reusable packages should return errors and let callers decide. Reserve panic for truly unrecoverable, programmer-error conditions."
  in       go

  pattern { panic($$$) }

  test {
    match    """
package store
func Get(k string) string {
	v, ok := m[k]
	if !ok {
		panic("missing key")
	}
	return v
}
"""
    no_match """
package store
func Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", errors.New("missing key")
	}
	return v, nil
}
"""
  }
}
```

**TypeScript — flag leftover `console.*` debugging** ([`examples/typescript/no-console.lint`](examples/typescript/no-console.lint)):

```
rule no-console {
  message  "Remove `console.$M` before committing; use a real logger instead"
  severity warning
  in       typescript
  note     "console.log/debug/info/trace calls are usually leftover debugging output."

  pattern { console.$M($$$) }
  where    $M in [log, debug, info, trace]

  test {
    match    "console.log('hello', user)"
    match    "console.debug(payload)"
    no_match "console.error('a real error')"
    no_match "logger.log('using a real logger')"
  }
}
```

The `where $M in [...]` line shows a capture (`$M`, the method name) being
constrained to a fixed set, so `console.error(...)` is *not* flagged.

There are ready-to-read examples for every language under
[`examples/`](examples/): `c/`, `cpp/`, `csharp/`, `go/`, `java/`, `python/`,
`rust/`, and `typescript/`.

## Documentation

- [docs/dsl.md](docs/dsl.md) — the complete `.lint` DSL reference: every clause
  and every `where` operator, with worked examples.
- [docs/writing-rules.md](docs/writing-rules.md) — a practical guide to authoring
  rules: discovering node kinds with `lintel parse`, the metavariable model,
  per-language patterns, fixes, tests, and known limitations.

## License

See the repository for license details.
