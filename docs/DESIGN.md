# sl-dbg — Design Document

## 1. Goals

1. **Stateless UX** — every CLI invocation is a complete, atomic action with structured output. No persistent REPL.
2. **AI-agent-first** — JSON output, predictable exit codes, blocking semantics that always return a deterministic state.
3. **Language-agnostic** — leverage existing DAP adapters so we don't reimplement debuggers.
4. **Production-deployable** — single binary, no runtime dependency, security-conscious defaults.
5. **Human-friendly fallback** — `--pretty` renders the same output for terminal users.

## 2. Non-Goals

- Not a new debug protocol. We ride on DAP.
- Not a new debugger engine. We wrap `debugpy`, `java-debug`, `dlv`, `lldb-dap`, etc.
- Not an IDE. No source editing, no UI.
- Not a tracer/profiler. (Maybe a future sibling tool.)

## 3. Architecture

```
┌──────────────────────────────────────────────────────────┐
│  Layer 5: Consumer (AI agent, human, CI script)          │
├──────────────────────────────────────────────────────────┤
│  Layer 4: sl-dbg CLI (this project)                      │
│           - argv parsing (cobra)                         │
│           - IPC client                                   │
│           - JSON formatter                               │
├──────────────────────────────────────────────────────────┤
│  Layer 3: sl-dbgd daemon (this project)                  │
│           - Session manager                              │
│           - Event multiplexer                            │
│           - Source path resolver                         │
│           - Adapter lifecycle                            │
├──────────────────────────────────────────────────────────┤
│  Layer 2: DAP client wrapper (this project + go-dap)     │
│           - Request/response correlation                 │
│           - Event subscription                           │
│           - Capability negotiation                       │
├──────────────────────────────────────────────────────────┤
│  Layer 1: Language DAP adapters (external)               │
│           - debugpy, java-debug, dlv, lldb-dap, …        │
├──────────────────────────────────────────────────────────┤
│  Layer 0: Native debug interface (external)              │
│           - JDWP, ptrace, V8 Inspector, ICorDebug, …     │
└──────────────────────────────────────────────────────────┘
                          │
                          ▼
                   Target process
```

### 3.1 Why a Daemon?

DAP sessions are **stateful**: the adapter holds a socket to the target, breakpoints persist between commands, the program may be paused waiting for `continue`. A pure one-shot CLI cannot satisfy this without re-establishing the entire session per call (which would lose pause state).

The daemon holds:
- Open DAP adapter subprocesses
- Open connection to target
- Buffered events (`stopped`, `output`, `breakpoint`) between CLI invocations
- Session-scoped state (current frame, last evaluation context)

The CLI is **truly stateless** — every invocation is an IPC round-trip.

### 3.2 Single-Binary Daemon

The daemon is **not a separate executable**. `sl-dbg` and `sl-dbg daemon` are the same binary invoked with different subcommands. This keeps distribution simple.

The daemon is auto-spawned on first command if not running:
```
sl-dbg break app.py:42
  └─ CLI connects to socket → ECONNREFUSED
      └─ CLI forks self as `sl-dbg daemon` (detached)
          └─ Daemon listens on socket
              └─ CLI retries → success
```

### 3.3 IPC Transport

**Unix domain socket** (Linux/macOS): `$XDG_RUNTIME_DIR/sl-dbg/daemon.sock` (fallback: `/tmp/sl-dbg-$UID.sock`)
**Named pipe** (Windows): `\\.\pipe\sl-dbg-$USERNAME`

Protocol: line-delimited JSON. Each line is one request or one response. Simple, debuggable with `nc`.

```
→ {"id":1,"cmd":"break","args":{"location":"app.py:42"}}
← {"id":1,"ok":true,"data":{"breakpoint":{"id":1,"verified":true}}}
```

For long-running blocking commands (`continue`, `step`), responses may take seconds. CLI keeps the socket open until response arrives or `--timeout` fires.

## 4. Component Breakdown

### 4.1 `cmd/sl-dbg/`
The entry point. Parses os.Args, hands off to cobra command tree in `internal/cli`.

### 4.2 `internal/cli/`
Cobra-based command implementations. Each command:
1. Parses flags
2. Builds an IPC request
3. Sends to daemon (auto-spawning if needed)
4. Formats response (JSON by default, pretty if `--pretty`)
5. Exits 0 on success, 1 on debugger error, 2 on usage error, 3 on IPC error

### 4.3 `internal/daemon/`
The long-running background server.
- `server.go` — accept connections, dispatch requests
- `session.go` — `Session` struct: DAP client + adapter subprocess + state
- `events.go` — event buffer and "wait for stopped" coordinator

### 4.4 `internal/dap/`
DAP client wrapper around `github.com/google/go-dap`.
- `client.go` — JSON-RPC framing, request/response correlation
- `events.go` — event subscription, typed handlers
- `capabilities.go` — feature detection (does this adapter support conditional BPs?)

### 4.5 `internal/adapter/`
Per-language adapter registry.
- `registry.go` — map language → adapter spec
- `installer.go` — auto-bootstrap missing adapters
- `python.go`, `java.go`, `go.go`, etc. — per-language launch logic

### 4.6 `internal/session/`
Session lifecycle: create, lookup, list, dispose. Session IDs are short UUIDs (`a1b2c3`).

### 4.7 `internal/ipc/`
Unix socket / named pipe server & client. Line-delimited JSON.

### 4.8 `internal/config/`
TOML config in `~/.config/sl-dbg/config.toml`. Adapter paths, default flags, source roots.

### 4.9 `internal/logging/`
Structured logging via `zap`. Logs go to `~/.local/state/sl-dbg/daemon.log` (rotated).

### 4.10 `pkg/api/`
Public types for the JSON output schema. Kept stable across versions.

