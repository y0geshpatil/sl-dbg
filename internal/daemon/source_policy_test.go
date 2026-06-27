package daemon

import (
	"path/filepath"
	"testing"
)

func TestHasDotDotSegment(t *testing.T) {
	cases := map[string]bool{
		"/etc/passwd":                 false,
		"/home/user/app.py":           false,
		"../../../etc/passwd":         true,
		"/tmp/app/../../etc/passwd":   true,
		"/tmp/app/..hidden":           false,
		"/tmp/app/hidden..":           false,
		"..":                          true,
		"":                            false,
		"a/b/../c":                    true,
		"a/b/c..d/e":                  false,
		"/tmp/.../x":                  false, // three dots is not a traversal segment
	}
	for in, want := range cases {
		if got := hasDotDotSegment(in); got != want {
			t.Errorf("hasDotDotSegment(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPathUnderRoots(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()

	cases := []struct {
		name  string
		path  string
		roots []string
		want  bool
	}{
		{"under root", filepath.Join(root, "sub", "file.py"), []string{root}, true},
		{"equal to root", root, []string{root}, true},
		{"outside roots", "/etc/passwd", []string{root}, false},
		{"in another tree", filepath.Join(other, "x"), []string{root}, false},
		{"empty roots denies", filepath.Join(root, "x"), nil, false},
		// Defence against the prefix-but-not-segment trap: a root of
		// "/tmp/foo" must NOT permit "/tmp/foobar/file". We construct
		// such a path by concatenating without a separator.
		{"prefix-but-not-segment", root + "barbaz/file", []string{root}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathUnderRoots(tc.path, tc.roots); got != tc.want {
				t.Errorf("pathUnderRoots(%q, %v) = %v, want %v", tc.path, tc.roots, got, tc.want)
			}
		})
	}
}
