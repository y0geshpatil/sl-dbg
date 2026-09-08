# Platform Support

`sl-dbg` is developed and tested first on macOS, with Linux intended to be first-class. Windows is not yet supported.

## Support Matrix

| Platform | Status | Notes |
|---|---|---|
| macOS amd64 / arm64 | Release targets | Unit and adapter smoke coverage on macOS CI; Unix-domain sockets. |
| Linux amd64 / arm64 | Release targets | Unit and adapter smoke coverage on Linux CI; per-user Unix-domain sockets. |
| Windows | Not yet supported | Requires named pipes or a Windows-specific socket strategy before support is claimed. |

## macOS

The installer defaults to `~/.local/bin/sl-dbg` on both macOS and Linux,
creating the directory without sudo. Other manually managed locations include:

- `~/bin/sl-dbg`
- `/usr/local/bin/sl-dbg`
- `/opt/homebrew/bin/sl-dbg` on Apple Silicon Homebrew installs

The daemon uses a per-user Unix-domain socket. Keep the daemon local; remote debugging should happen through SSH tunnels or language-specific debug ports, not through a network-exposed `sl-dbg` daemon.

## Linux

The daemon socket path is:

```text
$XDG_RUNTIME_DIR/sl-dbg/daemon.sock
```

If `XDG_RUNTIME_DIR` is unset, `sl-dbg` falls back to the system temporary
directory (`$TMPDIR` when set; usually `/tmp` on Linux):

```text
/tmp/sl-dbg-$UID/daemon.sock
```

The socket directory is created with `0700` permissions and the socket is chmodded to `0600`.
`SL_DBG_SOCKET=/absolute/path/daemon.sock` selects an isolated daemon endpoint
(used by e2e tests); it takes precedence over the runtime directory.

Recommended install paths:

- `~/.local/bin/sl-dbg` for per-user installs
- `/usr/local/bin/sl-dbg` for system-wide installs managed by an administrator

## Windows

Windows is untested and not yet supported. Unix-domain sockets exist on Windows 10 build 17063 and newer, but Go support and filesystem semantics differ enough that `sl-dbg` should not claim Windows support yet.

A supported Windows port should use a named pipe with an ACL restricted to the current user, or another Windows-native IPC transport with equivalent access control.

## PATH Setup

### bash

```bash
mkdir -p "$HOME/.local/bin"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.bashrc"
. "$HOME/.bashrc"
```

### zsh

```zsh
mkdir -p "$HOME/.local/bin"
echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.zshrc"
. "$HOME/.zshrc"
```

### fish

```fish
mkdir -p "$HOME/.local/bin"
fish_add_path "$HOME/.local/bin"
```

Restart the shell after changing the user PATH.

The installer cannot modify its parent shell's PATH. If `sl-dbg` is still not
found, run `"$HOME/.local/bin/sl-dbg" version` directly. If an older binary wins,
check `command -v sl-dbg`, prepend the new directory, and restart the shell.
When moving from `/usr/local/bin`, either explicitly select that `INSTALL_DIR`
as its owner or re-register MCP clients to use the new absolute binary path.

Rerunning the installer upgrades the binary, not running processes. Finish debug
sessions and run `sl-dbg daemon stop`; restart MCP clients afterward. The
installer and uninstaller never stop live debugging processes automatically.

## How to Verify Your Install

```bash
sl-dbg version
sl-dbg adapters
```

`sl-dbg version` should print structured version JSON. `sl-dbg adapters` should list available adapter installers and any detected local adapters.
