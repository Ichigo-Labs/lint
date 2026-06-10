package treesit

// Flat is one parsed tree as parallel arrays indexed by pre-order position
// (the order of recursively visiting Child(i)). Pre-order makes structure
// cheap to recover without pointers: a node's descendants are exactly the
// contiguous index range [i+1, SubtreeEnd[i]), and the children CSR can be
// rebuilt from Parent alone because siblings appear in source order.
// A Flat is immutable after Extract and safe for concurrent readers.
type Flat struct {
	Symbol    []uint16 // grammar symbol id (ErrorSymbol for ERROR nodes)
	StartByte []uint32
	EndByte   []uint32
	StartRow  []uint32
	StartCol  []uint32
	EndRow    []uint32
	EndCol    []uint32
	Parent    []int32   // pre-order index of parent; -1 for the root
	Flags     []uint8   // flag* bits
	NodeID    []uintptr // ts_node id, for mapping query captures back

	// ChildStart/ChildList are a CSR layout: the children of node i are
	// ChildList[ChildStart[i]:ChildStart[i+1]], in source order.
	ChildStart []int32
	ChildList  []int32

	// SubtreeEnd[i] is the index just past i's last descendant, so
	// [i, SubtreeEnd[i]) is i's whole subtree.
	SubtreeEnd []int32
}

// Len returns the number of nodes in the tree.
func (f *Flat) Len() int { return len(f.Symbol) }

// Children returns the child indices of node i, in source order.
// The returned slice aliases the CSR arrays; callers must not mutate it.
func (f *Flat) Children(i int) []int32 {
	return f.ChildList[f.ChildStart[i]:f.ChildStart[i+1]]
}

// IsNamed reports whether node i is a named (non-anonymous) node.
func (f *Flat) IsNamed(i int) bool { return f.Flags[i]&flagNamed != 0 }

// IsExtra reports whether node i is an extra (e.g. comment) node.
func (f *Flat) IsExtra(i int) bool { return f.Flags[i]&flagExtra != 0 }

// IsMissing reports whether node i was inserted by error recovery.
func (f *Flat) IsMissing(i int) bool { return f.Flags[i]&flagMissing != 0 }

// IsError reports whether node i is an ERROR node.
func (f *Flat) IsError(i int) bool { return f.Flags[i]&flagError != 0 }

// HasError reports whether node i's subtree contains a syntax error.
func (f *Flat) HasError(i int) bool { return f.Flags[i]&flagHasError != 0 }

// buildChildren derives the children CSR from Parent. Filling in ascending
// pre-order index visits each parent's children in source order, so the CSR
// preserves Child(i) order without sorting.
func (f *Flat) buildChildren() {
	n := len(f.Parent)
	start := make([]int32, n+1)
	for i := 1; i < n; i++ {
		start[f.Parent[i]+1]++
	}
	for i := 0; i < n; i++ {
		start[i+1] += start[i]
	}
	var list []int32
	if n > 1 {
		list = make([]int32, n-1) // every node except the root is someone's child
	}
	// Fill using start[p] as a moving cursor; afterwards every start[p] has
	// advanced to the old start[p+1] (trivially so for childless p), so one
	// right shift restores the offsets.
	for i := 1; i < n; i++ {
		p := f.Parent[i]
		list[start[p]] = int32(i)
		start[p]++
	}
	copy(start[1:], start[:n])
	start[0] = 0
	f.ChildStart = start
	f.ChildList = list
}

// buildSubtreeEnd computes pre-order subtree extents in one reverse pass:
// children carry larger indices than their parents, so by the time i is
// visited descending, SubtreeEnd[i] is final and can be folded into its
// parent's running maximum.
func (f *Flat) buildSubtreeEnd() {
	n := len(f.Parent)
	end := make([]int32, n)
	for i := range end {
		end[i] = int32(i + 1)
	}
	for i := n - 1; i > 0; i-- {
		if p := f.Parent[i]; end[i] > end[p] {
			end[p] = end[i]
		}
	}
	f.SubtreeEnd = end
}
