# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project intends to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once numbered releases begin.

## [Unreleased]

### Added
- Security threat-model documentation for the LLM → MCP → daemon → DAP adapter → target boundary, including hardening guidance for production MCP use (#24).
- GitHub Actions CI for Ubuntu and macOS with `go vet`, `go build`, `go test`, and Python/Go e2e smoke coverage; added a lint workflow with `golangci-lint` when configured and `go vet` fallback (#32).
- Platform support documentation for macOS, Linux socket paths, install locations, shell `PATH` setup, and Windows caveats (#35).
- Intended TOML config-file schema documentation for `[security]`, `[limits]`, `[audit]`, and `[defaults]` while CLI flags remain the active interface (#36).
- Claim-before-work SOP and contributor/triage/community documentation for safer parallel maintenance.
- GitHub release automation via GoReleaser and Homebrew tap publishing notes.
- MCP tool coverage for optional sessions, composites, default-session routing, and additional debugger commands.

### Changed
- Refreshed README and agent-facing docs for the Wave H+ MCP UX and production-readiness direction.
- Documented versioning policy and this changelog as the source of release notes (#34).
- Clarified Definition of Done expectations across agent and contributor documentation.

### Fixed
- MCP and daemon bugs from field testing, including `inspect_at` no-frame hangs, stale state after exit, and default-session handling.
- Open bug/gap batch covering debugger command behavior, MCP ergonomics, and safety-related edge cases across issues #2, #7, #8, #10, #11, #12, #14, #15, and #16.
- Wave H production-readiness blockers and MCP UX polish from commits `0be7f00` through `b40c98c`.

### Security
- Added documentation for residual, currently open security limitations around source reads, eval side effects, read-only data exposure, program allowlists, session limits, and audit logging (#18, #19, #20, #21, #22, #23).
- Documented opt-in hardening flags for read-only MCP deployments and source/program allowlists.

## [0.1.0] - 2026-06-27

### Added
- First numbered release placeholder; cuts the line under whichever SHA `git tag v0.1.0` is applied to.
