# lint

[![CI](https://github.com/Ichigo-Labs/lint/actions/workflows/ci.yml/badge.svg)](https://github.com/Ichigo-Labs/lint/actions/workflows/ci.yml)

**Quick and concise lint rules for agents.**

lint finds bugs and bad patterns by matching your code's *syntax tree*, not its
text: you write small rules in a focused DSL and run them across many languages
with `lint check`.

```
$ lint check
main.go
  6:2  warning use slog instead of fmt.Println [no-println]
     6 |     fmt.Println("hello")
       |     ^^^^^^^^^^^^^^^^^^^^

✖ 1 warning(s)
```

## Supported languages

| Language | `in` name (aliases) | Extensions |
| --- | --- | --- |
| C | `c` | `.c .h` |
| C++ | `cpp` (`c++`, `cc`) | `.cpp .cxx .cc .hpp .hxx .hh` |
| C# | `csharp` (`cs`, `c#`) | `.cs` |
| CSS / SCSS | `css` · `scss` | `.css` · `.scss` |
| Go | `go` (`golang`) | `.go` |
| HTML | `html` | `.html .htm` |
| Java | `java` | `.java` |
| JSON | `json` (`jsonc`) | `.json .jsonc` |
| Kotlin | `kotlin` (`kt`, `kts`) | `.kt .kts` |
| Python | `python` (`py`) | `.py .pyi` |
| Rust | `rust` (`rs`) | `.rs` |
| Swift | `swift` | `.swift` |
| TypeScript / JavaScript | `typescript` (`ts`) · `tsx` (`javascript`, `js`, `jsx`) | `.ts .mts .cts` · `.tsx .js .jsx .mjs .cjs` |
| XML | `xml` | `.xml .svg .xsd .xsl .xslt` |
| YAML | `yaml` (`yml`) | `.yaml .yml` |

Rows with a `·` group two registered languages of one family: the first `in`
name covers the first extension list, the second the second. `.ts` files use
the `typescript` grammar; `.tsx`/`.js` files use the `tsx` grammar — write
`in typescript, tsx` to cover both. Likewise `in css, scss`.

> **Note:** HTML, XML, CSS, SCSS, JSON, and YAML are only checked by rules
> that name them explicitly in an `in` clause. Their grammars parse nearly any
> text (a Python dict literal is valid JSON; almost anything is valid YAML),
> so rules that omit `in` (which normally compile for every language that can
> parse the pattern) skip them.

(Run `lint langs` to print this list.)

## Install

**CLI only**

```bash
curl -fsSL https://raw.githubusercontent.com/Ichigo-Labs/lint/master/install.sh | bash
```

**Project Hook**

```bash
curl -fsSL https://raw.githubusercontent.com/Ichigo-Labs/lint/master/install.sh | bash
lint init
```

1. Creates a platform agnostic copy of lint to `./bin`
2. Registers a lint pre-commit hook if one doesn't exist
3. Adds `./bin/.lint` to `.gitignore`

## Quickstart

```bash
# 1. Create a rules directory.
mkdir .lint

# 2. Write a rule. (A single-rule file may omit the rule{} wrapper.)
cat > .lint/no-println.lint <<'EOF'
rule no-println {
  message  "use slog instead of fmt.Println"
  severity warning
  in       go
  pattern  { fmt.Println($$$ARGS) }
}
EOF

# 3. Check the current directory.
lint check
```

Useful commands while authoring:

```bash
lint list                          # show discovered rules
lint test                          # run rules' inline test blocks
lint parse --lang go --pattern 'fmt.Println($$$)'   # see how a pattern compiles
lint parse main.go                 # dump a file's syntax tree (find node kinds)
lint new my-rule --lang go         # scaffold a new rule
lint langs                         # list supported languages
lint version                       # print version, commit, and build info
```

## Feature overview

| Feature | What it gives you |
| --- | --- |
| Structural matching | Patterns match the AST, so whitespace/comments don't, but operators do |
| One DSL, many languages | The same `.lint` syntax targets all supported languages |
| Metavariables | `$NAME` (named, back-referencing), `$_` (wildcard), `$$$NAME` (variadic), `$OP` (operator), implicit `$match` |
| `where` predicates | Constrain captures by regex, node kind, membership, equality, **numeric** bounds, variadic **`count`**, a sub-pattern, or **boolean `any`/`all` groups** |
| `let` definitions | Name a list/regex once (`let DEBUG = [...]`) and reuse it as `@DEBUG` across rules |
| Structural context | `inside` / `has`, ordering `precedes` / `follows`, depth-1 `directly inside` / `directly has` (each with `not`) |
| `any` / `all` / `not` | Combine matchers with boolean logic, including branch-scoped `where` predicates inside `any` / `all` |
| Raw queries | Drop to a Tree-sitter s-expression `query "..."` when patterns aren't enough |
| Path scoping | Limit a rule to files via `paths` / `exclude` globs (`*`, `?`, `**`) |
| Autofix | `fix "..."` templates rewritten by `lint check --fix` |
| Inline tests | `test { match ... no_match ... }` run by `lint test` |
| Suppression | `// lint:ignore [rule]` and `// lint:ignore-next-line` comments silence findings |
| Project config | `.lint.toml` disables rules or overrides severity per project |
| Severities & tags | `error`/`warning`/`info`; `url`/`tags` metadata; `--tag`, `--quiet`, `--error-on-warning` |
| JSON output | `--json` for editor/CI integration (includes `url`, `tags`) |
| Parallel | `-j` workers; one tree walk per file, shared across all rules |
| Polyglot rules | Omit `in` and a rule compiles for every language whose grammar accepts it |

## Example rules

These are real, tested rules from [`examples/`](examples/). Every rule there
carries an inline `test` block; run `lint test --rules examples/go` (etc.) to
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
[`examples/`](examples/): `c/`, `cpp/`, `csharp/`, `css/`, `go/`, `html/`,
`java/`, `json/`, `kotlin/`, `python/`, `rust/`, `scss/`, `swift/`,
`typescript/`, `xml/`, and `yaml/`.

## Documentation

- [docs/dsl.md](docs/dsl.md) — the complete `.lint` DSL reference: every clause
  and every `where` operator, with worked examples.
- [docs/writing-rules.md](docs/writing-rules.md) — a practical guide to authoring
  rules: discovering node kinds with `lint parse`, the metavariable model,
  per-language patterns, fixes, tests, and known limitations.

## License

See the repository for license details.
