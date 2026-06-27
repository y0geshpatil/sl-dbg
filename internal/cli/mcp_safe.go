package cli

// Secure-by-default plumbing for `sl-dbg mcp`. See issue #53.
//
// The MCP transport is the one place where the caller is an LLM acting on
// possibly-prompt-injected input, so the previous permissive defaults
// (`source` reads any file, `eval` runs any expression, `start` launches
// any binary) are unacceptable. This file:
//
//   - turns `--safe` into a single switch that flips every guard on,
//   - refuses to start when neither `--safe` nor SL_DBG_INSECURE=1 is set,
//   - emits a loud, attacker-pov WARN when run with SL_DBG_INSECURE=1,
//   - propagates the resolved knobs to the auto-spawned daemon via env vars
//     (the same SL_DBG_* names the daemon already reads at startup).

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// mcpSafeFlags collects every CLI knob that affects the secure-by-default
// posture. Kept as a struct so the policy resolver is unit-testable without
// going through cobra.
type mcpSafeFlags struct {
	Safe            bool
	AllowProgram    []string
	AllowSourceRoot []string
	MaxSessions     int
	AuditLog        string
	AllowEval       bool // explicit opt-in; default false in safe mode
}

// mcpSafePolicy is the resolved set of SL_DBG_* values plus the operational
// decision (allow / refuse / loud-warn) for one `sl-dbg mcp` invocation.
type mcpSafePolicy struct {
	// EnvUpdates lists the SL_DBG_* env vars to set before the daemon is
	// auto-spawned. They are *also* set in the current process via os.Setenv
	// so the auto-spawn inherits them.
	EnvUpdates map[string]string
	// Warnings are human-readable lines printed to stderr at startup.
	Warnings []string
	// Refuse, when non-nil, is a fatal error: the MCP server must NOT start.
	Refuse error
}

const safeRefusalHelp = `sl-dbg mcp refuses to start: insecure defaults are not safe for LLM clients.

The MCP transport executes commands on behalf of a model acting on possibly
prompt-injected input. The primitives exposed here (eval, source, start
--program) are RCE-grade, not debugger-grade, so default-deny is mandatory.

Re-run with one of:

  # Recommended: secure-by-default, requires you to declare what's allowed.
  sl-dbg mcp --safe --allow-program <name-or-glob> [--allow-source-root <dir>]

  # Opt-in to the legacy permissive mode (NOT for unattended agent use):
  SL_DBG_INSECURE=1 sl-dbg mcp

See docs/SECURITY.md for the full threat model.`

