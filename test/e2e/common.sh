#!/usr/bin/env bash
# Sourced after prerequisite checks, never run as a suite.
e2e_isolate() {
  SLDBG="$(cd "$(dirname "$SLDBG")" && pwd)/$(basename "$SLDBG")"
  umask 077
  E2E_ROOT="$REPO/.sl-dbg-e2e-${BASHPID:-$$}-$RANDOM"
  mkdir "$E2E_ROOT"
  trap e2e_cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  # A short relative socket works even in deep worktrees on macOS. The daemon
  # inherits this cwd; all CLI calls, including cleanup, keep the same cwd.
  cd "$E2E_ROOT"
  local key
  for key in $(compgen -v SL_DBG_); do
    unset "$key"
  done
  export SL_DBG_SOCKET=daemon.sock SL_DBG_ALLOW_EVAL=1
  export HOME="$E2E_ROOT/home"
  export XDG_CONFIG_HOME="$E2E_ROOT/config" XDG_STATE_HOME="$E2E_ROOT/state"
  export XDG_CACHE_HOME="$E2E_ROOT/cache" XDG_DATA_HOME="$E2E_ROOT/data"
  export XDG_RUNTIME_DIR="$E2E_ROOT/run"
  export TMPDIR="$E2E_ROOT/temp" TMP="$E2E_ROOT/temp" TEMP="$E2E_ROOT/temp"
  export PYTHONDONTWRITEBYTECODE=1
  mkdir "$HOME" "$XDG_CONFIG_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME" \
    "$XDG_DATA_HOME" "$XDG_RUNTIME_DIR" "$TMPDIR"
}

e2e_cleanup() {
  local status=$?
  trap - EXIT
  cd "$E2E_ROOT" || exit "$status"
  # Keep this guard for compatibility when testing an older installed binary.
  if [[ -S "$SL_DBG_SOCKET" ]]; then
    "$SLDBG" daemon stop >/dev/null 2>&1 || true
    local attempt
    for attempt in {1..30}; do
      [[ -S "$SL_DBG_SOCKET" ]] || break
      sleep 0.1
    done
  fi
  if [[ -n "${JVM_PID:-}" ]]; then
    kill "$JVM_PID" 2>/dev/null || true
    wait "$JVM_PID" 2>/dev/null || true
  fi
  cd "$REPO" || exit "$status"
  rm -rf -- "$E2E_ROOT"
  exit "$status"
}
