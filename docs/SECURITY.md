# Security considerations

A debugger can read runtime secrets, alter execution, and execute target code.
`sl-dbg` is a local, per-user debugger, not a sandbox or multi-tenant service.
Only debug programs and install adapters you trust.

## Trust boundaries

```text
MCP client -> sl-dbg mcp -> daemon (Unix socket) -> DAP adapter -> target
```

| Actor | Assumption |
|---|---|
| Local user | Owns the daemon, adapters, policy, and target process. |
| MCP client / LLM | May forward prompt-injected tool arguments; no implicit eval authority. |
| Target and adapter | Execute with the user's permissions; output may contain secrets or hostile text. |
| Other local users | Must not access the owner's daemon socket or debugging state. |

CLI and MCP requests share daemon-side checks. Inspection can still expose
credentials in variables, output, source, and stack frames. Read-only mode is
not a confidentiality boundary, and sending results to an LLM is a client decision.

## Implemented controls

The daemon listens on a local Unix-domain socket, not a network port. Its directory
is created with mode `0700`, and its socket with `0600`. See
[PLATFORMS.md](PLATFORMS.md) for runtime paths and test isolation. Windows is not
supported.

`--read-only` sessions refuse debugger mutations, including evaluation, variable
writes, breakpoints, and execution commands. DAP evaluation is not sandboxed:
the `watch` context does not prevent side effects. Disabling evaluation is a
separate daemon-wide control.

Source reads are restricted by session source roots and daemon policy. Program
allowlists and session caps reduce available operations but do not sandbox a
permitted interpreter or a launched program. They cannot protect against an
attacker already running as the same local user.

## MCP defaults and hardening

A bare `sl-dbg mcp` uses safe mode: it discovers an interpreter/program allowlist
from PATH, restricts source reads, disables evaluation, caps sessions, and enables
an audit log. Configure it explicitly for a project:

```bash
sl-dbg mcp --safe \
  --allow-program "$HOME/work/project/app.py" \
  --allow-source-root "$HOME/work/project" \
  --max-sessions 4 \
  --audit-log "$HOME/.local/state/sl-dbg/audit.log"
```

| Flag | Daemon environment | Safe-mode default |
|---|---|---|
| `--allow-program` | `SL_DBG_ALLOW_PROGRAM` | PATH-discovered java, python3, node, dlv |
| `--allow-source-root` | `SL_DBG_ALLOW_SOURCE_ROOT` | Current working directory |
| `--max-sessions` | `SL_DBG_MAX_SESSIONS` | `8` |
| `--audit-log` | `SL_DBG_AUDIT_LOG` | `$XDG_STATE_HOME/sl-dbg/audit.log`, or the user's local state directory |
| `--allow-eval` | `SL_DBG_ALLOW_EVAL` | `0` (disabled) |

The discovery list is not a promise of language support: supported adapters are
Python, Go, and Java. Program rules match the target path, not its interpreter;
an auto-discovered `python3` entry does not authorize arbitrary Python scripts.
Explicitly allow trusted target paths before launching them through MCP.
Add `--read-only` to hide mutating tools from MCP as well.
Opting into `--allow-eval` grants shell-equivalent capabilities in many targets.
Do not enable it merely to solve installation or runtime detection problems.

Policies apply when a daemon starts. Finish active sessions and stop an existing
daemon before changing its policy; then restart the MCP client. Stopping a daemon
interrupts sessions. `SL_DBG_INSECURE=1` explicitly opts out of safe defaults and
is not recommended for unattended agent use.

Audit records and daemon logs are local. Request arguments are redacted before
logging, but target output and inspection responses can contain sensitive data.
Treat logs, backups, and client transcripts accordingly.

## Installation and updates

The binary installer requires a matching release SHA-256 manifest and HTTPS.
Released Java adapter downloads require the same tag's JAR and checksum sidecar;
custom adapter URLs require an explicit expected SHA-256. Checksums detect
corruption but are not signatures or an independent guarantee against a
compromised release account. Python and Go adapters are installed through pip
and the Go toolchain respectively.

The installer does not execute Maven as a fallback for a failed released Java
download. Development builds may explicitly build the checked-out launcher.
Never use an untrusted source checkout as an installation directory.

## Remote debugging and limitations

Bind JDWP/debugpy/Delve ports to loopback and tunnel them over SSH or a trusted
port-forward. Debug protocols do not provide a general encrypted/authenticated
transport. Never expose debug ports publicly.

There is no telemetry. Config-file presets, `--audit`, source-file glob
`--allowlist-files` / `--denylist-files`, Windows ACLs, plugins, and eval sandboxing
are not implemented controls. Historical design examples are not security
guarantees. Use current `--help`, [COMMANDS.md](COMMANDS.md), and the running
server's `tools/list` for actual interfaces.

Report vulnerabilities privately using the root [security policy](../SECURITY.md).
