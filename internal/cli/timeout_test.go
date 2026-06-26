package cli

import "testing"

func TestParseTimeoutFlag(t *testing.T) {
	cases := []struct {
		in   string
		def  float64
		want float64
		err  bool
	}{
		{"", 7, 7, false},
		{"30s", 0, 30, false},
		{"1m", 0, 60, false},
		{"500ms", 0, 0.5, false},
		{"30", 0, 30, false},
		{"1.5", 0, 1.5, false},
		{"bogus", 0, 0, true},
	}
	for _, c := range cases {
		got, err := parseTimeoutFlag(c.in, c.def)
		if (err != nil) != c.err {
			t.Errorf("%q err=%v want err=%v", c.in, err, c.err)
		}
		if !c.err && got != c.want {
			t.Errorf("%q got %v want %v", c.in, got, c.want)
		}
	}
}
