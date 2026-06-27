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

# Verify the SHA-256 checksum against the release-published manifest.
# goreleaser publishes <archive-base>_checksums.txt for every release; we
# refuse to install if the file is missing or the hash doesn't match.
# Issue #68.
CHECKSUMS="${BINARY}_${VER_NO_V}_checksums.txt"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/${CHECKSUMS}"
echo "==> verifying SHA-256 against ${CHECKSUMS_URL}"
if [ "${INSTALL_SKIP_VERIFY:-0}" = "1" ]; then
  echo "==> WARNING: INSTALL_SKIP_VERIFY=1 set; skipping checksum verification"
elif curl -fsSL "$CHECKSUMS_URL" -o "${tmp}/${CHECKSUMS}"; then
  expected="$(awk -v a="${ARCHIVE}" '$2==a{print $1}' "${tmp}/${CHECKSUMS}")"
  if [ -z "$expected" ]; then
    die "checksum for ${ARCHIVE} not present in ${CHECKSUMS}; refusing to install"
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "${tmp}/${ARCHIVE}" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "${tmp}/${ARCHIVE}" | awk '{print $1}')"
  else
    die "neither sha256sum nor shasum available — cannot verify download"
  fi
  if [ "$expected" != "$actual" ]; then
    die "SHA-256 mismatch for ${ARCHIVE}: expected ${expected}, got ${actual}"
  fi
  echo "==> SHA-256 OK"
else
  die "could not fetch ${CHECKSUMS_URL}; refusing to install unverified binary (set INSTALL_SKIP_VERIFY=1 to override, NOT recommended)"
fi

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

  3. Register sl-dbg with your AI agent (one-shot, edits the agent's config):
       sl-dbg mcp install claude       # Claude Desktop
       sl-dbg mcp install cursor       # Cursor
       sl-dbg mcp install vscode       # ./.vscode/mcp.json (workspace-scoped)
       sl-dbg mcp install codex        # Codex CLI (~/.codex/config.toml)
       sl-dbg mcp install copilot      # GitHub Copilot CLI
       sl-dbg mcp install all          # every agent detected on this machine
       sl-dbg mcp install --print      # just print the snippet, do not touch files
     (Add --dry-run to preview, --force to overwrite an existing entry.
      A timestamped .bak of any existing config is written before the update.)

     Secure-by-default: the registered command runs 'sl-dbg mcp --safe
     --allow-program *'. That keeps the source-jail on, eval off, session
     cap on, and audit log on. To restrict which binaries debug_start may
     spawn, re-run with explicit allowlist entries:
       sl-dbg mcp install claude --allow-program /path/to/your/program
     Hide every mutating tool from the agent with:
       sl-dbg mcp install claude --read-only
     (--insecure restores the legacy permissive mode; NOT recommended.)

  4. Try it:
       sl-dbg start --lang python --program <your-script.py> --stop-on-entry

  Docs: https://y0geshpatil.github.io/sl-dbg-site/

To uninstall later:
  curl -fsSL https://y0geshpatil.github.io/sl-dbg-site/uninstall.sh | bash
EOF
