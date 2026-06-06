# Writing rules

A practical guide to authoring `.lint` rules. For the exhaustive syntax, see the
[DSL reference](dsl.md).

- [The authoring loop](#the-authoring-loop)
- [Discovering node kinds with `lintel parse`](#discovering-node-kinds-with-lintel-parse)
- [The metavariable model](#the-metavariable-model)
- [How patterns compile](#how-patterns-compile)
- [Common patterns by language](#common-patterns-by-language)
- [Narrowing with `where` and relations](#narrowing-with-where-and-relations)
- [Fixes](#fixes)
- [Tests](#tests)
- [Known limitations and gotchas](#known-limitations-and-gotchas)

## The authoring loop

A tight loop keeps rule writing fast:

```bash
lintel new my-rule --lang go        # scaffold .lintel/my-rule.lint
lintel parse --lang go --pattern 'panic($$$)'   # confirm the pattern compiles
lintel test my-rule                 # run just this rule's inline tests
lintel check path/to/code           # try it on real code
```

`lintel new` writes a starter rule with a `pattern`, a commented-out `where`/`fix`,
and a `test` block. Fill it in, then iterate with `lintel test` until the
`match`/`no_match` cases pass, and finally run `lintel check`.

## Discovering node kinds with `lintel parse`

Two predicates work on node *kinds* — `where $X kind <type>` and the `query`
escape hatch — so you need to know what Tree-sitter calls each piece of syntax.
`lintel parse <file>` prints the tree.

```
$ printf 'package p\nfunc f() { err := io.EOF; _ = err }\n' > d.go
$ lintel parse d.go
(source_file (package_clause (package_identifier)) (function_declaration name: (identifier) parameters: (parameter_list) body: (block (short_var_declaration left: (expression_list (identifier)) right: (expression_list (selector_expression operand: (identifier) field: (field_identifier)))) ...)))
```

From this you can read that `io.EOF` is a `selector_expression`, which is exactly
what the `error-equality` rule keys on (`where $TARGET kind selector_expression`).

To see how a *pattern* (not a file) resolves — its core node kind and the
s-expression it becomes — use `--lang` + `--pattern`:

```
$ lintel parse --lang go --pattern 'fmt.Println($$$ARGS)'
kind: call_expression
(call_expression function: (selector_expression operand: (identifier) field: (field_identifier)) arguments: (argument_list (identifier)))
```

The reported `kind` is the node your rule will match (and the node a `where`
`$match`/`fix` operates on). If that kind is too broad or too narrow, adjust the
pattern.

## The metavariable model

| Form | Captures? | Use |
| --- | --- | --- |
| `$NAME` | yes (one node) | name something you'll reference in `message`/`where`/`fix`; reuse = back-reference |
| `$_` | no | "one of anything here, I don't care" |
| `$$$NAME` | yes (a run, as text) | a variable-length list of args/params/statements |
| `$$$` | no | a variable-length list you don't need to name |

Three things to internalize:

1. **Back-references compare text.** Reusing a name requires identical source
   text. `$X == $X` matches `a == a`, not `a == b`. This is how
   `redundant-self-assignment` and `self-compare` rules work.

2. **Only named captures are usable later.** `message`, `note`, `where`, and
   `fix` can reference `$NAME` and `$$$NAME`, never `$_` or `$$$`.

3. **A variadic captures *text*, not a single node.** That text can be compared
   (`==`, `!=`, `matches`, `in`) and substituted in `fix`, but it has no single
   node, so `where $$$X kind ...` is always false (see limitations).

Example tying it together — the destination slice must differ from the appended
one:

```
any {
  pattern { $DST = append($SRC, $$$) }
  pattern { $DST := append($SRC, $$$) }
}
where $DST != $SRC
```

## How patterns compile

A pattern is parsed as real code in the target grammar. Because expressions and
statements can't always sit at a file's top level, lintel **automatically wraps
the pattern in scaffolding** (e.g. for Go it tries the raw text, then inside a
`func`, then as a `var` initializer) until one parses. You therefore write
patterns as bare fragments — `panic($$$)`, `$X == null`, `using namespace $NS;` —
without adding a surrounding function or class yourself.

A pattern with multiple statements compiles to a **sequence** that matches a run
of adjacent siblings:

```
$ lintel parse --lang python --pattern 'x = 1
y = 2'
sequence of 2 nodes:
  (expression_statement (assignment ...))
  (expression_statement (assignment ...))
```

If a pattern fails to compile, `lintel parse --pattern` prints
`pattern is not valid <lang>` — fix the syntax (it must be valid code) before
embedding it in a rule.

## Common patterns by language

Every snippet below is the core of a tested rule under [`examples/`](../examples).
They show the range of what one short pattern can express.

| Language | Pattern (or query) | Finds |
| --- | --- | --- |
| C | `pattern { gets($X) }` | the unbounded `gets()` call |
| C++ | `pattern { using namespace $NS; }` | a `using namespace` directive |
| C# | `pattern { $X == null }` | `== null` (prefer `is null`) |
| Go | `pattern { return }` | a naked `return` |
| Go | `pattern { panic($$$) }` | any `panic(...)` |
| Java | `pattern { $A == $STR }` + `where $STR kind string_literal` | comparing a `String` with `==` |
| Python | `pattern { $X == None }` | `== None` (prefer `is None`) |
| Rust | `pattern { $X.unwrap_or(Default::default()) }` | a verbose `unwrap_or` |
| TypeScript | `pattern { $X! }` | a non-null assertion |
| C | `query "(function_definition) @match"` | a whole function (then filter with `has`) |

A rule can target several languages at once when the pattern is valid in each —
`gets()` is flagged in both C and C++ with `in c, cpp`. Omit `in` entirely and
the rule compiles for *every* language whose grammar accepts the pattern.

## Narrowing with `where` and relations

Start broad with a `pattern`, then subtract false positives:

- **`where`** constrains a capture by text/kind/membership/equality/sub-pattern.
  See the [operator table](dsl.md#where-constraints).
- **`inside` / `not inside`** filter by an enclosing construct (e.g. only inside a
  `*testing.T` test function; everywhere *except* `main`).
- **`has` / `not has`** filter by what the candidate contains (e.g. a function
  that allocates but has *no* NULL check).

A good rule is usually "one positive matcher + a couple of negative filters."
The `unchecked-malloc` rule is a textbook case: match any function, require it
`has` an allocation, and require it does `not has` a NULL guard.

## Fixes

Add `fix "..."` to make a rule auto-fixable. The template uses the same captures
as the pattern and replaces the matched span:

```
pattern { $X == None }
fix     "$X is None"
```

Apply fixes with `lintel check --fix`. Keep fixes behavior-preserving and prefer
fixing the *smallest* span that makes the change correct. When fixes in one file
overlap, lintel applies the outermost first and skips conflicting ones.

Test a fix the same way you test matching — a `match` case proves the rule fires;
combine it with manual `lintel check --fix` on a scratch file to eyeball the
rewrite.

## Tests

Embed `test { ... }` and run `lintel test`. Cover both directions:

```
test {
  match    "package p\nfunc h() { fmt.Println(1) }"   # should fire
  no_match "package p\nfunc h() string { return fmt.Sprintf(\"x\") }"   # should not
}
```

Tips:

- Aliases exist: `match`/`bad`/`flag` and `no_match`/`ok`/`good`/`clean`.
- Add `count N` after a `match` snippet to assert an exact number of hits.
- Write `no_match` cases for the *near misses* you expect — the lookalikes that a
  naive regex would wrongly flag. That is where structural matching earns its
  keep.

<a id="test-snippets-must-parse"></a>
### Test snippets are parsed whole — make them valid

Unlike patterns, **test snippets are not scaffolded**: each is parsed as a
complete file in the rule's language. Write them as standalone, parseable code:

- **Go:** include a `package` and a `func` where needed.
- **Java / C#:** wrap members in a class, e.g.
  `class C { void m() { ... } }`.
- **Python / TypeScript / C / C++ / Rust:** a top-level statement or declaration
  is usually fine.

(Tree-sitter is forgiving and a fragment will sometimes still match, but don't
rely on it — a malformed snippet can parse into the wrong shape and give a
misleading pass or fail.) Triple-quoted strings (`"""..."""`) are the most
convenient way to write multi-line snippets; common indentation is stripped.

## Known limitations and gotchas

- **No operator/keyword metavariables.** You cannot capture an operator with a
  pattern — `$A $OP $B` is not valid code and fails to compile. To match "any
  comparison operator," use a raw `query` over `binary_expression`.

  ```
  $ lintel parse --lang go --pattern '$A $OP $B'
  lintel: pattern is not valid go:
    $A $OP $B
  ```

- **Bare metavariable patterns are rejected.** `pattern { $X }` would match every
  node and is refused.

- **A bare `$$$` statement body may not parse in strict-statement languages.**
  In Java, C, C++, and C#, a whole-body wildcard like `void m() { $$$ }` or
  `try { $$$ } catch (...) {}` fails to compile, because the placeholder isn't a
  valid standalone statement in those grammars. Match a more specific shape, or
  use `has { pattern { ... } }` / a raw `query "..."` to express "a method/try
  that contains X". (Expression and argument variadics like `f($$$)` are
  unaffected and work everywhere.)

- **`kind` and `is` need a single node.** A variadic capture (`$$$NAME`) binds
  text, not a node, so `where $$$X kind ...` is always false and
  `where $$$X not kind ...` always true. Use `kind`/`is` on `$NAME` captures.

- **`where` on an unbound name silently never matches.** If a `where` references
  a metavariable the pattern didn't capture (typo, or it's `$_`), the rule simply
  never fires. Constrain only names you actually capture.

- **Use `==`, not `=`.** The DSL has no single-`=` operator; `where $X = ...` is a
  parse error. Write `==` / `!=`.

- **Pattern-by-example can't express everything.** Alternation across operators,
  anchoring to "start of block," matching by node kind, and Tree-sitter
  predicates (`#eq?`, `#match?`) all live in `query "..."`. Reach for it when a
  pattern gets awkward.

- **`--rules <dir>` is single-directory but global.** It loads the `.lint` files
  directly inside that one directory (not recursively) and applies them to every
  file you check, regardless of where that file lives. For nested, scoped layouts
  rely on default `.lintel/` discovery instead, which walks ancestors and
  descendants and scopes each `.lintel/` to its own subtree.

- **Query predicates must sit inside the pattern's parentheses.** Tree-sitter
  treats a top-level `(#eq? ...)` as a separate pattern, so
  `(predefined_type) @x (#eq? @x "any")` silently ignores the predicate. Wrap the
  whole thing: `((predefined_type) @x (#eq? @x "any"))`. For simple equality,
  prefer the lintel-native `where $x == "any"`, which is evaluated by lintel
  itself and is not subject to this gotcha.

- **Severity vs. exit code.** Only `error` findings fail `lintel check` by
  default. Use `severity error` for things that should block CI, or run with
  `--error-on-warning`.
