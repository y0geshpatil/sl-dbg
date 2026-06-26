# Contributing to sl-dbg

Thanks for your interest in `sl-dbg`. This document covers development setup, code style, testing, and the change workflow.

## Development Setup

### Prerequisites
- Go 1.22+ (`brew install go`)
- One or more DAP adapters for integration testing:
  - `pip install debugpy` (Python — easiest)
  - `go install github.com/go-delve/delve/cmd/dlv@latest`
- `make`, `git`
- Optional: `golangci-lint`, `goreleaser`

### Build
```bash
git clone https://github.com/<owner>/sl-dbg
cd sl-dbg
make build
./bin/sl-dbg version
```

### Run Tests
```bash
make test               # unit tests
make test-integration   # integration (needs adapters)
make lint
```

### Common Tasks
```bash
make run ARGS="version"        # build and run
make run ARGS="start --lang python --program examples/python/buggy.py"
make tidy                      # tidy go.mod
make fmt                       # gofmt
```

## Project Layout

```
sl-dbg/
├── cmd/sl-dbg/             # main entry point
├── internal/
│   ├── cli/                # cobra command implementations
│   ├── daemon/             # background daemon
│   ├── dap/                # DAP client wrapper
│   ├── session/            # session manager
│   ├── adapter/            # per-language adapter registry
│   ├── ipc/                # Unix socket / named pipe transport
│   ├── config/             # config file loader
│   ├── logging/            # structured logging
│   └── buildinfo/          # version metadata
├── pkg/api/                # public JSON types
├── docs/                   # design docs
├── examples/               # sample programs to debug
├── test/                   # integration + e2e tests
├── scripts/                # release/install scripts
└── adapters/               # (future) bundled adapter binaries
```

## Code Style

- `gofmt` is law. Run `make fmt` before committing.
- `go vet` and `golangci-lint` must pass.
- Public types/funcs in `pkg/` need godoc comments.
- Internal types may be undocumented if name is self-evident, but exported methods always carry a one-line comment.
- Error wrapping: use `fmt.Errorf("doing X: %w", err)`. Define typed errors only at API boundaries.
- Avoid panics outside of `init()`. Panics inside goroutines must be recovered to the daemon log.
- No global mutable state outside `init()`. Pass dependencies explicitly.

## Claim Before You Code

Before opening an editor for any issue — even your own — claim it on GitHub. This stops two people (or two AI agents) silently duplicating work.

```bash
gh issue edit <N> --add-assignee @me --add-label in-progress
gh issue comment <N> --body "Picking this up.
- Who: <your handle, e.g. @alice or 'Copilot CLI session abc123' for AI agents>
- Branch: fix/<slug>
- Approach: <one line>
- ETA: <today / this week / unsure>"
```

If you walk away before shipping, comment `"stepping away, unclaiming"` and remove the assignee + label. Full SOP — including the matching close-with-SHA rule — is in [`TRIAGE.md → Claim-Before-Work SOP`](TRIAGE.md#claim-before-work-sop--required). AI coding agents must self-identify in the claim comment.

## Definition of Done

**Every PR must update all relevant surfaces in the same commit.** Code-only or docs-only PRs are accepted only when nothing else applies.

| If your change touches… | You MUST also update… |
|---|---|
| Logic in `internal/<pkg>` | A unit test in the same package's `*_test.go` |
| CLI surface (flag, command, response) | `test/e2e/<lang>.sh` to exercise the new path |
| Wire protocol (`internal/proto`) | `docs/COMMANDS.md` (schema + table-of-contents) |
| MCP tool (added/renamed/schema change) | `docs/AGENT-GUIDE.md` |
| `--help` output or JSON behavior | `README.md` or the relevant `docs/*.md` page |
| New error code | `docs/COMMANDS.md` error-codes section + `AGENTS.md` §4 rule 4 list |
| New codebase convention or gotcha worth remembering | `AGENTS.md` §4 (convention) or §5 (gotcha) |

A PR that adds logic without tests, or changes behavior without docs, will be sent back. This is non-negotiable — the project explicitly prioritises maintainability over velocity. If a change is genuinely impossible to test, that's usually a design smell; flag it in the PR description and propose a refactor instead of working around it.

The PR template enforces this with a checkbox list; AGENTS.md §4 rule 12 spells it out for AI coding agents working on the codebase.

## Commit & PR Conventions

- One logical change per commit.
- Use conventional-commit prefixes: `feat:`, `fix:`, `docs:`, `test:`, `refactor:`, `chore:`.
- PR title summarizes the change; PR body explains the **why**.
- Each PR adds/updates tests for changed behavior.
- Update `docs/COMMANDS.md` when commands or schemas change.
- Update `docs/ROADMAP.md` checkboxes when completing phase items.

## Testing Philosophy

- **Unit tests** in `internal/<pkg>/*_test.go` — pure logic, no subprocesses.
- **Integration tests** in `test/integration/` — drive a real adapter. Gated by `-tags=integration`.
- **End-to-end tests** in `test/e2e/` — spawn `bin/sl-dbg` as a subprocess and assert JSON output. Treat sl-dbg as a black box.

Every new command MUST have:
1. A unit test for argument parsing.
2. An integration test against at least one adapter (`python` is the default).
3. An entry in `docs/COMMANDS.md` with JSON schema.

## Adding a New Command

1. Create `internal/cli/<command>.go` with the cobra `*cobra.Command`.
2. Register in `internal/cli/root.go`.
3. Add IPC request/response types to `pkg/api/`.
4. Add daemon handler in `internal/daemon/handlers.go`.
5. Document in `docs/COMMANDS.md`.
6. Add tests.

## Adding a New Language Adapter

See `docs/ADAPTERS.md` § "Adding a New Adapter". Briefly:
1. Add `internal/adapter/<lang>.go` with a `Register()` call.
2. Add detection, install, and launch logic.
3. Add `examples/<lang>/buggy-program` fixture.
4. Add integration test in `test/integration/<lang>_test.go`.
5. Document in `docs/ADAPTERS.md`.

## Releases

Releases are tagged on `main` (`vX.Y.Z`). CI (`.github/workflows/release.yml`) produces:
- Cross-compiled binaries for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`.
- A `SHA256SUMS` file.
- Homebrew formula bump PR (planned).

Versioning: SemVer. v1.0.0 is the first stable, public-API-frozen release.

## Code of Conduct

We follow the [Contributor Covenant v2.1](https://www.contributor-covenant.org/version/2/1/code_of_conduct/). Be kind, assume good faith, ask before refactoring others' work.

## License

By contributing, you agree your contributions are licensed under Apache-2.0, the same as the project.
