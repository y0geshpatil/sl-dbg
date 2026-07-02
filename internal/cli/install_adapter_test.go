package cli

import (
	"fmt"
	"testing"
)

func TestIsReleaseVersion(t *testing.T) {
	tests := []struct {
		ver  string
		want bool
	}{
		// valid release versions — goreleaser injects these without a leading "v"
		{"1.2.3", true},
		{"0.3.0", true},
		{"10.0.1", true},
		// pre-release tags are published by goreleaser and have release assets
		{"1.2.3-alpha", true},
		{"1.2.3-beta", true},
		// dev / snapshot / dirty builds must NOT attempt a download
		{"", false},
		{"0.0.0-dev", false},
		{"1.2.3-dev", false},
		{"1.2.3-next", false},
		{"1.2.3-dirty", false},
		{"1.2.3-next-dirty", false},
		// goreleaser never injects a "v" prefix, but guard it anyway
		{"v1.2.3", false},
		// build metadata is not a valid release tag path component
		{"1.2.3+build.123", false},
	}
	for _, tc := range tests {
		got := isReleaseVersion(tc.ver)
		if got != tc.want {
			t.Errorf("isReleaseVersion(%q) = %v; want %v", tc.ver, got, tc.want)
		}
	}
}

// TestJavaAdapterURL verifies that the download URL is constructed correctly.
// buildinfo.Version is "1.2.3" (no "v"); GitHub release tags are "v1.2.3".
func TestJavaAdapterURL(t *testing.T) {
	ver := "1.2.3"
	got := fmt.Sprintf("https://github.com/y0geshpatil/sl-dbg/releases/download/v%s/sl-dbg-java-adapter.jar", ver)
	want := "https://github.com/y0geshpatil/sl-dbg/releases/download/v1.2.3/sl-dbg-java-adapter.jar"
	if got != want {
		t.Errorf("URL = %q; want %q", got, want)
	}
}
