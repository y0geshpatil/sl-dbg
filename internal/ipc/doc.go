// Package ipc implements the CLI<->daemon transport.
//
// On Unix systems, this is a Unix domain socket at
// $XDG_RUNTIME_DIR/sl-dbg/daemon.sock (fallback /tmp/sl-dbg-$UID.sock).
// On Windows, a named pipe \\.\pipe\sl-dbg-<user>.
//
// Status: scaffold only.
package ipc
