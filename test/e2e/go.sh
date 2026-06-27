#!/usr/bin/env bash
# End-to-end test for the Go adapter (delve).
# Prereqs: dlv on PATH (`go install github.com/go-delve/delve/cmd/dlv@latest`)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
SLDBG="${SL_DBG_BIN:-$REPO/bin/sl-dbg}"
# eval/set/watch/conditional-bp are default-denied on the daemon (#54); enable for tests.
export SL_DBG_ALLOW_EVAL="${SL_DBG_ALLOW_EVAL:-1}"

if ! command -v dlv >/dev/null 2>&1; then
  echo "SKIP: dlv not on PATH (run: go install github.com/go-delve/delve/cmd/dlv@latest)" >&2
  exit 77
fi
if [[ ! -x "$SLDBG" ]]; then
  echo "SKIP: sl-dbg not built at $SLDBG" >&2; exit 77
fi

FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
contains() { echo "$1" | grep -q "$2" || fail "expected $2 in: $1"; }

PROG="$REPO/examples/go/buggy.go"

"$SLDBG" stop 2>/dev/null || true

echo "== start =="
OUT=$("$SLDBG" start --lang go --program "$PROG" --stop-on-entry)
contains "$OUT" '"state":"paused"'

echo "== break =="
OUT=$("$SLDBG" break "$PROG":23)
contains "$OUT" '"line":23'

echo "== continue =="
OUT=$("$SLDBG" continue)
contains "$OUT" '"reason":"breakpoint"'

echo "== locals =="
OUT=$("$SLDBG" locals)
contains "$OUT" '"name":"item"'

echo "== eval =="
OUT=$("$SLDBG" eval "item")
contains "$OUT" '"result"'

echo "== stop =="
"$SLDBG" stop >/dev/null

if [[ $FAIL -gt 0 ]]; then
  echo "== $FAIL Go check(s) failed =="
  exit 1
fi
echo "== all Go e2e checks passed =="
