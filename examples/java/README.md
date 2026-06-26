# Java sample for sl-dbg

The Java adapter is a small, self-contained launcher that ships with sl-dbg —
it embeds Microsoft's `java-debug-core` and serves DAP over a local TCP socket
without requiring Eclipse JDT-LS or any OSGi runtime.

## Prerequisites

- **JDK 11+** (`java` and `javac` on `PATH`; verified with Homebrew `openjdk`).
- **Maven** (only for building the embedded launcher; one-time).

## One-time setup: build the embedded Java adapter

```bash
make java-adapter
```

This produces `adapters/java-launcher/target/sl-dbg-java-adapter.jar`. The Go
binary discovers it automatically; you can override the location with
`SL_DBG_JAVA_DEBUG_JAR=/path/to/sl-dbg-java-adapter.jar`.

## Debug a buggy program (attach mode)

```bash
# 1. Compile the sample with line tables (-g).
javac -g examples/java/Buggy.java

# 2. Start the JVM in JDWP server mode, suspended at entry.
java -agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=127.0.0.1:5005 \
     -cp examples/java Buggy &

# 3. Attach with sl-dbg (give it the source root so file:line breakpoints map).
sl-dbg attach --lang java --host 127.0.0.1 --port 5005 \
              --source-root examples/java

# 4. Set a breakpoint, then resume — sl-dbg defers configurationDone until the
#    first `continue` so that breakpoints set between attach and continue
#    actually catch.
sl-dbg break examples/java/Buggy.java:24
sl-dbg continue       # stops with item=10, total=0
sl-dbg locals
sl-dbg next           # step over: x=100 = 1000/10
sl-dbg continue       # next iteration: item=5, total=100
sl-dbg unbreak --all
sl-dbg continue       # runs to completion: result=50
sl-dbg stop
```

## Pause a running JVM

```bash
javac -g examples/java/SpinLoop.java

# Run with suspend=n so the JVM starts immediately.
java -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:5006 \
     -cp examples/java SpinLoop &

sl-dbg attach --lang java --host 127.0.0.1 --port 5006 \
              --source-root examples/java
sl-dbg pause          # suspends the VM, returns the stopped thread
sl-dbg stack          # shows SpinLoop.main:11 (inside Thread.sleep)
sl-dbg continue       # resumes
sl-dbg stop
```

## What works today

| Command                              | Status |
|--------------------------------------|--------|
| `attach --lang java --host:port`     | ✅     |
| `break file:line` (plain)            | ✅     |
| `unbreak <id>` / `unbreak --all`     | ✅     |
| `continue` / `next` / `step` / `finish` | ✅  |
| `pause`                              | ✅     |
| `stack`                              | ✅     |
| `locals` (variable inspection)       | ✅     |
| `stop`                               | ✅     |
| `eval <expr>` and conditional breakpoints | ⚠️ Not yet — needs an evaluation provider (planned). |
| `start --lang java` (launch mode)    | ⚠️ Not yet — needs a project-model launch front-end. |

For the moment, use JDWP attach with `suspend=y` (see flow above) for any
workflow where you want to break before the program starts running.
