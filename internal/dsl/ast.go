// Package dsl defines the `.lint` rule language: its AST and the
// lexer/parser that turns rule text into that AST. The engine package
// compiles these AST values into runnable matchers.
package dsl

// Position is a 1-based line/column into a `.lint` source file.
type Position struct {
	Line int
	Col  int
}

// Severity is how loud a finding is.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// Rule is one parsed `rule { ... }` block, before compilation.
type Rule struct {
	ID        string
	Message   string
	Note      string
	Severity  Severity
	Languages []string // names/aliases as written; resolved by the engine
	URL       string   // optional link to a longer explanation / style-guide entry
	Tags      []string // optional free-form labels (for `--tag` filtering, JSON output)

	// Match is the root matcher: a pattern, a raw query, or an any/all/not
	// combination of those. It produces candidate nodes plus metavar bindings.
	Match *Matcher

	// Where holds metavariable predicates that every candidate must satisfy.
	Where []Constraint

	// Paths, when non-empty, restricts the rule to files whose path matches one
	// of these globs (supporting `*`, `?`, and `**`). ExcludePaths removes files
	// matching any of its globs. Both are evaluated against the file path
	// relative to the working directory.
	Paths        []string
	ExcludePaths []string

	// Relations are structural context filters (inside / has, and negations).
	Relations []Relation

	// Fix is an optional rewrite template using $META placeholders. nil means
	// the rule is not auto-fixable.
	Fix *string

	// Tests are inline examples used by `lint test`.
	Tests []TestCase

	File string
	Pos  Position
}

// LetDef is a file-level `let NAME = ...` definition: either a membership list
// or a regex, referenced as @NAME in a `where` clause. References are resolved
// at parse time, so the engine never sees a LetDef.
type LetDef struct {
	IsRegex bool
	Text    string   // regex source (when IsRegex)
	List    []string // membership items (when !IsRegex)
	Pos     Position
}

// MatcherKind discriminates the Matcher union.
type MatcherKind int

const (
	MatchPattern MatcherKind = iota // structural code pattern
	MatchQuery                      // raw tree-sitter s-expression
	MatchAll                        // node must match every child
	MatchAny                        // node must match at least one child
	MatchNot                        // node must not match the sub-matcher
)

// Matcher is a node in the matcher tree. The active fields depend on Kind.
type Matcher struct {
	Kind MatcherKind

	// Pattern / Query leaves.
	Pattern string
	Query   string

	// All / Any.
	Children []*Matcher
	// Not.
	Sub *Matcher

	// Where holds constraints scoped to this matcher: when this matcher is one
	// branch of an `any { ... }` / `all { ... }`, these predicates apply only to
	// that branch's matches (not the whole rule). Rule-level constraints live on
	// Rule.Where instead.
	Where []Constraint

	Pos Position
}

// RelationKind enumerates relational context filters.
type RelationKind int

const (
	RelInside    RelationKind = iota // candidate is inside (descendant of) a match
	RelNotInside                     // candidate is not inside a match
	RelHas                           // candidate contains (ancestor of) a match
	RelNotHas                        // candidate does not contain a match

	RelPrecedes    // candidate has an earlier same-block sibling matching
	RelNotPrecedes // candidate has no earlier same-block sibling matching
	RelFollows     // candidate has a later same-block sibling matching
	RelNotFollows  // candidate has no later same-block sibling matching

	RelDirectlyInside    // candidate's immediate parent matches
	RelNotDirectlyInside // candidate's immediate parent does not match
	RelDirectlyHas       // candidate has an immediate child matching
	RelNotDirectlyHas    // candidate has no immediate child matching
)

// Relation is a structural context filter on a candidate node. Inside/Has
// matchers may bind additional metavariables that later constraints and fixes
// can reference.
type Relation struct {
	Kind    RelationKind
	Matcher *Matcher
	Pos     Position
}

// ConstraintKind enumerates `where` predicates.
type ConstraintKind int

const (
	ConRegex      ConstraintKind = iota // $X matches "re"
	ConNotRegex                         // $X not matches "re"
	ConKind                             // $X kind node_type
	ConNotKind                          // $X not kind node_type
	ConIn                               // $X in [a, b, c]
	ConNotIn                            // $X not in [a, b, c]
	ConEqVar                            // $X == $Y
	ConNeqVar                           // $X != $Y
	ConEqText                           // $X == "literal"
	ConNeqText                          // $X != "literal"
	ConPattern                          // $X is pattern{...}/query: capture matches a sub-matcher
	ConNotPattern                       // $X is not ...
	ConNumGt                            // $X > n   (capture parsed as a number)
	ConNumGe                            // $X >= n
	ConNumLt                            // $X < n
	ConNumLe                            // $X <= n
	ConCountGt                          // $X count > n   (number of nodes a variadic captured)
	ConCountGe                          // $X count >= n
	ConCountLt                          // $X count < n
	ConCountLe                          // $X count <= n
	ConCountEq                          // $X count == n
	ConCountNe                          // $X count != n
)

// Constraint is one `where` predicate over a captured metavariable.
type Constraint struct {
	Kind ConstraintKind
	Var  string // metavariable name without the leading '$'

	// Operand, by kind:
	Text    string   // regex source, node kind, comparison literal
	Other   string   // second metavariable name (for Eq/NeqVar)
	List    []string // membership set (for In/NotIn)
	Matcher *Matcher // sub-matcher (for Pattern/NotPattern)

	Pos Position
}

// TestExpectation is what a test case asserts.
type TestExpectation int

const (
	ExpectMatch   TestExpectation = iota // the rule should fire here
	ExpectNoMatch                        // the rule should stay silent here
)

// TestCase is one inline example for `lint test`.
type TestCase struct {
	Expect TestExpectation
	Code   string
	// Count, when >= 0, asserts an exact number of matches (only meaningful
	// with ExpectMatch). -1 means "one or more".
	Count int
	Pos   Position
}
