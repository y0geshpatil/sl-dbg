# sl-dbg — Roadmap

This roadmap is **phased and incremental**. Each phase produces a working tool; later phases extend rather than rewrite.

## Phase 0 — Scaffolding ✅ (current)

**Goal:** A buildable Go project skeleton with all docs, command tree, and core abstractions in place.

- [x] Project layout (`cmd/`, `internal/`, `pkg/`, `docs/`, `test/`)
- [x] `Makefile`, `go.mod`, `.gitignore`, `LICENSE`
- [x] README, DESIGN, COMMANDS, ROADMAP, AGENT-GUIDE, ADAPTERS, SECURITY
- [x] Cobra command tree (all commands stubbed)
- [x] `sl-dbg version`, `sl-dbg help` working
- [x] CI config (GitHub Actions, golangci-lint)

**Exit criteria:** `make build && ./bin/sl-dbg version` prints version JSON.

---

## Phase 1 — Python MVP (debugpy) ✅ DONE

**Goal:** Debug a Python program end-to-end with the core commands.

- [x] DAP client wrapper (`internal/dap`) over `github.com/google/go-dap`
- [x] Daemon (`sl-dbg daemon serve`) with Unix-socket IPC, auto-spawned by CLI
- [x] Adapter registry with python entry (debugpy)
- [x] Commands: `start`, `attach`, `break --if`, `breaks`, `unbreak`, `continue`,
      `step`, `next`, `finish`, `pause`, `state`, `stack`, `threads`, `locals`,
      `eval`, `set`, `snapshot`, `sessions`, `use`, `stop`, `adapters`
- [x] Verified live against `examples/python/buggy.py`

## Phase 1.5 — Go via Delve ✅ DONE

- [x] TCP-listen transport in adapter framework (auto picks free port)
- [x] `dlv dap` adapter wired with `--listen=127.0.0.1:{PORT}`
- [x] Verified live against `examples/go/buggy.go`

## Phase 2 — Java the right way (no source build for users)

**Goal:** `sl-dbg install-adapter java` becomes one command, downloads a
prebuilt fat-jar, and Java debugging just works — same UX as Python's
`pip install debugpy`.

- [ ] Add `adapters/java-launcher/` Maven project in this repo: a thin main
      class that instantiates `com.microsoft.java.debug.core.adapter.JdiDebugAdapter`
      and bridges stdio↔DAP. Dependencies (java-debug-core, rxjava, gson,
      commons-io) bundled via maven-shade-plugin into one fat jar.
- [ ] CI release workflow: on tag, `mvn package` and attach the jar as a
      GitHub Release asset (`sl-dbg-java-adapter-<ver>.jar`).
- [ ] Implement `sl-dbg install-adapter <lang>`: curl-downloads the asset
      to `~/.cache/sl-dbg/adapters/`.
- [ ] Live e2e against `examples/java/Buggy.java` (attach mode using JDWP).

**Why not source-build today:** Microsoft's `java-debug` is shipped as an
OSGi bundle for Eclipse JDT-LS. To use it standalone we need a launcher
class + dependency-shading. That's a sl-dbg-maintainer concern, not an
end-user concern. End users see only `sl-dbg install-adapter java`.

## Phase 3 — More adapters via `install-adapter`

Each follows the same install-adapter pattern (download a prebuilt
upstream binary into `~/.cache/sl-dbg/adapters/`):

- [ ] Node.js — `vscode-js-debug` (Microsoft, npm)
- [ ] Rust / C / C++ — `codelldb` (LLVM, GitHub Releases) or `lldb-dap`
- [ ] .NET — `netcoredbg` (Samsung, GitHub Releases)
- [ ] Ruby — `rdbg` (`gem install debug`)

## Phase 1 (original) — Python MVP — superseded

**Goal:** Debug a Python program end-to-end with the core commands.

- [ ] DAP client wrapper (`internal/dap`) over `github.com/google/go-dap`
- [ ] Foreground (no daemon yet) session in `start` command
- [ ] Adapter registry with python entry (debugpy)
- [ ] Implement commands:
  - [ ] `start --lang python --program <path>`
  - [ ] `break <file:line>`
  - [ ] `breaks`, `unbreak`
  - [ ] `continue`, `step`, `next`, `finish`
  - [ ] `stack`, `locals`, `eval`, `set`
  - [ ] `state`, `stop`
- [ ] JSON output envelope + error codes
- [ ] Unit tests for DAP wrapper
- [ ] E2E test: debug a fixture Python script

**Exit criteria:** Can fully debug `examples/python/buggy.py` from CLI.

**Estimated effort:** 1–2 weekends.

---

## Phase 2 — Daemon & Multi-Session

**Goal:** Background daemon with multiple concurrent sessions.

