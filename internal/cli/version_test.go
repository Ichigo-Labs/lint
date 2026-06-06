package cli

import "testing"

func TestShortCommit(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"1234567", "1234567"},
		{"12345678", "1234567"},
		{"42613ac482bbb20276b092a27b59cdd03fcdd224", "42613ac"},
	}
	for _, tt := range tests {
		if got := shortCommit(tt.in); got != tt.want {
			t.Errorf("shortCommit(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildInfoShort(t *testing.T) {
	tests := []struct {
		name string
		b    buildInfo
		want string
	}{
		{"no commit", buildInfo{Version: "v0.2.0", Commit: "unknown"}, "v0.2.0"},
		{"clean commit", buildInfo{Version: "v0.2.0", Commit: "42613ac482bb"}, "v0.2.0 (42613ac)"},
		{"dirty commit", buildInfo{Version: "0.1.0-dev", Commit: "42613ac482bb", Dirty: true}, "0.1.0-dev (42613ac-dirty)"},
	}
	for _, tt := range tests {
		if got := tt.b.short(); got != tt.want {
			t.Errorf("%s: short() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