## 5. Data Flow Example: `sl-dbg break app.py:42`

```
1. CLI parses argv → BreakRequest{location:"app.py:42"}
2. CLI dials daemon socket
   - If ECONNREFUSED → fork daemon, retry up to 5x with backoff
3. CLI sends: {"id":7,"cmd":"break","args":{...}}
4. Daemon receives request, looks up "current" session
5. Daemon translates → DAP setBreakpoints request
6. Daemon sends DAP request to debugpy subprocess via stdio
7. debugpy responds with {breakpoints:[{verified:true,line:42,id:1}]}
8. Daemon wraps in envelope, sends back over socket
9. CLI prints: {"ok":true,"data":{"breakpoint":{"id":1,"verified":true,...}}}
10. CLI exits 0
```

For `sl-dbg continue`:
- Step 4–6 same.
- Step 7: DAP responds immediately to `continue` request, but daemon **does NOT** reply to CLI yet.
- Daemon waits for next `stopped` event (or `exited`/`terminated`).
- Step 8: Daemon sends final state once paused: `{stopped:"breakpoint", location:...}`.

This blocking semantics gives the agent a deterministic state after every command.

## 6. Source Path Mapping

The adapter sends `setBreakpoints` with absolute paths from the launching environment. For attach-mode debugging across hosts, paths may not match. Resolution order:

1. Path exists locally → use as-is
2. `--source-root` flags (multiple allowed) → try each as prefix replacement
3. Config file `[source]` section → glob mappings
4. Fall back to class/module name + symbol search

Each session caches resolutions. Failed resolutions return `{verified:false, reason:"source_not_found"}` rather than erroring — the agent can decide.

## 7. Blocking Command Semantics

Commands that "wait for the next pause" — `continue`, `step`, `next`, `finish`, `until`, `pause`, `run`:

- Default timeout: 30s (configurable per command and globally)
- On timeout: return `{ok:true, state:"running", reason:"timeout"}` — target is still running.
- On stop: return `{ok:true, state:"paused", reason:"breakpoint|step|exception|pause", location:{...}}`
- On exit: return `{ok:true, state:"exited", exitCode:N}`
- On crash: return `{ok:false, error:{code:"target_crashed",...}}`

Agents can poll with `sl-dbg state` (non-blocking) if they need to check without committing to a wait.

## 8. Concurrency Model

- One daemon process per user (singleton via lock file).
- Each session = one goroutine reading DAP events from its adapter.
- Per-session request mutex (DAP is sequential within a session).
- Cross-session: fully concurrent.

Events from adapters are dispatched to:
- Per-session ring buffer (last N events for `sl-dbg events`)
- Blocked-command waiter (if any)

## 9. Error Model

Error envelope:
```json
{
  "ok": false,
  "error": {
    "code": "BREAKPOINT_NOT_VERIFIED",
    "message": "Class com.example.Foo not yet loaded",
    "details": {"pending": true},
    "hint": "Run `sl-dbg continue` to let the class load, then re-check with `sl-dbg breaks`."
  }
}
```

Error codes are stable strings (UPPER_SNAKE_CASE). Documented in `pkg/api/errors.go`.

Exit codes:
| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Debugger-level error (target crashed, BP not verified, etc.) |
| 2 | Usage error (bad flags, unknown command) |
| 3 | IPC error (daemon unreachable) |
| 4 | Adapter error (failed to spawn, protocol violation) |
| 130 | Interrupted (Ctrl-C) |

## 10. Security Model

- Daemon socket is mode `0600`, owned by user.
- No network listening by default. The daemon is **not** a TCP server.
- Remote attach uses **outbound** connections (sl-dbg → remote port), not inbound.
- `--read-only` mode forbids: `setVariable`, `setExpression`, `evaluate` (configurable), `goto`, memory writes.
- `--allowlist-files` restricts which source paths can have breakpoints set (defense against agent prompt injection).
- Audit log of every command in `~/.local/state/sl-dbg/audit.log` when `--audit` is set.

See `docs/SECURITY.md` for the threat model.

## 11. Extension Points

- **New language adapter** — add a registry entry + autoinstaller in `internal/adapter/`. Zero changes to core.
- **New command** — add a cobra subcommand + IPC handler. Most commands are <50 LOC.
- **New transport** — alternate IPC (TCP, gRPC) by implementing `ipc.Transport`.
- **MCP server mode** — `sl-dbg mcp` exposes the same command set over MCP. Implemented as a thin transport on top of the daemon API.

## 12. Open Questions

| Question | Current thinking |
|---|---|
| Should daemon support multi-user via system service? | No. User-scoped daemons only. Avoids privilege issues. |
| How to handle source from JARs/wheels (no local file)? | Use DAP `source` request to fetch from adapter; cache locally. |
| Should we record/replay sessions? | Future feature; out of scope for v1. |
| How to handle multi-threaded Python (GIL releases)? | Inherit DAP semantics — adapter decides which thread is "current". |
| Time-travel debugging (`rr`)? | Phase 5+ feature. Wrap rr for native code. |
| Cross-process debugging (parent + child JVMs)? | One session per process; user starts multiple `sl-dbg attach`. |

## 13. References

- DAP spec: https://microsoft.github.io/debug-adapter-protocol/specification
- DAP overview: https://microsoft.github.io/debug-adapter-protocol/overview
- go-dap library: https://github.com/google/go-dap
- nvim-dap (reference client): https://github.com/mfussenegger/nvim-dap
- java-debug: https://github.com/microsoft/java-debug
- debugpy: https://github.com/microsoft/debugpy
- Delve: https://github.com/go-delve/delve
- lldb-dap: https://github.com/llvm/llvm-project/tree/main/lldb/tools/lldb-dap
