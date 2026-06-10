// Package json exposes the vendored tree-sitter-json grammar.
// See VENDOR.md for provenance.
package json

//#include "tree_sitter/parser.h"
//TSLanguage *tree_sitter_json();
import "C"
import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the Tree-sitter grammar for JSON.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_json()))
}
