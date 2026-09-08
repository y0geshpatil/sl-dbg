#!/usr/bin/env bash
# Download a checksum-verified release. No shell profiles or MCP configs are changed.
# curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash
# curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | INSTALL_DIR="$HOME/bin" bash -s -- v0.5.4
set -euo pipefail

REPO="y0geshpatil/sl-dbg"
BINARY="sl-dbg"
INSTALL_DIR="${INSTALL_DIR:-${HOME:?HOME must be set}/.local/bin}"
VERSION="${1:-latest}"
tmp=""
staged=""

die() { echo "error: $*" >&2; exit 1; }
cleanup() {
  [ -z "$staged" ] || rm -f "$staged"
  [ -z "$tmp" ] || rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

[ "$#" -le 1 ] || die "usage: install.sh [latest|vVERSION]"
case "$INSTALL_DIR" in /*) ;; *) die "INSTALL_DIR must be an absolute path" ;; esac
[ "${INSTALL_SKIP_VERIFY:-0}" != 1 ] || die "INSTALL_SKIP_VERIFY is no longer supported; release checksums are required"
for tool in curl tar mktemp install awk; do
  command -v "$tool" >/dev/null 2>&1 || die "required tool '$tool' is missing; install it and retry"
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  die "install sha256sum (coreutils) or shasum (Perl) to verify downloads"
fi

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux) OS=linux ;;
  *) die "unsupported OS: $(uname -s); releases support macOS and Linux only" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m); releases support amd64 and arm64 only" ;;
esac

fetch() {
  curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' \
    --connect-timeout 15 --max-time 180 --retry 3 --output "$2" "$1"
}

tmp="$(mktemp -d)"
if [ "$VERSION" = latest ]; then
  fetch "https://api.github.com/repos/${REPO}/releases/latest" "$tmp/release.json" \
    || die "could not resolve latest release; check connectivity/API rate limits, or pass an explicit vVERSION from https://github.com/${REPO}/releases"
  VERSION="$(sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "$tmp/release.json")"
fi
[[ "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] \
  || die "invalid release version '$VERSION'; expected vMAJOR.MINOR.PATCH (optional prerelease)"
VER_NO_V="${VERSION#v}"
VERSION="v${VER_NO_V}"
ARCHIVE="${BINARY}_${VER_NO_V}_${OS}_${ARCH}.tar.gz"
CHECKSUMS="${BINARY}_${VER_NO_V}_checksums.txt"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

echo "==> sl-dbg ${VERSION} (${OS}/${ARCH})"
fetch "${BASE_URL}/${ARCHIVE}" "${tmp}/${ARCHIVE}" \
  || die "could not download ${ARCHIVE}; check connectivity and assets at https://github.com/${REPO}/releases/tag/${VERSION}"
fetch "${BASE_URL}/${CHECKSUMS}" "${tmp}/${CHECKSUMS}" \
  || die "could not download ${CHECKSUMS}; refusing to install an unverified binary"
expected="$(awk -v a="$ARCHIVE" '$2==a{print $1}' "${tmp}/${CHECKSUMS}")"
[[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]] \
  || die "expected exactly one SHA-256 for ${ARCHIVE} in ${CHECKSUMS}"
actual="$(sha256 "${tmp}/${ARCHIVE}")"
[ "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" = "$actual" ] \
  || die "SHA-256 mismatch for ${ARCHIVE}; existing installation unchanged"

# Extract only the expected binary, not arbitrary archive paths.
tar -xzf "${tmp}/${ARCHIVE}" -C "$tmp" "$BINARY" \
  || die "release archive does not contain ${BINARY}"
[ -f "${tmp}/${BINARY}" ] && [ ! -L "${tmp}/${BINARY}" ] \
  || die "release binary is not a regular file"
chmod 0755 "${tmp}/${BINARY}"
"${tmp}/${BINARY}" version || die "downloaded binary cannot run on this machine; existing installation unchanged"
mkdir -p "$INSTALL_DIR" || die "cannot create ${INSTALL_DIR}; choose a writable INSTALL_DIR"
[ -w "$INSTALL_DIR" ] || die "cannot write to ${INSTALL_DIR}; choose a user-owned INSTALL_DIR (no automatic sudo)"
[ ! -d "${INSTALL_DIR}/${BINARY}" ] || die "${INSTALL_DIR}/${BINARY} is a directory; refusing to replace it"
staged="$(mktemp "${INSTALL_DIR}/.sl-dbg.XXXXXX")"
install -m 0755 "${tmp}/${BINARY}" "$staged"
mv -f "$staged" "${INSTALL_DIR}/${BINARY}"
staged=""

echo "==> installed ${INSTALL_DIR}/${BINARY}"
case ":${PATH:-}:" in
  *":${INSTALL_DIR}:"*) ;;
  *) printf 'Add this to your shell profile, then restart your terminal:\n  export PATH=%q:"$PATH"\n' "$INSTALL_DIR" ;;
esac
resolved="$(command -v sl-dbg || true)"
if [ -n "$resolved" ] && [ "$resolved" != "${INSTALL_DIR}/${BINARY}" ]; then
  echo "warning: PATH currently resolves sl-dbg to $resolved; put $INSTALL_DIR first" >&2
fi
cat <<EOF

Next:
  "${INSTALL_DIR}/${BINARY}" install-adapter python   # or go / java
  "${INSTALL_DIR}/${BINARY}" adapters
  "${INSTALL_DIR}/${BINARY}" mcp install --print      # preview client registration

Upgrading? Existing daemons and MCP clients keep running the old code.
Finish active debug sessions, run "${INSTALL_DIR}/${BINARY}" daemon stop,
then restart your MCP client. The installer never interrupts debug sessions.

Docs: https://y0geshpatil.github.io/sl-dbg-site/
Uninstall using the same INSTALL_DIR with scripts/uninstall.sh.
EOF
