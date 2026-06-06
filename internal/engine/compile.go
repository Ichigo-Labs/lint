package engine

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/dsl"
	"github.com/ichigo-labs/lint/internal/lang"
)

// Match is a single structural hit: a span of source plus captured bindings.
type Match struct {
	Node                 *sitter.Node
	StartByte, EndByte   uint32
	StartPoint, EndPoint sitter.Point
	Bindings             Bindings

	// coverStart/coverEnd bound all captured nodes of a query match; used to
	// decide whether a query "matches at" a given node in relations/constraints.
	coverStart, coverEnd uint32
	hasCover             bool
}

// Finding is a reported rule violation.
type Finding struct {
	RuleID   string
	Message  string
	Severity dsl.Severity
	Note     string
	URL      string
	Tags     []string
	File     string

	StartByte, EndByte  uint32
	StartLine, StartCol int // 1-based
	EndLine, EndCol     int // 1-based

	Snippet  string
	Bindings map[string]string
	Fix      *string // replacement for [StartByte,EndByte), if the rule is fixable
}

// CompiledRule is a rule ready to run against parsed files.
type CompiledRule struct {
	ID       string
	Message  string
	Note     string
	Severity dsl.Severity
	URL      string
	Tags     []string
	Fix      *string

	// Paths/ExcludePaths are file-path globs scoping the rule (empty = all).
	Paths        []string
	ExcludePaths []string

	langs  []*lang.Language
	byLang map[string]*langRule
	raw    *dsl.Rule
}

