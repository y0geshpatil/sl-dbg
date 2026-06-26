# Platform Support

`sl-dbg` is developed and tested first on macOS, with Linux intended to be first-class. Windows is not yet supported.

## Support Matrix

| Platform | Status | Notes |
|---|---|---|
| macOS | Tested / assumed working | Primary development platform. Unix-domain sockets are supported. |
| Linux | Supported target | Uses per-user runtime directories when available; see socket paths below. |
| Windows | Not yet supported | Requires named pipes or a Windows-specific socket strategy before support is claimed. |

## macOS

Install the binary somewhere on your `PATH`, commonly:

- `~/bin/sl-dbg`
- `/usr/local/bin/sl-dbg`
- `/opt/homebrew/bin/sl-dbg` on Apple Silicon Homebrew installs

The daemon uses a per-user Unix-domain socket. Keep the daemon local; remote debugging should happen through SSH tunnels or language-specific debug ports, not through a network-exposed `sl-dbg` daemon.

## Linux

The daemon socket path is:

```text
$XDG_RUNTIME_DIR/sl-dbg/daemon.sock
```

If `XDG_RUNTIME_DIR` is unset, `sl-dbg` falls back to:

```text
/tmp/sl-dbg-$UID/daemon.sock
```

The socket directory is created with `0700` permissions and the socket is chmodded to `0600`.

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

### PowerShell

PowerShell snippets are for future Windows support or cross-shell convenience on macOS/Linux:

```pwsh
$dir = Join-Path $HOME ".local/bin"
New-Item -ItemType Directory -Force -Path $dir | Out-Null
[Environment]::SetEnvironmentVariable("PATH", "$dir$([IO.Path]::PathSeparator)$env:PATH", "User")
```

Restart the shell after changing the user PATH.

## How to Verify Your Install

```bash
sl-dbg version
sl-dbg adapters
```

`sl-dbg version` should print structured version JSON. `sl-dbg adapters` should list available adapter installers and any detected local adapters.
