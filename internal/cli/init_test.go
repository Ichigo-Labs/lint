package cli

import "testing"

func TestIsReleaseTag(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Real, downloadable release tags.
		{"v0.2.0", true},
		{"v0.2.0-rc1", true},
		{"v1.10.3", true},
		// Not downloadable tags.
		{"0.2.0", false},     // missing leading v
		{"0.1.0-dev", false}, // dev default
		{"latest", false},
		{"", false},
		// Module pseudo-versions (no release asset) — all three Go forms.
		{"v0.0.0-20260101120000-abcdef123456", false},
		{"v1.2.3-0.20260101120000-abcdef123456", false},
		{"v1.2.4-rc1.0.20260101120000-abcdef123456", false},
	}
	for _, tt := range tests {
		if got := isReleaseTag(tt.in); got != tt.want {
			t.Errorf("isReleaseTag(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