// HasTag reports whether the rule carries the given tag.
func (r *CompiledRule) HasTag(tag string) bool {
	for _, t := range r.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Languages returns the languages this rule applies to.
func (r *CompiledRule) Languages() []*lang.Language { return r.langs }

// Raw returns the source rule (for tests and diagnostics).
func (r *CompiledRule) Raw() *dsl.Rule { return r.raw }

type langRule struct {
	l         *lang.Language
	root      *compiledMatcher
	where     []compiledConstraint
	relations []compiledRelation
}

type compiledMatcher struct {
	kind     dsl.MatcherKind
	pattern  *Pattern
	query    *sitter.Query
	querySrc string
	children []*compiledMatcher
	sub      *compiledMatcher
	where    []compiledConstraint
}

type compiledConstraint struct {
	kind    dsl.ConstraintKind
	varName string
	text    string
	other   string
	set     map[string]bool
	re      *regexp.Regexp
	matcher *compiledMatcher
	num     float64 // parsed threshold for numeric/count constraints
}

type compiledRelation struct {
	kind    dsl.RelationKind
	matcher *compiledMatcher
}

// Compile turns a parsed rule into an executable rule. Patterns are compiled
// per target language. When the rule names no languages, it is compiled for
// every language whose grammar can parse its patterns.
func Compile(r *dsl.Rule) (*CompiledRule, error) {
	cr := &CompiledRule{
		ID:           r.ID,
		Message:      r.Message,
		Note:         r.Note,
		Severity:     r.Severity,
		URL:          r.URL,
		Tags:         r.Tags,
		Fix:          r.Fix,
		Paths:        r.Paths,
		ExcludePaths: r.ExcludePaths,
		byLang:       map[string]*langRule{},
		raw:          r,
	}

	var targets []*lang.Language
	explicit := len(r.Languages) > 0
	if explicit {
		for _, name := range r.Languages {
			l, ok := lang.Get(name)
			if !ok {
				return nil, fmt.Errorf("rule %q: unknown language %q", r.ID, name)
			}
			targets = append(targets, l)
		}
	} else {
		for _, name := range lang.Names() {
			l, _ := lang.Get(name)
			targets = append(targets, l)
		}
	}

	for _, l := range targets {
		lr, err := compileForLang(l, r)
		if err != nil {
			if explicit {
				return nil, fmt.Errorf("rule %q [%s]: %w", r.ID, l.Name, err)
			}
			continue // language can't parse the pattern; skip it
		}
		if _, seen := cr.byLang[l.Name]; !seen {
			cr.byLang[l.Name] = lr
			cr.langs = append(cr.langs, l)
		}
	}
	if len(cr.byLang) == 0 {
		return nil, fmt.Errorf("rule %q: no language could compile its pattern", r.ID)
	}
	return cr, nil
}

func compileForLang(l *lang.Language, r *dsl.Rule) (*langRule, error) {
	root, err := compileMatcher(l, r.Match)
	if err != nil {
		return nil, err
	}
	if !canGenerate(root) {
		return nil, fmt.Errorf("rule needs at least one positive pattern/query (a rule made only of `not` cannot select anything)")
	}
	lr := &langRule{l: l, root: root}
	for _, c := range r.Where {
		cc, err := compileConstraint(l, c)
		if err != nil {
			return nil, err
		}
		lr.where = append(lr.where, cc)
	}
	for _, rel := range r.Relations {
		m, err := compileMatcher(l, rel.Matcher)
		if err != nil {
			return nil, err
		}
		lr.relations = append(lr.relations, compiledRelation{kind: rel.Kind, matcher: m})
	}
	return lr, nil
}

// canGenerate reports whether a matcher can produce candidate nodes on its
// own. Negation-only matchers cannot.
func canGenerate(cm *compiledMatcher) bool {
	switch cm.kind {
	case dsl.MatchPattern, dsl.MatchQuery:
		return true
	case dsl.MatchAll, dsl.MatchAny:
		for _, ch := range cm.children {
			if canGenerate(ch) {
				return true
			}
		}
		return false
	default: // MatchNot
		return false
	}
}

func compileMatcher(l *lang.Language, m *dsl.Matcher) (*compiledMatcher, error) {
	switch m.Kind {
	case dsl.MatchPattern:
		p, err := compilePattern(l, m.Pattern)
		if err != nil {
			return nil, err
		}
		return &compiledMatcher{kind: dsl.MatchPattern, pattern: p}, nil
	case dsl.MatchQuery:
		q, err := sitter.NewQuery([]byte(m.Query), l.Grammar())
		if err != nil {
			return nil, fmt.Errorf("invalid query: %w", err)
		}
		if q.CaptureCount() == 0 {
			return nil, fmt.Errorf("query must capture at least one node (e.g. add @match): %s", m.Query)
		}
		return &compiledMatcher{kind: dsl.MatchQuery, query: q, querySrc: m.Query}, nil
	case dsl.MatchAll, dsl.MatchAny:
		cm := &compiledMatcher{kind: m.Kind}
		for _, ch := range m.Children {
			c, err := compileMatcher(l, ch)
			if err != nil {
				return nil, err
			}
			cm.children = append(cm.children, c)
		}
		for _, c := range m.Where {
			cc, err := compileConstraint(l, c)
			if err != nil {
				return nil, err
			}
			cm.where = append(cm.where, cc)
		}
		return cm, nil
	case dsl.MatchNot:
		sub, err := compileMatcher(l, m.Sub)
		if err != nil {
			return nil, err
		}
		return &compiledMatcher{kind: dsl.MatchNot, sub: sub}, nil
	}
	return nil, fmt.Errorf("unknown matcher kind")
}

func compileConstraint(l *lang.Language, c dsl.Constraint) (compiledConstraint, error) {
	cc := compiledConstraint{kind: c.Kind, varName: c.Var, text: c.Text, other: c.Other}
	switch c.Kind {
	case dsl.ConRegex, dsl.ConNotRegex:
		re, err := regexp.Compile(c.Text)
		if err != nil {
			return cc, fmt.Errorf("invalid regex %q: %w", c.Text, err)
		}
		cc.re = re
	case dsl.ConIn, dsl.ConNotIn:
		cc.set = map[string]bool{}
		for _, it := range c.List {
			cc.set[it] = true
		}
	case dsl.ConPattern, dsl.ConNotPattern:
		m, err := compileMatcher(l, c.Matcher)
		if err != nil {
			return cc, err
		}
		cc.matcher = m
	case dsl.ConNumGt, dsl.ConNumGe, dsl.ConNumLt, dsl.ConNumLe,
		dsl.ConCountGt, dsl.ConCountGe, dsl.ConCountLt, dsl.ConCountLe,
		dsl.ConCountEq, dsl.ConCountNe:
		n, err := strconv.ParseFloat(c.Text, 64)
		if err != nil {
			return cc, fmt.Errorf("invalid number %q in where constraint: %w", c.Text, err)
		}
		cc.num = n
	}
	return cc, nil
}

// --- execution -----------------------------------------------------------

// ParseSource parses source bytes under a language grammar.
func ParseSource(l *lang.Language, src []byte) (*sitter.Tree, error) {
	parser := sitter.NewParser()
	parser.SetLanguage(l.Grammar())
	return parser.ParseCtx(context.Background(), nil, src)
}

// Run executes the rule against a parsed file for a specific language. It builds
// a one-off node index; callers checking many rules against the same tree should
// share one via RunIndexed.
func (r *CompiledRule) Run(l *lang.Language, tree *sitter.Tree, src []byte) []Finding {
	return r.RunIndexed(l, tree, src, BuildIndex(tree))
}

// RunIndexed is Run with a caller-supplied per-file node index, so a tree is
// walked once for a whole ruleset instead of once per rule.
func (r *CompiledRule) RunIndexed(l *lang.Language, tree *sitter.Tree, src []byte, idx *Index) []Finding {
	lr, ok := r.byLang[l.Name]
	if !ok {
		return nil
	}
	ctx := &matchCtx{l: l, tsrc: src, index: idx}
	root := tree.RootNode()

	candidates := lr.root.find(ctx, root)

	var findings []Finding
	seen := map[[2]uint32]bool{}
	for _, m := range candidates {
		b := m.Bindings
		if b == nil {
			b = Bindings{}
		}
		bindMatch(b, m, src)
		if !lr.passesRelations(ctx, m, b) {
			continue
		}
		if !lr.passesConstraints(ctx, b) {
			continue
		}
		key := [2]uint32{m.StartByte, m.EndByte}
		if seen[key] {
			continue
		}
		seen[key] = true
		findings = append(findings, r.toFinding(m, b, src))
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].StartByte != findings[j].StartByte {
			return findings[i].StartByte < findings[j].StartByte
		}
		return findings[i].EndByte < findings[j].EndByte
	})
	return findings
}

