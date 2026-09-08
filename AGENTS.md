# AGENTS.md

> Orientation for AI coding agents (Claude Code, Copilot CLI, Cursor, Aider, …) that need to **modify this codebase**. Humans should still start with [`README.md`](README.md).
>
> If you're an agent that wants to **use** sl-dbg as a debugger tool, see [`docs/AGENT-GUIDE.md`](docs/AGENT-GUIDE.md) instead.

---

## 1. What this project is, in one paragraph

`sl-dbg` is a **stateless, JSON-first command-line debugger** written in Go. Every debugger operation (`break`, `continue`, `locals`, `eval`, …) is a separate one-shot CLI invocation that round-trips through a long-lived **per-user daemon** (`sl-dbg daemon serve`), which in turn talks **DAP** (Microsoft's Debug Adapter Protocol) to language-specific subprocess adapters (`debugpy` for Python, `dlv dap` for Go, the bundled `sl-dbg-java-adapter.jar` for Java). The same binary also runs as an **MCP server** (`sl-dbg mcp`) that exposes every command as a JSON-RPC 2.0 tool for LLM clients.

```
sl-dbg CLI  ──unix-socket──►  sl-dbgd (daemon)  ──DAP over stdio──►  adapter  ──►  target process
   │                              │
   └── sl-dbg mcp (stdio) ────────┘   (same in-process Server.handle dispatcher)
```

---

## 2. Repo layout — the only paths that matter

```
cmd/sl-dbg/                       main package, ~30 LOC; wires cobra root and dispatches `daemon serve` / `mcp` / everything else
internal/cli/                     cobra commands and the `repl`; calls go through `pkg/api` or `callRaw` → daemon over IPC
internal/daemon/                  the brains — Server.handle dispatches proto.Cmd* to handle<X>; one handler per command
  ├─ server.go                    main dispatch + ~half the handlers (start, break, eval, locals, set, snapshot, …)
  ├─ handlers_v2.go               the rest (watch, breakFn, breakEx, threads, globals, print, until, …)
  ├─ redact.go                    secret scrubbing for the request log
  └─ *_test.go                    table-driven handler tests; do NOT spawn real adapters
internal/session/                 Session struct, lifecycle, lastTerminal, event multiplexer, BP store
internal/adapter/                 per-language launch-arg builders. java.go is the only one with real logic; others are 30 LOC
internal/dap/                     thin go-dap wrapper (Continue, Next, StepIn, StepOut, Evaluate, …). Just types + sendRecv.
internal/proto/                   the WIRE schema between CLI/MCP and daemon. *Every* command has Args + Result types here.
internal/ipc/                     unix socket + pidfile + log paths
internal/mcp/                     MCP server (stdio JSON-RPC 2.0): server.go = tool registry + dispatch; composites.go = the smart composite tools
pkg/api/                          public Go SDK that wraps the daemon protocol
test/e2e/                         shell scripts (java.sh, python.sh, go.sh) driven by run-all.sh; the only end-to-end coverage we have
examples/                         tiny target programs used by e2e
docs/                             user-facing docs (DESIGN, COMMANDS, AGENT-GUIDE, …)
adapters/java-launcher/           Maven module that builds the embedded Java DAP launcher fat-jar
```

**Where to make changes** depends on the layer:

| Change | Touch these |
|---|---|
| New CLI flag / subcommand | `internal/cli/commands.go` (+ `pkg/api` if exposed) |
| New daemon command | `internal/proto/proto.go` (types + `Cmd*` const) → handler in `internal/daemon/` → CLI binding → MCP tool entry in `internal/mcp/server.go` |
| New MCP-only composite | `internal/mcp/composites.go` (use existing `Caller.Call` to chain daemon commands) |
| Per-language behavior | `internal/adapter/<lang>.go` (`BuildLaunchArgs`, `BuildAttachArgs`) and `internal/daemon/handlers_v2.go` for Java-aware shortcuts like `eval` auto-qualify |
| Wire-protocol field | `internal/proto/proto.go` only — types are shared between CLI, daemon, MCP |

---

## 3. Build, test, run — the actual commands

```bash
# Build the single binary (fast; output: bin/sl-dbg)
make build

# Install onto your PATH (uses go install → $GOPATH/bin; symlinked to /Users/yogesh/bin/sl-dbg on the maintainer's box)
make install

# Targeted unit tests (no adapters spawned)
go test ./internal/daemon/...
go test ./internal/mcp/...
go test ./...

# Full unit + e2e (requires python3 + debugpy + Java JDK on PATH; dlv optional)
make test

# Install the Java DAP adapter jar for local development.
# For end users: sl-dbg install-adapter java  (downloads jar from GitHub Releases automatically).
# For source-checkout dev: make java-adapter  (requires Maven + JDK 11+; builds the jar locally).
# Only needed when adapters/java-launcher/**/*.java changes.
make java-adapter

# Stop the running daemon (always do this between binary swaps, otherwise the old daemon serves stale code)
bin/sl-dbg daemon stop
```

**There is no separate linter target you must run.** `go vet ./...` is part of `make lint` but is not gating CI. `golangci-lint` runs if installed.

---

## 4. Conventions you MUST follow

1. **Wire schema lives in `internal/proto`.** Never define request/response types inside a handler file. Every CLI subcommand, every MCP tool, and the Go SDK all unmarshal the same struct.

2. **One handler per `proto.Cmd*` constant.** Dispatch happens in `Server.handle` (server.go). When you add a command, add a `case` there.

3. **Every response goes through `ok()` or `errResp()`** (server.go). Free-form `proto.Response{}` literals are a smell.

4. **Errors use a stable code taxonomy.** Don't invent a new code unless you must. Existing codes: `USAGE_ERROR`, `SESSION_NOT_FOUND`, `LAUNCH_FAILED`, `ADAPTER_FAILED`, `READ_ONLY_MODE`, `TIMEOUT`, `MISSING_DEBUG_INFO`, `EVAL_NO_THIS`, `EVAL_NAME_UNKNOWN`, `EVAL_SYNTAX_ERROR`, `EVAL_RUNTIME_EXCEPTION`, `EVAL_DENIED`, `EVAL_DISABLED`, `CLASS_NOT_LOADED`, `STALE_FRAME`, `VM_DISCONNECTED`, `BREAKPOINT_UNVERIFIED`, `INSPECT_NOT_PAUSED`, `PAUSE_TIMEOUT`, `PROGRAM_NOT_ALLOWED`, `SOURCE_PATH_DENIED`, `RESOURCE_EXHAUSTED`. When wrapping an adapter error, prefer `adapterErr(err, op)` (server.go) — it maps known JDI strings to actionable codes.

5. **Read-only sessions must refuse mutations.** Call `refuseIfReadOnly(sess)` at the top of any handler that changes state (set, continue, step, break*, watch add/remove, eval, …). Pure-inspection commands (locals, stack, snapshot, watches list) pass through. Note: `eval` is treated as mutating because expression-form evaluation in Python/Java/Go DAP adapters permits arbitrary side effects (process spawn, file I/O); the DAP `context: "watch"` hint is advisory only and does not sandbox the adapter. Issue #56.

6. **Never log raw `req.Args`.** Use `redactArgs(req.Args)` (daemon/redact.go). Secrets leaking into `daemon.sock.log` is a P0 — there are tests in `redact_test.go`.

7. **Sessions default to "newest started".** `session.Manager.register` promotes every new session to default. The MCP `resolveSession` and the CLI `--session ""` path both rely on this. Don't reintroduce first-wins.

8. **MCP tools auto-receive `session` as an optional arg** via `objectSchema()`. You don't need to (and shouldn't) add it manually to every property map.

