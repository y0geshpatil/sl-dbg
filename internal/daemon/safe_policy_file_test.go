package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSafePolicyFileSurvivesRespawn reproduces issue #69:
//
//	t=0   `sl-dbg mcp --safe --allow-program java ...` writes safe-policy.env
//	t=N   the safe daemon dies (crash / signal)
//	t=N+ε an unrelated CLI invocation auto-spawns a replacement daemon.
//	      Its process env has NO SL_DBG_* vars, so LoadPolicyFromEnv would
//	      previously return an empty (insecure) policy — silently undoing the
//	      operator's --safe guarantee.
//
// With the fix, LoadPolicyFromEnv sources the persisted env-file before
// reading the environment, so the replacement daemon comes up with the same
// safe policy. Caller-provided env still wins (lets tests / explicit
// invocations override).
func TestSafePolicyFileSurvivesRespawn(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	// Scrub every SL_DBG_* var the test cares about so the "respawned
	// daemon has no env" precondition is real.
	for _, k := range []string{
		"SL_DBG_ALLOW_PROGRAM", "SL_DBG_ALLOW_SOURCE_ROOT",
		"SL_DBG_MAX_SESSIONS", "SL_DBG_AUDIT_LOG", "SL_DBG_ALLOW_EVAL",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	// Sanity: with no file and no env, policy is empty.
	if p := LoadPolicyFromEnv(); len(p.AllowProgram) != 0 || p.AllowEval {
		t.Fatalf("baseline must be empty policy; got %+v", p)
	}

	// Simulate `sl-dbg mcp --safe --allow-program java --max-sessions 8` by
	// writing the file the CLI would write.
	dir := filepath.Join(state, "sl-dbg")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "SL_DBG_ALLOW_PROGRAM=java\n" +
		"SL_DBG_MAX_SESSIONS=8\n" +
		"SL_DBG_ALLOW_EVAL=0\n" +
		"SL_DBG_AUDIT_LOG=/tmp/sl-dbg-audit.log\n"
	if err := os.WriteFile(filepath.Join(dir, "safe-policy.env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// "Respawned daemon" reload: env still empty, file present.
	p := LoadPolicyFromEnv()
	if len(p.AllowProgram) != 1 || p.AllowProgram[0] != "java" {
		t.Errorf("AllowProgram not recovered from file: %v", p.AllowProgram)
	}
	if p.MaxSessions != 8 {
		t.Errorf("MaxSessions not recovered: got %d, want 8", p.MaxSessions)
	}
	if p.AllowEval {
		t.Errorf("AllowEval must remain false")
	}
	if p.AuditLogPath != "/tmp/sl-dbg-audit.log" {
		t.Errorf("AuditLogPath not recovered: %q", p.AuditLogPath)
	}

	// Caller env wins over the file (explicit override stays explicit).
	t.Setenv("SL_DBG_ALLOW_PROGRAM", "python3")
	q := LoadPolicyFromEnv()
	if len(q.AllowProgram) != 1 || q.AllowProgram[0] != "python3" {
		t.Errorf("explicit env must override file; got %v", q.AllowProgram)
	}

	// Non-SL_DBG_ keys in the file are ignored (defence against malicious file).
	os.Unsetenv("SL_DBG_ALLOW_PROGRAM")
	bad := body + "PATH=/tmp/evil:/usr/bin\n" + "MALICIOUS=x\n"
	if err := os.WriteFile(filepath.Join(dir, "safe-policy.env"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	prevPath := os.Getenv("PATH")
	_ = LoadPolicyFromEnv()
	if got := os.Getenv("PATH"); got != prevPath {
		t.Errorf("safe-policy.env was allowed to mutate PATH: was %q now %q", prevPath, got)
	}
}