// bindMatch exposes the whole matched span to where/relations/message/fix as the
// implicit metavariable $match, mirroring the @match capture a query already
// provides. It never overwrites an existing binding (a user capture literally
// named "match", or a query's own @match/@target), so explicit captures win. The
// node is recorded only when the span is exactly one node, so `where $match kind`
// / `is` behave sensibly (a statement-sequence or query-cover span has no single
// node, like a variadic).
func bindMatch(b Bindings, m Match, src []byte) {
	if _, ok := b["match"]; ok {
		return
	}
	bv := Binding{Text: string(src[m.StartByte:m.EndByte])}
	if m.Node != nil && m.Node.StartByte() == m.StartByte && m.Node.EndByte() == m.EndByte {
		bv.Node = m.Node
	}
	b["match"] = bv
}

func (r *CompiledRule) toFinding(m Match, b Bindings, src []byte) Finding {
	f := Finding{
		RuleID:    r.ID,
		Message:   expandTemplate(r.Message, b),
		Severity:  r.Severity,
		Note:      r.Note,
		URL:       r.URL,
		Tags:      r.Tags,
		StartByte: m.StartByte,
		EndByte:   m.EndByte,
		StartLine: int(m.StartPoint.Row) + 1,
		StartCol:  int(m.StartPoint.Column) + 1,
		EndLine:   int(m.EndPoint.Row) + 1,
		EndCol:    int(m.EndPoint.Column) + 1,
		Snippet:   string(src[m.StartByte:m.EndByte]),
		Bindings:  map[string]string{},
	}
	for k, v := range b {
		f.Bindings[k] = v.Text
	}
	if r.Fix != nil {
		fixed := expandTemplate(*r.Fix, b)
		f.Fix = &fixed
	}
	return f
}

func (lr *langRule) passesConstraints(ctx *matchCtx, b Bindings) bool {
	for _, c := range lr.where {
		if !c.eval(ctx, b) {
			return false
		}
	}
	return true
}

func (lr *langRule) passesRelations(ctx *matchCtx, m Match, b Bindings) bool {
	for _, rel := range lr.relations {
		switch rel.kind {
		case dsl.RelInside:
			ok, nb := matchAncestor(ctx, rel.matcher, m.Node, b)
			if !ok {
				return false
			}
			b.merge(nb)
		case dsl.RelNotInside:
			if ok, _ := matchAncestor(ctx, rel.matcher, m.Node, b); ok {
				return false
			}
		case dsl.RelHas:
			ok, nb := matchDescendant(ctx, rel.matcher, m.Node, b)
			if !ok {
				return false
			}
			b.merge(nb)
		case dsl.RelNotHas:
			if ok, _ := matchDescendant(ctx, rel.matcher, m.Node, b); ok {
				return false
			}
		case dsl.RelPrecedes:
			ok, nb := matchSibling(ctx, rel.matcher, m.Node, b, true)
			if !ok {
				return false
			}
			b.merge(nb)
		case dsl.RelNotPrecedes:
			if ok, _ := matchSibling(ctx, rel.matcher, m.Node, b, true); ok {
				return false
			}
		case dsl.RelFollows:
			ok, nb := matchSibling(ctx, rel.matcher, m.Node, b, false)
			if !ok {
				return false
			}
			b.merge(nb)
		case dsl.RelNotFollows:
			if ok, _ := matchSibling(ctx, rel.matcher, m.Node, b, false); ok {
				return false
			}
		case dsl.RelDirectlyInside:
			ok, nb := matchDirectParent(ctx, rel.matcher, m.Node, b)
			if !ok {
				return false
			}
			b.merge(nb)
		case dsl.RelNotDirectlyInside:
			if ok, _ := matchDirectParent(ctx, rel.matcher, m.Node, b); ok {
				return false
			}
		case dsl.RelDirectlyHas:
			ok, nb := matchDirectChild(ctx, rel.matcher, m.Node, b)
			if !ok {
				return false
			}
			b.merge(nb)
		case dsl.RelNotDirectlyHas:
			if ok, _ := matchDirectChild(ctx, rel.matcher, m.Node, b); ok {
				return false
			}
		}
	}
	return true
}

