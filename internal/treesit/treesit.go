// Package treesit serializes an already-parsed Tree-sitter tree into flat
// parallel Go arrays in a single cgo call. The rest of the program can then
// inspect node kinds, ranges and structure as plain slice reads instead of
// one cgo transition per node (which is what used to dominate checking).
package treesit

/*
#include "treesit.h"
*/
import "C"

import (
	"sync"
	"unsafe"
)

// Flag bits stored per node in Flat.Flags, mirroring LINT_TREESIT_* in
// treesit.h. They are Go constants (not C enum references) so files that
// only consume a Flat stay free of cgo types.
const (
	flagNamed    = 1 << 0
	flagExtra    = 1 << 1
	flagMissing  = 1 << 2
	flagError    = 1 << 3
	flagHasError = 1 << 4
)

// ErrorSymbol is ts_builtin_sym_error: the symbol every ERROR node reports.
// It lies outside the language's symbol table, so SymbolNames/SymbolCanon
// slices do not cover it; SymbolName and Canonical special-case it.
const ErrorSymbol uint16 = 0xFFFF

// Extract walks the tree rooted at the raw *TSTree pointer once in C and
// returns its flat form. The caller must keep whatever owns the tree alive
// across the call (smacker frees trees from a GC finalizer); prefer
// ExtractTree, which handles that.
func Extract(tree unsafe.Pointer) *Flat {
	cf := C.lint_treesit_extract((*C.TSTree)(tree))
	if cf.failed {
		// A C-side malloc failure (or a >2^31-node tree) is unrecoverable
		// mid-check; no caller can do better than crashing with context.
		// lint:ignore-next-line panic-in-library
		panic("treesit: tree too large or out of memory extracting it")
	}
	n := int(cf.count)
	f := &Flat{
		Symbol:    copyOut[uint16](unsafe.Pointer(cf.symbol), n),
		StartByte: copyOut[uint32](unsafe.Pointer(cf.start_byte), n),
		EndByte:   copyOut[uint32](unsafe.Pointer(cf.end_byte), n),
		StartRow:  copyOut[uint32](unsafe.Pointer(cf.start_row), n),
		StartCol:  copyOut[uint32](unsafe.Pointer(cf.start_col), n),
		EndRow:    copyOut[uint32](unsafe.Pointer(cf.end_row), n),
		EndCol:    copyOut[uint32](unsafe.Pointer(cf.end_col), n),
		Parent:    copyOut[int32](unsafe.Pointer(cf.parent), n),
		Flags:     copyOut[uint8](unsafe.Pointer(cf.flags), n),
		NodeID:    copyOut[uintptr](unsafe.Pointer(cf.node_id), n),
	}
	C.lint_treesit_flat_free(&cf)
	f.buildChildren()
	f.buildSubtreeEnd()
	return f
}

// copyOut copies n C-allocated elements into a fresh Go slice. The element
// types are layout-identical on both sides (fixed-width ints; uintptr_t and
// Go uintptr are both the pointer word), so a view-and-copy is exact.
func copyOut[T any](p unsafe.Pointer, n int) []T {
	if n == 0 {
		return nil
	}
	out := make([]T, n)
	copy(out, unsafe.Slice((*T)(p), n))
	return out
}

// symtab is the per-language symbol metadata, fetched through cgo once per
// language per process and shared by every Extract user afterwards.
type symtab struct {
	names []string
	canon []uint16
}

// symtabs caches symtab by raw *TSLanguage pointer. Grammar pointers are
// static C data, so the key never dangles.
var symtabs sync.Map

func tableFor(lang unsafe.Pointer) *symtab {
	if v, ok := symtabs.Load(lang); ok {
		return v.(*symtab)
	}
	l := (*C.TSLanguage)(lang)
	count := int(C.ts_language_symbol_count(l))
	t := &symtab{
		names: make([]string, count),
		canon: make([]uint16, count),
	}
	first := make(map[string]uint16, count)
	for s := 0; s < count; s++ {
		name := C.GoString(C.ts_language_symbol_name(l, C.TSSymbol(s)))
		t.names[s] = name
		// Aliasing can give several symbol ids the same surface name (the
		// name is all Type() exposes), so canonicalize to the first id per
		// name: print-equal kinds become integer-equal.
		if f, ok := first[name]; ok {
			t.canon[s] = f
		} else {
			first[name] = uint16(s)
			t.canon[s] = uint16(s)
		}
	}
	v, _ := symtabs.LoadOrStore(lang, t)
	return v.(*symtab)
}

// SymbolNames returns the symbol-id -> kind-name table for a raw *TSLanguage
// pointer. The slice is shared and must be treated as read-only. ErrorSymbol
// is out of range; resolve names through SymbolName.
func SymbolNames(lang unsafe.Pointer) []string {
	return tableFor(lang).names
}

// SymbolCanon returns the canonicalization table for a raw *TSLanguage
// pointer: canon[s] is the first symbol id whose name equals names[s].
// The slice is shared and must be treated as read-only.
func SymbolCanon(lang unsafe.Pointer) []uint16 {
	return tableFor(lang).canon
}

// SymbolName resolves a node symbol against a SymbolNames table, handling
// the out-of-table ERROR symbol the way ts_language_symbol_name does.
func SymbolName(names []string, sym uint16) string {
	if sym == ErrorSymbol {
		return "ERROR"
	}
	if int(sym) < len(names) {
		return names[sym]
	}
	return ""
}

// Canonical resolves a node symbol against a SymbolCanon table. ERROR has no
// table entry and no same-named twin, so it canonicalizes to itself.
func Canonical(canon []uint16, sym uint16) uint16 {
	if int(sym) < len(canon) {
		return canon[sym]
	}
	return sym
}
