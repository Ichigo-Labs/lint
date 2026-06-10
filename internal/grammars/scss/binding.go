// Package scss exposes the vendored tree-sitter-scss grammar.
// See VENDOR.md for provenance.
package scss

//#include "tree_sitter/parser.h"
//TSLanguage *tree_sitter_scss();
import "C"
import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the Tree-sitter grammar for SCSS.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_scss()))
}
