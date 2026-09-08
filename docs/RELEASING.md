# Releasing sl-dbg

Source and release assets live in `y0geshpatil/sl-dbg`; the documentation site is
`y0geshpatil/sl-dbg-site`. The supported release matrix is macOS (`darwin`) and
Linux, each on `amd64` and `arm64`. Windows and Homebrew distribution are not
currently supported. Homebrew publishing was disabled; the old tap configuration
could fail an otherwise uploaded release when credentials were missing.

## Required assets

For a tag `vX.Y.Z`, a complete release contains:

| Asset | Purpose |
|---|---|
| `sl-dbg_X.Y.Z_darwin_amd64.tar.gz` | Intel macOS binary |
| `sl-dbg_X.Y.Z_darwin_arm64.tar.gz` | Apple Silicon binary |
| `sl-dbg_X.Y.Z_linux_amd64.tar.gz` | x86-64 Linux binary |
| `sl-dbg_X.Y.Z_linux_arm64.tar.gz` | ARM64 Linux binary |
| `sl-dbg_X.Y.Z_checksums.txt` | SHA-256 manifest for archives |
| `sl-dbg-java-adapter.jar` | Standalone Java DAP launcher |
| `sl-dbg-java-adapter.jar.sha256` | SHA-256 plus the exact JAR filename |

Each archive includes `sl-dbg`, the full Apache-2.0 `LICENSE`, `README.md`, and
the command documentation. The JAR and sidecar are GoReleaser `extra_files`;
do not assume the archive checksum manifest covers extra files.

Released binaries request the JAR from their own version's release, not `latest`.
`v0.5.4` was published without the Java JAR; current source changes do not repair
that already-published release. Do not declare clean-machine Java installation
working until a complete new release has been published and exercised.

## Local preflight (does not publish)

Requires Go **1.26.4**, JDK 11+, Maven, and GoReleaser **2.16.0** (the CI-pinned versions).
The Go language minimum remains 1.22, but releases use the newer compiler:
Go 1.22 binaries can fail to load on current macOS (`missing LC_UUID`), and
current Delve requires a newer toolchain for its target builds.

```bash
make build
go test ./...
make java-adapter
goreleaser check
goreleaser release --snapshot --clean
bash scripts/verify-release.sh dist adapters/java-launcher/target
```

`make java-adapter` builds the JAR and sidecar. Snapshot mode skips publishing
and produces all four archives with a `-next` version. The CI snapshot job checks
the same contract on pull requests with read-only repository permissions.
Run the Python, Go, and Java e2e suites as well; missing prerequisites are skips,
not evidence of a successful language integration.
The current Java suite has an unresolved function-breakpoint verification
assertion (`Buggy.compute` stays unverified after class load). Do not suppress
that failure or describe the full suite as passing.
Release CI on JDK 11 also misses the earlier line breakpoint after suspended
attach, causing the target to exit before inspection. Both platforms fail this
gate; an isolated Temurin 11 reproduction also misses an unconditional
breakpoint. **Do not merge/tag/publish around this failure.**

## Publish (maintainer action)

1. Review a green CI run, the changelog, third-party license notices, and the
   required asset list above.
2. Create and push an annotated version tag only after release approval.
3. The `release` workflow runs unit tests, builds the Java JAR and checksum, and
   runs GoReleaser with the built-in `GITHUB_TOKEN` (`contents: write`).
4. Check the actual release assets, not only workflow status. Exercise a fresh
   install for each platform and a matching Java adapter install.
5. Sync the website's canonical doc snapshots from the released core commit.

No Homebrew token or cross-repository token is needed. A failed publishing run
can leave partial assets: inspect them before retrying, and do not instruct users
to bypass checksum verification.

## Installation and upgrades

```bash
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash
export PATH="$HOME/.local/bin:$PATH"
sl-dbg version

# Choose a particular published version:
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash -s -- v0.5.4

# Custom user-owned directory; env belongs to bash, not curl:
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | INSTALL_DIR="$HOME/bin" bash
```

The script creates the install directory, verifies HTTPS downloads with SHA-256,
checks that the binary runs, and atomically replaces the destination. It never
invokes sudo, changes shell profiles, or stops debugging sessions. Missing assets,
checksum failures, and failed executable checks leave the existing binary intact.
The checksum-skip environment variable is no longer supported.

To upgrade, repeat installation. Finish active sessions, run `sl-dbg daemon stop`,
and restart MCP clients to load new code. When migrating from `/usr/local/bin`,
put the new directory first on PATH and re-register MCP clients with `--force`
so their absolute executable paths follow the move. Use the same `INSTALL_DIR`
with `scripts/uninstall.sh` to remove the intended copy.

For source development, clone this repository and use `make build`.
`make setup` builds into `bin/` and installs adapters; it does not install the CLI
onto PATH. `go install ...@latest` builds source, without GoReleaser version
metadata; it is not equivalent to installing a published binary.