// matchSibling looks for a sibling of the candidate's statement, on the given
// side (before=earlier, else later), that the matcher accepts. It scopes the
// search to the candidate's lowest enclosing block: it finds the ancestor whose
// parent is a block and compares that ancestor's siblings there. This looks
// through statement wrappers (a call inside an expression_statement reaches its
// block) without spilling into outer blocks.
func matchSibling(ctx *matchCtx, cm *compiledMatcher, n *sitter.Node, seed Bindings, before bool) (bool, Bindings) {
	stmt, block := enclosingStatement(n)
	if block == nil {
		return false, nil
	}
	sibs := relevantChildren(block, ctx.l)
	idx := indexOfNode(sibs, stmt)
	if idx < 0 {
		return false, nil
	}
	if before {
		for i := idx - 1; i >= 0; i-- {
			if nb, ok := cm.matchesNode(ctx, sibs[i], seed); ok {
				return true, nb
			}
		}
	} else {
		for i := idx + 1; i < len(sibs); i++ {
			if nb, ok := cm.matchesNode(ctx, sibs[i], seed); ok {
				return true, nb
			}
		}
	}
	return false, nil
}

// enclosingStatement returns the candidate's statement node (the ancestor that
// sits directly inside a block) and that block. Returns nil if the candidate has
// no enclosing block.
func enclosingStatement(n *sitter.Node) (stmt, block *sitter.Node) {
	for a := n; a != nil; a = a.Parent() {
		p := a.Parent()
		if p == nil {
			return nil, nil
		}
		if blockKinds[p.Type()] {
			return a, p
		}
	}
	return nil, nil
}

// indexOfNode finds target in nodes by span and kind (pointer identity is not
// guaranteed across tree-sitter node lookups).
func indexOfNode(nodes []*sitter.Node, target *sitter.Node) int {
	for i, n := range nodes {
		if n.StartByte() == target.StartByte() && n.EndByte() == target.EndByte() && n.Type() == target.Type() {
			return i
		}
	}
	return -1
}

// matchDirectParent tests the candidate's immediate parent against the matcher.
func matchDirectParent(ctx *matchCtx, cm *compiledMatcher, n *sitter.Node, seed Bindings) (bool, Bindings) {
	parent := n.Parent()
	if parent == nil {
		return false, nil
	}
	nb, ok := cm.matchesNode(ctx, parent, seed)
	return ok, nb
}

// matchDirectChild tests the candidate's immediate (relevant) children against
// the matcher — a depth-1 version of matchDescendant.
func matchDirectChild(ctx *matchCtx, cm *compiledMatcher, n *sitter.Node, seed Bindings) (bool, Bindings) {
	for _, c := range relevantChildren(n, ctx.l) {
		if nb, ok := cm.matchesNode(ctx, c, seed); ok {
			return true, nb
		}
	}
	return false, nil
}

// matchAncestor walks up from n (exclusive) looking for an ancestor the matcher
// accepts, seeded with the current bindings for cross-pattern back-references.
func matchAncestor(ctx *matchCtx, cm *compiledMatcher, n *sitter.Node, seed Bindings) (bool, Bindings) {
	if n == nil {
		return false, nil
	}
	for a := n.Parent(); a != nil; a = a.Parent() {
		if nb, ok := cm.matchesNode(ctx, a, seed); ok {
			return true, nb
		}
	}
	return false, nil
}

