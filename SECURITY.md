# Security Policy

## Reporting a vulnerability

**Do not file public issues for security vulnerabilities.** Instead:

Use GitHub's [private security advisory flow](https://github.com/y0geshpatil/sl-dbg/security/advisories/new).
If private reporting is unavailable, ask the maintainer to enable it without
posting vulnerability details publicly.

Include the affected version, platform, reproduction steps, impact, and any
proposed fix. Please omit real credentials or private target data. Maintainers
will coordinate a disclosure timeline; this volunteer project has no guaranteed
response SLA.

## Supported versions

Security fixes target the latest source and next release. Older releases are not
maintained as separate security branches. Review release notes before upgrading,
finish active debug sessions, stop the old daemon, and restart MCP clients.

## In scope

- Secret/credential leakage in logs, command output, or telemetry.
- Sandbox escapes (e.g., `--read-only` not actually blocking mutation).
- Arbitrary code execution via crafted DAP responses, env vars, or user-controlled file paths.
- Local privilege escalation via the daemon socket or IPC layer.
- Bundled adapter (`sl-dbg-java-adapter.jar`) vulnerabilities specific to our launcher (upstream `java-debug` issues should go to Microsoft).

## Out of scope

- Vulnerabilities in language adapters maintained by third parties (`debugpy`, `dlv`, etc.) — report to those projects.
- Issues that require the attacker to already have full shell access as the running user.
- DoS via local-only resource exhaustion (the daemon is per-`$UID`).

## Threat model

See [docs/SECURITY.md](docs/SECURITY.md) for the full threat model, hardening features (`--read-only`, secret redaction, log rotation), and recommended deployment patterns.
