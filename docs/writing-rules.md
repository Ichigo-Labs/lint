# Writing rules

A practical guide to authoring `.lint` rules. For the exhaustive syntax, see the
[DSL reference](dsl.md).

- [The authoring loop](#the-authoring-loop)
- [Discovering node kinds with `lint parse`](#discovering-node-kinds-with-lint-parse)
- [The metavariable model](#the-metavariable-model)
- [How patterns compile](#how-patterns-compile)
- [Common patterns by language](#common-patterns-by-language)
- [Markup, style, and data languages](#markup-style-and-data-languages)
- [Narrowing with `where` and relations](#narrowing-with-where-and-relations)
- [Fixes](#fixes)
- [Tests](#tests)
- [Known limitations and gotchas](#known-limitations-and-gotchas)

## The authoring loop

A tight loop keeps rule writing fast:

```bash
lint new my-rule --lang go        # scaffold .lint/my-rule.lint
lint parse --lang go --pattern 'panic($$$)'   # confirm the pattern compiles
lint test my-rule                 # run just this rule's inline tests
lint check path/to/code           # try it on real code
```

`lint new` writes a starter rule with a `pattern`, a commented-out `where`/`fix`,
and a `test` block. Fill it in, then iterate with `lint test` until the
`match`/`no_match` cases pass, and finally run `lint check`.

## Discovering node kinds with `lint parse`

Two predicates work on node *kinds* — `where $X kind <type>` and the `query`
escape hatch — so you need to know what Tree-sitter calls each piece of syntax.
`lint parse <file>` prints the tree.

```
$ printf 'package p\nfunc f() { err := io.EOF; _ = err }\n' > d.go
$ lint parse d.go
(source_file (package_clause (package_identifier)) (function_declaration name: (identifier) parameters: (parameter_list) body: (block (short_var_declaration left: (expression_list (identifier)) right: (expression_list (selector_expression operand: (identifier) field: (field_identifier)))) ...)))
```

From this you can read that `io.EOF` is a `selector_expression`, which is exactly
what the `error-equality` rule keys on (`where $TARGET kind selector_expression`).

To see how a *pattern* (not a file) resolves — its core node kind and the
s-expression it becomes — use `--lang` + `--pattern`:

```
$ lint parse --lang go --pattern 'fmt.Println($$$ARGS)'
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
statements can't always sit at a file's top level, lint **automatically wraps
the pattern in scaffolding** (e.g. for Go it tries the raw text, then inside a
`func`, then as a `var` initializer) until one parses. You therefore write
patterns as bare fragments — `panic($$$)`, `$X == null`, `using namespace $NS;` —
without adding a surrounding function or class yourself.

A pattern with multiple statements compiles to a **sequence** that matches a run
of adjacent siblings:

```
$ lint parse --lang python --pattern 'x = 1
y = 2'
sequence of 2 nodes:
  (expression_statement (assignment ...))
  (expression_statement (assignment ...))
```

If a pattern fails to compile, `lint parse --pattern` prints
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
| Kotlin | `pattern { $X!! }` | a `!!` not-null assertion |
| Python | `pattern { $X == None }` | `== None` (prefer `is None`) |
| Rust | `pattern { $X.unwrap_or(Default::default()) }` | a verbose `unwrap_or` |
| Swift | `pattern { $X as! $T }` | a force cast (prefer `as?`) |
| TypeScript | `pattern { $X! }` | a non-null assertion |
| HTML | `pattern { <$_ $$$ style=$_ $$$> }` | any element with an inline `style` attribute |
| CSS / SCSS | `pattern { $P: $$$V !important }` | any `!important` declaration |
| SCSS | `pattern { color: $C }` + `where $C matches "^#"` | a hard-coded color literal |
| XML | `pattern { <version>$V</version> }` + `where $V matches "-SNAPSHOT$"` | a SNAPSHOT dependency version |
| JSON | `pattern { "$NAME": "$V" }` + `where $V matches "-SNAPSHOT$"` | a SNAPSHOT version in any pair |
| YAML | `pattern { image: $IMG }` + `where $IMG matches ":latest$"` | a `:latest` container image |
| C | `query "(function_definition) @match"` | a whole function (then filter with `has`) |

A rule can target several languages at once when the pattern is valid in each —
`gets()` is flagged in both C and C++ with `in c, cpp`. Omit `in` entirely and
the rule compiles for *every* language whose grammar accepts the pattern —
except HTML, XML, CSS, SCSS, JSON, and YAML, which must always be named
explicitly (their grammars accept nearly any text, so implicit targeting
would misfire).

## Markup, style, and data languages

HTML, XML, CSS, SCSS, JSON, and YAML patterns follow the same metavariable
model, with a few semantics specific to these grammars:

- **Open-tag patterns match start tags.** `<div $$$ style=$_ $$$>` (no closing
  tag) matches the start tag of *any* matching element, closed or not. A full
  element pattern (`<li>$X</li>`) matches whole elements. One quirk: an open
  tag needs at least one attribute (or `$$$`) to parse — write `<div $$$>`,
  not a bare `<div>`.
- **Metavariables work inside quoted attribute values.** `<a href="$URL">`
  binds the value without its quotes. (In code languages `$` stays literal
  inside strings; markup attribute values are where metavariables are most
  useful, so the rewrite applies there.) In HTML, an unquoted `attr=$X` form
  also matches quoted targets; XML patterns must quote attribute values to
  parse.
- **Quoting and case are normalized in HTML.** `class=x` matches `class="x"`,
  and tag/attribute names compare case-insensitively — `<div $$$ style=$_ $$$>`
  also flags `<DIV STYLE="…">`. XML stays case-sensitive, per spec, and
  compares quoted values as written.
- **XML attribute variadics.** `<dependency $$$A>$$$C</dependency>` matches a
  `<dependency>` element with any attributes and children. A self-closing
  pattern (`<a $$$/>`) only matches self-closing elements — `<a/>` and
  `<a></a>` are distinct syntax. Use `any { … }` to cover both.
- **SCSS variables stay literal.** In `in scss` rules, `$lowercase` is SCSS's
  own variable syntax and matches literally; only `$UPPERCASE`, `$_`, and
  `$$$` are metavariables. (`pattern { color: $primary }` finds uses of the
  `$primary` variable; `pattern { color: $C }` captures any value.)
- **A metavariable must cover a whole value, name, or text node.**
  `<p>hello $X</p>` does not compile — HTML lexes `hello $X` into one text
  token, so the metavariable cannot bind. Match the whole text (`<p>$T</p>`)
  and narrow with `where $T matches "..."`. The same applies to CSS colors:
  write `color: $C` + `where $C matches "^#"`, not `color: #$C`.
- **JSON metavariables may be written bare or quoted.** `"version": $V` and
  `"version": "$V"` are equivalent: JSON has no bare-token form, so the
  rewrite quotes the metavariable, and the quoted form matches a value of
  *any* kind — string content binds without its quotes. Object variadics
  (`{"a": 1, $$$}`) are not expressible; match the pair (`"a": 1`) instead,
  which finds it in any object at any depth.
- **YAML pairs match at any depth.** `image: $IMG` finds the pair nested
  anywhere (a Kubernetes container spec, say). Quoted and plain scalars are
  distinct kinds: `name: "x"` does not match `name: x`. YAML patterns are
  indentation-sensitive like YAML itself; fragments parse at the top level.
- **Explicit `in` required.** These languages are never targeted implicitly;
  a rule must say `in html`, `in css, scss`, `in yaml`, etc.

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

Apply fixes with `lint check --fix`. Keep fixes behavior-preserving and prefer
fixing the *smallest* span that makes the change correct. When fixes in one file
overlap, lint applies the outermost first and skips conflicting ones.

Test a fix the same way you test matching — a `match` case proves the rule fires;
combine it with manual `lint check --fix` on a scratch file to eyeball the
rewrite.

## Tests

Embed `test { ... }` and run `lint test`. Cover both directions:

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
- **HTML / XML / CSS / SCSS / YAML:** any fragment parses at the top level
  (`<div>x</div>`, `a { color: red; }`, `key: value`).
- **JSON:** the snippet must be a single JSON document (`{"key": 1}`).

(Tree-sitter is forgiving and a fragment will sometimes still match, but don't
rely on it — a malformed snippet can parse into the wrong shape and give a
misleading pass or fail.) Triple-quoted strings (`"""..."""`) are the most
convenient way to write multi-line snippets; common indentation is stripped.

## Known limitations and gotchas

- **Operator metavariables are `$OP` only, in operator position.** `$A $OP $B`
  captures any binary/comparison operator (the names `$OP`, `$OP1`, … are
  reserved for this). It binds the operator as text — constrain it with quoted
  values (`where $OP in ["==", "!="]`), not bare ones. `$OP` outside operator
  position (`foo($OP)`) fails to compile, and the placeholder is binary, so it
  matches binary/comparison nodes (in Python, `comparison_operator`). For
  anything more exotic, drop to a raw `query` over the operator node.

  ```
  $ lint parse --lang go --pattern '$A $OP $B'
  kind: binary_expression
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
  text, not a node. In a `where` clause you reference it with a single `$`
  (`$NAME`, not `$$$NAME`), and `where $NAME kind ...` is always false (it has no
  single node) while `where $NAME not kind ...` is always true. What *does* work
  on a variadic: text predicates (`matches`, `in`, `==`) and `count` (its node
  count, e.g. `where $ARGS count > 3`). Use `kind`/`is` on `$NAME` single
  captures.

- **`where` on an unbound name silently never matches.** If a `where` references
  a metavariable the pattern didn't capture (typo, or it's `$_`), the rule simply
  never fires. Constrain only names you actually capture.

- **Use `==`, not `=`.** The DSL has no single-`=` operator; `where $X = ...` is a
  parse error. Write `==` / `!=`.

- **Pattern-by-example can't express everything.** Alternation across operators,
  anchoring to "start of block," matching by node kind, and Tree-sitter
  predicates (`#eq?`, `#match?`) all live in `query "..."`. Reach for it when a
  pattern gets awkward.

- **Rules live in the project-root `.lint/`.** By default lint loads every
  `.lint` file in the `.lint/` directory at the root you run it from and applies
  them to every file you check. `--rules <dir>` overrides that, loading the
  `.lint` files directly inside that one directory instead (not recursively);
  either way the rules are global, regardless of where each checked file lives.

- **Query predicates must sit inside the pattern's parentheses.** Tree-sitter
  treats a top-level `(#eq? ...)` as a separate pattern, so
  `(predefined_type) @x (#eq? @x "any")` silently ignores the predicate. Wrap the
  whole thing: `((predefined_type) @x (#eq? @x "any"))`. For simple equality,
  prefer the lint-native `where $x == "any"`, which is evaluated by lint
  itself and is not subject to this gotcha.

- **Severity vs. exit code.** Only `error` findings fail `lint check` by
  default. Use `severity error` for things that should block CI, or run with
  `--error-on-warning`.
