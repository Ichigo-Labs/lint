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

	// Match is the root matcher: a pattern, a raw query, or an any/all/not
	// combination of those. It produces candidate nodes plus metavar bindings.
	Match *Matcher

	// Where holds metavariable predicates that every candidate must satisfy.
	Where []Constraint

	// Relations are structural context filters (inside / has, and negations).
	Relations []Relation

	// Fix is an optional rewrite template using $META placeholders. nil means
	// the rule is not auto-fixable.
	Fix *string

	// Tests are inline examples used by `lintel test`.
	Tests []TestCase

	File string
	Pos  Position
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

	Pos Position
}

// RelationKind enumerates relational context filters.
type RelationKind int

const (
	RelInside    RelationKind = iota // candidate is inside (descendant of) a match
	RelNotInside                     // candidate is not inside a match
	RelHas                           // candidate contains (ancestor of) a match
	RelNotHas                        // candidate does not contain a match
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

// TestCase is one inline example for `lintel test`.
type TestCase struct {
	Expect TestExpectation
	Code   string
	// Count, when >= 0, asserts an exact number of matches (only meaningful
	// with ExpectMatch). -1 means "one or more".
	Count int
	Pos   Position
}
