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

## Status (v0.1)

| Language | Adapter | Install (smooth) | E2E verified |
|---|---|---|---|
| Python | `debugpy` (Microsoft) | `pip install --user debugpy` | ✅ |
| Go | `dlv dap` (Delve) | `go install github.com/go-delve/delve/cmd/dlv@latest` | ✅ |
| Java | `java-debug` (Microsoft, embedded) | `sl-dbg install-adapter java` — builds the bundled launcher fat-jar | ✅ |
| Node.js, Rust, C/C++, .NET | planned | each lands via `sl-dbg install-adapter <lang>` downloading a published binary | ⏳ |

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
```bash
# From a source checkout — one command, sets up CLI + every adapter:
git clone <repo> sl-dbg && cd sl-dbg
make setup            # builds ./bin/sl-dbg AND installs python/go/java adapters

# Or install adapters individually any time:
./bin/sl-dbg install-adapter all              # python (debugpy) + go (dlv) + java (launcher jar)
./bin/sl-dbg install-adapter python           # just debugpy
./bin/sl-dbg install-adapter go               # just dlv
./bin/sl-dbg install-adapter java             # just the embedded Java DAP launcher

# Future, once we publish releases:
brew install sl-dbg                           # macOS / Linux (planned)
curl -sSL https://sl-dbg.dev/install.sh | sh  # universal (planned)
```

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
# Expose sl-dbg as MCP tools (planned)
sl-dbg mcp
# Now any MCP-compatible agent (Claude Desktop, Cursor, Copilot CLI…)
# can call set_breakpoint, continue, get_variables, evaluate, etc.
```

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
| [docs/DESIGN.md](docs/DESIGN.md) | Architecture, components, data flow, design rationale |
| [docs/COMMANDS.md](docs/COMMANDS.md) | Full command reference & JSON schemas |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Phased build plan with milestones |
| [docs/ADAPTERS.md](docs/ADAPTERS.md) | Per-language adapter setup & auto-install |
| [docs/AGENT-GUIDE.md](docs/AGENT-GUIDE.md) | How AI agents should use sl-dbg |
| [docs/SECURITY.md](docs/SECURITY.md) | Threat model & safe-use guide |
| [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) | Development setup, code style, testing |

## Status

🚧 **Pre-alpha.** Active scaffolding. See [ROADMAP.md](docs/ROADMAP.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