9. **No `[planned]` commands in the CLI.** If it isn't implemented, don't register the cobra command. `goto` was removed for exactly this reason.

10. **Stateless invocations.** A user-visible CLI command translates to ONE daemon round-trip whenever possible. If you need multiple round-trips, add a composite (CLI side: helper function; MCP side: `composites.go`).

11. **Claim before you code.** Before opening any editor for an issue, claim it on GitHub so a parallel human or agent doesn't duplicate the work:

    ```bash
    gh issue edit <N> --add-assignee @me --add-label in-progress
    gh issue comment <N> --body "Picking this up. Agent: <your-handle>. Branch: fix/<slug>. Approach: <one line>. ETA: <today/this week/unsure>."
    ```

    If you're an AI agent, **identify yourself** in the comment (e.g. `Copilot CLI`, `claude-sonnet`, `gpt-5.3-codex` + the session ID if you have one). If you stop working before shipping, post `"stepping away, unclaiming"` and remove the assignee + label. Full SOP in [`docs/TRIAGE.md`](docs/TRIAGE.md#claim-before-work-sop--required).

    When the work ships, close with a reference: `gh issue close <N> -c "Fixed in <sha>. <what changed>. Covered by Test<Name>."` — this is the audit trail.

12. **Definition of Done — every change MUST update all four of these in the same PR:**

    | Surface touched | Required update |
    |---|---|
    | **Logic / handler** (`internal/daemon`, `internal/session`, `internal/adapter/*`, `internal/mcp/composites.go`, `internal/cli/*`) | A new or updated **unit test** under the same package's `*_test.go`. No PR ships untested logic. |
    | **CLI surface** (new flag, subcommand, JSON field) | An **e2e assertion** in `test/e2e/<lang>.sh` exercising the new path against a real adapter. |
    | **Wire protocol** (`internal/proto`) | Update **`docs/COMMANDS.md`** with the new field + schema, and bump the doc table-of-contents if the command name is new. |
    | **MCP tool** (added, renamed, schema change) | Update `tools/list` snapshot if there's one; document the tool in **`docs/AGENT-GUIDE.md`** (composites section or table). |
    | **User-visible behavior** (anything observable in `sl-dbg --help` output, JSON responses, or error codes) | Update **`README.md`** or the relevant `docs/*.md` page in the same commit. README and docs drift is treated as a bug. |
    | **New error code** | Append to the AGENTS.md §4 rule 4 list and to `docs/COMMANDS.md` error-codes section. |
    | **New convention or gotcha** discovered while debugging | Add a bullet under AGENTS.md §4 (convention) or §5 (gotcha) so the next agent doesn't trip on it. |

    A PR that adds code without tests, or changes behavior without docs, will be sent back. This is non-negotiable — it's the difference between this codebase staying maintainable and it rotting. If you can't write a test for a change, that's a signal the design is wrong; ask before working around it.

