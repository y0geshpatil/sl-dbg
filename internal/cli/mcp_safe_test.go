package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv returns a getenv-like closure for the given key/value map.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// fakeLookpath returns a LookPath-like closure: keys map to absolute paths;
// missing keys return an error so the tested code knows the binary is absent.
func fakeLookpath(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
}

// noLookpath simulates an empty PATH (nothing discoverable).
func noLookpath(string) (string, error) { return "", os.ErrNotExist }

func TestResolveMCPSafePolicy_AutoSafeWhenNoFlags(t *testing.T) {
	// Issue #70: bare `sl-dbg mcp` with no flags and no SL_DBG_INSECURE must
	// auto-apply --safe, discovering interpreters from PATH.
	lp := fakeLookpath(map[string]string{"java": "/usr/bin/java", "python3": "/usr/bin/python3"})
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(nil), lp, "/work")
	if pol.Refuse != nil {
		t.Fatalf("auto-safe must not refuse when interpreters exist on PATH: %v", pol.Refuse)
	}
	if got := pol.EnvUpdates["SL_DBG_ALLOW_PROGRAM"]; got == "" || !strings.Contains(got, "java") {
		t.Errorf("expected auto-discovered SL_DBG_ALLOW_PROGRAM to include java; got %q", got)
	}
	joined := strings.Join(pol.Warnings, "\n")
	if !strings.Contains(joined, "--safe is now the default") {
		t.Errorf("auto-safe must announce the implicit flip; got:\n%s", joined)
	}
	if !strings.Contains(joined, "auto-discovered") {
		t.Errorf("auto-safe must announce the auto-discovered program list; got:\n%s", joined)
	}
}

func TestResolveMCPSafePolicy_AutoSafeRefusesWhenNoInterpretersFound(t *testing.T) {
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(nil), noLookpath, "/work")
	if pol.Refuse == nil {
		t.Fatalf("auto-safe with empty PATH must refuse with an actionable message")
	}
	if !strings.Contains(pol.Refuse.Error(), "--allow-program") {
		t.Errorf("refusal should name --allow-program; got: %s", pol.Refuse.Error())
	}
}

func TestResolveMCPSafePolicy_InsecureWarnsLoudly(t *testing.T) {
	env := map[string]string{"SL_DBG_INSECURE": "1"}
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(env), noLookpath, "/work")
	if pol.Refuse != nil {
		t.Fatalf("insecure escape hatch should not refuse: %v", pol.Refuse)
	}
	joined := strings.Join(pol.Warnings, "\n")
	for _, want := range []string{"SL_DBG_INSECURE", "eval", "source", "start --program"} {
		if !strings.Contains(joined, want) {
			t.Errorf("insecure warning missing %q; got:\n%s", want, joined)
		}
	}
	if len(pol.EnvUpdates) != 0 {
		t.Errorf("insecure mode should not export SL_DBG_* env vars; got %v", pol.EnvUpdates)
	}
}

func TestResolveMCPSafePolicy_SafeAutoDiscoversAllowProgram(t *testing.T) {
	// Explicit --safe without --allow-program: same auto-discovery path,
	// but no "default" banner (only the discovered-programs warning).
	lp := fakeLookpath(map[string]string{"node": "/usr/local/bin/node"})
	pol := resolveMCPSafePolicy(mcpSafeFlags{Safe: true}, fakeEnv(nil), lp, "/work")
	if pol.Refuse != nil {
		t.Fatalf("--safe with PATH-discoverable interpreter must succeed: %v", pol.Refuse)
	}
	if pol.EnvUpdates["SL_DBG_ALLOW_PROGRAM"] == "" {
		t.Errorf("expected auto-discovery to populate SL_DBG_ALLOW_PROGRAM")
	}
	if strings.Contains(strings.Join(pol.Warnings, "\n"), "is now the default") {
		t.Errorf("explicit --safe should NOT print the auto-safe banner")
	}
}

