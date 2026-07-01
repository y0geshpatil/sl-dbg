[![CI](https://github.com/y0geshpatil/sl-dbg/actions/workflows/ci.yml/badge.svg)](https://github.com/y0geshpatil/sl-dbg/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Docs](https://img.shields.io/badge/docs-sl--dbg--site-informational)](https://y0geshpatil.github.io/sl-dbg-site/)

# sl-dbg

> **Stateless Debugger** — a command-per-invocation CLI debugger built for AI agents, scripts, and humans who live in the terminal.

`sl-dbg` is a thin, JSON-first command-line wrapper around the [Debug Adapter Protocol (DAP)](https://microsoft.github.io/debug-adapter-protocol/). Where traditional debuggers (`gdb`, `pdb`, `jdb`) drop you into an interactive REPL, `sl-dbg` exposes every debugger operation as a **standalone shell command** with structured JSON output.

```bash
sl-dbg start --lang python --program ./app.py
sl-dbg break app.py:42 --if "user.id == 5"
sl-dbg continue          # blocks; returns JSON when paused
sl-dbg locals            # → {"user": {...}, "items": [...]}
sl-dbg eval "len(items)" # → {"result": 4, "type": "int"}
sl-dbg step
sl-dbg stack
sl-dbg stop
```

That's it. No REPL. No protocol. Just commands.

## Status

`sl-dbg` is a single static Go binary with an embedded daemon and MCP server. macOS + Linux are first-class; Windows is out of scope.

| Language | Adapter | Install | E2E verified |
|---|---|---|---|
| Python | `debugpy` (Microsoft) | `pip install --user debugpy` | ✅ |
| Go | `dlv dap` (Delve) | `go install github.com/go-delve/delve/cmd/dlv@latest` | ✅ |
| Java | `java-debug` (Microsoft, embedded launcher) | `sl-dbg install-adapter java` | ✅ |
| Node.js, Rust, C/C++, .NET | planned | each lands via `sl-dbg install-adapter <lang>` | ⏳ |

### Why is Java different?
Of all mainstream languages, Java is the **only** one whose official DAP adapter (Microsoft's `java-debug`) does not ship as a standalone runnable. It's an OSGi bundle meant to be loaded inside Eclipse JDT-LS. The clean fix — implemented like every other professional DAP tool (CodeLLDB, netcoredbg, Delve) — is for the `sl-dbg` project to publish a small Java launcher fat-jar via GitHub Releases, fetched on demand by `sl-dbg install-adapter java`. That work is tracked in `ROADMAP.md`. No user-facing build-from-source step.

## Why?

| Use case | Why sl-dbg |
|---|---|
| **AI coding agents** (Claude, Copilot, Cursor, …) | Each shell call is atomic; agent observes JSON state, decides next action, calls again. No protocol code in the agent. |
| **CI / automation scripts** | `sl-dbg snapshot > state.json` to capture runtime state. Scriptable end-to-end. |
| **Remote / containerized debugging** | `sl-dbg attach --host pod.svc --port 5005` over SSH or `kubectl port-forward`. No GUI required. |
| **Quick interactive use** | Faster than firing up an IDE for a one-off bug. |

## Features

- 🎯 **One command, one action** — fully stateless UX, JSON in & out
- 🌐 **Multi-language** — Python, Java, Go, Node.js, C/C++, Rust, .NET (via DAP adapters)
- 🔌 **Launch or attach** — start a fresh process or attach to a running one (host:port or PID)
- 🎚️ **Full breakpoint suite** — line, conditional, hit-count, logpoint, function, exception, data
- 🔍 **Deep inspection** — call stack, locals, globals, expression evaluation, modify variables
- 🤖 **MCP-ready** — `sl-dbg mcp` exposes all commands as MCP tools for AI agents
- 📦 **Single static binary** — no runtime dependencies for `sl-dbg` itself
- 🔒 **Production-safe** — `--read-only` mode forbids state mutation; SSH-tunnel friendly

## Quick Start

### Install

Pick the method that fits your environment. Release artifacts are
attached to every tag on the [GitHub Releases tab](https://github.com/y0geshpatil/sl-dbg/releases) — no authentication required.

```bash
# 1. Universal curl one-liner (recommended — no Go toolchain needed):
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash

# 2. Pin a specific version:
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash -s -- v0.1.0

# 3. Install to a per-user dir (no sudo):
INSTALL_DIR=$HOME/.local/bin \
  curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash

# 4. Direct download — grab any .tar.gz from:
#   https://github.com/y0geshpatil/sl-dbg/releases
# Then: tar -xzf sl-dbg_*.tar.gz && mv sl-dbg /usr/local/bin/
```

After installing the binary, install the language adapters you actually
need (none are bundled — they live in their respective ecosystems):

```bash
sl-dbg install-adapter all              # python (debugpy) + go (dlv) + java (launcher jar)
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
sl-dbg start --lang python --program ./app.py
# → {"ok":true,"session":"a1b2","state":"paused","location":{"file":"app.py","line":1}}

sl-dbg break app.py:42
sl-dbg continue
sl-dbg locals
sl-dbg eval "user.name"
sl-dbg stop
```

### Attach to a running JVM
```bash
# Target (started normally with debug agent):
#   java -agentlib:jdwp=transport=dt_socket,server=y,address=*:5005 -jar app.jar

sl-dbg attach --lang java --host localhost --port 5005
sl-dbg break com.example.UserService:42 --if "user.id < 0"
sl-dbg continue
sl-dbg locals
```

### Use from an AI agent
```bash
# Register sl-dbg with your agent (one of: claude|cursor|vscode|codex|copilot|all)
sl-dbg mcp install claude

# Or print the JSON/TOML snippet to paste yourself
sl-dbg mcp install --print

# Under the hood, agents launch this — exposes every command as an MCP tool.
# A bare `sl-dbg mcp` is secure-by-default (issue #70): auto-discovers java,
# python3, node, dlv on PATH and jails the source reader at cwd. Pass
# --allow-program explicitly to override the auto-discovered allowlist.
sl-dbg mcp --safe --allow-program java --allow-program python3
```
`sl-dbg mcp install` does a safe read-merge-write with a timestamped `.bak`
backup. It refuses to overwrite an existing entry unless `--force` is passed,
and `--dry-run` shows the diff without touching disk.

The registered command runs `sl-dbg mcp --safe` by default — secure-by-default
mode (source jail on, eval off, session cap on, audit log on). The program
allowlist is auto-discovered from PATH (java, python3, node, dlv), so common
debugging workflows just work. To restrict which binaries the agent may launch
via `debug_start`, pass `--allow-program /path/to/your/program` (repeatable).
Pass `--read-only` to register the server with every mutating tool hidden,
or `--insecure` to fall back to the legacy permissive mode (not recommended).

### Uninstall
```bash
# Remove the binary AND the sl-dbg entry from every detected agent's MCP config
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/uninstall.sh | bash

# Or surgically, just one agent
sl-dbg mcp uninstall claude       # also: cursor | vscode | codex | copilot | all
sl-dbg mcp uninstall all --dry-run
```


## Platform Support

macOS is the tested development platform and Linux is supported with per-user Unix-domain sockets. Windows is not yet supported; it needs a named-pipe or Windows-native IPC implementation before support is claimed. See [docs/PLATFORMS.md](docs/PLATFORMS.md) for socket paths, install locations, shell `PATH` setup, and verification commands.

## Language-specific caveats

**Java**
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
                       │ Unix socket / named pipe
┌──────────────────────▼───────────────────────────────────┐
│  sl-dbgd (daemon, same binary)                           │
│  Session manager • Event multiplexer • Source mapping    │
└──────────────────────┬───────────────────────────────────┘
                       │ DAP (JSON-RPC over stdio)
┌──────────────────────▼───────────────────────────────────┐
│  Language DAP adapter (subprocess)                       │
│  debugpy • java-debug • dlv dap • lldb-dap • netcoredbg  │
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
