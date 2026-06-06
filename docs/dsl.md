# The `.lint` DSL reference

A `.lint` file describes one or more **rules**. Each rule tells lintel what code
to flag (a structural pattern), what to say (a message), and — optionally — how
to fix it and how to test it.

This document is the complete reference. For a task-oriented walkthrough, see
[writing-rules.md](writing-rules.md).

- [File structure](#file-structure)
- [Strings and bodies](#strings-and-bodies)
- [Rule fields](#rule-fields)
- [Metavariables](#metavariables)
- [How matching works](#how-matching-works)
- [Matcher clauses](#matcher-clauses) — `pattern`, `query`, `any`, `all`, `not`
- [Relation clauses](#relation-clauses) — `inside`, `has`, and negations
- [`where` constraints](#where-constraints)
- [`fix`](#fix)
- [`test`](#test)
- [Severity and exit codes](#severity-and-exit-codes)

## File structure

A file may contain one or more explicit `rule` blocks:

```
rule <id> {
  <field>...
}

rule <other-id> {
  <field>...
}
```

A file with **a single rule may omit the `rule { }` wrapper**; the fields appear
at top level and the rule's id defaults to the file's base name (e.g.
`no-eval.lint` → id `no-eval`):

```
# no-eval.lint
message "avoid eval()"
in      typescript
pattern { eval($$$) }
```

Comments use `#`, `//`, or `/* ... */` and may appear between clauses.

## Strings and bodies

Three string forms are accepted anywhere a string is expected (`message`,
`note`, `where ... matches`, etc.):

| Form | Escapes | Notes |
| --- | --- | --- |
| `"..."` | `\n \t \r \" \\` | ordinary double-quoted |
| `` `...` `` | none (raw) | trimmed of surrounding whitespace |
| `"""..."""` | none (raw) | best for multi-line test code; common indentation is stripped |

Pattern/query/fix/test **bodies** additionally accept a brace block, where the
contents are treated as opaque target-language code:

```
pattern { errors.New(fmt.Sprintf($$$ARGS)) }
```

Inside a brace body, braces are balanced and braces inside strings/comments are
ignored, so target-language `{ }` works as expected. Leading newlines and common
indentation are stripped, so

```
pattern {
  foo()
}
```

is exactly the pattern `foo()`.

## Rule fields

| Field | Aliases | Meaning |
| --- | --- | --- |
| `message` | `msg` | The text shown for a finding. May reference captures (see below). Defaults to the rule id. |
| `severity` | — | `error`, `warning` (default), or `info`. `warn` is accepted for `warning`. |
| `note` | `help` | An optional longer explanation printed under the finding. |
| `in` | `language`, `languages` | Comma-separated languages this rule applies to, e.g. `in go, typescript`. If omitted, the rule is compiled for **every** language whose grammar can parse its pattern. |

`message` and `note` expand `$NAME` / `$$$NAME` placeholders using the rule's
captured metavariables. For example, with `pattern { fmt.$M($$$) }`:

```
message "fmt.$M is debug output; use a logger or remove it"
```

renders as `fmt.Println is debug output; ...` for a `fmt.Println(...)` hit.

## Metavariables

A metavariable is a hole in a pattern that captures a piece of the syntax tree.

| Syntax | Meaning |
| --- | --- |
| `$NAME` | One named node. **Reuse is a back-reference**: every occurrence must capture the same source text. |
| `$_` | One anonymous node (wildcard). Not captured; cannot be referenced later. |
| `$$$NAME` | Zero or more nodes — a *run* of arguments, parameters, or statements. Captured (the spanned text). |
| `$$$` | Anonymous variadic (zero or more, not captured). `$$NAME` / `$$` are also variadic. |

Names follow identifier rules (letters, digits, `_`). A back-reference compares
*text*, so `$X == $X` matches `a == a` but not `a == b`.

Only **named** metavariables produce bindings usable in `message`, `note`,
`where`, and `fix`. `$_` and `$$$` are anonymous and produce no binding.

> A pattern that is **nothing but a single metavariable** (e.g. `pattern { $X }`)
> is rejected, because it would match every node.

## How matching works

Matching is **structural** over the Tree-sitter syntax tree:

- Whitespace, indentation, and line breaks are ignored.
- Comments are not code and never match.
- **Operators and keywords are significant.** `$A + $B` matches `+` only; it will
  not match `$A - $B`.
- A pattern is written as real code in the target language. It can be an
  expression, a statement, a declaration, or a *sequence* of consecutive
  statements.

You can inspect how any pattern compiles:

```
$ lintel parse --lang go --pattern 'fmt.Println($$$ARGS)'
kind: call_expression
(call_expression function: (selector_expression operand: (identifier) field: (field_identifier)) arguments: (argument_list (identifier)))
```

A multi-statement pattern compiles to a *sequence* that matches a run of adjacent
statements:

```
$ lintel parse --lang python --pattern 'x = 1
y = 2'
sequence of 2 nodes:
  (expression_statement (assignment left: (identifier) right: (integer)))
  (expression_statement (assignment left: (identifier) right: (integer)))
```

When a rule has **several top-level positive matchers**
(`pattern`/`query`/`any`/`all`), they are AND-ed: a single node must satisfy all
of them, sharing bindings. (To require two *separate* pieces of code, use the
`has` / `inside` relations.)

## Matcher clauses

A rule needs at least one positive matcher.

### `pattern { ... }`

A structural pattern. The body may be a brace block, a string, or triple-quoted
text.

```
rule panic-in-library {
  message "avoid panic; return an error instead"
  in      go
  pattern { panic($$$) }

  test {
    match    "package p\nfunc Get() string { panic(\"missing\") }"
    no_match "package p\nfunc Get() (string, error) { return \"\", nil }"
  }
}
```

A pattern with two or more statements matches a run of consecutive sibling
statements, and metavariable back-references work across them:

```
rule double-assign {
  message "variable written twice with no read in between; the first write is dead"
  in      go
  pattern {
    $X = $_
    $X = $_
  }

  test {
    match """
package p
func f() {
	x := 0
	x = 1
	x = 2
	_ = x
}
"""
    no_match """
package p
func f() {
	x := 0
	x = compute()
	use(x)
}
"""
  }
}
```

### `query "..."`

A raw Tree-sitter S-expression query — the escape hatch for things
pattern-by-example can't express (alternations, anchors, "any operator", node
kinds). The capture named `@match` (or `@target`) sets the reported span; if
neither is present, the widest capture is used. Standard Tree-sitter predicates
(`#eq?`, `#match?`, …) are supported.

```
rule empty-catch-block {
  message "empty catch block swallows the exception silently; log it or let it propagate"
  in      csharp
  query   "(catch_clause) @match"
  where   $match matches "\\{\\s*\\}\\s*$"

  test {
    match    "class C { void M() { try { F(); } catch { } } }"
    no_match "class C { void M() { try { F(); } catch (Exception ex) { Log(ex); } } }"
  }
}
```

Captures become bindings, so a query can feed `where`, `message`, and `fix`. You
can also use inline predicates instead of a `where`:

```
query "((binary_expression left: (number_literal) @n operator: \"==\") @match (#match? @n \"[.eEfF]\"))"
```

### `any { ... }`

Matches if **any** child matcher matches. Children are themselves matchers
(`pattern`, `query`, `any`, `all`, `not`).

```
rule append-wrong-slice {
  message "append result stored in $DST but it appends to $SRC — likely the wrong slice"
  in      go
  any {
    pattern { $DST = append($SRC, $$$) }
    pattern { $DST := append($SRC, $$$) }
  }
  where $DST != $SRC

  test {
    match    "package p\nfunc f(a, b []int, x int) { a = append(b, x) }"
    no_match "package p\nfunc f(a []int, x int) { a = append(a, x) }"
  }
}
```

### `all { ... }`

Matches if **all** child matchers match the same node, merging their bindings.
Its main use is grouping a positive matcher with a `not` (or extra positives)
inside a larger combinator such as `any` or a relation. At the top level it is
equivalent to listing the matchers separately.

```
rule call-with-args {
  message "call to f() that actually passes arguments"
  in      go
  all {
    pattern { f($$$) }
    not pattern { f() }
  }

  test {
    match    "package p\nfunc g() { f(1) }"
    no_match "package p\nfunc g() { f() }"
  }
}
```

### `not <matcher>`

As a matcher, `not` succeeds when its sub-matcher does **not** match the node.
Combined with a positive matcher (top-level clauses AND together), it subtracts
cases:

```
rule eq-not-nil {
  message "== comparison (nil comparisons are allowed)"
  in      go
  pattern { $X == $Y }
  not pattern { $_ == nil }

  test {
    match    "package p\nfunc f(a, b int) bool { return a == b }"
    no_match "package p\nfunc f(a *int) bool { return a == nil }"
  }
}
```

> `not inside` and `not has` (below) are different: they negate a *relation* to
> surrounding code, not the candidate node itself.

## Relation clauses

Relations filter a candidate by the code **around** it. The relation's own
pattern may bind additional metavariables that later `where`/`message`/`fix` can
use.

### `inside pattern { ... }` / `not inside pattern { ... }`

The candidate must be (or must not be) a descendant of a node matching the inner
matcher.

```
rule time-sleep-in-test {
  message "time.Sleep in a test makes it slow and flaky"
  in      go
  pattern { time.Sleep($$$) }
  inside pattern { func $_($_ *testing.T) { $$$ } }

  test {
    match """
package p
func TestThing(t *testing.T) {
	go start()
	time.Sleep(100 * time.Millisecond)
	check(t)
}
"""
    no_match """
package p
func retry() {
	for {
		time.Sleep(time.Second)
		if done() { return }
	}
}
"""
  }
}
```

`not inside` is the inverse — here, flag `os.Exit` everywhere *except* in `main`:

```
rule os-exit-outside-main {
  message "avoid os.Exit outside main(); return an error instead"
  in      go
  pattern { os.Exit($$$) }
  not inside pattern { func main() { $$$ } }

  test {
    match    "package cli\nfunc run() { if failed { os.Exit(1) } }"
    no_match "package main\nfunc main() { if err := run(); err != nil { os.Exit(1) } }"
  }
}
```

### `has pattern { ... }` / `not has pattern { ... }`

The candidate must contain (or must not contain) a descendant matching the inner
matcher. This rule reports a C function that allocates with `malloc`/`calloc`
but has no NULL check (note `has any { ... }` and several `not has`):

```
rule unchecked-malloc {
  message "the result of malloc/calloc is used without a NULL check"
  in      c, cpp
  query "(function_definition) @match"
  has any {
    pattern { $T *$PV = malloc($$$A); }
    pattern { $T *$PV = calloc($$$A); }
  }
  not has pattern { $PV == NULL }
  not has pattern { $PV != NULL }
  not has pattern { !$PV }

  test {
    match    "void f(const char *s){ char *p = malloc(10); strcpy(p, s); }"
    no_match "void h(const char *s){ char *p = malloc(10); if (!p) return; strcpy(p, s); }"
  }
}
```

Note how `$PV` is bound by the `has` pattern and then reused in the `not has`
patterns — relations share bindings.

## `where` constraints

`where` adds a predicate over a **captured** metavariable. Multiple `where`
clauses all must hold. A `where` that references an unbound name causes the rule
to never fire on that candidate, so only constrain names you actually capture.

| Operator | True when |
| --- | --- |
| `$X matches "re"` | the capture's text matches the RE2 regex |
| `$X not matches "re"` | it does not match the regex |
| `$X kind <node_type>` | the captured node's Tree-sitter kind equals `node_type` |
| `$X not kind <node_type>` | the kind differs (or there is no node, e.g. a variadic) |
| `$X in [a, b, "c"]` | the text equals one of the listed items |
| `$X not in [...]` | the text is none of them |
| `$X == $Y` | two captures have equal text |
| `$X != $Y` | two captures differ |
| `$X == "lit"` | the text equals the literal |
| `$X != "lit"` | the text differs from the literal |
| `$X is pattern { ... }` | the captured subtree (or a descendant) matches the sub-pattern |
| `$X is not pattern { ... }` | it does not match the sub-pattern |

List items may be bare identifiers, strings, or numbers. The right side of `is` /
`is not` may be a `pattern { ... }` **or** a `query "..."`.

### `matches` / `not matches`

Combine with `kind` to scope a comparison to error sentinels:

```
rule error-equality {
  message "compare errors with errors.Is, not `$ERR == $TARGET`"
  in      go
  pattern { $ERR == $TARGET }
  where   $ERR matches "(?i)err"
  where   $TARGET kind selector_expression

  test {
    match    "package p\nfunc r() error { if err == io.EOF { return nil }; return err }"
    no_match "package p\nfunc r() { if mode == config.Debug { return } }"
  }
}
```

`not matches` excludes a subset — here, the `Print`/`Println` calls but not the
`*f` formatting variants:

```
rule print-not-f {
  message "fmt.Print/Println (not the formatting variants)"
  in      go
  pattern { fmt.$M($$$) }
  where   $M not matches "f$"

  test {
    match    "package p\nfunc h() { fmt.Println(1) }"
    no_match "package p\nfunc h() { fmt.Printf(\"%d\", 1) }"
  }
}
```

### `kind` / `not kind`

`kind` checks the captured node's Tree-sitter type (discover types with
`lintel parse <file>`). Use `not kind` to exclude a shape:

```
rule eq-not-selector {
  message "== where the right side is not a selector (e.g. not io.EOF)"
  in      go
  pattern { $X == $Y }
  where   $Y not kind selector_expression

  test {
    match    "package p\nfunc f(a, b int) bool { return a == b }"
    no_match "package p\nfunc f(a error) bool { return a == io.EOF }"
  }
}
```

> `kind` needs a single captured node. A variadic capture (`$$$NAME`) has no
> single node, so `$X kind ...` is always false for it and `$X not kind ...`
> always true.

### `in` / `not in`

Membership against a fixed set:

```
rule no-debug-print {
  message "fmt.$M is debug output; use a logger or remove it"
  in      go
  pattern { fmt.$M($$$) }
  where   $M in [Print, Println, Printf]

  test {
    match    "package p\nfunc h() { fmt.Println(\"x\") }"
    no_match "package p\nfunc r() string { return fmt.Sprintf(\"x\") }"
  }
}
```

```
rule fmt-not-sprint {
  message "fmt call other than the Sprint* family"
  in      go
  pattern { fmt.$M($$$) }
  where   $M not in [Sprintf, Sprint, Sprintln]

  test {
    match    "package p\nfunc h() { fmt.Println(1) }"
    no_match "package p\nfunc h() string { return fmt.Sprintf(\"%d\", 1) }"
  }
}
```

### `==` / `!=` (capture vs capture)

Back-reference comparison by text. Equal captures find self-comparisons:

```
rule self-compare {
  message "$X == $X is always true"
  in      go
  pattern { $X == $Y }
  where   $X == $Y

  test {
    match    "package p\nfunc f(a int) bool { return a == a }"
    no_match "package p\nfunc f(a, b int) bool { return a == b }"
  }
}
```

Unequal captures find mismatches (see `append-wrong-slice` above, which uses
`where $DST != $SRC`).

### `==` / `!=` (capture vs literal)

```
rule eq-not-literal-nil {
  message "== comparison other than == nil"
  in      go
  pattern { $X == $Y }
  where   $Y != "nil"

  test {
    match    "package p\nfunc f(a, b int) bool { return a == b }"
    no_match "package p\nfunc f(a *int) bool { return a == nil }"
  }
}
```

(`$match == "any"` is how the TypeScript `no-explicit-any` rule pins a
`predefined_type` capture to exactly `any`.)

### `is pattern` / `is not pattern`

Constrain a capture by matching its subtree against a sub-pattern. `is` succeeds
if the captured node *or any descendant* matches:

```
rule compare-call-result {
  message "comparing a call result with == (consider storing it first)"
  in      go
  pattern { $X == $Y }
  where   $X is pattern { $_($$$) }

  test {
    match    "package p\nfunc f() bool { return g() == 3 }"
    no_match "package p\nfunc f(a int) bool { return a == 3 }"
  }
}
```

```
rule eq-lhs-not-call {
  message "== whose left side is not a call"
  in      go
  pattern { $X == $Y }
  where   $X is not pattern { $_($$$) }

  test {
    match    "package p\nfunc f(a int) bool { return a == 3 }"
    no_match "package p\nfunc f() bool { return g() == 3 }"
  }
}
```

## `fix`

`fix` is a rewrite template. When a rule matches, `lintel check --fix` replaces
the matched span with the expanded template. Use the same `$NAME` / `$$$NAME`
placeholders as the pattern.

```
rule errors-new-sprintf {
  message "use fmt.Errorf instead of errors.New(fmt.Sprintf(...))"
  in      go
  pattern { errors.New(fmt.Sprintf($$$ARGS)) }
  fix     "fmt.Errorf($$$ARGS)"

  test {
    match    "package p\nfunc f(n string) error { return errors.New(fmt.Sprintf(\"bad: %s\", n)) }"
    no_match "package p\nfunc f(n string) error { return fmt.Errorf(\"bad: %s\", n) }"
  }
}
```

Running it:

```
$ lintel check --fix
applied 1 fix(es) across 1 file(s)
```

`--fix` rewrites files in place. Overlapping fixes in one file are applied
outermost-first; conflicting overlaps are skipped.

## `test`

Inline tests live in a `test { ... }` block and run with `lintel test`. Each
entry is a snippet that either should or should not trigger the rule.

| Keyword | Aliases | Asserts |
| --- | --- | --- |
| `match` | `bad`, `flag` | the snippet triggers the rule (≥ 1 finding) |
| `no_match` | `ok`, `good`, `clean` | the snippet triggers nothing |

A `match` entry may be followed by `count N` to assert an exact number of hits:

```
rule panic-count {
  message "panic() found"
  in      go
  pattern { panic($$$) }

  test {
    match """
package p
func f() { panic(1); panic(2) }
""" count 2
  }
}
```

Run them:

```
$ lintel test
6 rule(s), 16 case(s): 16 passed, 0 failed
```

Snippets must parse **as a whole**. For Java/C# wrap members in a class
(`class C { void m() { ... } }`); for Go include a `package`/`func` as needed.
See [writing-rules.md](writing-rules.md#test-snippets-must-parse) for details.

## Severity and exit codes

- `severity error` findings make `lintel check` exit non-zero.
- `warning` and `info` do not affect the exit code by default.
- `--error-on-warning` makes warnings (and errors) fail the run.
- `--quiet` shows only `error` findings.

`lintel test` exits non-zero if any case fails or any rule fails to load.
