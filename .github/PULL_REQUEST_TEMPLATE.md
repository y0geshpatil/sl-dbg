<!--
Thank you for the contribution! Please fill in the sections below.
Keep PRs focused: one feature / bug per PR. Bigger reorganisations should
be discussed in an issue first.
-->

## Definition of Done — REQUIRED

Every PR must satisfy **all** of these before review. PRs missing any will be sent back.

- [ ] **Unit tests added/updated** for the changed logic (`internal/<pkg>/*_test.go`). New code without a unit test does not ship.
- [ ] **E2E test updated** in `test/e2e/<lang>.sh` if the change is user-visible from the CLI (a new flag, command, JSON field, or behavior).
- [ ] **`docs/COMMANDS.md` updated** if you changed a command's args, response, or error codes.
- [ ] **`README.md` or relevant `docs/*.md` updated** if behavior visible in `--help` or in JSON responses changed.
- [ ] **`docs/AGENT-GUIDE.md` updated** if you added/changed an MCP tool.
- [ ] **AGENTS.md §4 / §5 updated** if you discovered a new codebase convention or gotcha worth saving for the next agent.
- [ ] `go test ./...` passes
- [ ] `make test` (Python + Go e2e) passes — and Java e2e if Java was touched
- [ ] No secrets, tokens, or PII in the diff or test fixtures
- [ ] Co-authored-by trailer kept if AI-assisted

## Summary

<!-- One paragraph: what this PR does and why. -->

## Related issue

Closes #
<!-- (or: Refs #NNN if related but not closing) -->

## What changed

- [ ] CLI surface (new flag / subcommand) — updated `docs/COMMANDS.md`
- [ ] MCP tool registry / schema — added/updated tool in `internal/mcp/server.go`
- [ ] Wire protocol (`internal/proto`) — bumped or kept `schema:"1"` (explain)
- [ ] Daemon handler — covered by unit tests in `internal/daemon/*_test.go`
- [ ] Language adapter — verified end-to-end via `make test`
- [ ] Documentation only

## How to verify

```bash
# Exact commands a reviewer should run
make build
go test ./...
make test
# + any targeted reproduction:
```

## Risk / compatibility

<!--
Any callers (CLI users, MCP agents, Go SDK consumers) that could break?
If you changed proto.* fields, did you keep them backwards-compatible?
-->

## Checklist (see "Definition of Done" above for the full list)

- [ ] I have read [AGENTS.md](../AGENTS.md) §4 *Conventions* (especially rule 12, the same Definition of Done)
- [ ] I verified all "Definition of Done" boxes above
