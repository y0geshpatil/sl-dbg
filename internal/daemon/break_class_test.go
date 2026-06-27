package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveJavaClassToFile(t *testing.T) {
	root := t.TempDir()
	// Create root/com/example/Foo.java and root/Bare.java
	if err := os.MkdirAll(filepath.Join(root, "com", "example"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite := func(p string) {
		if err := os.WriteFile(p, []byte("// stub"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(filepath.Join(root, "com", "example", "Foo.java"))
	mustWrite(filepath.Join(root, "Bare.java"))

	cases := []struct {
		name  string
		class string
		want  string
		ok    bool
	}{
		{"fully-qualified", "com.example.Foo", filepath.Join(root, "com", "example", "Foo.java"), true},
		{"bare", "Bare", filepath.Join(root, "Bare.java"), true},
		{"inner-stripped", "com.example.Foo$Inner", filepath.Join(root, "com", "example", "Foo.java"), true},
		{"bare-via-fq-fallback", "com.example.Bare", filepath.Join(root, "Bare.java"), true},
		{"missing", "no.such.Class", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveJavaClassToFile(tc.class, []string{root})
			if ok != tc.ok {
				t.Fatalf("ok: got %v, want %v (path=%q)", ok, tc.ok, got)
			}
			if got != tc.want {
				t.Errorf("path: got %q, want %q", got, tc.want)
			}
		})
	}
}
