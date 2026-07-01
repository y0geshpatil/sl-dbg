# Contributing to sl-dbg

Thanks for taking the time to help! Contributions of all shapes are
welcome — bug reports, docs fixes, tests, new language adapters.

## TL;DR

```bash
git clone https://github.com/y0geshpatil/sl-dbg
cd sl-dbg
make build
make test
```

Before opening a PR:

- `go test ./...` (or `make test`) must pass; new behaviour needs a test.
- Update docs whenever you change flags, env vars, MCP tools, or safety
  behaviour — `README.md`, `docs/COMMANDS.md`, `docs/SECURITY.md`.
- Keep the change surgical; no drive-by reformatting.

## Full guide

- [`docs/CONTRIBUTING.md`](docs/CONTRIBUTING.md) — development setup,
  code style, testing, and the change workflow.
- [`AGENTS.md`](AGENTS.md) — definition of done, security posture,
  release process. **Read this before doing anything non-trivial.**
- [`docs/TRIAGE.md`](docs/TRIAGE.md) — how issues and PRs are labelled
  and prioritised.

## Reporting bugs and security issues

- Regular bugs: open an issue with the exact command line, expected vs
  actual JSON, `sl-dbg version` output, and OS/arch.
- Security issues: do **not** open a public issue — see
  [`SECURITY.md`](SECURITY.md).

## License

By contributing, you agree that your contributions will be licensed under
the [Apache License 2.0](LICENSE).
