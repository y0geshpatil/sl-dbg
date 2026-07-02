package cli

import "testing"

func TestIsReleaseVersion(t *testing.T) {
	tests := []struct {
		ver  string
		want bool
	}{
		// valid release versions
		{"1.2.3", true},
		{"0.3.0", true},
		{"10.0.1", true},
		// dev / snapshot variants — must not attempt download
		{"", false},
		{"0.0.0-dev", false},
		{"1.2.3-dev", false},
		{"1.2.3-next", false},
		{"1.2.3-dirty", false},
		{"1.2.3-next-dirty", false},
		// pre-release / build metadata that should NOT be treated as release
		{"1.2.3-alpha", false},   // unknown suffix → blocked by -dev/-next/-dirty? No, alpha != those suffixes
		{"1.2.3-beta", false},    // same
		{"v1.2.3", false},        // version with v prefix is not injected by goreleaser (goreleaser strips it)
		{"1.2.3+build.123", false}, // build metadata contains non-release marker via HasSuffix? Actually this should pass through; guard it
	}
	for _, tc := range tests {
		got := isReleaseVersion(tc.ver)
		if got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v; want %v", tc.ver, got, tc.want)
		}
	}
}
