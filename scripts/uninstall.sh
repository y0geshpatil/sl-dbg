#!/usr/bin/env bash
# sl-dbg uninstaller — removes the binary and (optionally) the per-agent
# MCP registrations. Reverses scripts/install.sh.
#
# Usage:
#   curl -fsSL https://y0geshpatil.github.io/sl-dbg-site/uninstall.sh | bash
#
# Skip MCP cleanup (only remove the binary):
#   curl -fsSL https://y0geshpatil.github.io/sl-dbg-site/uninstall.sh | bash -s -- --keep-mcp
#
# Override install dir (must match where it was installed):
#   INSTALL_DIR=$HOME/.local/bin curl -fsSL .../uninstall.sh | bash
set -euo pipefail

BINARY="sl-dbg"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
KEEP_MCP="no"

for arg in "$@"; do
  case "$arg" in
    --keep-mcp) KEEP_MCP="yes" ;;
    *) echo "warning: ignoring unknown argument: $arg" >&2 ;;
  esac
done

BIN_PATH="${INSTALL_DIR}/${BINARY}"

if [ "$KEEP_MCP" != "yes" ] && [ -x "$BIN_PATH" ]; then
  echo "==> removing sl-dbg from MCP-aware agents on this machine"
  # 'all' only touches configs that actually exist; safe on machines
  # without every agent installed.
  "$BIN_PATH" mcp uninstall all 2>&1 | sed 's/^/    /' || true
fi

if [ -e "$BIN_PATH" ]; then
  echo "==> removing $BIN_PATH"
  if [ -w "$INSTALL_DIR" ]; then
    rm -f "$BIN_PATH"
  elif command -v sudo >/dev/null 2>&1; then
    sudo rm -f "$BIN_PATH"
  else
    echo "error: cannot remove $BIN_PATH (no write perm and sudo unavailable)" >&2
    exit 1
  fi
else
  echo "==> $BIN_PATH not found, skipping binary removal"
fi

echo "==> done"
cat <<EOF

What this did NOT remove:
  • Language adapters you installed via 'sl-dbg install-adapter ...'
    (debugpy, dlv, java-debug). Remove them with your package manager.
  • Daemon state under \$XDG_RUNTIME_DIR or /tmp/sl-dbg-* (cleared at next boot).
  • Backups created during install ('*.bak.<timestamp>' next to each agent config).

Reinstall later with:
  curl -fsSL https://y0geshpatil.github.io/sl-dbg-site/install.sh | bash
EOF
