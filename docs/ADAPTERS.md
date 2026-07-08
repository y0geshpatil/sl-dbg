# sl-dbg — Adapters

`sl-dbg` itself does not debug code. It drives **DAP adapters** — separate processes maintained by language teams (Microsoft, Google, JetBrains, LLVM, etc.) — that translate DAP requests into native debug operations.

This document explains how each supported language is wired up, what the user must have installed, and how `sl-dbg` auto-bootstraps missing adapters.

## Adapter Registry

`internal/adapter/registry.go` declares one entry per language:

```go
type Adapter struct {
    Lang           string                    // "python", "java", …
    DetectCommand  []string                  // e.g., ["python", "-c", "import debugpy"]
    LaunchCommand  func(cfg LaunchCfg) []string
    InstallSteps   []InstallStep
    DefaultPort    int
    Capabilities   []string                  // optional hints
}
```

## Per-Language Setup

### Python — `debugpy`
**Adapter:** `python -m debugpy.adapter`  
**Install:** `pip install --user debugpy`  
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
# java -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=*:5005 -jar app.jar
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

### Node.js — `vscode-js-debug`
**Adapter:** `js-debug` from `vscode-js-debug` releases (downloaded by sl-dbg)  
**Install:** auto-downloaded; or `npm install -g js-debug`  
**Target requirement:** Node 14+.

```bash
sl-dbg start --lang node --program app.js
sl-dbg attach --lang node --host localhost --port 9229
```

### C / C++ / Rust — `lldb-dap`
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

### .NET — `netcoredbg`
**Adapter:** `netcoredbg --interpreter=vscode`  
**Install:** auto-download from https://github.com/Samsung/netcoredbg/releases  
**Target requirement:** .NET 6+.

```bash
sl-dbg start --lang dotnet --program ./bin/Debug/net8.0/MyApp.dll
sl-dbg attach --lang dotnet --pid 12345
```

## Install Flow

Run `sl-dbg install-adapter <lang>` (or `sl-dbg install-adapter all`) after installing the binary.

### Java

`install-adapter java` tries three strategies in order:

1. **`$SL_DBG_JAVA_ADAPTER_URL`** — if this env var is set, download the jar from that URL directly.
2. **GitHub Releases auto-download** — when the running binary is a release build, the matching
   `sl-dbg-java-adapter.jar` is downloaded from `github.com/y0geshpatil/sl-dbg/releases`. This is
   the normal path for users who installed via `install.sh` or Homebrew (no Maven required).
3. **Local Maven build** — fallback for source-checkout installs. Finds
   `adapters/java-launcher/pom.xml` relative to the binary and runs
   `mvn -q -DskipTests package`. Requires Maven + JDK 11+.

In non-interactive (CI, agent) mode, if the adapter is missing `sl-dbg` returns:
```json
{"ok":false,"error":{"code":"ADAPTER_FAILED",
  "message":"adapter \"java\" not installed: ...",
  "hint":"Run `sl-dbg install-adapter java` to download and install the adapter automatically (no Maven required)."}}
```

### Python

`install-adapter python` runs `python3 -m pip install --user --upgrade debugpy`.
Requires Python 3.8+ on PATH.

### Go

`install-adapter go` runs `go install github.com/go-delve/delve/cmd/dlv@latest`.
Requires Go 1.21+ on PATH.

## Manual Override

To point sl-dbg at a custom or vendored jar, set an env var before starting the daemon:

```bash
export SL_DBG_JAVA_DEBUG_JAR=/path/to/my-java-adapter.jar
sl-dbg start --lang java --main com.example.App --classpath .
```

For python and go, install the adapter version you want by running the respective
package manager commands (`pip install debugpy==X.Y.Z`, `go install …@vX.Y.Z`) and
sl-dbg will pick up whatever is on PATH.

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
