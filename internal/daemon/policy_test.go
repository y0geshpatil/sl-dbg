package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyProgramAllowed(t *testing.T) {
	cases := []struct {
		name    string
		allow   []string
		program string
		wantErr bool
	}{
		{"no allowlist permits anything", nil, "/usr/local/bin/java", false},
		{"glob match on basename", []string{"java", "python3"}, "/opt/jdk/bin/java", false},
		{"glob match on full path", []string{"/opt/jdk/bin/*"}, "/opt/jdk/bin/java", false},
		{"reject when not in allowlist", []string{"java"}, "/bin/sh", true},
		{"empty program OK (attach)", []string{"java"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Policy{AllowProgram: tc.allow}
			err := p.ProgramAllowed(tc.program)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestPolicySourcePathAllowed(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "src")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	p := Policy{AllowSourceRoot: []string{tmp}}

	if err := p.SourcePathAllowed(filepath.Join(sub, "App.java")); err != nil {
		t.Errorf("file under root must be allowed, got %v", err)
	}
	if err := p.SourcePathAllowed("/etc/passwd"); err == nil {
		t.Errorf("path outside roots must be denied")
	}
	// Path traversal attempt: cleanup should normalise and reject.
	traversal := filepath.Join(tmp, "..", "..", "etc", "passwd")
	if err := p.SourcePathAllowed(traversal); err == nil {
		t.Errorf("path traversal must be denied")
	}

	// No allowlist = permissive.
	if err := (Policy{}).SourcePathAllowed("/etc/passwd"); err != nil {
		t.Errorf("empty allowlist must permit, got %v", err)
	}
}

func TestPolicyEvalEnabled(t *testing.T) {
	// Default: eval is disabled (issue #54 — secure-by-default).
	if err := (Policy{}).EvalEnabled(); err == nil {
		t.Errorf("zero-value Policy must deny eval; got nil error")
	}
	if err := (Policy{AllowEval: true}).EvalEnabled(); err != nil {
		t.Errorf("AllowEval=true must permit, got %v", err)
	}
}

func TestParseBoolEnv(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "on", " 1 "}
	falsy := []string{"", "0", "false", "no", "off", "maybe", "2", "asdf"}
	for _, s := range truthy {
		if !parseBoolEnv(s) {
			t.Errorf("parseBoolEnv(%q) = false, want true", s)
		}
	}
	for _, s := range falsy {
		if parseBoolEnv(s) {
			t.Errorf("parseBoolEnv(%q) = true, want false (default-deny)", s)
		}
	}
}

func TestLoadPolicyAllowEvalDefault(t *testing.T) {
	// Without SL_DBG_ALLOW_EVAL, AllowEval must be false (default-deny).
	t.Setenv("SL_DBG_ALLOW_EVAL", "")
	p := LoadPolicyFromEnv()
	if p.AllowEval {
		t.Errorf("default AllowEval must be false; got true")
	}
	t.Setenv("SL_DBG_ALLOW_EVAL", "1")
	p = LoadPolicyFromEnv()
	if !p.AllowEval {
		t.Errorf("SL_DBG_ALLOW_EVAL=1 must enable eval")
	}
	t.Setenv("SL_DBG_ALLOW_EVAL", "no")
	p = LoadPolicyFromEnv()
	if p.AllowEval {
		t.Errorf("SL_DBG_ALLOW_EVAL=no must disable eval")
	}
}

func TestPolicyEvalAllowed(t *testing.T) {
	p := Policy{DenyEvalPatterns: []string{"FileOutputStream", "Runtime.getRuntime"}}
	if err := p.EvalAllowed("user.name + 1"); err != nil {
		t.Errorf("benign expr must pass: %v", err)
	}
	if err := p.EvalAllowed("new FileOutputStream(\"/tmp/x\").write(65)"); err == nil {
		t.Errorf("RCE expr must be denied")
	}
	if err := p.EvalAllowed("Runtime.getRuntime().exec(\"sh\")"); err == nil {
		t.Errorf("Runtime expr must be denied")
	}

	// LoadPolicyFromEnv defaults populate the deny list.
	def := LoadPolicyFromEnv()
	found := false
	for _, n := range def.DenyEvalPatterns {
		if strings.Contains(n, "FileOutputStream") {
			found = true
		}
	}
	if !found {
		t.Errorf("default deny list should contain FileOutputStream; got %v", def.DenyEvalPatterns)
	}
}

func TestLoadPolicyFromEnv(t *testing.T) {
	t.Setenv("SL_DBG_ALLOW_PROGRAM", "java:python3")
	t.Setenv("SL_DBG_ALLOW_SOURCE_ROOT", "/tmp/work:/tmp/src")
	t.Setenv("SL_DBG_MAX_SESSIONS", "4")
	t.Setenv("SL_DBG_AUDIT_LOG", "/tmp/sl-dbg-audit.log")
	t.Setenv("SL_DBG_DENY_EVAL_PATTERNS", "-")

	p := LoadPolicyFromEnv()
	if len(p.AllowProgram) != 2 || p.AllowProgram[0] != "java" {
		t.Errorf("AllowProgram = %v", p.AllowProgram)
	}
	if len(p.AllowSourceRoot) != 2 {
		t.Errorf("AllowSourceRoot = %v", p.AllowSourceRoot)
	}
	if p.MaxSessions != 4 {
		t.Errorf("MaxSessions = %d", p.MaxSessions)
	}
	if p.AuditLogPath != "/tmp/sl-dbg-audit.log" {
		t.Errorf("AuditLogPath = %q", p.AuditLogPath)
	}
	if p.DenyEvalPatterns != nil {
		t.Errorf("DenyEvalPatterns should be nil when '-' opt-out, got %v", p.DenyEvalPatterns)
	}
}

func TestAuditLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	p := Policy{AuditLogPath: path}
	a, err := p.OpenAudit()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Log("eval", "abc123", map[string]interface{}{"expr": "x"})
	a.Log("start", "abc123", map[string]interface{}{"program": "java"})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), string(b))
	}
	if !strings.Contains(lines[0], "\"event\":\"eval\"") {
		t.Errorf("line 0 missing event: %s", lines[0])
	}

	// Nil-safe: an empty path yields a no-op logger.
	noop, err := (Policy{}).OpenAudit()
	if err != nil {
		t.Fatal(err)
	}
	noop.Log("eval", "x", nil) // must not panic
	noop.Close()
}

func TestIsPythonSpecial(t *testing.T) {
	for _, n := range []string{"special variables", "function variables", "__init__", "__class__"} {
		if !isPythonSpecial(n) {
			t.Errorf("%q should be special", n)
		}
	}
	for _, n := range []string{"x", "self", "_private", "__init", "init__"} {
		if isPythonSpecial(n) {
			t.Errorf("%q should NOT be special", n)
		}
	}
}