// matchDescendant searches the subtree of n (excluding n) for a node the
// matcher accepts.
func matchDescendant(ctx *matchCtx, cm *compiledMatcher, n *sitter.Node, seed Bindings) (bool, Bindings) {
	if n == nil {
		return false, nil
	}
	var found bool
	var bindings Bindings
	count := int(n.ChildCount())
	for i := 0; i < count && !found; i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		walk(c, func(d *sitter.Node) {
			if found {
				return
			}
			if nb, ok := cm.matchesNode(ctx, d, seed); ok {
				found, bindings = true, nb
			}
		})
	}
	return found, bindings
}

func (c compiledConstraint) eval(ctx *matchCtx, b Bindings) bool {
	bv, ok := b[c.varName]
	if !ok {
		return false // constraint references an unbound metavariable
	}
	switch c.kind {
	case dsl.ConRegex:
		return c.re.MatchString(bv.Text)
	case dsl.ConNotRegex:
		return !c.re.MatchString(bv.Text)
	case dsl.ConKind:
		return bv.Node != nil && bv.Node.Type() == c.text
	case dsl.ConNotKind:
		return bv.Node == nil || bv.Node.Type() != c.text
	case dsl.ConIn:
		return c.set[bv.Text]
	case dsl.ConNotIn:
		return !c.set[bv.Text]
	case dsl.ConEqVar:
		ov, ok := b[c.other]
		return ok && bv.Text == ov.Text
	case dsl.ConNeqVar:
		ov, ok := b[c.other]
		// Require the other operand to be bound; otherwise the predicate is
		// vacuously true (a typo'd metavar) and adds nothing.
		return ok && bv.Text != ov.Text
	case dsl.ConEqText:
		return bv.Text == c.text
	case dsl.ConNeqText:
		return bv.Text != c.text
	case dsl.ConPattern:
		if bv.Node == nil {
			return false
		}
		_, ok := matchNodeOrDescendant(ctx, c.matcher, bv.Node, b)
		return ok
	case dsl.ConNotPattern:
		if bv.Node == nil {
			return true
		}
		_, ok := matchNodeOrDescendant(ctx, c.matcher, bv.Node, b)
		return !ok
	case dsl.ConNumGt, dsl.ConNumGe, dsl.ConNumLt, dsl.ConNumLe:
		v, err := strconv.ParseFloat(strings.TrimSpace(bv.Text), 64)
		if err != nil {
			return false // a non-numeric capture never satisfies a numeric bound
		}
		return cmpNum(c.kind, v, c.num)
	case dsl.ConCountGt, dsl.ConCountGe, dsl.ConCountLt, dsl.ConCountLe,
		dsl.ConCountEq, dsl.ConCountNe:
		n := 1 // a single capture counts as one node
		if bv.Variadic {
			n = bv.Count
		}
		return cmpNum(c.kind, float64(n), c.num)
	}
	return false
}

// cmpNum applies a numeric/count comparison constraint.
func cmpNum(kind dsl.ConstraintKind, a, b float64) bool {
	switch kind {
	case dsl.ConNumGt, dsl.ConCountGt:
		return a > b
	case dsl.ConNumGe, dsl.ConCountGe:
		return a >= b
	case dsl.ConNumLt, dsl.ConCountLt:
		return a < b
	case dsl.ConNumLe, dsl.ConCountLe:
		return a <= b
	case dsl.ConCountEq:
		return a == b
	case dsl.ConCountNe:
		return a != b
	}
	return false
}

// matchNodeOrDescendant checks whether the matcher accepts n or any node in its
// subtree.
func matchNodeOrDescendant(ctx *matchCtx, cm *compiledMatcher, n *sitter.Node, seed Bindings) (Bindings, bool) {
	var res Bindings
	var ok bool
	walk(n, func(d *sitter.Node) {
		if ok {
			return
		}
		if nb, matched := cm.matchesNode(ctx, d, seed); matched {
			res, ok = nb, true
		}
	})
	return res, ok
}

// expandTemplate substitutes $NAME / $$$NAME occurrences with binding text.
func expandTemplate(tmpl string, b Bindings) string {
	out := make([]byte, 0, len(tmpl))
	i, n := 0, len(tmpl)
	for i < n {
		if tmpl[i] != '$' {
			out = append(out, tmpl[i])
			i++
			continue
		}
		j := i
		for j < n && tmpl[j] == '$' {
			j++
		}
		start := j
		for j < n && isIdentContByte(tmpl[j]) {
			j++
		}
		name := tmpl[start:j]
		if name == "" {
			out = append(out, tmpl[i:j]...)
			i = j
			continue
		}
		if bv, ok := b[name]; ok {
			out = append(out, bv.Text...)
		} else {
			out = append(out, tmpl[i:j]...)
		}
		i = j
	}
	return string(out)
}
