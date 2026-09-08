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
if ! command -v javac >/dev/null 2>&1; then
  echo "SKIP: javac not on PATH (install a JDK)" >&2; exit 77
fi
JAVA_JAR="${SL_DBG_JAVA_DEBUG_JAR:-$HOME/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar}"
if [[ ! -f "$JAVA_JAR" ]]; then
  echo "SKIP: java adapter not installed (run: sl-dbg install-adapter java)" >&2; exit 77
fi
if [[ ! -x "$SLDBG" ]]; then
  echo "SKIP: sl-dbg not built at $SLDBG" >&2; exit 77
fi

JAVA_JAR="$(cd "$(dirname "$JAVA_JAR")" && pwd)/$(basename "$JAVA_JAR")"
source "$SCRIPT_DIR/common.sh"
e2e_isolate
export SL_DBG_JAVA_DEBUG_JAR="$JAVA_JAR"

FAIL=0
fail() { echo "FAIL: $*" >&2; FAIL=$((FAIL+1)); }
contains() { echo "$1" | grep -q "$2" || fail "expected $2 in: $1"; }

SRC_DIR="$E2E_ROOT/source"
mkdir "$SRC_DIR" "$E2E_ROOT/classes"
cp "$REPO/examples/java/Buggy.java" "$SRC_DIR/Buggy.java"
javac -g -d "$E2E_ROOT/classes" "$SRC_DIR/Buggy.java"

# Start suspended JVM in background.
JVM_LOG="$E2E_ROOT/jvm.log"
java -agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=127.0.0.1:0 \
  -cp "$E2E_ROOT/classes" Buggy >"$JVM_LOG" 2>&1 &
JVM_PID=$!

# JDWP reports its kernel-assigned port; probing it with nc consumes a handshake.
PORT=""
for _ in {1..100}; do
  PORT=$(sed -n 's/^Listening for transport dt_socket at address: \([0-9][0-9]*\).*$/\1/p' "$JVM_LOG" | head -n 1)
  [[ -n "$PORT" ]] && break
  kill -0 "$JVM_PID" 2>/dev/null || break
  sleep 0.1
done
if [[ -z "$PORT" ]]; then
  echo "FAIL: JVM did not report a JDWP port" >&2
  cat "$JVM_LOG" >&2
  exit 1
fi

echo "== attach =="
OUT=$("$SLDBG" attach --lang java --host 127.0.0.1 --port "$PORT" --source-root "$SRC_DIR")
contains "$OUT" '"reason":"attached"'

echo "== conditional break =="
OUT=$("$SLDBG" break "$SRC_DIR/Buggy.java":24 --if "item < 0")
contains "$OUT" '"line":24'

echo "== continue (should stop at first negative item) =="
OUT=$("$SLDBG" continue)
contains "$OUT" '"reason":"breakpoint"'
if [[ "$OUT" != *'"reason":"breakpoint"'* ]]; then
  "$SLDBG" output >&2
  "$SLDBG" events --tail 20 >&2
  exit 1
fi

echo "== eval: qualified static call =="
OUT=$("$SLDBG" eval "Buggy.compute(item)")
contains "$OUT" '"result":"-1000"'

echo "== eval: string concat =="
OUT=$("$SLDBG" eval '"x=" + item')
contains "$OUT" '"result":"\\\"x=-1\\\""'

echo "== break-fn (Class.method form gets normalized to Class#method) =="
# Buggy is already loaded (we are paused inside Buggy.process), so the
# adapter must verify the function breakpoint immediately. Regression test
# for issue #59: pre-fix this stayed verified:false forever.
OUT=$("$SLDBG" break-fn "Buggy.compute")
contains "$OUT" '"function":"Buggy.compute"'
contains "$OUT" '"verified":true'

echo "== breaks retains function verification =="
OUT=$("$SLDBG" breaks)
contains "$OUT" '"function":"Buggy.compute"[^}]*"verified":true'

echo "== watch --add =="
OUT=$("$SLDBG" watch --add "item")
contains "$OUT" '"result":"-1"'

echo "== locals =="
OUT=$("$SLDBG" locals)
contains "$OUT" '"name":"item"'

echo "== source --around 3 =="
OUT=$("$SLDBG" source --file "$SRC_DIR/Buggy.java" --line 24 --around 3)
contains "$OUT" 'compute(item)'

echo "== continue hits the verified function breakpoint =="
OUT=$("$SLDBG" continue)
contains "$OUT" '"reason":"function breakpoint"'
OUT=$("$SLDBG" stack)
contains "$OUT" 'Buggy.compute'

echo "== stop =="
"$SLDBG" stop >/dev/null

echo "== launch with entry pause =="
OUT=$("$SLDBG" start --lang java --main Buggy --classpath "$E2E_ROOT/classes" \
  --cwd "$SRC_DIR" --source-root "$SRC_DIR" --stop-on-entry)
contains "$OUT" '"state":"paused"'
contains "$OUT" '"reason":"entry"'

echo "== unconditional line breakpoint after launch =="
OUT=$("$SLDBG" break "$SRC_DIR/Buggy.java":24)
contains "$OUT" '"verified":true'
for ITEM in 10 5; do
  OUT=$("$SLDBG" continue)
  contains "$OUT" '"reason":"breakpoint"'
  contains "$OUT" '"line":24'
  OUT=$("$SLDBG" eval "item")
  contains "$OUT" "\"result\":\"$ITEM\""
done
"$SLDBG" stop >/dev/null

if [[ $FAIL -gt 0 ]]; then
  echo "== $FAIL Java check(s) failed =="
  exit 1
fi
echo "== all Java e2e checks passed =="
