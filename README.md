[![CI](https://github.com/y0geshpatil/sl-dbg/actions/workflows/ci.yml/badge.svg)](https://github.com/y0geshpatil/sl-dbg/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-sl--dbg--site-informational)](https://y0geshpatil.github.io/sl-dbg-site/)

# sl-dbg

> **Stateless Debugger** — a command-per-invocation CLI debugger built for AI agents, scripts, and humans who live in the terminal.

`sl-dbg` is a thin, JSON-first command-line wrapper around the [Debug Adapter Protocol (DAP)](https://microsoft.github.io/debug-adapter-protocol/). Where traditional debuggers (`gdb`, `pdb`, `jdb`) drop you into an interactive REPL, `sl-dbg` exposes every debugger operation as a **standalone shell command** with structured JSON output.

```bash
sl-dbg start --lang python --program ./app.py --stop-on-entry
sl-dbg break app.py:42
sl-dbg continue          # blocks; returns JSON when paused
sl-dbg locals            # → {"user": {...}, "items": [...]}
sl-dbg step
sl-dbg stack
sl-dbg stop
```

That's it. No REPL. No protocol. Just commands.

## Status

`sl-dbg` is a single static Go binary with an embedded daemon and MCP server. macOS + Linux are first-class; Windows is out of scope.

| Language | Adapter | Install | Smoke coverage |
|---|---|---|---|
| Python | `debugpy` (Microsoft) | `sl-dbg install-adapter python` | ✅ |
| Go | `dlv dap` (Delve) | `sl-dbg install-adapter go` | ✅ |
| Java | `java-debug` (Microsoft, embedded launcher) | `sl-dbg install-adapter java` | Launch/attach and inspection; function-breakpoint limitation below |

### Why is Java different?
Microsoft's `java-debug` needs a standalone launcher outside an IDE. This
repository builds that launcher in `adapters/java-launcher`; complete releases
include `sl-dbg-java-adapter.jar` and its SHA-256 sidecar. Released binaries fetch
the adapter from their matching release; end users need JDK 11+, not Maven.
**The existing v0.5.4 release lacks the Java JAR.** Until a complete release is
published, use the [documented source build](docs/ADAPTERS.md) for Java.

## Why?

| Use case | Why sl-dbg |
|---|---|
| **AI coding agents** (Claude, Copilot, Cursor, …) | Each shell call is atomic; agent observes JSON state, decides next action, calls again. No protocol code in the agent. |
| **CI / automation scripts** | `sl-dbg snapshot > state.json` to capture runtime state. Scriptable end-to-end. |
| **Remote / containerized debugging** | `sl-dbg attach --host pod.svc --port 5005` over SSH or `kubectl port-forward`. No GUI required. |
| **Quick interactive use** | Faster than firing up an IDE for a one-off bug. |

## Features

- 🎯 **One command, one action** — fully stateless UX, JSON in & out
- 🌐 **Multi-language** — Python, Java and Go via DAP adapters
- 🔌 **Launch or attach** — start a fresh process or attach to a running one (host:port or PID)
- 🎚️ **Breakpoint controls** — line, conditional, hit-count, logpoint, function, exception (adapter-dependent)
- 🔍 **Deep inspection** — call stack, locals, globals, expression evaluation, modify variables
- 🤖 **MCP-ready** — `sl-dbg mcp` exposes all commands as MCP tools for AI agents
- 📦 **Single static binary** — no runtime dependencies for `sl-dbg` itself
- 🔒 **Explicit safety controls** — `--read-only` refuses debugger mutations; evaluation is disabled by default. Debugging is not a sandbox.

## Quick Start

### Install

macOS or Linux, amd64 or arm64. No Go compiler is needed for the binary.
The installer uses HTTPS and mandatory release SHA-256 checksums, installs to
`~/.local/bin` without sudo, and leaves shell profiles and MCP configs unchanged.
It needs Bash, curl, tar, and `sha256sum` or `shasum`.

```bash
# Install the latest published release:
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
sl-dbg version

# Pin a release (choose a version from the Releases page):
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash -s -- v0.5.4

# Choose a different user-owned directory (the variable belongs on bash, not curl):
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | INSTALL_DIR="$HOME/bin" bash
```

Persist the PATH line in your shell profile; see [platform setup](docs/PLATFORMS.md).
For inspection before execution, download `install.sh` to a file, read it, then
run `bash install.sh`. Checksums detect corrupted assets; they are not signatures
and do not independently authenticate a compromised release account.

After installing the binary, install the language adapters you actually
need (none are bundled — they live in their respective ecosystems):

```bash
sl-dbg install-adapter all              # attempts all three; errors if any installation fails
sl-dbg install-adapter python           # just debugpy
sl-dbg install-adapter go               # just dlv
sl-dbg install-adapter java             # just the embedded Java DAP launcher
```

Tagged releases (and the binaries `install.sh` pulls) are produced by
[GoReleaser](.goreleaser.yaml) via the `release` GitHub Actions workflow
on every `git tag v*` push, and attached to the [Releases tab](https://github.com/y0geshpatil/sl-dbg/releases).
See [docs/RELEASING.md](docs/RELEASING.md) for the cut-a-release playbook.

### Debug a Python script
```bash
printf 'prices = [10, 20, 25]\ntotal = sum(prices)\ndiscount = 5\nprint(total - discount)\n' > demo.py
sl-dbg install-adapter python
sl-dbg start --lang python --program ./demo.py --stop-on-entry
sl-dbg break demo.py:4
sl-dbg continue
sl-dbg locals           # total is 55, discount is 5
sl-dbg stop
```

Language runtimes are separate prerequisites: Python with `venv`, Go for Delve
and Go targets, JDK 11+ for Java. See [adapter setup](docs/ADAPTERS.md) for
requirements, isolated Python environments, detection, and troubleshooting.

### Attach to a running JVM
```bash
# Target (started normally with debug agent):
#   java -agentlib:jdwp=transport=dt_socket,server=y,address=127.0.0.1:5005 -jar app.jar

sl-dbg attach --lang java --host localhost --port 5005
sl-dbg break com.example.UserService:42
sl-dbg continue
sl-dbg locals
```

### Use from an AI agent
```bash
# Register sl-dbg with your agent (one of: claude|cursor|vscode|codex|copilot|all)
sl-dbg mcp install claude --allow-program "$PWD/demo.py"

# Or print the JSON/TOML snippet to paste yourself
sl-dbg mcp install --print

# Under the hood, agents launch this — exposes every command as an MCP tool.
# A bare `sl-dbg mcp` is secure-by-default (issue #70): auto-discovers java,
# python3, node, dlv on PATH and jails the source reader at cwd. Pass
# --allow-program explicitly to override the auto-discovered allowlist.
sl-dbg mcp --safe --allow-program "$PWD/demo.py"
```
`sl-dbg mcp install` does a safe read-merge-write with a timestamped `.bak`
backup. It refuses to overwrite an existing entry unless `--force` is passed,
and `--dry-run` shows the diff without touching disk.

The registered command runs `sl-dbg mcp --safe` by default — secure-by-default
mode (source jail on, eval off, session cap on, audit log on). The program
allowlist is auto-discovered from PATH (java, python3, node, dlv). Program rules
match the target path, not the interpreter: `python3` alone does not authorize
an arbitrary Python script. To permit the target the agent may launch
via `debug_start`, pass `--allow-program /path/to/your/program` (repeatable).
Pass `--read-only` to register the server with every mutating tool hidden,
or `--insecure` to fall back to the legacy permissive mode (not recommended).

### Upgrade or uninstall

Rerun the installer to upgrade atomically. Existing daemons and MCP clients keep
using old code: finish debug sessions, run `sl-dbg daemon stop`, then restart the
MCP client. Installation never interrupts active sessions.
`daemon stop` waits for shutdown and is a no-op when no daemon is running.

For older `/usr/local/bin` installations, either set `INSTALL_DIR=/usr/local/bin`
and run as its owner, or put `~/.local/bin` **before** `/usr/local/bin` in PATH.
Use `command -v sl-dbg` to check which copy runs. Re-register MCP clients with
`--force` when moving the binary; registrations use an absolute executable path.

```bash
# Finish sessions and stop the daemon BEFORE removing the binary:
sl-dbg daemon stop
# Remove ~/.local/bin/sl-dbg and detected MCP registrations (current workspace only):
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/uninstall.sh | bash

# Or surgically, just one agent
sl-dbg mcp uninstall claude       # also: cursor | vscode | codex | copilot | all
sl-dbg mcp uninstall all --dry-run
```

Use the same `INSTALL_DIR` override for removal. `--keep-mcp` removes only the
binary. MCP cleanup failures retain the binary and return an error. Adapters,
caches, backups, and other workspaces' registrations are not deleted.

## Platform Support

macOS is the tested development platform and Linux is supported with per-user Unix-domain sockets. Windows is not yet supported; it needs a named-pipe or Windows-native IPC implementation before support is claimed. See [docs/PLATFORMS.md](docs/PLATFORMS.md) for socket paths, install locations, shell `PATH` setup, and verification commands.

## Language-specific caveats

**Java**
- The current Java smoke observes `break-fn Buggy.compute` returning `verified:false` even after the class is loaded. Line breakpoints and inspection pass; the full Java suite is not green.
- Compile with `javac -g` to get local variables — without `-g`, `locals` returns only `arg0/arg1/…` (JDWP limitation; `sl-dbg` will print a hint when it detects this).
- `globals` returns no scope because the Java DAP doesn't expose statics as a scope. Use `sl-dbg eval ClassName.fieldName` (the `Hint` field on the response points at the current class).
- Conditional breakpoints on a `for (...)` header line fire on loop init when the loop variable isn't yet in scope. Put the breakpoint on the body line for reliable conditions.
- The `--source-root` flag is required when attaching so `sl-dbg source` can resolve files.

**Python**
- `debugpy` reports `hitBreakpointIds` as `null`; `--once` is honored via a file:line fallback (handled internally).

## Power-user features

- **`sl-dbg repl`** — interactive shell that keeps one daemon connection open across many commands (10× snappier than spawning sl-dbg per command). `exit` / Ctrl-D to leave. `!<line>` and `#` comments supported.
- **`sl-dbg print <expr> --depth N`** — recursive expansion for collections and nested objects. Use instead of `fields <ref>` when raw cells aren't useful.
  ```bash
  sl-dbg print myMap --depth 3
  sl-dbg print --ref 7 --depth 5 --max 100
  ```
- **`--name <id>`** — give a session a memorable name instead of a random hex id:
  ```bash
  sl-dbg start --name api --lang python --program ./api.py
  sl-dbg --session api break ./api.py:42
  ```
- **`--frame N`** — every inspection command (`locals`, `globals`, `eval`, `set`, `watch --add`, `print`) accepts `--frame N` to inspect a caller's frame without stepping out.
- **`events --tail N --since RFC3339`** — bounded log queries; same for `output`.
- **Pretty on TTY** — `--pretty` auto-enables when stdout is a terminal; remains plain JSON when piped.

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│  sl-dbg (thin CLI)                                       │
│  argv → IPC → JSON out                                   │
└──────────────────────┬───────────────────────────────────┘
                                              │ Unix socket
┌──────────────────────▼───────────────────────────────────┐
│  sl-dbgd (daemon, same binary)                           │
│  Session manager • Event multiplexer • Source mapping    │
└──────────────────────┬───────────────────────────────────┘
                       │ DAP (JSON-RPC over stdio)
┌──────────────────────▼───────────────────────────────────┐
│  Language DAP adapter (subprocess)                       │
│  debugpy • java-debug • dlv dap                           │
└──────────────────────┬───────────────────────────────────┘
                       │ Native debug interface
                       ▼
                  Target process
```

See [docs/DESIGN.md](docs/DESIGN.md) for the full architecture.

## Documentation

| Document | What it covers |
|---|---|
| [AGENTS.md](AGENTS.md) | Orientation for AI agents that need to *modify* this codebase |
| [docs/DESIGN.md](docs/DESIGN.md) | Architecture, components, data flow, design rationale |
| [docs/COMMANDS.md](docs/COMMANDS.md) | Full command reference & JSON schemas |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Phased build plan with milestones |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | Per-language adapter setup & auto-install |
| [docs/PLATFORMS.md](docs/PLATFORMS.md) | Platform support, install paths, socket locations, and PATH setup |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Intended config-file schema and CLI-flag equivalents |
| [docs/AGENT-GUIDE.md](docs/AGENT-GUIDE.md) | How AI agents should *use* sl-dbg as a debugger |
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model & safe-use guide |
| [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) | Development setup, code style, testing (see also root [CONTRIBUTING.md](CONTRIBUTING.md)) |
| [docs/TRIAGE.md](docs/TRIAGE.md) | Issue/PR labels, priorities, and how triage works |
| [CHANGELOG.md](CHANGELOG.md) | Release notes and version history |
| [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) | Community ground rules |
| [SECURITY.md](SECURITY.md) | How to report a vulnerability (don't file public issues) |

## Status

Pre-1.0. Python / Go / Java adapters are fully working with field-tested coverage; the wire schema (`schema:"1"`) is considered stable. See [docs/ROADMAP.md](docs/ROADMAP.md).

## Contributing

Issues and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md) for the short version and [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for the full development guide. For anything non-trivial, please read [AGENTS.md](AGENTS.md) first (definition of done, security posture, release process).

## License

Apache-2.0. See [LICENSE](LICENSE).
