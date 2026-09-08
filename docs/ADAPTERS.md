# sl-dbg — Adapters

`sl-dbg` itself does not debug code. It drives **DAP adapters** — separate processes maintained by language teams (Microsoft, Google, JetBrains, LLVM, etc.) — that translate DAP requests into native debug operations.

This document explains how each supported language is wired up and how to install its adapter explicitly. Python, Go, and Java are supported; the other languages below are planned, not registered.

## Adapter Registry

`internal/adapter/registry.go` defines the `Spec` registered by each language:

```go
type Spec struct {
    Lang            string
    AdapterID       string
    Detect          func() (string, error)
    LaunchAdapter   func() ([]string, Transport, error)
    BuildLaunchArgs func(LaunchCfg) (map[string]interface{}, error)
    BuildAttachArgs func(AttachCfg) (map[string]interface{}, error)
    InstallHint     string
}
```

## Per-Language Setup

### Python — `debugpy`
**Adapter:** `<resolved-python> -m debugpy.adapter`

**Install:** `sl-dbg install-adapter python` (isolated virtual environment; no system pip changes)

**Target requirement:** Python 3.8+ on the target.

```bash
sl-dbg start --lang python --program app.py
sl-dbg attach --lang python --host localhost --port 5678
sl-dbg attach --lang python --pid 12345
```

For attach by PID, the target must be running with debugpy already loaded — either via:
- `python -m debugpy --listen 5678 --wait-for-client app.py`, or
- In-code: `import debugpy; debugpy.listen(5678); debugpy.wait_for_client()`

### Java — `sl-dbg-java-adapter` (embedded launcher)
**Adapter:** `java -jar ~/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar --port {PORT}`  
**Install:** `sl-dbg install-adapter java` — downloads the pre-built jar from the matching
GitHub Release automatically. No Maven, no source checkout required.  
**Target requirement:** JDK 8+ on the machine running the target process.

```bash
# Launch a fresh JVM
sl-dbg start --lang java --main com.example.App --classpath ./build/libs/*

# Stop on entry
sl-dbg start --lang java --main Foo --classpath . --stop-on-entry

# Attach (target must have JDWP enabled)
# java -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:5005 -jar app.jar
sl-dbg attach --lang java --host localhost --port 5005

# Source mapping for remote targets
sl-dbg attach --lang java --host prod.svc --port 5005 \
              --source-root ./src/main/java \
              --source-root ./target/generated-sources
```

**Jar resolution order:**
1. `$SL_DBG_JAVA_DEBUG_JAR` env var (override for custom builds)
2. `~/.cache/sl-dbg/adapters/sl-dbg-java-adapter.jar` (installed by `install-adapter java`)
3. `<repo>/adapters/java-launcher/target/sl-dbg-java-adapter.jar` (local dev build)

### Go — `dlv dap`
**Adapter:** `dlv dap`  
**Install:** `go install github.com/go-delve/delve/cmd/dlv@latest`  
**Target requirement:** Go toolchain.

```bash
sl-dbg start --lang go --program ./cmd/myapp
sl-dbg attach --lang go --host localhost --port 2345
sl-dbg attach --lang go --pid 12345
```

### Node.js — `vscode-js-debug` (planned, not supported)
**Adapter:** `js-debug` from `vscode-js-debug` releases (downloaded by sl-dbg)  
**Install:** auto-downloaded; or `npm install -g js-debug`  
**Target requirement:** Node 14+.

```bash
sl-dbg start --lang node --program app.js
sl-dbg attach --lang node --host localhost --port 9229
```

### C / C++ / Rust — `lldb-dap` (planned, not supported)
**Adapter:** `lldb-dap` (ships with modern LLVM / Xcode)  
**Install:** `brew install llvm` (macOS) / `apt install lldb` (Debian/Ubuntu) / Xcode Command Line Tools  
**Target requirement:** Debug symbols in the binary (`-g` for clang/gcc, `cargo build` for Rust).

```bash
sl-dbg start --lang cpp --program ./a.out
sl-dbg attach --lang cpp --pid 12345
```

For Rust:
```bash
sl-dbg start --lang rust --program ./target/debug/myapp
```

### .NET — `netcoredbg` (planned, not supported)
**Adapter:** `netcoredbg --interpreter=vscode`  
**Install:** auto-download from https://github.com/Samsung/netcoredbg/releases  
**Target requirement:** .NET 6+.

```bash
sl-dbg start --lang dotnet --program ./bin/Debug/net8.0/MyApp.dll
sl-dbg attach --lang dotnet --pid 12345
```

## Install Flow

Run `sl-dbg install-adapter <lang>` (or `sl-dbg install-adapter all`) after installing the binary.
`all` attempts every supported adapter and exits nonzero if any prerequisite or install fails.
Use `--force` to reinstall. Run installation and the daemon as the same user, with the same
`HOME`, `PATH`, and toolchain configuration. Restart an existing daemon after changing its
environment; an already running daemon does not inherit later shell changes.

### Java

`install-adapter java` keeps a valid cached jar unless `--force` is passed. Installation requires
`java` on PATH (JDK 11+). When installation is needed, it chooses one of these sources:

