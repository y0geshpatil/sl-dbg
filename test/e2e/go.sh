#!/usr/bin/env bash
# End-to-end test for the Go adapter (delve).
# Prereqs: Go and dlv on PATH or in a Go install directory.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
SLDBG="${SL_DBG_BIN:-$REPO/bin/sl-dbg}"

if ! command -v go >/dev/null 2>&1; then
  echo "SKIP: go not on PATH" >&2; exit 77
fi
DLV="$(command -v dlv || true)"
if [[ -z "$DLV" ]]; then
  GO_BIN="$(go env GOBIN)"
  IFS=: read -r -a GO_PATHS <<< "$(go env GOPATH)"
  for candidate in "${GO_BIN:+$GO_BIN/dlv}" "${GO_PATHS[@]/%//bin/dlv}" "$HOME/go/bin/dlv"; do
    if [[ -n "$candidate" && -x "$candidate" ]]; then
      DLV="$candidate"
      break
    fi
  done
fi
if [[ -z "$DLV" ]]; then
  echo "SKIP: dlv not found (run: sl-dbg install-adapter go)" >&2
  exit 77
fi
if [[ ! -x "$SLDBG" ]]; then
  echo "SKIP: sl-dbg not built at $SLDBG" >&2; exit 77
fi
DLV="$(cd "$(dirname "$DLV")" && pwd)/$(basename "$DLV")"

source "$SCRIPT_DIR/common.sh"
e2e_isolate
export GOCACHE="$E2E_ROOT/go-cache" GOPATH="$E2E_ROOT/go"
export PATH="$(dirname "$DLV"):$PATH"

FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
contains() { echo "$1" | grep -q "$2" || fail "expected $2 in: $1"; }

PROG="$REPO/examples/go/buggy.go"

echo "== start =="
OUT=$("$SLDBG" start --lang go --program "$PROG" --stop-on-entry)
contains "$OUT" '"state":"paused"'

echo "== break =="
OUT=$("$SLDBG" break "$PROG":24)
contains "$OUT" '"line":24'

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
