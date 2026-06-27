#!/usr/bin/env bash
# sl-dbg installer — fetches the latest tagged GitHub release binary for
# the current OS/arch and drops it into a directory on $PATH.
#
# Usage (after the repo is public):
#   curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash
#
# Or pin a version:
#   curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh \
#     | bash -s -- v0.3.0
#
# Override install dir:
#   INSTALL_DIR=$HOME/.local/bin curl ... | bash
#
# Why a script and not just `go install`? Most users don't have a Go
# toolchain. This pulls the prebuilt tarball produced by goreleaser so the
# install completes in seconds with no compiler dependency.
set -euo pipefail

REPO="y0geshpatil/sl-dbg"
BINARY="sl-dbg"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"

die() { echo "error: $*" >&2; exit 1; }

# Normalize OS/arch to the names goreleaser uses in the archive filename.
detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux)  echo linux ;;
    *)      die "unsupported OS: $(uname -s) — sl-dbg ships darwin + linux only" ;;
  esac
}
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;;
    arm64|aarch64) echo arm64 ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

# Resolve "latest" to a concrete tag via the GitHub API so we can build
# a deterministic download URL. Falls back to the redirect target of
# /releases/latest when jq isn't available.
resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return
  fi
  if command -v jq >/dev/null 2>&1; then
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | jq -r .tag_name
  else
    # Follow the redirect; the final URL ends in /tag/<version>.
    curl -fsSLI -o /dev/null -w '%{url_effective}\n' \
      "https://github.com/${REPO}/releases/latest" | sed 's|.*/tag/||'
  fi
}

VERSION="$(resolve_version)"
[ -n "$VERSION" ] || die "could not resolve latest version"

# goreleaser archive naming: sl-dbg_<version>_<os>_<arch>.tar.gz
# The leading 'v' is stripped from the version in the filename.
VER_NO_V="${VERSION#v}"
ARCHIVE="${BINARY}_${VER_NO_V}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

echo "==> sl-dbg ${VERSION} (${OS}/${ARCH})"
echo "==> downloading ${URL}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$URL" -o "${tmp}/${ARCHIVE}" \
  || die "download failed — check the tag exists and the repo is public"
tar -xzf "${tmp}/${ARCHIVE}" -C "$tmp"

# Install: try the requested dir without sudo first; fall back to sudo
# only if the user can't write it directly.
if [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "${tmp}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
elif command -v sudo >/dev/null 2>&1; then
  echo "==> ${INSTALL_DIR} is not writable; using sudo"
  sudo install -m 0755 "${tmp}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
else
  die "cannot write to ${INSTALL_DIR} and sudo is unavailable — set INSTALL_DIR=~/.local/bin"
fi

echo "==> installed $(${INSTALL_DIR}/${BINARY} version 2>/dev/null || echo "${BINARY} ${VERSION}")"
cat <<EOF

Next steps:
  1. Make sure ${INSTALL_DIR} is on your PATH.
  2. Install language adapters you need:
       sl-dbg install-adapter python   # debugpy via pip
       sl-dbg install-adapter go       # dlv via go install
       sl-dbg install-adapter java     # java-debug via Maven
  3. Try it:
       sl-dbg start --lang python --program <your-script.py> --stop-on-entry
EOF