// resolveMCPSafePolicy decides what to do for one `sl-dbg mcp` invocation
// given the user's flags and the SL_DBG_INSECURE environment escape hatch.
//
// `getenv` is injected for testability; pass os.Getenv in production.
// `cwd` is the working directory used as the default --allow-source-root in
// safe mode when the user didn't pass one explicitly.
func resolveMCPSafePolicy(f mcpSafeFlags, getenv func(string) string, cwd string) mcpSafePolicy {
	pol := mcpSafePolicy{EnvUpdates: map[string]string{}}

	insecure := false
	switch strings.ToLower(strings.TrimSpace(getenv("SL_DBG_INSECURE"))) {
	case "1", "true", "yes", "on":
		insecure = true
	}

	if !f.Safe && !insecure {
		pol.Refuse = errors.New(safeRefusalHelp)
		return pol
	}

	if f.Safe {
		// Programs must be explicitly allowlisted. The threat model treats
		// `start --program /any/binary` as RCE, so silently defaulting it to
		// some heuristic would defeat the point of safe mode.
		if len(f.AllowProgram) == 0 {
			pol.Refuse = errors.New(
				"sl-dbg mcp --safe: --allow-program is required (e.g. --allow-program java --allow-program python3)\n" +
					"This is the program allowlist passed to the daemon as SL_DBG_ALLOW_PROGRAM.\n" +
					"It guards `debug_start` against arbitrary-binary launches by an MCP client.")
			return pol
		}
		pol.EnvUpdates["SL_DBG_ALLOW_PROGRAM"] = strings.Join(f.AllowProgram, ":")

		// Source jail. Default to caller's cwd when the user didn't pass one;
		// this matches the README's "MCP from a project root" expectation.
		roots := f.AllowSourceRoot
		if len(roots) == 0 {
			if cwd == "" {
				pol.Refuse = errors.New("sl-dbg mcp --safe: cannot determine cwd for default --allow-source-root; pass --allow-source-root <dir> explicitly")
				return pol
			}
			roots = []string{cwd}
			pol.Warnings = append(pol.Warnings,
				fmt.Sprintf("sl-dbg mcp --safe: --allow-source-root defaulted to cwd (%s); pass --allow-source-root explicitly to override.", cwd))
		}
		// Resolve every root to a cleaned absolute path before exporting,
		// so the daemon's prefix check matches what the user actually meant.
		abs := make([]string, 0, len(roots))
		for _, r := range roots {
			if a, err := filepath.Abs(r); err == nil {
				abs = append(abs, filepath.Clean(a))
			} else {
				abs = append(abs, r)
			}
		}
		pol.EnvUpdates["SL_DBG_ALLOW_SOURCE_ROOT"] = strings.Join(abs, ":")

		// Session cap. 8 matches the issue's suggested default and the
		// hardening recipe in docs/SECURITY.md.
		cap := f.MaxSessions
		if cap <= 0 {
			cap = 8
		}
		pol.EnvUpdates["SL_DBG_MAX_SESSIONS"] = strconv.Itoa(cap)

		// Audit log. Default under XDG state dir; create parent lazily in
		// daemon (policy.OpenAudit already does that).
		audit := f.AuditLog
		if audit == "" {
			audit = defaultAuditLogPath()
		}
		pol.EnvUpdates["SL_DBG_AUDIT_LOG"] = audit

		// Eval. The default in safe mode is OFF — this is the single biggest
		// reason the MCP surface is RCE-grade. The user can opt back in with
		// `--allow-eval` if they accept the risk.
		if f.AllowEval {
			pol.EnvUpdates["SL_DBG_ALLOW_EVAL"] = "1"
			pol.Warnings = append(pol.Warnings,
				"sl-dbg mcp --safe --allow-eval: `eval` is RE-ENABLED. Any expression the model emits will run in the target process.")
		} else {
			pol.EnvUpdates["SL_DBG_ALLOW_EVAL"] = "0"
		}
		return pol
	}

	// Insecure escape hatch. Print exactly which guards are off so the user
	// who opted in cannot later claim they were surprised.
	pol.Warnings = append(pol.Warnings,
		"================================================================",
		"sl-dbg mcp: SL_DBG_INSECURE=1 — running with permissive defaults.",
		"  * eval        : ALLOWED   (model can run arbitrary code)",
		"  * source read : ALLOWED   (model can read any file you can)",
		"  * start --program: ALLOWED (model can launch any binary)",
		"  * session cap : NONE",
		"  * audit log   : DISABLED",
		"This mode is intended for local CLI / single-developer use only.",
		"For LLM-driven clients, re-run with `--safe` instead.",
		"================================================================")
	return pol
}

// applyMCPSafePolicy writes warnings to `w` and exports any env updates so
// the auto-spawned daemon inherits them. Returns the Refuse error if any.
func applyMCPSafePolicy(p mcpSafePolicy, w io.Writer) error {
	for _, line := range p.Warnings {
		fmt.Fprintln(w, line)
	}
	if p.Refuse != nil {
		return p.Refuse
	}
	for k, v := range p.EnvUpdates {
		_ = os.Setenv(k, v)
	}
	return nil
}

// defaultAuditLogPath picks an OS-appropriate location for the audit log.
// Mirrors what docs/SECURITY.md's hardening recipe suggests.
func defaultAuditLogPath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); v != "" {
		return filepath.Join(v, "sl-dbg", "audit.log")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "sl-dbg", "audit.log")
	}
	return filepath.Join(os.TempDir(), "sl-dbg-audit.log")
}
