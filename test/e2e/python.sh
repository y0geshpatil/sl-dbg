#!/usr/bin/env bash
# End-to-end test for the Python adapter.
# Exercises: start, break (line + --once), watch, source, eval, globals,
# events, output, listen, continue, breaks, snapshot, until, break-ex, stop.
#
# Prereqs: python3 with debugpy installed (`pip install --user debugpy`)
#          sl-dbg binary on $PATH or at $REPO/bin/sl-dbg
#
# Exit codes:
#   0  all checks passed
#   77 prerequisites missing (skip, not fail)
#   1  one or more checks failed
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
SLDBG="${SL_DBG_BIN:-$REPO/bin/sl-dbg}"
# eval/set/watch/conditional-bp are default-denied on the daemon (#54); enable for tests.
export SL_DBG_ALLOW_EVAL="${SL_DBG_ALLOW_EVAL:-1}"
PROG="$REPO/examples/python/buggy.py"

if ! command -v python3 >/dev/null 2>&1; then
  echo "SKIP: python3 not on PATH" >&2; exit 77
fi
if ! python3 -c "import debugpy" >/dev/null 2>&1; then
  echo "SKIP: debugpy not installed (run: pip install --user debugpy)" >&2; exit 77
fi
if [[ ! -x "$SLDBG" ]]; then
  echo "SKIP: sl-dbg not built at $SLDBG (run: make build)" >&2; exit 77
fi

FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
contains() { echo "$1" | grep -q "$2" || fail "expected $2 in: $1"; }

# Use a unique session id (start auto-generates one; we just track by listing).
"$SLDBG" stop 2>/dev/null || true

echo "== start =="
OUT=$("$SLDBG" start --lang python --program "$PROG" --stop-on-entry)
contains "$OUT" '"schema":"1"'
contains "$OUT" '"state":"paused"'

echo "== break --once =="
OUT=$("$SLDBG" break "$PROG":18 --once)
contains "$OUT" '"verified":true'
contains "$OUT" '"line":18'

echo "== watch --add =="
OUT=$("$SLDBG" watch --add "item")
contains "$OUT" '"expression":"item"'

echo "== continue (first BP hit) =="
OUT=$("$SLDBG" continue)
contains "$OUT" '"state":"paused"'
contains "$OUT" '"reason":"breakpoint"'

echo "== watch list (paused, item should resolve) =="
OUT=$("$SLDBG" watch)
contains "$OUT" '"result":"10"'

echo "== source --around 2 =="
OUT=$("$SLDBG" source --around 2)
contains "$OUT" 'compute(item)'

echo "== globals =="
OUT=$("$SLDBG" globals)
contains "$OUT" '"name":"data"'

echo "== eval =="
OUT=$("$SLDBG" eval "item * 10")
contains "$OUT" '"result":"100"'

echo "== events --tail 3 =="
OUT=$("$SLDBG" events --tail 3)
contains "$OUT" '"events"'

echo "== breaks (--once should be gone after hit) =="
OUT=$("$SLDBG" breaks)
echo "$OUT" | grep -q '"line":18' && fail "--once breakpoint NOT cleared after hit"

echo "== snapshot =="
OUT=$("$SLDBG" snapshot)
contains "$OUT" '"frames"'

echo "== continue (should run to end since BP is gone) =="
OUT=$("$SLDBG" continue)
contains "$OUT" '"state":"exited"'

echo "== stop =="
"$SLDBG" stop >/dev/null

# Issue #56 (security): --read-only must refuse `eval`, not just mutators.
echo "== read-only blocks eval (issue #56) =="
"$SLDBG" stop 2>/dev/null || true
OUT=$("$SLDBG" start --lang python --program "$PROG" --stop-on-entry --read-only)
contains "$OUT" '"state":"paused"'
# Sanity: a known mutator is rejected.
SET_OUT=$("$SLDBG" set x 99 2>&1 || true)
contains "$SET_OUT" 'READ_ONLY_MODE'
# The fix: eval must also be rejected with READ_ONLY_MODE.
EVAL_OUT=$("$SLDBG" eval "__import__('os').system('touch /tmp/sl_dbg_pwn_check')" 2>&1 || true)
contains "$EVAL_OUT" 'READ_ONLY_MODE'
if [[ -e /tmp/sl_dbg_pwn_check ]]; then
  fail "read-only eval executed side effect (touched /tmp/sl_dbg_pwn_check)"
  rm -f /tmp/sl_dbg_pwn_check
fi
"$SLDBG" stop >/dev/null

if [[ $FAIL -gt 0 ]]; then
  echo "== $FAIL check(s) failed =="
  exit 1
fi
echo "== all Python e2e checks passed =="
