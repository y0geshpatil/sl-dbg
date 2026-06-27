# Releasing sl-dbg

## Prerequisites (one-time setup before the first public release)

1. **Make the repo public** at https://github.com/y0geshpatil/sl-dbg/settings.
   `go install` and the curl installer both require this.
2. **Create the Homebrew tap repo** `y0geshpatil/homebrew-sl-dbg` (an empty
   public repo is enough; GoReleaser pushes the formula automatically).
3. **Add repo secrets** under Settings → Secrets and variables → Actions:
   - `HOMEBREW_TAP_GITHUB_TOKEN` — a PAT with `repo` scope on the tap repo.
   - `GITHUB_TOKEN` is provided by Actions automatically.

## Cutting a release

1. Make sure `main` is green on CI.
2. Bump the version in your changelog notes (if any).
3. Tag and push:
   ```bash
   git tag -a v0.1.0 -m "v0.1.0: first release"
   git push origin v0.1.0
   ```
4. `.github/workflows/release.yml` runs GoReleaser, which:
   - builds `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`
   - uploads tarballs + checksums to the GitHub Release
   - publishes a Homebrew formula to the
     `y0geshpatil/homebrew-sl-dbg` tap

## Required secrets

| Name | Scope | Why |
|---|---|---|
| `GITHUB_TOKEN` | provided by Actions | upload release assets |
| `HOMEBREW_TAP_GITHUB_TOKEN` | personal access token with `repo` on `y0geshpatil/homebrew-sl-dbg` | push formula updates |

## Installing the released binary

### Homebrew (macOS / Linux)

```bash
brew tap y0geshpatil/sl-dbg
brew install sl-dbg

# fetch / build language adapters (one-time):
sl-dbg install-adapter all
```

### Curl one-liner (universal)

Once the repo is public, the installer script pulls the right tarball
for your OS/arch from the latest GitHub Release:

```bash
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash

# pin a version:
curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash -s -- v0.1.0

# install to a per-user dir (no sudo):
INSTALL_DIR=$HOME/.local/bin curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash
```

### Direct download

Grab a tarball from the GitHub Releases page, extract, put `sl-dbg` on `$PATH`.

### From source

```bash
go install github.com/y0geshpatil/sl-dbg/cmd/sl-dbg@latest
# or, if you want the adapters bundled in one shot:
git clone https://github.com/y0geshpatil/sl-dbg
cd sl-dbg
make setup    # build + install adapters
```

## Local dry-run

```bash
goreleaser build --snapshot --clean --single-target
./dist/sl-dbg_<os>_<arch>*/sl-dbg version
```

A full snapshot release (no publish) is:

```bash
goreleaser release --snapshot --clean --skip=publish
```
