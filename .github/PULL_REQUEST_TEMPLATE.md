<!--
Thank you for the contribution! Please fill in the sections below.
Keep PRs focused: one feature / bug per PR. Bigger reorganisations should
be discussed in an issue first.
-->

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

## Checklist

- [ ] `go test ./...` passes
- [ ] `make test` (Python + Go e2e) passes — and Java e2e if Java was touched
- [ ] Read [AGENTS.md](../AGENTS.md) §4 *Conventions* and complied
- [ ] No secrets, tokens, or PII in the diff or test fixtures
- [ ] Co-authored-by trailer kept if AI-assisted
