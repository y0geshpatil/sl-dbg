#!/usr/bin/env bash
# sl-dbg installer — fetches the latest tagged release binary for the
# current OS/arch from the PUBLIC release-mirror repo and drops it on
# $PATH. No authentication needed even though the source repo is private,
# because release artifacts live in y0geshpatil/sl-dbg-releases.
#
# Usage:
#   curl -fsSL https://sl-dbg.dev/install.sh | bash
#
# Pin a version:
#   curl -fsSL https://sl-dbg.dev/install.sh | bash -s -- v0.3.0
#
# Override install dir (no sudo needed if you own the dir):
#   INSTALL_DIR=$HOME/.local/bin curl -fsSL https://sl-dbg.dev/install.sh | bash
#
# Why a script and not just `go install`? Most users don't have a Go
# toolchain. This pulls the prebuilt tarball produced by goreleaser so the
# install completes in seconds with no compiler dependency.
set -euo pipefail

REPO="y0geshpatil/sl-dbg-releases"   # public mirror; source repo is private
BINARY="sl-dbg"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"

die() { echo "error: $*" >&2; exit 1; }

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

# Resolve "latest" to a concrete tag. Use the public API (no auth needed
# for public repos). We avoid jq by grep+sed on the JSON.
resolve_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return
  fi
  curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
    | grep -m1 '"tag_name"' \
    | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
}

VERSION="$(resolve_version)"
[ -n "$VERSION" ] || die "could not resolve latest version (no releases published yet?)"

# goreleaser archive: sl-dbg_<version-no-v>_<os>_<arch>.tar.gz
VER_NO_V="${VERSION#v}"
ARCHIVE="${BINARY}_${VER_NO_V}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

echo "==> sl-dbg ${VERSION} (${OS}/${ARCH})"
echo "==> downloading ${URL}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL "$URL" -o "${tmp}/${ARCHIVE}" \
  || die "download failed — check that ${VERSION} exists at https://github.com/${REPO}/releases"
tar -xzf "${tmp}/${ARCHIVE}" -C "$tmp"

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
