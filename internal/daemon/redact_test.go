package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactEnvList(t *testing.T) {
	raw := json.RawMessage(`{"lang":"python","program":"app.py","env":["FOO=bar","PASSWORD=hunter2","API_KEY=sk-abc123"]}`)
	got := redactArgs(raw)
	for _, leak := range []string{"hunter2", "sk-abc123"} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted output leaks %q: %s", leak, got)
		}
	}
	if !strings.Contains(got, "FOO=bar") {
		t.Errorf("non-sensitive env entries should survive: %s", got)
	}
}

func TestRedactMapPasswordKey(t *testing.T) {
	raw := json.RawMessage(`{"password":"hunter2","token":"ghp_xxx","user":"alice"}`)
	got := redactArgs(raw)
	if strings.Contains(got, "hunter2") || strings.Contains(got, "ghp_xxx") {
		t.Errorf("password/token leaked: %s", got)
	}
	if !strings.Contains(got, "alice") {
		t.Errorf("non-secret field should survive: %s", got)
	}
}

func TestRedactValuePatternInArgs(t *testing.T) {
	raw := json.RawMessage(`{"args":["--token=sk-abcdefgh123456789","--debug"]}`)
	got := redactArgs(raw)
	if strings.Contains(got, "sk-abcdefgh123456789") {
		t.Errorf("token-like arg should be redacted: %s", got)
	}
}

func TestRedactEmptyAndBinary(t *testing.T) {
	if redactArgs(nil) != "" {
		t.Errorf("empty args should render as empty string")
	}
	if redactArgs(json.RawMessage(`not json`)) != "<unparseable>" {
		t.Errorf("unparseable args should be flagged")
	}
}

// --- adapterErr / cleanAdapterMessage tests (issue #7) ---

func TestCleanAdapterMessageStripsDapPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"dap error: Cannot evaluate because of java.lang.RuntimeException: / by zero.", "/ by zero"},
		{"dap error: Cannot evaluate because of java.lang.NullPointerException: Cannot access field of primitive type: null.", "Cannot access field of primitive type: null"},
		{"Name unknown: globalCounter", "Name unknown: globalCounter"},
		{"plain message", "plain message"},
	}
	for _, c := range cases {
		got := cleanAdapterMessage(c.in)
		if got != c.want {
			t.Errorf("cleanAdapterMessage(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAdapterErrTaxonomy(t *testing.T) {
	cases := []struct {
		err  string
		code string
	}{
		{"dap error: Cannot evaluate because of java.lang.ArithmeticException: / by zero", "EVAL_RUNTIME_EXCEPTION"},
		{"java.lang.NullPointerException: Cannot access field of primitive type: null", "EVAL_RUNTIME_EXCEPTION"},
		{"java.lang.ClassCastException: cannot cast X to Y", "EVAL_RUNTIME_EXCEPTION"},
		{"Cannot find symbol: foo", "EVAL_SYNTAX_ERROR"},
		{"unrelated random thing", "ADAPTER_FAILED"},
	}
	for _, c := range cases {
		resp := adapterErr(errFromString(c.err), "eval")
		if resp.Error == nil || resp.Error.Code != c.code {
			t.Errorf("adapterErr(%q): code=%v, want %s", c.err, resp.Error, c.code)
		}
	}
}

type stringErr string

func (s stringErr) Error() string { return string(s) }

func errFromString(s string) error { return stringErr(s) }
