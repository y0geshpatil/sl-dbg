#!/usr/bin/env bash
# Regression for standalone-suite isolation and cleanup on failure.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$SCRIPT_DIR/../.." && pwd)"
if ! command -v python3 >/dev/null 2>&1; then
  echo "SKIP: python3 needed for isolation regression" >&2; exit 77
fi
umask 077
FIXTURE="$REPO/.sl-dbg-isolation-${BASHPID:-$$}-$RANDOM"
mkdir "$FIXTURE"
trap 'rm -rf -- "$FIXTURE"' EXIT
mkdir "$FIXTURE/user-home" "$FIXTURE/user-state"
printf 'untouched\n' > "$FIXTURE/user-daemon"
printf 'untouched\n' > "$FIXTURE/user-state/safe-policy.env"
cat > "$FIXTURE/sl-dbg" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == "daemon stop" ]]
[[ "$SL_DBG_SOCKET" == "daemon.sock" && "$HOME" == "$PWD/home" ]]
printf '%s\n' "$PWD" >> "$E2E_TEST_LOG"
rm -- "$SL_DBG_SOCKET"
SH
chmod 700 "$FIXTURE/sl-dbg"
export E2E_TEST_LOG="$FIXTURE/calls" E2E_TEST_ROOT="$FIXTURE"
export E2E_TEST_COMMON="$SCRIPT_DIR/common.sh" E2E_TEST_REPO="$REPO"
set +e
HOME="$FIXTURE/user-home" XDG_STATE_HOME="$FIXTURE/user-state" \
  SL_DBG_SOCKET="$FIXTURE/user-daemon" SL_DBG_AUDIT_LOG="$FIXTURE/user-state/audit" \
  bash -c '
    set -euo pipefail
    REPO="$E2E_TEST_REPO"
    SLDBG="$E2E_TEST_ROOT/sl-dbg"
    source "$E2E_TEST_COMMON"
    e2e_isolate
    [[ "$HOME" == "$E2E_ROOT/home" && "$XDG_STATE_HOME" == "$E2E_ROOT/state" ]]
    [[ "$TMPDIR" == "$E2E_ROOT/temp" && -z "${SL_DBG_AUDIT_LOG+x}" ]]
    printf "%s\n" "$E2E_ROOT" > "$E2E_TEST_ROOT/owned-root"
    python3 -c "import socket; s=socket.socket(socket.AF_UNIX); s.bind(\"daemon.sock\")"
    exit 42
  '
STATUS=$?
set -e
[[ "$STATUS" == 42 ]] || { echo "FAIL: cleanup changed exit status: $STATUS" >&2; exit 1; }
OWNED_ROOT="$(cat "$FIXTURE/owned-root")"
[[ ! -e "$OWNED_ROOT" ]] || { echo "FAIL: suite scratch directory leaked" >&2; exit 1; }
[[ "$(cat "$E2E_TEST_LOG")" == "$OWNED_ROOT" ]] || { echo "FAIL: cleanup used wrong daemon" >&2; exit 1; }
[[ "$(cat "$FIXTURE/user-daemon")" == untouched ]]
[[ "$(cat "$FIXTURE/user-state/safe-policy.env")" == untouched ]]
[[ ! -e "$FIXTURE/user-state/audit" ]]
[[ -z "$(ls -A "$FIXTURE/user-home")" ]]
echo "== all isolation regression checks passed =="
