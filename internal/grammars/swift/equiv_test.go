package swift_test

import (
	"testing"

	upstream "github.com/smacker/go-tree-sitter/swift"

	"github.com/ichigo-labs/lint/internal/grammars/grammartest"
	vendored "github.com/ichigo-labs/lint/internal/grammars/swift"
)

// TestMatchesUpstream proves the runtime-decompressed parse tables reproduce
// the original smacker grammar exactly: both grammars must yield identical
// trees on every testdata sample. Running in CI on every release platform,
// this also guards the blob's baked-in struct layout and endianness.
func TestMatchesUpstream(t *testing.T) {
	grammartest.EqualOnTestdata(t, vendored.GetLanguage(), upstream.GetLanguage())
}
