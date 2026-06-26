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
