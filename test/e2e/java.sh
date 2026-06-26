#!/usr/bin/env bash
# End-to-end test for the Java adapter.
# Spins up a JDWP-suspended JVM, attaches sl-dbg, exercises:
#   attach, break (line + conditional), eval (qualified static + string concat),
#   watch, continue, locals, source, stop.
#
# Prereqs: java (JDK 11+) on PATH, sl-dbg-java-adapter.jar installed
#          (run: sl-dbg install-adapter java)
#
# Exit codes: 0 pass, 77 skipped (missing prereqs), 1 fail
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
SLDBG="${SL_DBG_BIN:-$REPO/bin/sl-dbg}"

if ! command -v java >/dev/null 2>&1; then
  echo "SKIP: java not on PATH" >&2; exit 77
fi
if [[ ! -f "$HOME/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar" ]]; then
  echo "SKIP: java adapter not installed (run: sl-dbg install-adapter java)" >&2; exit 77
fi
if [[ ! -x "$SLDBG" ]]; then
  echo "SKIP: sl-dbg not built at $SLDBG" >&2; exit 77
fi

FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
contains() { echo "$1" | grep -q "$2" || fail "expected $2 in: $1"; }

PORT=${SL_DBG_E2E_PORT:-15005}
SRC_DIR="$REPO/examples/java"
cd "$SRC_DIR"
javac -g Buggy.java 2>/dev/null || true

# Start suspended JVM in background.
java -agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=127.0.0.1:$PORT \
  -cp . Buggy >/tmp/sl-dbg-java-e2e.out 2>&1 &
JVM_PID=$!
trap 'kill -9 $JVM_PID 2>/dev/null || true; rm -f $SRC_DIR/*.class' EXIT

# Wait for port.
for _ in 1 2 3 4 5 6 7 8 9 10; do
  if nc -z 127.0.0.1 $PORT 2>/dev/null; then break; fi
  sleep 0.3
done

"$SLDBG" stop 2>/dev/null || true

echo "== attach =="
OUT=$("$SLDBG" attach --lang java --host 127.0.0.1 --port $PORT --source-root "$SRC_DIR")
contains "$OUT" '"reason":"attached"'

echo "== conditional break =="
OUT=$("$SLDBG" break "$SRC_DIR/Buggy.java":24 --if "item < 0")
contains "$OUT" '"line":24'

echo "== continue (should stop at first negative item) =="
OUT=$("$SLDBG" continue)
contains "$OUT" '"reason":"breakpoint"'

echo "== eval: qualified static call =="
OUT=$("$SLDBG" eval "Buggy.compute(item)")
contains "$OUT" '"result":"-1000"'

echo "== eval: string concat =="
OUT=$("$SLDBG" eval '"x=" + item')
contains "$OUT" '"result":"\\\"x=-1\\\""'

echo "== watch --add =="
OUT=$("$SLDBG" watch --add "item")
contains "$OUT" '"result":"-1"'

echo "== locals =="
OUT=$("$SLDBG" locals)
contains "$OUT" '"name":"item"'

echo "== source --around 3 =="
OUT=$("$SLDBG" source --file "$SRC_DIR/Buggy.java" --line 24 --around 3)
contains "$OUT" 'compute(item)'

echo "== stop =="
"$SLDBG" stop >/dev/null

if [[ $FAIL -gt 0 ]]; then
  echo "== $FAIL Java check(s) failed =="
  exit 1
fi
echo "== all Java e2e checks passed =="
