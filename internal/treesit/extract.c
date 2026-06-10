#include "treesit.h"

#include <stdint.h>
#include <stdlib.h>

void lint_treesit_flat_free(lint_treesit_flat *f) {
  free(f->symbol);
  free(f->start_byte);
  free(f->end_byte);
  free(f->start_row);
  free(f->start_col);
  free(f->end_row);
  free(f->end_col);
  free(f->parent);
  free(f->flags);
  free(f->node_id);
  *f = (lint_treesit_flat){0};
}

// reserve grows every parallel array to hold at least `need` entries,
// doubling so the walk stays amortized O(n) even when the descendant-count
// seed undershoots. realloc(NULL, ..) covers the first allocation.
static bool reserve(lint_treesit_flat *f, uint32_t *cap, uint32_t need) {
  if (need <= *cap) {
    return true;
  }
  uint32_t ncap = *cap ? *cap : 256;
  while (ncap < need) {
    ncap *= 2;
  }
#define LINT_TREESIT_GROW(field, type)                       \
  do {                                                       \
    void *p = realloc(f->field, (size_t)ncap * sizeof(type)); \
    if (!p) {                                                \
      lint_treesit_flat_free(f);                             \
      f->failed = true;                                      \
      return false;                                          \
    }                                                        \
    f->field = (type *)p;                                    \
  } while (0)
  LINT_TREESIT_GROW(symbol, uint16_t);
  LINT_TREESIT_GROW(start_byte, uint32_t);
  LINT_TREESIT_GROW(end_byte, uint32_t);
  LINT_TREESIT_GROW(start_row, uint32_t);
  LINT_TREESIT_GROW(start_col, uint32_t);
  LINT_TREESIT_GROW(end_row, uint32_t);
  LINT_TREESIT_GROW(end_col, uint32_t);
  LINT_TREESIT_GROW(parent, int32_t);
  LINT_TREESIT_GROW(flags, uint8_t);
  LINT_TREESIT_GROW(node_id, uintptr_t);
#undef LINT_TREESIT_GROW
  *cap = ncap;
  return true;
}

// lint_treesit_extract walks the whole tree with a TSTreeCursor, entirely in
// C, so Go pays one cgo transition per tree instead of several per node.
// The cursor's first-child/next-sibling/parent moves enumerate exactly the
// visible nodes that ts_node_child(i) recursion yields, in the same
// pre-order; the Go-side equivalence test pins that down per grammar.
lint_treesit_flat lint_treesit_extract(const TSTree *tree) {
  lint_treesit_flat f = {0};
  uint32_t cap = 0;

  TSNode root = ts_tree_root_node(tree);
  // Exact node count is known up front, so the doubling path is normally
  // never taken; it remains as a guard rather than a hard assumption.
  if (!reserve(&f, &cap, ts_node_descendant_count(root))) {
    return f;
  }

  TSTreeCursor cursor = ts_tree_cursor_new(root);
  // parent is the pre-order index of the parent of the node the cursor is
  // currently on. Descending pushes the just-recorded index; ascending pops
  // through the already-recorded parent links, so no separate stack.
  int32_t parent = -1;
  for (;;) {
    TSNode node = ts_tree_cursor_current_node(&cursor);
    // Indices are int32 on the Go side; a tree that large (>2^31 nodes needs
    // a multi-GB file and ~90GB of arrays) fails explicitly instead of
    // wrapping parent links negative.
    if (f.count > INT32_MAX) {
      ts_tree_cursor_delete(&cursor);
      lint_treesit_flat_free(&f);
      f.failed = true;
      return f;
    }
    if (!reserve(&f, &cap, f.count + 1)) {
      ts_tree_cursor_delete(&cursor);
      return f;
    }
    uint32_t i = f.count++;
    f.symbol[i] = ts_node_symbol(node);
    f.start_byte[i] = ts_node_start_byte(node);
    f.end_byte[i] = ts_node_end_byte(node);
    TSPoint sp = ts_node_start_point(node);
    TSPoint ep = ts_node_end_point(node);
    f.start_row[i] = sp.row;
    f.start_col[i] = sp.column;
    f.end_row[i] = ep.row;
    f.end_col[i] = ep.column;
    f.parent[i] = parent;
    uint8_t flags = 0;
    if (ts_node_is_named(node)) flags |= LINT_TREESIT_NAMED;
    if (ts_node_is_extra(node)) flags |= LINT_TREESIT_EXTRA;
    if (ts_node_is_missing(node)) flags |= LINT_TREESIT_MISSING;
    if (ts_node_is_error(node)) flags |= LINT_TREESIT_ERROR;
    if (ts_node_has_error(node)) flags |= LINT_TREESIT_HAS_ERROR;
    f.flags[i] = flags;
    f.node_id[i] = (uintptr_t)node.id;

    if (ts_tree_cursor_goto_first_child(&cursor)) {
      parent = (int32_t)i;
      continue;
    }
    for (;;) {
      if (ts_tree_cursor_goto_next_sibling(&cursor)) {
        break;
      }
      if (!ts_tree_cursor_goto_parent(&cursor)) {
        ts_tree_cursor_delete(&cursor);
        return f;
      }
      parent = f.parent[parent];
    }
  }
}
