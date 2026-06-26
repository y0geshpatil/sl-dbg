# Java sample for sl-dbg

## Prerequisites

1. **JDK** (any JDK 11+ — verified with the Homebrew `openjdk`).
2. **java-debug adapter**. **Caveat**: Microsoft's java-debug project does
   **not** publish a standalone DAP server JAR. The plugin JAR on Maven
   Central (`com.microsoft.java:com.microsoft.java.debug.plugin`) is an
   OSGi bundle that only runs inside Eclipse JDT-LS.

   Until sl-dbg ships its own self-contained Java launcher (planned), you
   need to either:

   - Build java-debug from source (`git clone microsoft/java-debug && mvn package`)
     and assemble a fat jar that exposes `com.microsoft.java.debug.core.adapter.JdiDebugAdapter`
     on its classpath, then set `SL_DBG_JAVA_DEBUG_JAR=/path/to/fatjar.jar`, or
   - Use a different runner (vscode-java-debug, IntelliJ) for now.

   This limitation is tracked in ROADMAP.md.

## Build

```bash
javac -g examples/java/Buggy.java
```

## Debug (attach mode — recommended for v0.1)

```bash
# 1. Start the JVM in JDWP server mode, suspended at entry.
java -agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=*:5005 \
     -cp examples/java Buggy &

# 2. Attach with sl-dbg.
sl-dbg attach --lang java --host localhost --port 5005 \
              --source-root examples/java

# 3. Set a conditional breakpoint and let it rip.
sl-dbg break examples/java/Buggy.java:22 --if "item < 0"
sl-dbg continue
sl-dbg locals
sl-dbg eval "compute(item)"
sl-dbg stop
```

## Notes

`sl-dbg start --lang java` (launch mode) is not yet implemented because
`java-debug` requires a project model (classpath, module path, JDK home, source
paths). Use the JDWP-attach flow above for now; project-aware launch will
follow in a later release.
