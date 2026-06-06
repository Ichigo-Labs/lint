# The `.lint` DSL reference

A `.lint` file describes one or more **rules**. Each rule tells lint what code
to flag (a structural pattern), what to say (a message), and — optionally — how
to fix it and how to test it.

This document is the complete reference. For a task-oriented walkthrough, see
[writing-rules.md](writing-rules.md).

- [File structure](#file-structure)
- [Strings and bodies](#strings-and-bodies)
- [Rule fields](#rule-fields)
- [Metavariables](#metavariables) — including [operator metavariables](#operator-metavariables)
- [`let` definitions](#let-definitions)
- [How matching works](#how-matching-works)
- [Matcher clauses](#matcher-clauses) — `pattern`, `query`, `any`, `all`, `not`
- [Relation clauses](#relation-clauses) — `inside`, `has`, `precedes`, `follows`, `directly`, and negations
- [`where` constraints](#where-constraints) — regex, kind, membership, numeric, `count`, sub-pattern, boolean `any`/`all` groups
- [Path scoping](#path-scoping) — `paths` / `exclude` globs
- [`fix`](#fix)
- [`test`](#test)
- [Severity and exit codes](#severity-and-exit-codes)
- [Suppressing findings](#suppressing-findings)
- [Project configuration](#project-configuration)

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
| `url` | `link` | An optional link to a longer explanation / style-guide entry. Surfaced in `--json`. |
| `tags` | `tag` | Comma-separated free-form labels, e.g. `tags security, correctness`. Surfaced in `--json` and selectable with `lint check --tag`. |
| `paths` | `path` | Comma-separated (or bracketed) string globs restricting the rule to matching files, e.g. `paths "src/components/**"`. See [Path scoping](#path-scoping). |
| `exclude` | `excludePaths` | String globs removing files from the rule even when they match `paths`, e.g. `exclude "**/*.test.ts"`. |

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
| `$OP` | An **operator** hole in a binary/comparison expression: `$A $OP $B` matches any operator and binds it. The names `$OP`, `$OP1`, `$OP2`, … are reserved for this; reuse back-references the operator text. See [operator metavariables](#operator-metavariables). |

Every match also exposes an implicit **`$match`** binding — the whole matched
span — usable in `where`, `message`, and `fix`. For a `query`, an explicit
`@match`/`@target` capture takes precedence over the implicit one.

### Operator metavariables

`$OP` lets one pattern cover a whole family of operators. `$A $OP $B` compiles to
a binary/comparison expression and binds `$OP` to whichever operator the target
uses, so you can constrain or report it:

```
rule self-comparison-operator {
  message "both operands of `$OP` are identical (`$X $OP $X`)"
  in      go
  pattern { $X $OP $X }   # matches a == a, a - a, a & a, ...
}
```

Constrain the operator with `where` — operators must be quoted (they are not
identifiers), e.g. `where $OP in ["==", "!="]`. Scope notes: the placeholder is a
binary operator, so `$A $OP $B` matches binary/comparison nodes (in Python,
specifically `comparison_operator`, not arithmetic `binary_operator`). Putting
`$OP` outside operator position (e.g. `foo($OP)`) is a compile error.

Names follow identifier rules (letters, digits, `_`). A back-reference compares
*text*, so `$X == $X` matches `a == a` but not `a == b`.

Only **named** metavariables produce bindings usable in `message`, `note`,
`where`, and `fix`. `$_` and `$$$` are anonymous and produce no binding.

> A pattern that is **nothing but a single metavariable** (e.g. `pattern { $X }`)
> is rejected, because it would match every node.

## `let` definitions

A file may define named, reusable lists and regexes with `let`, then reference
them as `@NAME` in a `where` clause. Definitions are **file-scoped** (visible to
every rule in the file) and may appear at the top of the file or between rules:

```
let DEBUG = [Println, Printf, Print]
let ERRNAME = "(?i)err"

rule no-debug-print {
  in      go
  pattern { fmt.$M($$$) }
  where   $M in @DEBUG          # same as: where $M in [Println, Printf, Print]
}

rule error-compare {
  in      go
  pattern { $E == $_ }
  where   $E matches @ERRNAME    # same as: where $E matches "(?i)err"
}
```

A `[ ... ]` definition can only be used with `in` (and `not in`); a `"..."`
definition only with `matches` (and `not matches`). Referencing an undefined or
wrong-typed name is a parse error. References are resolved at parse time, so a
`let` is pure sugar — the engine sees an ordinary `in`/`matches` constraint.

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
$ lint parse --lang go --pattern 'fmt.Println($$$ARGS)'
kind: call_expression
(call_expression function: (selector_expression operand: (identifier) field: (field_identifier)) arguments: (argument_list (identifier)))
```

A multi-statement pattern compiles to a *sequence* that matches a run of adjacent
statements:

```
$ lint parse --lang python --pattern 'x = 1
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

#### Branch-scoped `where`

A `where` clause written **directly inside** an `any { ... }` or `all { ... }`
block constrains only the matches that block produces — unlike a rule-level
`where`, which applies to every candidate. This lets each branch of an `any`
carry its own predicate by wrapping it in an `all`:

```
rule on-handler-naming {
  in typescript
  any {
    # arrow / function values: always required to be on*
    query "(jsx_attribute (property_identifier) @name (jsx_expression [(arrow_function) (function_expression)]))"
    # identifier values: only flagged when the name looks like a verb
    all {
      query "(jsx_attribute (property_identifier) @name (jsx_expression (identifier)))"
      where $name matches "^(open|close|toggle|set|select)([A-Z]|$)"
    }
  }
  where $name not matches "^on[A-Z]"
}
```

Here the verb-prefix predicate applies **only** to the identifier-valued branch;
the arrow/function branch is unaffected. The trailing rule-level `where` still
applies to both branches. Branch-scoped predicates may reference that branch's
captures (e.g. `$name`) and the implicit `$match` span.

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

### `precedes` / `follows` (and negations)

These filter by an **earlier** or **later sibling in the same block**. They look
through statement wrappers, so a statement pattern matches against the sibling
statements at the enclosing block level. A classic use is "a `Lock` with no later
`Unlock` in the same block":

```
rule lock-without-unlock {
  in      go
  pattern { $MU.Lock() }
  not follows pattern { $MU.Unlock() }   # no later sibling unlocks the same mutex
}
```

`precedes pattern { ... }` is the mirror image (an *earlier* sibling matches), and
`not precedes` / `not follows` negate. The sibling matcher shares bindings with
the candidate, so `$MU` above must be the same mutex on both sides.

### `directly inside` / `directly has` (and negations)

The depth-1 forms of `inside` / `has`: `directly inside` matches the candidate's
**immediate parent**, and `directly has` matches an **immediate child** (rather
than any ancestor/descendant). `not directly inside` / `not directly has` negate.

```
rule operand-of-plus {
  in      go
  pattern { x }
  directly inside pattern { $A + $B }   # x is an immediate operand of `+`
}
```

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
| `$X in @NAME` | the text is in a `let` list (`not in @NAME` negates) |
| `$X matches @NAME` | the text matches a `let` regex (`not matches @NAME` negates) |
| `$X == $Y` | two captures have equal text |
| `$X != $Y` | two captures differ |
| `$X == "lit"` | the text equals the literal |
| `$X != "lit"` | the text differs from the literal |
| `$X > n` / `>= n` / `< n` / `<= n` | the capture parses as a number satisfying the bound (a non-numeric capture never matches) |
| `$X count > n` (`>= < <= == !=`) | the number of nodes a variadic captured satisfies the bound |
| `$X is pattern { ... }` | the captured subtree (or a descendant) matches the sub-pattern |
| `$X is not pattern { ... }` | it does not match the sub-pattern |
| `any { <constraint>... }` | **at least one** child constraint holds (boolean OR) |
| `all { <constraint>... }` | **every** child constraint holds (boolean AND) |

List items may be bare identifiers, strings, or numbers. The right side of `is` /
`is not` may be a `pattern { ... }` **or** a `query "..."`. Number literals may be
integers or decimals (`3`, `60000`, `2.5`).

### Boolean groups (`any` / `all`)

Top-level `where` clauses are ANDed together. To express **OR**, or to mix OR and
AND, wrap constraints in a `where any { ... }` or `where all { ... }` group.
Groups may reference different metavariables and nest arbitrarily:

```
rule handler-prop-naming {
  in tsx
  query "(_ name: (identifier) @comp (jsx_attribute (property_identifier) @name (jsx_expression [(arrow_function) (function_expression) (identifier)] @value)) @match)"
  where $comp matches "^[A-Z]"
  where $name not matches "^on([A-Z]|$)"
  where any {
    $value kind arrow_function          # inline functions are always handlers
    $value kind function_expression
    all {                                # identifiers only when the name is a verb
      $value kind identifier
      $name matches "^(open|close|toggle|select|set)([A-Z]|$)"
    }
  }
}
```

A group needs at least one child. As with any `where`, a child that references an
unbound metavariable simply evaluates to false (it never throws).

### Numeric comparisons and `count`

`>`, `<`, `>=`, `<=` compare a capture's text **as a number** — handy for magic
thresholds. A capture that is not numeric simply fails the predicate (it is not
an error):

```
pattern { setTimeout($_, $MS) }
where   $MS > 60000
```

`count` counts how many nodes a **variadic** (`$$$NAME`) absorbed — arguments,
parameters, or statements — so you can flag arity. A single (non-variadic)
capture counts as 1:

```
pattern { function $F($$$PARAMS) { $$$ } }
where   $PARAMS count > 4        # functions with more than four parameters
```

> Variadics are referenced in `where` with a single `$` (e.g. `$PARAMS`), the
> same name as the `$$$PARAMS` that captured them.

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
`lint parse <file>`). Use `not kind` to exclude a shape:

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

## Path scoping

By default a rule applies to every file whose language it can parse. The `paths`
and `exclude` fields restrict that set using file-path globs, evaluated against
each file's path relative to the working directory:

```
rule no-console-in-components {
  in       typescript
  paths    "src/components/**"
  exclude  "**/*.test.ts", "**/*.stories.tsx"
  message  "no console.* in components"
  pattern  { console.$M($$$) }
}
```

Glob syntax:

| Token | Matches |
| --- | --- |
| `*` | any run of characters except the path separator `/` |
| `?` | a single character except `/` |
| `**` | any run of characters, including `/` (crosses directories) |

`paths` is an allow-list: when present, a file must match at least one glob.
`exclude` is a deny-list applied afterward, so it wins over `paths`. Both accept
a comma-separated list or a bracketed list (`paths ["a/**", "b/**"]`). Globs are
matched against the cwd-relative path; files outside the working directory keep
their given path and so generally won't match a relative glob.

## `fix`

`fix` is a rewrite template. When a rule matches, `lint check --fix` replaces
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
$ lint check --fix
applied 1 fix(es) across 1 file(s)
```

`--fix` rewrites files in place. Overlapping fixes in one file are applied
outermost-first; conflicting overlaps are skipped.

## `test`

Inline tests live in a `test { ... }` block and run with `lint test`. Each
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
$ lint test
6 rule(s), 16 case(s): 16 passed, 0 failed
```

Snippets must parse **as a whole**. For Java/C# wrap members in a class
(`class C { void m() { ... } }`); for Go include a `package`/`func` as needed.
See [writing-rules.md](writing-rules.md#test-snippets-must-parse) for details.

## Severity and exit codes

- `severity error` findings make `lint check` exit non-zero.
- `warning` and `info` do not affect the exit code by default.
- `--error-on-warning` makes warnings (and errors) fail the run.
- `--quiet` shows only `error` findings.

`lint test` exits non-zero if any case fails or any rule fails to load.

## Suppressing findings

A source comment can silence findings on a line. The directive works in every
language's comment syntax (`//`, `#`, `/* */`):

```go
score := compute() // lint:ignore no-magic            // silence no-magic here
total := score*2   // lint:ignore                       // silence ALL rules here

// lint:ignore-next-line no-panic
panic("boom")                                            // silenced by the line above
```

- `lint:ignore [ids]` silences the listed rules on the **same** line; with no
  ids, it silences **all** rules on that line.
- `lint:ignore-next-line [ids]` silences the **following** line.
- Multiple ids are comma-separated (`lint:ignore a,b`); text after the ids is
  treated as a human explanation and ignored.

## Project configuration

An optional `.lint.toml` (or `lint.toml`) at the project root disables rules or
overrides their severity without editing the rule files — useful for tuning a
shared ruleset per project:

```toml
# Disable rules everywhere.
disable = ["no-console", "experimental-rule"]

# Or configure a single rule.
[rules.no-println]
severity = "error"      # promote a warning to an error

[rules.legacy-check]
enabled = false         # same as adding it to `disable`
```

Precedence, highest first: an inline `lint:ignore` comment, then project config
(disable / severity), then the rule's own `severity`.
