# Releasing sl-dbg

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
     `yogeshpatil/homebrew-sl-dbg` tap

## Required secrets

| Name | Scope | Why |
|---|---|---|
| `GITHUB_TOKEN` | provided by Actions | upload release assets |
| `HOMEBREW_TAP_GITHUB_TOKEN` | personal access token with `repo` on `yogeshpatil/homebrew-sl-dbg` | push formula updates |

## Installing the released binary

### Homebrew (macOS / Linux)

```bash
brew tap yogeshpatil/sl-dbg
brew install sl-dbg

# fetch / build language adapters (one-time):
sl-dbg install-adapter all
```

### Direct download

Grab a tarball from the GitHub Releases page, extract, put `sl-dbg` on `$PATH`.

### From source

```bash
git clone https://github.com/yogeshpatil/sl-dbg
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
