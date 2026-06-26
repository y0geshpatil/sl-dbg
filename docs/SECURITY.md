# sl-dbg — Security Considerations

A debugger is a **god-mode tool**: it can read all memory, modify any variable, and execute arbitrary code in the target. `sl-dbg` inherits this power. This document explains the threat model and safe-use guidelines.

## Threat Model

| Attacker | Capability we must defend against |
|---|---|
| Local unprivileged user | Reading another user's sl-dbg socket → debugging their processes |
| Compromised AI agent / prompt injection | Agent receives malicious instruction "delete user data via eval" |
| Hostile target process | Target manipulates debugger via crafted DAP responses (adapter bugs) |
| Network attacker | If user opens JDWP/CDP port to internet → RCE |

## Defenses

### 1. Daemon Socket Permissions
- Unix socket created with mode `0600`, owned by the user.
- Path under `$XDG_RUNTIME_DIR` (per-user tmpfs on Linux); fallback `/tmp/sl-dbg-$UID.sock` with strict permissions.
- Windows named pipe ACL restricted to current user SID.

### 2. No Inbound Network by Default
- Daemon does NOT listen on TCP. Period.
- All remote debugging is via **outbound** connections (`sl-dbg` → remote port).
- Users opening JDWP/debugpy ports to networks is **their** responsibility — but we document SSH tunneling prominently.

### 3. `--read-only` Mode
Forbids state-mutating operations:
- `setVariable`, `setExpression`
- `evaluate` with `context=repl` (configurable: block all eval, or block only `repl`)
- `goto` (changes flow)
- Memory writes
- Any logpoint that contains shell-injectable syntax

Activate per-session:
```bash
sl-dbg attach --lang java --host prod.svc --port 5005 --read-only
```

Or globally via config:
```toml
[security]
default_read_only = true
```

### 4. File Allowlist / Denylist
Restrict which source paths can have breakpoints set. Useful when an AI agent might be misled by a prompt-injected source comment.

```bash
sl-dbg start --lang python --program app.py \
  --allowlist-files "src/**/*.py" \
  --denylist-files "src/secrets/**"
```

Config:
```toml
[security]
allowlist_files = ["src/**/*.py", "tests/**/*.py"]
denylist_files = ["**/secrets/**", "**/.env*"]
```

Any `break <path>:line` outside allowlist returns `BREAKPOINT_DENIED`.

### 5. Eval Sandboxing (Best Effort)
Eval cannot be safely sandboxed — DAP `evaluate` runs in the target's full interpreter context. We can only:
- Refuse eval entirely in `--read-only` (recommended for production).
- Limit eval result size to prevent memory exhaustion (default: 1 MB).
- Truncate logpoint output (default: 4 KB per event).

**The honest truth:** if you let an LLM eval arbitrary expressions in a production process, you have given it shell-equivalent power. Don't do that.

### 6. Audit Log
Opt-in audit trail of every command:
```bash
sl-dbg --audit attach ...
```
Writes JSON Lines to `~/.local/state/sl-dbg/audit.log`:
```json
{"ts":"...","session":"a1b2","cmd":"eval","args":{"expr":"os.system('rm -rf /')"},"result":"refused:read_only"}
```

### 7. Adapter Process Hygiene
- Each adapter runs as a child of `sl-dbgd`, inherits its uid, not setuid.
- Auto-downloaded adapter binaries verified by SHA-256 against pinned checksums.
- No code execution from network-fetched artifacts beyond running the adapter itself.

### 8. No Telemetry by Default
If telemetry is added (Phase 6), it is **opt-in only**, anonymous, and never includes:
- Source code
- Variable values
- File paths
- Breakpoint conditions
- Target hostnames

Only: command name, language, sl-dbg version, anonymous install ID, success/error counts.

## Recommended Profiles

### Local Development (default)
- Read-write mode.
- No allowlist.
- No audit.
- Trust local processes.

### CI / Automation
- `--read-only` for inspection-only jobs.
- Allowlist to repo root.
- Audit to artifact storage.

### Production Attach (rare, careful)
- **Always** `--read-only`.
- Allowlist to known source paths.
- Audit log to centralized logging.
- SSH tunnel — never expose debug ports.
- Use a service account whose JVM/Python process has narrow permissions.
- Detach immediately after investigation.

## What sl-dbg WILL NOT Do

- **No remote sl-dbg-to-sl-dbg protocol.** The daemon never accepts external connections.
- **No bundled adapters from untrusted sources.** Only Microsoft / Google / LLVM / Samsung official releases.
- **No silent eval.** Every `eval` is audit-loggable.
- **No source upload to telemetry.** Source paths and contents are local-only.
- **No remote code execution of plugins.** Plugins (Phase 7) load only local files.

## What sl-dbg CANNOT Protect Against

