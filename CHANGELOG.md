# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project intends to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once numbered releases begin.

## [Unreleased]

## [0.5.5] - 2026-09-08

### Changed
- Binary installation defaults to `~/.local/bin`, without automatic sudo or shell-profile edits. Explicit `INSTALL_DIR` remains supported; put it on the `bash` side of a download pipeline.
- Binary downloads require HTTPS and release SHA-256 checksums, verify the executable before atomic replacement, and retain existing installations on failure. The checksum bypass is removed.
- Python adapter installation uses an isolated managed virtual environment instead of `pip --user`. Go detection includes persisted `go env` install paths.
- Java release installation requires the matching version's JAR and `sl-dbg-java-adapter.jar.sha256`; failures no longer silently build a different local adapter. Refresh cached Java adapters with `--force` after upgrading.
- Release packaging includes all four macOS/Linux amd64/arm64 archives, documentation, full Apache-2.0 license, Java JAR, and checksum sidecar. Inactive Homebrew publishing is removed.

### Fixed
- Correct VS Code and Copilot CLI MCP registration paths/schemas, preserve existing configuration safely, propagate partial failures, and allow client-free uninstall.
- Retain entry pauses that arrive before launch completes without replaying stale stops into subsequent resume operations.
- `daemon stop` no longer starts an absent daemon and waits for acknowledged shutdown. Long-lived MCP processes reap exited daemon children so an immediate restart can succeed.
- Isolate smoke tests and MCP documentation generation from user daemons, caches, and configuration.

### Upgrade notes
- Existing `/usr/local/bin` installations may shadow `~/.local/bin`; check `command -v sl-dbg` and update PATH or use the original `INSTALL_DIR`. Re-register MCP clients with `--force` when moving the executable.
- Finish debugging sessions, run `sl-dbg daemon stop`, then restart MCP clients. Use explicit target-path permissions, for example `sl-dbg mcp install vscode --allow-program "$PWD/demo.py"`; interpreter names alone do not permit arbitrary script paths.
- The older v0.5.4 release lacks the Java JAR. Its MCP registration and daemon lifecycle behavior also predates these fixes.

### Known limitations
- Release is blocked by JDK 11 line-breakpoint failures after suspended attach on macOS/Linux CI; the target exits before inspection. An isolated Temurin 11 reproduction also misses an unconditional line breakpoint.
- The Java smoke test observes `break-fn Buggy.compute` returning `verified:false` after the class is loaded. Line/conditional breakpoints and inspection work in the exercised flow; the full Java suite is not claimed to pass.
- Windows and Homebrew distribution are not supported. Checksums detect corrupted assets, not a compromised release publisher.

## Historical development notes

The notes below were previously grouped as unreleased work. They are retained
for history, not as the current installation or compatibility contract.

### Added
- Security threat-model documentation for the LLM → MCP → daemon → DAP adapter → target boundary, including hardening guidance for production MCP use (#24).
- GitHub Actions CI for Ubuntu and macOS with `go vet`, `go build`, `go test`, and Python/Go/Java e2e smoke coverage; added a lint workflow with `golangci-lint` when configured and `go vet` fallback (#32).
- Platform support documentation for macOS, Linux socket paths, install locations, shell `PATH` setup, and Windows caveats (#35).
- Intended TOML config-file schema documentation for `[security]`, `[limits]`, `[audit]`, and `[defaults]` while CLI flags remain the active interface (#36).
- Claim-before-work SOP and contributor/triage/community documentation for safer parallel maintenance.
- GitHub release automation via GoReleaser; `sl-dbg-java-adapter.jar` is now published as a standalone release asset alongside the binary tarballs.
- MCP tool coverage for optional sessions, composites, default-session routing, and additional debugger commands.

### Changed
- Refreshed README and agent-facing docs for the Wave H+ MCP UX and production-readiness direction.
- Documented versioning policy and this changelog as the source of release notes (#34).
- Clarified Definition of Done expectations across agent and contributor documentation.
- `sl-dbg install-adapter java` now auto-downloads the pre-built jar from GitHub Releases — no Maven or source checkout required. Falls back to a local Maven build when run from a source checkout.
- `install.sh` next-steps message, `docs/ADAPTERS.md`, `docs/RELEASING.md`, and `AGENTS.md` updated to reflect the new no-Maven install flow.

### Fixed
- MCP and daemon bugs from field testing, including `inspect_at` no-frame hangs, stale state after exit, and default-session handling.
- Open bug/gap batch covering debugger command behavior, MCP ergonomics, and safety-related edge cases across issues #2, #7, #8, #10, #11, #12, #14, #15, and #16.
- Wave H production-readiness blockers and MCP UX polish from commits `0be7f00` through `b40c98c`.
- `install-adapter java` failed with "no prebuilt jar available and in-tree Maven project not found" for end users installing via `install.sh` or Homebrew; resolved by auto-downloading from the matching GitHub Release.

### Security
- Added documentation for residual, currently open security limitations around source reads, eval side effects, read-only data exposure, program allowlists, session limits, and audit logging (#18, #19, #20, #21, #22, #23).
- Documented opt-in hardening flags for read-only MCP deployments and source/program allowlists.

## [0.1.0] - 2026-06-27

### Added
- First numbered release placeholder; cuts the line under whichever SHA `git tag v0.1.0` is applied to.
