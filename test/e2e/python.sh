#!/usr/bin/env bash
# End-to-end test for the Python adapter.
# Exercises: start, break (line + --once), watch, source, eval, globals,
# events, output, listen, continue, breaks, snapshot, until, break-ex, stop.
#
# Prereqs: PATH python3 with debugpy available (e.g. a development venv)
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
PROG="$REPO/examples/python/buggy.py"

if ! command -v python3 >/dev/null 2>&1; then
  echo "SKIP: python3 not on PATH" >&2; exit 77
fi
DEBUGPY_ROOT=""
# Do not transplant a managed venv's packages into a different target Python.
if ! DEBUGPY_ROOT=$(PYTHONDONTWRITEBYTECODE=1 python3 -c 'import pathlib, debugpy; print(pathlib.Path(debugpy.__file__).parent.parent)' 2>/dev/null); then
  echo "SKIP: PATH python3 cannot import debugpy (use a debugpy-enabled Python environment)" >&2; exit 77
fi
if [[ ! -x "$SLDBG" ]]; then
  echo "SKIP: sl-dbg not built at $SLDBG (run: make build)" >&2; exit 77
fi

source "$SCRIPT_DIR/common.sh"
e2e_isolate
STOP_OUT=$("$SLDBG" daemon stop)
echo "$STOP_OUT" | grep -q '"shutdown":"not running"'
[[ ! -S "$SL_DBG_SOCKET" ]]
export PYTHONPATH="$DEBUGPY_ROOT${PYTHONPATH:+:$PYTHONPATH}"
export SL_DBG_E2E_MARKER="$E2E_ROOT/read-only-side-effect"

FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
contains() { echo "$1" | grep -q "$2" || fail "expected $2 in: $1"; }

echo "== install-adapter python detects existing debugpy without installing =="
OUT=$("$SLDBG" install-adapter python 2>&1)
contains "$OUT" 'debugpy already available'
[[ ! -e "$HOME/.cache/sl-dbg/adapters/python" ]] || fail "detection created a managed environment"
[[ ! -e "$SL_DBG_SOCKET" ]] || fail "install-adapter started a daemon"

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
OUT=$("$SLDBG" start --lang python --program "$PROG" --stop-on-entry --read-only)
contains "$OUT" '"state":"paused"'
# Sanity: a known mutator is rejected.
SET_OUT=$("$SLDBG" set x 99 2>&1 || true)
contains "$SET_OUT" 'READ_ONLY_MODE'
# The fix: eval must also be rejected with READ_ONLY_MODE.
EVAL_OUT=$("$SLDBG" eval "__import__('pathlib').Path(__import__('os').environ['SL_DBG_E2E_MARKER']).touch()" 2>&1 || true)
contains "$EVAL_OUT" 'READ_ONLY_MODE'
if [[ -e "$SL_DBG_E2E_MARKER" ]]; then
  fail "read-only eval executed side effect (created owned marker)"
fi
"$SLDBG" stop >/dev/null

if [[ $FAIL -gt 0 ]]; then
  echo "== $FAIL check(s) failed =="
  exit 1
fi
echo "== all Python e2e checks passed =="