- [ ] IPC server on Unix socket (`internal/ipc`)
- [ ] Daemon auto-spawn on first CLI call
- [ ] `sl-dbg daemon start|stop|status|logs`
- [ ] Session manager with stable IDs
- [ ] `sl-dbg sessions`, `sl-dbg use`, `--session` flag
- [ ] Event buffering between CLI calls
- [ ] Graceful daemon shutdown when all sessions end
- [ ] Lock file to enforce singleton daemon
- [ ] Windows named-pipe transport (parity)

**Exit criteria:** Two concurrent Python debug sessions work; daemon survives CLI process exits.

**Estimated effort:** 1 week.

---

## Phase 3 — Full DAP Coverage

**Goal:** Every DAP capability surfaced through CLI.

- [ ] Conditional / hit-count / logpoint breakpoints
- [ ] Function breakpoints (`break-fn`)
- [ ] Exception breakpoints (`break-ex`)
- [ ] Data breakpoints (`watch <var>`)
- [ ] `pause`, `goto`, `until`
- [ ] Reverse stepping (`back`) where supported
- [ ] Watch expressions (`watch-expr`, `watches`)
- [ ] `exception` details
- [ ] `modules`, `loadedSources`
- [ ] `source` request (fetch source from adapter)
- [ ] `snapshot` command
- [ ] `output --follow`, `events --follow`
- [ ] Capability detection per adapter (gracefully degrade)

**Exit criteria:** Feature parity with VS Code's debug UI for Python.

**Estimated effort:** 2–3 weeks.

---

## Phase 4 — Multi-Language

**Goal:** First-class support for Java, Go, Node, C/C++, .NET, Rust.

For each language:
- [ ] Adapter spec in registry
- [ ] Auto-installer
- [ ] Launch config builder (language-specific args)
- [ ] Attach config builder
- [ ] Source path mapping helpers
- [ ] Per-language integration test

Order:
1. [ ] **Java** (`java-debug` jar) — primary target, port-based attach is killer feature
2. [ ] **Go** (`dlv dap`)
3. [ ] **Node.js** (vscode-js-debug)
4. [ ] **C/C++** (`lldb-dap`)
5. [ ] **.NET** (`netcoredbg`)
6. [ ] **Rust** (via `lldb-dap` + rustc debug info)

**Exit criteria:** Same command set works identically across all 6 languages.

**Estimated effort:** 1–2 weeks per language.

---

## Phase 5 — MCP & Agent Integration

**Goal:** First-class AI agent experience.

- [ ] `sl-dbg mcp` — speak MCP over stdio
- [ ] Tool schema for every command (`set_breakpoint`, `continue`, `get_variables`, …)
- [ ] Example agent integrations: Claude Desktop, Cursor, Copilot CLI
- [ ] Higher-level convenience commands designed for agents:
  - [ ] `sl-dbg snapshot` (already in Phase 3, expand here)
  - [ ] `sl-dbg find-bug --hypothesis "..."` — semi-guided
  - [ ] `sl-dbg trace --on-call <fn>` — record calls
- [ ] `docs/AGENT-GUIDE.md` with patterns and prompt templates
- [ ] `examples/agents/` with working scripts

**Exit criteria:** A Claude Desktop user can install sl-dbg and ask "debug my Python script and find why x is wrong" in one prompt.

**Estimated effort:** 2–3 weeks.

---

## Phase 6 — Production Polish

**Goal:** Ready for v1.0 release.

- [ ] `--read-only` mode enforcement
- [ ] `--allowlist-files` / `--denylist-files` for breakpoint paths
- [ ] Audit log
- [ ] Self-update (`sl-dbg update`)
- [ ] Homebrew formula
- [ ] GitHub Actions release pipeline (multi-arch binaries)
- [ ] Install script `install.sh`
- [ ] `winget` / `scoop` / `apt` / `dnf` packages
- [ ] Shell completion scripts (bash, zsh, fish, PowerShell)
- [ ] Telemetry (opt-in, anonymous, privacy-respecting)
- [ ] Comprehensive integration test matrix
- [ ] Performance: <50ms CLI startup, <5ms IPC overhead

**Exit criteria:** Tag `v1.0.0`, publish to all major package managers.

---

## Phase 7 — Beyond v1

Ideas for later versions:

- Recording & replay of debug sessions
- Time-travel debugging via `rr` integration
- Distributed tracing correlation (link debug session to traces)
- Web UI (`sl-dbg web --port 8080`) — optional, doesn't replace CLI
- Plugin system for custom commands
- Native LSP integration (debug from your editor without VS Code)
- Container/k8s helpers (`sl-dbg kube attach <pod>` auto-port-forwards)

---

## Quality Bars (apply to every phase)

- ≥80% unit test coverage on `internal/*`
- E2E test for every command + adapter combination
- Zero `panic()` reachable from user input
- All errors are typed and documented
- Cobra `--help` shows real examples for every command
- All JSON responses validate against `pkg/api/schema.json`
