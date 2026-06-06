# lint

[![CI](https://github.com/Ichigo-Labs/lint/actions/workflows/ci.yml/badge.svg)](https://github.com/Ichigo-Labs/lint/actions/workflows/ci.yml)

**lint** finds bugs and bad patterns in your code by matching its *syntax
tree*, not its text. You write small rules in a focused DSL, and lint runs
them across C, C++, C#, Go, Java, Kotlin, Python, Rust, Swift, and
TypeScript/JavaScript — one rule language for every codebase.

It is a modern, multi-language, AST-based successor to
[checkr](https://github.com/ichigo-labs/checkr): same "write a project lint rule
in a minute" spirit, but matching is structural (over a Tree-sitter parse tree)
instead of line-by-line regex.

```
$ lint check
main.go
  6:2  warning use slog instead of fmt.Println [no-println]
     6 |     fmt.Println("hello")
       |     ^^^^^^^^^^^^^^^^^^^^

✖ 1 warning(s)
```

## Why structural matching?

A grep/regex rule like `fmt\.Println` can't tell `fmt.Println(x)` from
`myfmt.Println` in a comment, a string, or `fmt.Sprintln`. lint matches the
*shape* of the code:

- **Formatting doesn't matter.** `panic( "x" )`, `panic("x")`, and a `panic`
  split across lines are all the same pattern.
- **Operators *do* matter.** `$A + $B` matches `+`, never `-`.
- **Comments and strings are not code.** A pattern only matches real syntax.
- **Metavariables capture and back-reference.** `$X = append($Y, ...)` with
  `where $X != $Y` finds copy/paste slice bugs that no regex can express.

Compared to per-language linters (ESLint, golangci-lint, Roslyn analyzers),
lint trades depth for reach: you get *one* tiny rule language and a fast feedback
loop (`lint test`) for project-specific rules across every language in your
repo, without writing a plugin in each ecosystem.

## Install

### Quick install (prebuilt binary)

The installer downloads a prebuilt binary for your platform, verifies its
checksum, and puts it on your `PATH` — **no Go or C compiler required**:

```bash
curl -fsSL https://raw.githubusercontent.com/Ichigo-Labs/lint/master/install.sh | bash
```

It installs into `/usr/local/bin` if that's writable, otherwise `~/.local/bin`,
and supports **Linux** and **macOS** (amd64 and arm64). **Windows** (amd64)
binaries are published too — grab the `.zip` from the
[releases page](https://github.com/Ichigo-Labs/lint/releases) (see below).

```bash
# install a specific version
curl -fsSL https://raw.githubusercontent.com/Ichigo-Labs/lint/master/install.sh | LINT_VERSION=v0.2.0 bash

# install into a directory of your choosing
curl -fsSL https://raw.githubusercontent.com/Ichigo-Labs/lint/master/install.sh | LINT_INSTALL_DIR="$HOME/bin" bash
```

Prefer to do it by hand? Grab a tarball (or `.zip` on Windows) from the
[releases page](https://github.com/Ichigo-Labs/lint/releases), verify it
against `checksums.txt`, and drop the `lint` binary (`lint.exe` on Windows) on
your `PATH`.

### Build from source

lint embeds Tree-sitter grammars, so building it needs **Go and a C compiler
with CGO enabled**:

```bash
git clone https://github.com/Ichigo-Labs/lint
cd lint
CGO_ENABLED=1 go build -o lint ./cmd/lint
```

That produces a `lint` binary in the current directory. Put it on your `PATH`
(or run `make install` to install it into `$GOBIN`). `make` and `make test` are
also available.

> Building without CGO (`CGO_ENABLED=0`) will fail — the grammars are C and are
> compiled into the binary.

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
```

### How rules are discovered

Running `lint` (or `lint check`) with no `--rules` flag discovers every
`.lint/` directory **from the current directory up through its ancestors and
down through its descendants**, and loads every `*.lint` file in them. Ancestor
rules apply to the whole tree; a nested `.lint/` applies only to its own
subtree. This lets a monorepo keep global rules at the root and package-specific
rules deeper down.

Pass `--rules <dir>` to load rules from exactly one directory instead (the files
directly inside it — discovery is not recursive into that directory).

## Git pre-commit hook

Run lint automatically before every commit so violations never land in history.

By default `lint check` exits non-zero only on **error**-severity findings, so a
hook will block a commit on errors but let warnings through. Add
`--error-on-warning` if you want warnings to block commits too.

### Option A — a plain git hook

Create `.githooks/pre-commit` in your repo and make it executable
(`chmod +x .githooks/pre-commit`):

```bash
#!/usr/bin/env bash
# Lint the files staged for this commit and block on findings.
set -euo pipefail

# Staged files (added/copied/modified). lint silently skips paths whose
# language it doesn't recognize, so passing all of them is safe.
files="$(git diff --cached --name-only --diff-filter=ACM)"
[ -z "$files" ] && exit 0

# Drop --error-on-warning to block only on error-severity findings.
echo "$files" | xargs lint check --error-on-warning
```

Then point git at that tracked directory so the hook is shared with the whole
team (unlike `.git/hooks/`, which isn't committed):

```bash
git config core.hooksPath .githooks
```

### Option B — the [pre-commit](https://pre-commit.com) framework

Add a local hook to `.pre-commit-config.yaml`:

```yaml
repos:
  - repo: local
    hooks:
      - id: lint
        name: lint
        entry: lint check --error-on-warning
        language: system
        pass_filenames: true
        types: [text]
```

Then install it with `pre-commit install`. pre-commit passes the staged files to
`lint check`, which checks only those paths against your discovered `.lint/`
rules.

> Either way, `lint` must be on the hook's `PATH` — install it with the
> [installer above](#install) (or `make install`).

## Feature overview

| Feature | What it gives you |
| --- | --- |
| Structural matching | Patterns match the AST, so whitespace/comments don't, but operators do |
| One DSL, many languages | The same `.lint` syntax targets all supported languages |
| Metavariables | `$NAME` (named, back-referencing), `$_` (wildcard), `$$$NAME` (variadic), `$OP` (operator), implicit `$match` |
| `where` predicates | Constrain captures by regex, node kind, membership, equality, **numeric** bounds, variadic **`count`**, or a sub-pattern |
| `let` definitions | Name a list/regex once (`let DEBUG = [...]`) and reuse it as `@DEBUG` across rules |
| Structural context | `inside` / `has`, ordering `precedes` / `follows`, depth-1 `directly inside` / `directly has` (each with `not`) |
| `any` / `all` / `not` | Combine matchers with boolean logic |
| Raw queries | Drop to a Tree-sitter s-expression `query "..."` when patterns aren't enough |
| Autofix | `fix "..."` templates rewritten by `lint check --fix` |
| Inline tests | `test { match ... no_match ... }` run by `lint test` |
| Suppression | `// lint:ignore [rule]` and `// lint:ignore-next-line` comments silence findings |
| Project config | `.lint.toml` disables rules or overrides severity per project |
| Severities & tags | `error`/`warning`/`info`; `url`/`tags` metadata; `--tag`, `--quiet`, `--error-on-warning` |
| JSON output | `--json` for editor/CI integration (includes `url`, `tags`) |
| Parallel + scoped | `-j` workers, one tree walk per file shared across rules; `.lint/` scopes rules to subtrees |
| Polyglot rules | Omit `in` and a rule compiles for every language whose grammar accepts it |

## Supported languages

| Language | `in` name (aliases) | Extensions |
| --- | --- | --- |
| C | `c` | `.c .h` |
| C++ | `cpp` (`c++`, `cc`) | `.cpp .cxx .cc .hpp .hxx .hh` |
| C# | `csharp` (`cs`, `c#`) | `.cs` |
| Go | `go` (`golang`) | `.go` |
| Java | `java` | `.java` |
| Kotlin | `kotlin` (`kt`, `kts`) | `.kt .kts` |
| Python | `python` (`py`) | `.py .pyi` |
| Rust | `rust` (`rs`) | `.rs` |
| Swift | `swift` | `.swift` |
| TypeScript | `typescript` (`ts`) | `.ts .mts .cts` |
| TSX / JS | `tsx` (`javascript`, `js`, `jsx`) | `.tsx .js .jsx .mjs .cjs` |

(Run `lint langs` to print this list.)

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
[`examples/`](examples/): `c/`, `cpp/`, `csharp/`, `go/`, `java/`, `kotlin/`,
`python/`, `rust/`, `swift/`, and `typescript/`.

## Documentation

- [docs/dsl.md](docs/dsl.md) — the complete `.lint` DSL reference: every clause
  and every `where` operator, with worked examples.
- [docs/writing-rules.md](docs/writing-rules.md) — a practical guide to authoring
  rules: discovering node kinds with `lint parse`, the metavariable model,
  per-language patterns, fixes, tests, and known limitations.

## License

See the repository for license details.