- **A target process determined to detect/escape debugging** — DAP is cooperative; a hostile process can ptrace-deny, fork to detach, or scramble memory.
- **An adversary with shell access as the same user** — they can read the socket, read the audit log, MITM the adapter.
- **A trojaned DAP adapter** — if `debugpy` itself is malicious, sl-dbg cannot help.
- **Network attacker who can MITM the debug port** — JDWP, CDP, and Delve's network protocols are unencrypted by design. Use SSH tunnels or k8s port-forward.

## Reporting Security Issues

Please report vulnerabilities privately to `security@sl-dbg.dev` (placeholder — set up before public release). Do not file public GitHub issues for security bugs.

See `SECURITY.md` (top-level, planned) for the formal disclosure process.

---

## Threat Model

For MCP and agent-driven use, the core trust assumption is that `sl-dbg` is a local per-user debugger. It is not a multi-tenant service and it should not be exposed as a network daemon.

| Actor | Trust level | Assumption |
|---|---|---|
| Local user | Trusted | Owns the daemon, socket, audit log, target process, and policy decisions. |
| Local MCP client | Semi-trusted | Runs as the same user but may forward LLM-generated tool calls or prompt-injected arguments. |
| Remote LLM provider | Untrusted | Can suggest debugger actions but must not be trusted with implicit filesystem, process, or eval authority. |
| RAG / web / copy-paste input | Untrusted | May contain malicious instructions, paths, expressions, or launch arguments. |

```text
LLM client → MCP server (sl-dbg mcp) → daemon (Unix socket) → DAP adapter → target program
```

The highest-risk path is untrusted text becoming a debugger command that reads files, launches programs, evaluates expressions, or mutates a paused process.

## Trust Boundaries

| Boundary | What must be validated before forwarding |
|---|---|
| LLM client → MCP server | Tool names, JSON schema, argument types, missing required fields, and policy flags such as read-only mode. Treat all model-provided paths, expressions, and program names as untrusted. |
| MCP server → daemon | Session selection, command allow/deny policy, read-only mutation checks, source-root restrictions, allowed program list, request size, and per-client/session limits. |
| CLI → daemon | Same daemon-side validation as MCP. A local CLI is trusted to request work, but the daemon still owns canonical enforcement. |
| Daemon → DAP adapter | Adapter launch path, adapter arguments, environment, working directory, timeout, output size, and protocol framing. Never let adapter-specific errors bypass sl-dbg error handling. |
| DAP adapter → target program | Launch/attach policy, debug port exposure, eval/set permissions, breakpoint locations, and source lookup roots. Assume target output and DAP responses can be malformed or hostile. |
| Daemon → audit log | Redact secrets before writing, bound record size, create files with user-only permissions, and avoid logging raw request payloads. |

## Known Limitations (unfixed)

These are known residual risks or correctness gaps. Some mitigations may land in the same PR window, but operators should assume the limitation exists until the issue is closed and a release is cut.

- [#18](https://github.com/y0geshpatil/sl-dbg/issues/18): `debug_source` can read arbitrary files unless source roots are restricted. The `--allow-source-root` control is opt-in; if it is not set, source reads may still allow paths under the current working directory.
- [#19](https://github.com/y0geshpatil/sl-dbg/issues/19): `debug_eval` can trigger language side effects, including file creation or other target-process actions. Eval is not a sandbox.
- [#20](https://github.com/y0geshpatil/sl-dbg/issues/20): read-only mode still permits inspection commands such as `locals`, `globals`, `output`, and `source`, which can expose runtime secrets.
- [#21](https://github.com/y0geshpatil/sl-dbg/issues/21): without an explicit program allowlist, agent-driven `start` requests may launch unexpected local binaries.
- [#22](https://github.com/y0geshpatil/sl-dbg/issues/22): per-client and per-process limits are still being hardened; unrestricted local clients can cause resource pressure by opening sessions.
- [#23](https://github.com/y0geshpatil/sl-dbg/issues/23): audit logging is required for production accountability but may be opt-in or incomplete until fully implemented.
- [#4](https://github.com/y0geshpatil/sl-dbg/issues/4): watch expressions may not survive stop/start within the same daemon. This is primarily a correctness issue, but operators should not rely on watches as a persistent safety control.
- [#3](https://github.com/y0geshpatil/sl-dbg/issues/3): wrapper primitive rendering is a correctness/usability gap, not currently a security boundary.

## Hardening Recipe

For a production MCP deployment, prefer a narrow, read-only policy with explicit launch and source allowlists plus an audit trail:

```bash
sl-dbg mcp \
  --read-only \
  --allow-program "java" \
  --allow-program "python3" \
  --allow-source-root ~/work \
  --max-sessions 4 \
  --audit-log ~/.local/state/sl-dbg/audit.log
```

Operational notes:

- Run the MCP client, daemon, and target under the least-privileged local user that can debug the process.
- Never expose the daemon socket or MCP stdio bridge over a network service.
- Use SSH tunnels or Kubernetes port-forwarding for remote debug ports.
- Keep eval disabled or covered by read-only policy for unattended LLM workflows.
- Review the audit log after agent-driven sessions and rotate it with user-only permissions.
