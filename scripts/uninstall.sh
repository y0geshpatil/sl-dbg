#!/usr/bin/env bash
# sl-dbg uninstaller — removes the binary and (optionally) the per-agent
# MCP registrations. Reverses scripts/install.sh.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/uninstall.sh | bash
#
# Skip MCP cleanup (only remove the binary):
#   curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/uninstall.sh | bash -s -- --keep-mcp
#
# Override install dir (must match where it was installed):
#   curl -fsSL .../uninstall.sh | INSTALL_DIR="$HOME/.local/bin" bash
set -euo pipefail

BINARY="sl-dbg"
INSTALL_DIR="${INSTALL_DIR:-${HOME:?HOME must be set}/.local/bin}"
KEEP_MCP="no"

for arg in "$@"; do
  case "$arg" in
    --keep-mcp) KEEP_MCP="yes" ;;
    *) echo "error: unknown argument: $arg (usage: uninstall.sh [--keep-mcp])" >&2; exit 1 ;;
  esac
done

BIN_PATH="${INSTALL_DIR}/${BINARY}"
case "$INSTALL_DIR" in
  /*) ;;
  *) echo "error: INSTALL_DIR must be an absolute path" >&2; exit 1 ;;
esac
if [ -d "$BIN_PATH" ]; then
  echo "error: $BIN_PATH is a directory; refusing to remove it" >&2
  exit 1
fi
if { [ -e "$BIN_PATH" ] || [ -L "$BIN_PATH" ]; } && [ ! -w "$INSTALL_DIR" ]; then
  echo "error: cannot remove $BIN_PATH; run as its owner (no automatic sudo)" >&2
  exit 1
fi

if [ "$KEEP_MCP" != "yes" ] && [ -x "$BIN_PATH" ]; then
  echo "==> removing sl-dbg from MCP-aware agents on this machine"
  # 'all' only touches configs that actually exist; safe on machines
  # without every agent installed.
  if ! "$BIN_PATH" mcp uninstall all; then
    echo "error: MCP cleanup failed; binary retained. Fix the config and retry, or use --keep-mcp." >&2
    exit 1
  fi
fi

if [ -e "$BIN_PATH" ] || [ -L "$BIN_PATH" ]; then
  echo "==> removing $BIN_PATH"
  rm -f "$BIN_PATH"
else
  echo "==> $BIN_PATH not found, skipping binary removal"
fi

echo "==> done"
cat <<EOF

What this did NOT remove:
  - Language adapters and caches (debugpy, dlv, Java launcher).
    See docs/ADAPTERS.md for removal; they may be shared by other tools.
  - Running daemons or debug sessions. Finish sessions and run
    '$BIN_PATH daemon stop' BEFORE uninstalling; restart MCP clients afterward.
  - Backups created during MCP registration ('*.bak.<timestamp>').
  - Workspace MCP registrations outside the current directory.

Reinstall later with:
  curl -fsSL https://raw.githubusercontent.com/y0geshpatil/sl-dbg/main/scripts/install.sh | bash
EOF
