#ifndef LINT_TREESIT_H_
#define LINT_TREESIT_H_

#include <stdbool.h>
#include <stdint.h>

// Declarations vendored from tree-sitter's api.h as shipped in
// github.com/smacker/go-tree-sitter@v0.0.0-20240827094217-dd81d9e9be82.
// Only declarations: the definitions are compiled into smacker's package
// archive and resolved at final link, so these struct layouts must match
// that copy of api.h byte-for-byte (TestSmackerLayouts and the equivalence
// test guard against a silent ABI drift on a module bump).

typedef uint16_t TSSymbol;
typedef struct TSLanguage TSLanguage;
typedef struct TSTree TSTree;

typedef struct TSPoint {
  uint32_t row;
  uint32_t column;
} TSPoint;

typedef struct TSNode {
  uint32_t context[4];
  const void *id;
  const TSTree *tree;
} TSNode;

typedef struct TSTreeCursor {
  const void *tree;
  const void *id;
  uint32_t context[3];
} TSTreeCursor;

TSNode ts_tree_root_node(const TSTree *self);
TSTreeCursor ts_tree_cursor_new(TSNode node);
void ts_tree_cursor_delete(TSTreeCursor *self);
TSNode ts_tree_cursor_current_node(const TSTreeCursor *self);
bool ts_tree_cursor_goto_parent(TSTreeCursor *self);
bool ts_tree_cursor_goto_next_sibling(TSTreeCursor *self);
bool ts_tree_cursor_goto_first_child(TSTreeCursor *self);
TSSymbol ts_node_symbol(TSNode self);
uint32_t ts_node_start_byte(TSNode self);
TSPoint ts_node_start_point(TSNode self);
uint32_t ts_node_end_byte(TSNode self);
TSPoint ts_node_end_point(TSNode self);
bool ts_node_is_named(TSNode self);
bool ts_node_is_missing(TSNode self);
bool ts_node_is_extra(TSNode self);
bool ts_node_has_error(TSNode self);
bool ts_node_is_error(TSNode self);
uint32_t ts_node_descendant_count(TSNode self);
uint32_t ts_language_symbol_count(const TSLanguage *self);
const char *ts_language_symbol_name(const TSLanguage *self, TSSymbol symbol);

// lint_treesit_flags bit layout; mirrored by the Go flag constants.
enum {
  LINT_TREESIT_NAMED = 1u << 0,
  LINT_TREESIT_EXTRA = 1u << 1,
  LINT_TREESIT_MISSING = 1u << 2,
  LINT_TREESIT_ERROR = 1u << 3,
  LINT_TREESIT_HAS_ERROR = 1u << 4,
};

// lint_treesit_flat carries one whole tree across the cgo boundary in a
// single call: parallel arrays, one entry per visible node in pre-order
// (the order of recursively visiting ts_node_child(i)). All arrays are
// malloc'd; the Go side copies them into Go memory and calls
// lint_treesit_flat_free exactly once.
typedef struct {
  uint32_t count;
  bool failed; // allocation failure; all arrays already freed
  uint16_t *symbol;
  uint32_t *start_byte;
  uint32_t *end_byte;
  uint32_t *start_row;
  uint32_t *start_col;
  uint32_t *end_row;
  uint32_t *end_col;
  int32_t *parent; // pre-order index of parent, -1 for the root
  uint8_t *flags;  // LINT_TREESIT_* bits
  uintptr_t *node_id;
} lint_treesit_flat;

lint_treesit_flat lint_treesit_extract(const TSTree *tree);
void lint_treesit_flat_free(lint_treesit_flat *f);

#endif // LINT_TREESIT_H_
