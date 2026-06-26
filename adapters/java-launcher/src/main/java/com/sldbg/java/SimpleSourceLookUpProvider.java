package com.sldbg.java;

import java.io.IOException;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Collections;
import java.util.List;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import com.microsoft.java.debug.core.DebugException;
import com.microsoft.java.debug.core.JavaBreakpointLocation;
import com.microsoft.java.debug.core.adapter.ISourceLookUpProvider;
import com.microsoft.java.debug.core.protocol.Types.SourceBreakpoint;

/**
 * Minimal source-lookup provider that derives the fully-qualified class name
 * from a Java source file by reading its {@code package} declaration.
 *
 * <p>Sufficient for line breakpoints in attach mode where the user supplies
 * source roots via DAP {@code sourcePaths}. Does not understand nested/inner
 * classes or multiple top-level classes; the JVM will fall back to matching
 * via the outer class name plus line table.
 */
public final class SimpleSourceLookUpProvider implements ISourceLookUpProvider {

    private static final Pattern PACKAGE_DECL =
            Pattern.compile("^\\s*package\\s+([a-zA-Z_$][a-zA-Z0-9_$.]*)\\s*;", Pattern.MULTILINE);

    @Override
    public boolean supportsRealtimeBreakpointVerification() {
        return false;
    }

    @Override
    public String[] getFullyQualifiedName(String uri, int[] lines, int[] columns) throws DebugException {
        String fqn = computeFqn(uri);
        String[] out = new String[lines.length];
        for (int i = 0; i < lines.length; i++) {
            out[i] = fqn;
        }
        return out;
    }

    @Override
    public JavaBreakpointLocation[] getBreakpointLocations(String sourceUri, SourceBreakpoint[] sourceBreakpoints)
            throws DebugException {
        String fqn = computeFqn(sourceUri);
        JavaBreakpointLocation[] out = new JavaBreakpointLocation[sourceBreakpoints.length];
        for (int i = 0; i < sourceBreakpoints.length; i++) {
            SourceBreakpoint sb = sourceBreakpoints[i];
            JavaBreakpointLocation loc = new JavaBreakpointLocation(sb.line, sb.column);
            loc.setClassName(fqn);
            out[i] = loc;
        }
        return out;
    }

    @Override
    public String getSourceFileURI(String fullyQualifiedName, String sourcePath) {
        if (sourcePath == null) {
            return null;
        }
        // Caller (StackTraceRequestHandler) walks sourcePaths itself; we just
        // echo back a best-effort URI so it has *something*.
        try {
            return Paths.get(sourcePath).toUri().toString();
        } catch (Exception e) {
            return sourcePath;
        }
    }

    @Override
    public String getSourceContents(String uri) {
        try {
            Path p = pathFromUri(uri);
            if (p != null && Files.isRegularFile(p)) {
                return new String(Files.readAllBytes(p), StandardCharsets.UTF_8);
            }
        } catch (IOException ignored) {
        }
        return null;
    }

    @Override
    public List<MethodInvocation> findMethodInvocations(String uri, int line) {
        return Collections.emptyList();
    }

    private static String computeFqn(String uri) {
        Path file = pathFromUri(uri);
        if (file == null) {
            return stripExtension(lastSegment(uri));
        }
        String className = stripExtension(file.getFileName().toString());
        String pkg = readPackage(file);
        return pkg.isEmpty() ? className : pkg + "." + className;
    }

    private static Path pathFromUri(String uri) {
        if (uri == null) {
            return null;
        }
        try {
            if (uri.startsWith("file:")) {
                return Paths.get(URI.create(uri));
            }
            return Paths.get(uri);
        } catch (Exception e) {
            return null;
        }
    }

    private static String readPackage(Path file) {
        try {
            String src = new String(Files.readAllBytes(file), StandardCharsets.UTF_8);
            Matcher m = PACKAGE_DECL.matcher(src);
            if (m.find()) {
                return m.group(1);
            }
        } catch (IOException ignored) {
        }
        return "";
    }

    private static String lastSegment(String s) {
        int i = Math.max(s.lastIndexOf('/'), s.lastIndexOf('\\'));
        return i < 0 ? s : s.substring(i + 1);
    }

    private static String stripExtension(String s) {
        int dot = s.lastIndexOf('.');
        return dot < 0 ? s : s.substring(0, dot);
    }
}