13. **Comments are sparse on purpose.** Only comment non-obvious *why*. Don't restate the code. Run-of-the-mill setters, helpers, switches stay un-commented.

14. **Test discipline.** Daemon unit tests use a fake `dap.Client` (see `internal/daemon/*_test.go`); they MUST NOT spawn a real adapter or rely on network. Adapter behavior is covered by `test/e2e/*.sh` end-to-end.

---

## 5. Common gotchas (these have bitten previous agents)

- **Don't run `pkill`, `killall`, or `kill $VAR`** in shell commands — the agent runtime blocks them. Use literal numeric PIDs: `kill 12345`. To stop the daemon, prefer `bin/sl-dbg daemon stop`.
- **Daemon is sticky.** After rebuilding, the *old* daemon keeps serving until you stop it. `make build` does NOT restart the daemon for you.
- **Isolate smoke tests.** Use a short temporary `SL_DBG_SOCKET` and temporary HOME/config paths; never stop a user's default daemon during installation validation. The e2e scripts isolate their own sockets.
- **Installer environment belongs to Bash.** In a pipeline use `curl ... | INSTALL_DIR="$HOME/bin" bash`, not `INSTALL_DIR=... curl ... | bash`. The default install location is `~/.local/bin`; no automatic sudo or daemon stop.
- **Release Java assets are a pair.** Publish `sl-dbg-java-adapter.jar` and `sl-dbg-java-adapter.jar.sha256` under the binary's version tag. The sidecar is separate from GoReleaser's archive checksum manifest; `make java-adapter` generates it.
- **Java adapter jar is cached** at `~/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar`. Edits to `adapters/java-launcher/**/*.java` require `make java-adapter` to take effect.
- **`parseLocation` distinguishes file paths from class names** by presence of `/`, `\`, or a known source extension. If your change makes `ComplexLoopDebug:14` get prepended with cwd, you've broken Java line breakpoints. There's no test for this — run the live Java e2e (`make test`) to catch it.
- **`stopOnEntry` works via `WaitForStop(ctx, 5s)`** inside `handleStart`. Don't return the launch response before the entry pause arrives, or callers will see `state="initializing"` and race.
- **Entry events can precede the launch response.** `WaitForStop` replays the current paused state with atomic waiter registration; execution/resume waiters must still wait for a new stop, not replay the previous one.
- **Java `configurationDone` resumes a suspended attach.** The first Java continue must not send a second resume after deferred configuration; it can release a class-prepare suspension before breakpoints bind. Keep launch/subsequent-continue and Python/Go paths distinct.
- **`FuncBPs()` returns a copy.** Use `UpdateFuncBP` to persist adapter IDs/verification; updating the returned slice does not update the session or the command's response.
- **Schemas with `required: ["session"]`** would break the default-session UX. `session` is always optional.
- **Bash quirk: `attach` appears in some SQL/keyword denylists** the agent runtime ships with; if a query fails, rephrase.

---

## 6. How to verify changes before declaring done

Don't ship without at least:

1. `make build && go test ./...` clean
2. `bin/sl-dbg daemon stop && make test` clean (Python + Go e2e; Java e2e if you touched anything Java)
3. For Java-touching changes: run the live smoke against `/Users/yogesh/workplace/sldbg-test/ComplexLoopDebug.java` (the maintainer's canonical reproducer):

   ```bash
   bin/sl-dbg daemon stop; sleep 1
   bin/sl-dbg start --lang java --main ComplexLoopDebug \
       --classpath /Users/yogesh/workplace/sldbg-test \
       --cwd /Users/yogesh/workplace/sldbg-test --stop-on-entry
   # → expect: state=paused, reason=entry, session=<id>
   bin/sl-dbg break ComplexLoopDebug:14
   bin/sl-dbg continue
   # → expect: location.file = /Users/yogesh/workplace/sldbg-test/ComplexLoopDebug.java
   bin/sl-dbg stack
   bin/sl-dbg stop
   ```

4. For MCP-touching changes: drive a stdio session in Python (see `/tmp/mcp_test_java.py` pattern from session checkpoints) — verify `init → start → state → stack → locals → stop` round-trips cleanly.

---

## 7. Quick anchors into important code

| Concept | File:line |
|---|---|
| CLI root + subcommand registration | `internal/cli/root.go` |
| Daemon dispatch switch (every `proto.Cmd*`) | `internal/daemon/server.go` — `Server.handle` |
| Session manager (lifecycle + default routing) | `internal/session/session.go` — `Manager.register`, `installWaiter` |
| MCP tool registry | `internal/mcp/server.go` — `toolRegistry` (~line 335+) |
| MCP composites (run_until_break, inspect_at, explain_pause, snapshot_compact) | `internal/mcp/composites.go` |
| Secret redaction | `internal/daemon/redact.go` |
| Error → code mapping | `internal/daemon/server.go` — `adapterErr` |
| Java source-path inference | `internal/adapter/java.go` — `inferJavaSourceRoots` |
| BP file/line pre-flight validation | `internal/daemon/server.go` — `handleBreak` (~line 350+) |

---

## 8. Things explicitly out of scope right now

- New language adapters beyond Python / Go / Java (Node/Rust/C++/.NET listed as "planned" — don't implement without explicit ask).
- Time-travel / reverse debugging.
- A persistent multi-user daemon. The daemon is per-`$UID` per-`$TMPDIR`; that's intentional.
- Windows. macOS + Linux only.
- Telemetry / phone-home.

Anything outside this scope: ask the maintainer (`@y0geshpatil`) first.
