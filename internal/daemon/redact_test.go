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
