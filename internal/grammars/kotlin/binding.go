// Package kotlin exposes the vendored kotlin grammar. Its parse tables are
// stored gzip-compressed (tables.bin.gz) and decompressed into C memory on
// the first GetLanguage call; see VENDOR.md for provenance and regeneration.
package kotlin

/*
#include "parser.h"
const TSLanguage *lint_tree_sitter_kotlin(void);
void lint_kotlin_set_tables(const void *parse_table,
                        const void *small_parse_table,
                        const void *small_parse_table_map,
                        const void *parse_actions);
*/
import "C"

import (
	_ "embed"
	"sync"
	"unsafe"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/ichigo-labs/lint/internal/grammars/tables"
)

//go:embed tables.bin.gz
var tablesGz []byte

var once sync.Once

// GetLanguage returns the Tree-sitter grammar for kotlin. The first call
// decompresses the parse tables into C-allocated memory that lives for the
// rest of the process (the grammar reads it on every parse).
func GetLanguage() *sitter.Language {
	once.Do(func() {
		s := tables.Sections(tablesGz, 4)
		C.lint_kotlin_set_tables(C.CBytes(s[0]), C.CBytes(s[1]), C.CBytes(s[2]), C.CBytes(s[3]))
	})
	return sitter.NewLanguage(unsafe.Pointer(C.lint_tree_sitter_kotlin()))
}
