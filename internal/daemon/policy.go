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
//   SL_DBG_ALLOW_EVAL          — boolean (1/true/yes). When unset or false
//                                (default), every code-evaluating command
//                                (eval, set, watch-add, break with --condition)
//                                is refused with EVAL_DISABLED. This is the
//                                only real security boundary against
//                                LLM-driven MCP callers; the daemon cannot
//                                tell CLI vs MCP traffic apart on the socket,
//                                so the knob is daemon-wide. `sl-dbg mcp
//                                --safe` keeps this unset; pass --allow-eval
//                                to opt in. Issue #53 / #54.
//   SL_DBG_DENY_EVAL_PATTERNS  — colon-separated substring list applied AFTER
//                                SL_DBG_ALLOW_EVAL=1 lets the call through.
//                                NOT a security boundary — a literal token
//                                deny-list is trivially bypassable via
//                                reflection / dunder traversal / getattr.
//                                Kept only as a best-effort CLI convenience
//                                (typo guard against common shell mistakes).
//                                Defaults to a hardcoded list of Java
//                                side-effect classes. Set to "-" to disable.
//                                Issue #19 / #54.
//   SL_DBG_INSECURE            — boolean. When set, `sl-dbg mcp` will start
//                                without --safe (legacy permissive mode).
//                                Prints a loud startup banner. Issue #53.
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
	AllowEval        bool     // SL_DBG_ALLOW_EVAL; default false = deny eval/set/conditional-bp (issue #53/#54)
	DenyEvalPatterns []string // substring matches against eval expression (NOT a security boundary)
}

// LoadPolicyFromEnv builds a Policy from the SL_DBG_* environment variables.
// Before reading the environment it sources any persisted --safe policy file
// (written by `sl-dbg mcp --safe`) so a daemon respawned by an unrelated CLI
// invocation does not silently downgrade to an empty policy. Issue #69.
func LoadPolicyFromEnv() Policy {
	loadSafePolicyFile()
	p := Policy{
		AllowProgram:    splitColonList(os.Getenv("SL_DBG_ALLOW_PROGRAM")),
		AllowSourceRoot: cleanAbs(splitColonList(os.Getenv("SL_DBG_ALLOW_SOURCE_ROOT"))),
		AuditLogPath:    strings.TrimSpace(os.Getenv("SL_DBG_AUDIT_LOG")),
		AllowEval:       parseBoolEnv(os.Getenv("SL_DBG_ALLOW_EVAL")),
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

// parseBoolEnv accepts the usual truthy spellings (1/true/yes/on, case-
// insensitive). Anything else — including empty — is false. Used by
// SL_DBG_ALLOW_EVAL: default-deny is the whole point of issue #54, so we
// deliberately do NOT treat unrecognised values as true.
func parseBoolEnv(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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
// at least one allowed root (SL_DBG_ALLOW_SOURCE_ROOT). When no roots are
// configured this returns an error — the caller (handleSource) must have
// already established the path is outside the session's own trusted roots,
// so an empty allowlist here means "deny by default". Issue #18 / #46.
// Rejects path-traversal attempts ("../..") because Clean+Abs normalises
// them and the resulting absolute path must still be under a root.
func (p Policy) SourcePathAllowed(path string) error {
	if len(p.AllowSourceRoot) == 0 {
		return fmt.Errorf("path %q is outside the session's source roots and SL_DBG_ALLOW_SOURCE_ROOT is unset", path)
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

// EvalEnabled returns nil when the daemon is configured to allow expression
// evaluation (SL_DBG_ALLOW_EVAL=1). Otherwise returns a non-nil error whose
// message is suitable for the EVAL_DISABLED response. Issue #54.
//
// This is the *real* security boundary against LLM-driven MCP callers.
// EvalAllowed (the substring deny-list) is a secondary CLI typo-guard that
// runs *after* this check passes; it is trivially bypassable and must never
// be relied upon for security.
func (p Policy) EvalEnabled() error {
	if p.AllowEval {
		return nil
	}
	return fmt.Errorf("eval is disabled for this daemon")
}

// EvalAllowed returns nil when expr does not match any deny pattern. Issue
// #19. The deny set is intentionally substring-based (cheap, no parser
// dependency) and is NOT a security boundary — it is trivially bypassable
// via reflection / dunder traversal / getattr (issue #54). It survives only
// as a typo-guard. The real gate is EvalEnabled / SL_DBG_ALLOW_EVAL.
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

// safePolicyFilePath mirrors cli.SafePolicyFilePath() — duplicated here to
// avoid a daemon→cli import cycle. Both must agree on the on-disk location.
// Issue #69.
func safePolicyFilePath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); v != "" {
		return filepath.Join(v, "sl-dbg", "safe-policy.env")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "sl-dbg", "safe-policy.env")
	}
	return filepath.Join(os.TempDir(), "sl-dbg-safe-policy.env")
}

// loadSafePolicyFile sources KEY=VALUE pairs from the persisted --safe
// policy file (if present) into the process environment, so a daemon
// respawned by an unrelated CLI invocation still honors the policy the
// operator set with `sl-dbg mcp --safe`. Pre-existing env vars take
// precedence — explicit caller intent wins over the cached file.
// Best-effort: any IO/parse error leaves env untouched. Issue #69.
func loadSafePolicyFile() {
	path := safePolicyFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := line[eq+1:]
		if !strings.HasPrefix(key, "SL_DBG_") {
			continue // refuse to source unknown keys
		}
		if _, present := os.LookupEnv(key); present {
			continue // caller env wins
		}
		_ = os.Setenv(key, val)
	}
}
