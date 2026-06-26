#!/usr/bin/env bash
# Creates a PRIVATE GitHub repo `y0geshpatil/sl-dbg` and pushes `main`.
# Run this once after `gh auth login`.

set -euo pipefail

REPO="y0geshpatil/sl-dbg"
DESC="sl-dbg — stateless, JSON-first command-line debugger with embedded daemon + MCP server (Python/Go/Java)"

if ! gh auth status >/dev/null 2>&1; then
  echo "→ Logging in to GitHub..."
  gh auth login -h github.com -p https -w
fi

cd "$(dirname "$0")/.."

if git remote get-url origin >/dev/null 2>&1; then
  echo "✓ origin already set: $(git remote get-url origin)"
else
  echo "→ Creating private repo $REPO and pushing main..."
  gh repo create "$REPO" --private --description "$DESC" --source=. --remote=origin --push
fi

git push -u origin main
echo "✓ Pushed. View at: https://github.com/$REPO"
