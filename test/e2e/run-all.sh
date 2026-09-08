#!/usr/bin/env bash
# Runs every e2e test under test/e2e. Skip-on-prereq (exit 77) does NOT fail the run.
# Each test owns its isolated daemon and cleans it up, including when run alone.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS=0; SKIP=0; FAIL=0
for t in "$DIR"/*.sh; do
  case "$(basename "$t")" in run-all.sh|common.sh) continue ;; esac
  echo
  echo "############### $(basename "$t") ###############"
  bash "$t"
  rc=$?
  case "$rc" in
    0)  PASS=$((PASS+1)) ;;
    77) SKIP=$((SKIP+1)); echo "(skipped)" ;;
    *)  FAIL=$((FAIL+1)) ;;
  esac
done
echo
echo "############### summary: pass=$PASS skip=$SKIP fail=$FAIL ###############"
[[ $FAIL -eq 0 ]]
