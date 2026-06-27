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

func TestResolveMCPSafePolicy_RefusesWithoutSafeOrInsecure(t *testing.T) {
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(nil), "/work")
	if pol.Refuse == nil {
		t.Fatalf("expected refusal when neither --safe nor SL_DBG_INSECURE is set")
	}
	if !strings.Contains(pol.Refuse.Error(), "--safe") {
		t.Errorf("refusal should mention --safe; got: %s", pol.Refuse.Error())
	}
	if !strings.Contains(pol.Refuse.Error(), "SL_DBG_INSECURE") {
		t.Errorf("refusal should mention SL_DBG_INSECURE; got: %s", pol.Refuse.Error())
	}
}

func TestResolveMCPSafePolicy_InsecureWarnsLoudly(t *testing.T) {
	env := map[string]string{"SL_DBG_INSECURE": "1"}
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(env), "/work")
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

func TestResolveMCPSafePolicy_SafeRequiresAllowProgram(t *testing.T) {
	pol := resolveMCPSafePolicy(mcpSafeFlags{Safe: true}, fakeEnv(nil), "/work")
	if pol.Refuse == nil {
		t.Fatalf("safe mode without --allow-program must refuse")
	}
	if !strings.Contains(pol.Refuse.Error(), "--allow-program") {
		t.Errorf("refusal should name --allow-program; got: %s", pol.Refuse.Error())
	}
}

func TestResolveMCPSafePolicy_SafeAppliesAllDefaults(t *testing.T) {
	cwd := "/home/me/proj"
	pol := resolveMCPSafePolicy(mcpSafeFlags{
		Safe:         true,
		AllowProgram: []string{"java", "python3"},
	}, fakeEnv(nil), cwd)
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
	}, fakeEnv(nil), "/work")
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
	// Warning should call out that eval is re-enabled.
	joined := strings.Join(pol.Warnings, "\n")
	if !strings.Contains(joined, "eval` is RE-ENABLED") {
		t.Errorf("expected loud warning when --allow-eval is set under --safe; got:\n%s", joined)
	}
	// And no defaulted-cwd warning since the user passed roots explicitly.
	if strings.Contains(joined, "defaulted to cwd") {
		t.Errorf("should not warn about defaulted cwd when --allow-source-root is set")
	}
}

func TestApplyMCPSafePolicy_WritesWarningsAndReturnsRefuse(t *testing.T) {
	var buf bytes.Buffer
	pol := resolveMCPSafePolicy(mcpSafeFlags{}, fakeEnv(nil), "/work")
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