func TestResolveMCPSafePolicy_SafeAppliesAllDefaults(t *testing.T) {
	cwd := "/home/me/proj"
	pol := resolveMCPSafePolicy(mcpSafeFlags{
		Safe:         true,
		AllowProgram: []string{"java", "python3"},
	}, fakeEnv(nil), noLookpath, cwd)
	if pol.Refuse != nil {
		t.Fatalf("safe mode with --allow-program should succeed: %v", pol.Refuse)
	}
	e := pol.EnvUpdates
	if e["SL_DBG_ALLOW_PROGRAM"] != "java:python3" {
		t.Errorf("SL_DBG_ALLOW_PROGRAM=%q", e["SL_DBG_ALLOW_PROGRAM"])
	}
	if e["SL_DBG_ALLOW_SOURCE_ROOT"] != filepath.Clean(cwd) {
		t.Errorf("SL_DBG_ALLOW_SOURCE_ROOT=%q want %q", e["SL_DBG_ALLOW_SOURCE_ROOT"], cwd)
	}
	if e["SL_DBG_MAX_SESSIONS"] != "8" {
		t.Errorf("SL_DBG_MAX_SESSIONS=%q want 8", e["SL_DBG_MAX_SESSIONS"])
	}
	if e["SL_DBG_ALLOW_EVAL"] != "0" {
		t.Errorf("SL_DBG_ALLOW_EVAL=%q want 0 (eval must be OFF in safe mode)", e["SL_DBG_ALLOW_EVAL"])
	}
	if e["SL_DBG_AUDIT_LOG"] == "" {
		t.Errorf("SL_DBG_AUDIT_LOG must default to non-empty path in safe mode")
	}
	// Warning about defaulted source root must be present.
	joined := strings.Join(pol.Warnings, "\n")
	if !strings.Contains(joined, "--allow-source-root defaulted to cwd") {
		t.Errorf("expected warning about defaulted source root; got:\n%s", joined)
	}
}

func TestResolveMCPSafePolicy_SafeHonoursExplicitFlags(t *testing.T) {
	pol := resolveMCPSafePolicy(mcpSafeFlags{
		Safe:            true,
		AllowProgram:    []string{"java"},
		AllowSourceRoot: []string{"/srv/a", "/srv/b"},
		MaxSessions:     4,
		AuditLog:        "/var/log/sl-dbg.log",
		AllowEval:       true,
	}, fakeEnv(nil), noLookpath, "/work")
	if pol.Refuse != nil {
		t.Fatalf("unexpected refusal: %v", pol.Refuse)
	}
	e := pol.EnvUpdates
	if e["SL_DBG_ALLOW_SOURCE_ROOT"] != "/srv/a:/srv/b" {
		t.Errorf("source roots not propagated: %q", e["SL_DBG_ALLOW_SOURCE_ROOT"])
	}
	if e["SL_DBG_MAX_SESSIONS"] != "4" {
		t.Errorf("max sessions not propagated: %q", e["SL_DBG_MAX_SESSIONS"])
	}
	if e["SL_DBG_AUDIT_LOG"] != "/var/log/sl-dbg.log" {
		t.Errorf("audit log not propagated: %q", e["SL_DBG_AUDIT_LOG"])
	}
	if e["SL_DBG_ALLOW_EVAL"] != "1" {
		t.Errorf("--allow-eval should set SL_DBG_ALLOW_EVAL=1; got %q", e["SL_DBG_ALLOW_EVAL"])
	}
	joined := strings.Join(pol.Warnings, "\n")
	if !strings.Contains(joined, "eval` is RE-ENABLED") {
		t.Errorf("expected loud warning when --allow-eval is set under --safe; got:\n%s", joined)
	}
	if strings.Contains(joined, "defaulted to cwd") {
		t.Errorf("should not warn about defaulted cwd when --allow-source-root is set")
	}
}

func TestApplyMCPSafePolicy_WritesWarningsAndReturnsRefuse(t *testing.T) {
	var buf bytes.Buffer
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(nil), noLookpath, "/work")
	if err := applyMCPSafePolicy(pol, &buf); err == nil {
		t.Fatalf("expected error from applyMCPSafePolicy with refusal")
	}
}

func TestApplyMCPSafePolicy_SetsEnvVars(t *testing.T) {
	// t.Setenv ensures the env mutation is reverted after the test.
	t.Setenv("SL_DBG_ALLOW_PROGRAM", "")
	t.Setenv("SL_DBG_ALLOW_EVAL", "")
	pol := mcpSafePolicy{
		EnvUpdates: map[string]string{
			"SL_DBG_ALLOW_PROGRAM": "java",
			"SL_DBG_ALLOW_EVAL":    "0",
		},
	}
	var buf bytes.Buffer
	if err := applyMCPSafePolicy(pol, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v := os.Getenv("SL_DBG_ALLOW_PROGRAM"); v != "java" {
		t.Errorf("SL_DBG_ALLOW_PROGRAM=%q want java", v)
	}
	if v := os.Getenv("SL_DBG_ALLOW_EVAL"); v != "0" {
		t.Errorf("SL_DBG_ALLOW_EVAL=%q want 0", v)
	}
}
