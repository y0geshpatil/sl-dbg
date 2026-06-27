package daemon

import "testing"

func TestNormalizeFuncBPName(t *testing.T) {
	cases := []struct {
		name string
		lang string
		in   string
		want string
	}{
		{"java-bare-class-method", "java", "Buggy.fib", "Buggy#fib"},
		{"java-fq-class-method", "java", "com.example.Foo.bar", "com.example.Foo#bar"},
		{"java-already-hash", "java", "Buggy#fib", "Buggy#fib"},
		{"java-fq-already-hash", "java", "com.example.Foo#bar", "com.example.Foo#bar"},
		{"java-strips-parens", "java", "Buggy.fib(int)", "Buggy#fib"},
		{"java-strips-parens-with-hash", "java", "com.example.Foo#bar(int,int)", "com.example.Foo#bar"},
		{"java-method-only", "java", "fib", "fib"}, // no class qualifier: passes through unchanged and will remain unverified at the adapter — Java requires Class#method
		{"java-empty", "java", "", ""},
		{"python-untouched", "python", "mymodule.compute", "mymodule.compute"},
		{"go-untouched", "go", "main.Compute", "main.Compute"},
		{"unknown-lang-untouched", "rust", "foo::bar", "foo::bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFuncBPName(tc.lang, tc.in); got != tc.want {
				t.Fatalf("normalizeFuncBPName(%q, %q) = %q; want %q", tc.lang, tc.in, got, tc.want)
			}
		})
	}
}
