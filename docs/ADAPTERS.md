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

### Java — `java-debug` (Microsoft)
**Adapter:** `java -jar /path/to/java-debug.jar`  
**Install:** `sl-dbg` auto-downloads `com.microsoft.java.debug.plugin-<ver>.jar` from GitHub releases into `~/.cache/sl-dbg/adapters/java-debug.jar`.  
**Target requirement:** JDK 8+ on the target.

```bash
# Launch
sl-dbg start --lang java --main com.example.App --classpath ./build/libs/*

# Attach (target must have JDWP enabled)
# java -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=*:5005 -jar app.jar
sl-dbg attach --lang java --host localhost --port 5005

# Source mapping for remote targets
sl-dbg attach --lang java --host prod.svc --port 5005 \
              --source-root ./src/main/java \
              --source-root ./target/generated-sources
```

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

## Auto-Install Flow

On first use of a language, `sl-dbg` runs `detectCommand`. If it fails:

1. Print a one-line consent prompt:
   ```
   [sl-dbg] python adapter (debugpy) not found.
   [sl-dbg] Install? [Y/n]
   ```
   Suppressible with `--yes` or config `auto_install = true`.

2. Run the install steps (pip/go/curl/etc.).

3. Cache the resolved adapter path in `~/.config/sl-dbg/adapters.toml`.

4. Proceed with the original command.

In non-interactive (CI, agent) mode, the absence of a TTY auto-fails with a clear error:
```json
{"ok":false,"error":{"code":"ADAPTER_NOT_FOUND",
  "message":"debugpy is not installed",
  "hint":"Run: pip install --user debugpy, or pass --yes to auto-install"}}
```

## Manual Adapter Configuration

Override defaults in `~/.config/sl-dbg/adapters.toml`:

```toml
[adapters.python]
command = ["/opt/venv/bin/python", "-m", "debugpy.adapter"]

[adapters.java]
command = ["java", "-jar", "/opt/sl-dbg/java-debug.jar"]
java_home = "/opt/openjdk-17"

[adapters.go]
command = ["/usr/local/bin/dlv", "dap"]

[adapters.cpp]
command = ["/opt/llvm/bin/lldb-dap"]
```

This lets users pin specific adapter versions or use vendored adapters.

## Adding a New Adapter

To support a new language, add a file to `internal/adapter/<lang>.go`:

```go
func init() {
    Register(Adapter{
        Lang: "ruby",
        DetectCommand: []string{"gem", "list", "-i", "debug"},
        LaunchCommand: func(cfg LaunchCfg) []string {
            return []string{"rdbg", "--open", "--port", "12345", cfg.Program}
        },
        InstallSteps: []InstallStep{
            {Cmd: []string{"gem", "install", "debug"}, Description: "Install debug gem"},
        },
        DefaultPort: 12345,
    })
}
```

No core code changes. The adapter is picked up automatically.

## Compatibility Matrix

| Adapter | Min version | OSes | Attach by PID | Conditional BP | Logpoints | Reverse step |
|---|---|---|---|---|---|---|
| debugpy | 1.6.0 | mac/linux/win | ✅ | ✅ | ✅ | ❌ |
| java-debug | 0.40+ | all | via JDWP | ✅ | ✅ | ❌ |
| dlv dap | 1.21+ | all | ✅ | ✅ | ✅ | ❌ |
| vscode-js-debug | 1.80+ | all | via Node port | ✅ | ✅ | ❌ |
| lldb-dap | LLVM 17+ | mac/linux | ✅ | ✅ | ✅ | partial |
| netcoredbg | 3.1+ | all | ✅ | ✅ | ✅ | ❌ |

`sl-dbg adapters` shows the live status on a given machine.
