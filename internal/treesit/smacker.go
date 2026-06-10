package treesit

import (
	"runtime"
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// This file reaches through smacker's unexported struct fields to recover
// the raw C pointers, because the bindings expose no accessor. The layouts
// mirrored here are pinned to go-tree-sitter v0.0.0-20240827094217:
//
//	type Tree struct { *BaseTree; p *Parser; cache map[C.TSNode]*Node }
//	type BaseTree struct { c *C.TSTree; isClosed bool }
//	type Language struct { ptr unsafe.Pointer }
//
// In each case the pointer we need is the first word of the struct.
// TestSmackerLayouts re-verifies the field names/offsets with reflect, so a
// module bump that reorders fields fails loudly instead of corrupting memory.
//
// The sitter import also guarantees smacker's compiled C objects (which
// define the ts_* symbols treesit.h declares) are present at link time.

// TreePtr returns the raw *TSTree behind a smacker Tree. The pointer is
// owned by the Tree's finalizer: it is only valid while t remains reachable.
func TreePtr(t *sitter.Tree) unsafe.Pointer {
	base := *(*unsafe.Pointer)(unsafe.Pointer(t)) // Tree.BaseTree
	return *(*unsafe.Pointer)(base)               // BaseTree.c
}

// LangPtr returns the raw *TSLanguage behind a smacker Language. Grammar
// languages are static C data, so the result never expires.
func LangPtr(l *sitter.Language) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(l)) // Language.ptr
}

// ExtractTree is Extract for a smacker tree. It keeps t reachable for the
// duration of the C walk; with the bare Extract(TreePtr(t)) form, t could be
// collected mid-walk and its finalizer would free the tree under the cursor.
func ExtractTree(t *sitter.Tree) *Flat {
	f := Extract(TreePtr(t))
	runtime.KeepAlive(t)
	return f
}
