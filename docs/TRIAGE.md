# Triage & Labels

This document defines how issues and PRs are labelled and prioritised on `sl-dbg`. Maintainers apply these labels; contributors don't need to memorise them but knowing the scheme helps frame a useful report.

---

## Priority (set exactly one)

| Label | Meaning | Target turnaround |
|---|---|---|
| `P0-blocker` | Crash, data loss, secret leak, security vulnerability, sl-dbg unusable for a core workflow on a supported platform. | Drop everything; same day |
| `P1-high` | Important user-facing bug, a heavily-used command misbehaves, a new release should not ship without the fix. | Within 1 week |
| `P2-normal` | Most bugs and enhancements. Should be addressed in the normal release cadence. | Best effort, 1 month-ish |
| `P3-low` | Polish, nice-to-have, edge case unlikely to bite many users. | When it bubbles to the top |
| `P4-someday` | Aspirational / out-of-scope-for-now. Kept open for visibility but not on any roadmap. | No commitment |

**Default**: every newly-filed issue carries `needs-triage`. A maintainer replaces it with one of the P-labels above (plus other labels below) within ~48h.

---

## Type (set exactly one)

| Label | Use for |
|---|---|
| `bug` | Something doesn't work as documented |
| `enhancement` | New feature, flag, MCP tool, or behaviour change |
| `question` | Usage / "how do I" — moved to Discussions when appropriate |
| `documentation` | README, docs/, AGENTS.md, code comments |
| `refactor` | No behaviour change; code cleanup |
| `chore` | Build, CI, release plumbing |

---

## Area (set one or more)

| Label | Touches |
|---|---|
| `area/cli` | `internal/cli/` — cobra commands, flags, repl |
| `area/daemon` | `internal/daemon/` — handlers, session manager, IPC server |
| `area/mcp` | `internal/mcp/` — tool registry, composites, schemas |
| `area/proto` | `internal/proto/` — wire schema (high-blast-radius changes) |
| `area/adapter/python` | `debugpy`-specific behaviour |
| `area/adapter/go` | `dlv`-specific behaviour |
| `area/adapter/java` | bundled `java-debug` launcher, `adapters/java/` |
| `area/sdk` | `pkg/api/` Go SDK |
| `area/docs` | `docs/`, README, AGENTS |
| `area/release` | GoReleaser, Homebrew tap, install scripts |

---

## Status (mutually exclusive)

| Label | Meaning |
|---|---|
| `needs-triage` | Fresh — awaiting maintainer review |
| `needs-info` | Author response needed; auto-close after 30 days of silence |
| `confirmed` | Reproduced; ready to be worked on |
| `in-progress` | Someone is actively working on this (assign yourself) |
| `blocked` | Waiting on something external (upstream adapter fix, etc.) |
| `wontfix` | Out of scope or by design — closed |
| `duplicate` | See linked issue — closed |

---

## Special-interest

| Label | Use for |
|---|---|
| `good-first-issue` | Small, well-scoped, mentoring available; great for newcomers |
| `help-wanted` | Maintainers won't get to it soon; community PRs welcome |
| `breaking-change` | Will require a major version bump or migration note |
| `regression` | Worked in version X, broken in version Y; tag both versions in the body |
| `security` | Security-relevant — apply alongside priority; private disclosure first (see SECURITY.md) |

---

## How a typical bug flows

1. Reporter files via *Bug report* template → gets `bug` + `needs-triage`.
2. Maintainer reproduces (or asks for more info → `needs-info`).
3. Maintainer adds `P*`, `area/*`, and removes `needs-triage` → adds `confirmed`.
4. Someone picks it up → `in-progress` + assignee.
5. PR opened → links via `Closes #N`.
6. PR merged → issue auto-closed.

If a `P0-blocker` lands, the maintainer cuts a patch release immediately rather than batching.

---

## A note for AI agents filing or triaging issues

If you're an AI coding agent (Copilot, Claude, etc.) filing issues:

- Always include `sl-dbg version`, OS, and language adapter — these unblock triage.
- Quote the **exact JSON response**, not a paraphrase. Stable error codes (e.g., `LAUNCH_FAILED`, `EVAL_NO_THIS`) speed routing enormously.
- One issue per bug. Don't bundle.
- For feature requests, propose the CLI/MCP shape concretely; "make X easier" without a shape is hard to act on.

See [`AGENTS.md`](../AGENTS.md) for codebase orientation if you intend to send a PR.