**After upgrading the sl-dbg binary, run `sl-dbg install-adapter java --force`** to fetch the jar
from the new binary's matching release tag. Cached jars are not version-tracked or automatically
matched to the binary; without `--force`, a valid older cached jar is retained.

1. **`$SL_DBG_JAVA_ADAPTER_URL`** — download from this HTTPS URL, requiring
   **`$SL_DBG_JAVA_ADAPTER_SHA256`** to contain the expected 64-character hex digest.
2. **GitHub Releases auto-download** — when the running binary is a release build, the matching
   `sl-dbg-java-adapter.jar` is downloaded from `github.com/y0geshpatil/sl-dbg/releases`. This is
   the normal path for users who installed a complete release via `install.sh` (no Maven required).
   Both the jar and `sl-dbg-java-adapter.jar.sha256` sidecar are fetched from the **same version tag**.
   Missing or invalid checksums fail closed, including older releases without a sidecar.
   A failed release download preserves its underlying error and never silently substitutes a local build.
3. **Local Maven build** — for development binaries only, when no custom URL is supplied. Finds
   `adapters/java-launcher/pom.xml` relative to the binary and runs
   `mvn -q -DskipTests package`. Requires Maven + JDK 11+.

Downloads require HTTPS (including redirects), have a two-minute timeout per request and a
256 MiB jar size limit. The SHA-256 checksum and jar structure are checked before atomically
replacing the destination. Interrupted downloads, checksum mismatches, and invalid Maven
output leave the previous jar intact. A release sidecar has standard `sha256sum` format:
`<64 hex characters>  sl-dbg-java-adapter.jar`.
The checksum detects corruption; authenticity relies on the HTTPS release source.

In non-interactive (CI, agent) mode, if the adapter is missing `sl-dbg` returns:
```json
{"ok":false,"error":{"code":"ADAPTER_FAILED",
  "message":"adapter \"java\" not installed: ...",
  "hint":"Run `sl-dbg install-adapter java` to download and install the adapter automatically (no Maven required)."}}
```

### Python

`install-adapter python` first checks for an importable debugpy. Otherwise it uses `python3`
(falling back to `python`) to create `~/.cache/sl-dbg/adapters/python` with `-m venv`, then runs
that environment's `bin/python -m pip install --upgrade debugpy`. It does not use `--user`,
`sudo`, or `--break-system-packages`, and works with PEP 668-managed Python installations
and activated project virtual environments. Python's venv support is required; Debian/Ubuntu
may need `python3-venv`. Pip/network failures are reported without changing system packages.

Detection and launch use the same order: the managed environment, then `python3`, then
`python` on PATH, selecting an interpreter that imports debugpy. Adapter probes time out after
five seconds. The **target program** still uses `python3` (or `python`) on PATH, not the adapter
environment, preserving project dependencies. The target interpreter comes from the daemon's
startup `PATH`: activate the desired target environment **before starting the daemon**.
When switching project virtual environments, finish active debug sessions, stop the daemon
with `sl-dbg daemon stop`, activate the new environment, then start a new session so the daemon
inherits the updated `PATH`. `--force` installs/upgrades the managed adapter even if another
debugpy exists.

### Go

`install-adapter go` runs `go install github.com/go-delve/delve/cmd/dlv@latest`.
Requires a Go toolchain supported by the current Delve release on PATH.
Detection, installation checks, and launch share executable resolution: PATH, the configured
Go install directory (including `go env -w GOBIN`/`GOPATH` settings), exported `$GOBIN`,
`$GOPATH/bin`, and `~/go/bin`. These directories need not be on PATH. Non-executable files
and directories named `dlv` are not accepted. Installation verifies the resulting executable
can be resolved before reporting success.

## Manual Override

To point sl-dbg at a custom or vendored jar, set an env var before starting the daemon:

```bash
export SL_DBG_JAVA_DEBUG_JAR=/path/to/my-java-adapter.jar
sl-dbg start --lang java --main com.example.App --classpath .
```

For Python, install a chosen version in the managed environment with
`~/.cache/sl-dbg/adapters/python/bin/python -m pip install debugpy==X.Y.Z`, or install it in
your active Python environment if no managed adapter exists. For Go, run
`go install github.com/go-delve/delve/cmd/dlv@vX.Y.Z`; sl-dbg resolves it as described above.

## Adding a New Adapter

To support a new language, add a file to `internal/adapter/<lang>.go` using the
`adapter.Spec` struct and register it via `adapter.Register()` in an `init()` function.
See `internal/adapter/python.go` for the simplest example.

## Compatibility Matrix

| Adapter | Min version | OSes | Attach by PID | Conditional BP | Logpoints | Reverse step |
|---|---|---|---|---|---|---|
| debugpy | 1.6.0 | mac/linux | ✅ | ✅ | ✅ | ❌ |
| sl-dbg-java-adapter | 0.1.0 | mac/linux | via JDWP | ✅ | ✅ | ❌ |
| dlv dap | 1.21+ | mac/linux | ✅ | ✅ | ✅ | ❌ |

`sl-dbg adapters` shows the live status on a given machine.
