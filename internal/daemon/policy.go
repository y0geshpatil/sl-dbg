// Security policy for the daemon. Sourced from environment variables on
// daemon startup (since the daemon auto-spawns from the first `sl-dbg` call,
// there is no good place to put CLI flags until #36 lands a config file).
//
// Supported env vars (all optional; empty/unset = backward-compatible defaults):
//
//   SL_DBG_ALLOW_PROGRAM       — colon-separated glob list. When non-empty,
//                                handleStart rejects programs not matching any
//                                glob. Issue #21.
//   SL_DBG_ALLOW_SOURCE_ROOT   — colon-separated absolute path list. When
//                                non-empty, handleSource rejects files outside
//                                any allowed prefix. Issue #18.
//   SL_DBG_MAX_SESSIONS        — integer. 0 / unset = unlimited. handleStart
//                                returns RESOURCE_EXHAUSTED beyond the cap.
//                                Issue #22.
//   SL_DBG_AUDIT_LOG           — file path. When non-empty, every start /
//                                attach / eval / set / break-with-condition is
//                                appended as one NDJSON line. Issue #23.
//   SL_DBG_DENY_EVAL_PATTERNS  — colon-separated substring list. When the eval
//                                expression contains any of these, the call is
//                                rejected with EVAL_DENIED. Defaults to a
//                                hardcoded list of Java side-effect classes
//                                (FileOutputStream, ProcessBuilder, …) when
//                                the daemon sees a read-only session — issue
//                                #19. Set to "-" to disable.
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Policy captures the security knobs loaded from the environment. The zero
// value is a permissive (legacy) policy that matches pre-security-cluster
// behavior — every existing test relies on that.
type Policy struct {
	AllowProgram     []string // globs
	AllowSourceRoot  []string // absolute path prefixes (cleaned)
	MaxSessions      int      // 0 = unlimited
	AuditLogPath     string   // "" = no audit
	DenyEvalPatterns []string // substring matches against eval expression
}

// LoadPolicyFromEnv builds a Policy from the SL_DBG_* environment variables.
func LoadPolicyFromEnv() Policy {
	p := Policy{
		AllowProgram:    splitColonList(os.Getenv("SL_DBG_ALLOW_PROGRAM")),
		AllowSourceRoot: cleanAbs(splitColonList(os.Getenv("SL_DBG_ALLOW_SOURCE_ROOT"))),
		AuditLogPath:    strings.TrimSpace(os.Getenv("SL_DBG_AUDIT_LOG")),
	}
	if v := strings.TrimSpace(os.Getenv("SL_DBG_MAX_SESSIONS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			p.MaxSessions = n
		}
	}
	switch raw := os.Getenv("SL_DBG_DENY_EVAL_PATTERNS"); raw {
	case "":
		// Hardcoded defaults — these are the obvious Java side-effect classes
		// that defeat the `context: "watch"` evaluator hint. Issue #19.
		p.DenyEvalPatterns = []string{
			"FileOutputStream", "FileWriter", "RandomAccessFile",
			"ProcessBuilder", "Runtime.getRuntime", "java.lang.Runtime",
			"java.nio.file.Files", "java.io.File.delete", "createTempFile",
			"System.exit", "System.load", "URLClassLoader",
		}
	case "-":
		// Explicit opt-out.
		p.DenyEvalPatterns = nil
	default:
		p.DenyEvalPatterns = splitColonList(raw)
	}
	return p
}

func splitColonList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ":")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func cleanAbs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if abs, err := filepath.Abs(p); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	return out
}

// ProgramAllowed returns nil when program matches at least one configured
// glob (or no policy is set). Otherwise returns a USAGE_ERROR-shaped reason.
// Used by handleStart / handleAttach. Issue #21.
func (p Policy) ProgramAllowed(program string) error {
	if len(p.AllowProgram) == 0 {
		return nil
	}
	if program == "" {
		return nil // attach sessions with no program path
	}
	base := filepath.Base(program)
	for _, glob := range p.AllowProgram {
		if ok, _ := filepath.Match(glob, program); ok {
			return nil
		}
		if ok, _ := filepath.Match(glob, base); ok {
			return nil
		}
	}
	return fmt.Errorf("program %q is not in SL_DBG_ALLOW_PROGRAM allowlist", program)
}

// SourcePathAllowed returns nil when path resolves to an absolute path under
// at least one allowed root (or no roots are configured — legacy behavior).
// Rejects path-traversal attempts ("../..") because Clean+Abs normalises
// them and the resulting absolute path must still be under a root. Issue #18.
func (p Policy) SourcePathAllowed(path string) error {
	if len(p.AllowSourceRoot) == 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	abs = filepath.Clean(abs)
	for _, root := range p.AllowSourceRoot {
		if abs == root || strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("path %q is outside SL_DBG_ALLOW_SOURCE_ROOT allowlist", path)
}

// EvalAllowed returns nil when expr does not match any deny pattern. Issue
// #19. The deny set is intentionally substring-based (cheap, no parser
// dependency); false positives are acceptable for a security knob.
func (p Policy) EvalAllowed(expr string) error {
	for _, needle := range p.DenyEvalPatterns {
		if strings.Contains(expr, needle) {
			return fmt.Errorf("expression contains deny-listed token %q (set SL_DBG_DENY_EVAL_PATTERNS=- to disable)", needle)
		}
	}
	return nil
}

// AuditLogger writes one NDJSON entry per audited event. nil-safe: when
// AuditLogPath is empty the methods are no-ops. Issue #23.
type AuditLogger struct {
	mu   sync.Mutex
	file *os.File
}

func (p Policy) OpenAudit() (*AuditLogger, error) {
	if p.AuditLogPath == "" {
		return &AuditLogger{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(p.AuditLogPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p.AuditLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &AuditLogger{file: f}, nil
}

// Log appends a single NDJSON record. Best-effort: I/O errors are swallowed
// because audit failures must never break a live debugging session.
func (a *AuditLogger) Log(event string, sess string, args interface{}) {
	if a == nil || a.file == nil {
		return
	}
	rec := map[string]interface{}{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"event":   event,
		"session": sess,
		"args":    args,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line = append(line, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.file.Write(line)
}

func (a *AuditLogger) Close() {
	if a == nil || a.file == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.file.Close()
	a.file = nil
}
