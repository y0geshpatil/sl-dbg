#!/usr/bin/env bash
# Runs every e2e test under test/e2e. Skip-on-prereq (exit 77) does NOT fail the run.
# Each test runs against a freshly-restarted daemon so prior state never leaks.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/../.." && pwd)"
SLDBG="${SL_DBG_BIN:-$REPO/bin/sl-dbg}"
PASS=0; SKIP=0; FAIL=0
for t in "$DIR"/*.sh; do
  case "$(basename "$t")" in run-all.sh) continue ;; esac
  echo
  echo "############### $(basename "$t") ###############"
  # Kill any existing daemon so each suite starts clean.
  "$SLDBG" daemon stop >/dev/null 2>&1 || true
  sleep 0.3
  bash "$t"
  rc=$?
  case "$rc" in
    0)  PASS=$((PASS+1)) ;;
    77) SKIP=$((SKIP+1)); echo "(skipped)" ;;
    *)  FAIL=$((FAIL+1)) ;;
  esac
done
"$SLDBG" daemon stop >/dev/null 2>&1 || true
echo
echo "############### summary: pass=$PASS skip=$SKIP fail=$FAIL ###############"
[[ $FAIL -eq 0 ]]
