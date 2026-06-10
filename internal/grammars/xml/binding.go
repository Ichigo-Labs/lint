// Package xml exposes the vendored tree-sitter-xml grammar.
// See VENDOR.md for provenance.
package xml

//#include "tree_sitter/parser.h"
//TSLanguage *tree_sitter_xml();
import "C"
import (
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"
)

// GetLanguage returns the Tree-sitter grammar for XML.
func GetLanguage() *sitter.Language {
	return sitter.NewLanguage(unsafe.Pointer(C.tree_sitter_xml()))
}
