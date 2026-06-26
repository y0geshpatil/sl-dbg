package com.sldbg.java;

import java.io.IOException;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import com.microsoft.java.debug.core.DebugException;
import com.microsoft.java.debug.core.JavaBreakpointLocation;
import com.microsoft.java.debug.core.adapter.IDebugAdapterContext;
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
 *
 * <p>When asked to resolve a stack-frame source by FQN, walks the configured
 * source roots and returns a URI only if the file actually exists. Returning
 * a non-existent fabricated path (e.g. the JVM's {@code Unsafe.java}) would
 * make the client display misleading source content, so we return {@code null}
 * in that case and let the client treat the frame as source-less.
 */
public final class SimpleSourceLookUpProvider implements ISourceLookUpProvider {

    private static final Pattern PACKAGE_DECL =
            Pattern.compile("^\\s*package\\s+([a-zA-Z_$][a-zA-Z0-9_$.]*)\\s*;", Pattern.MULTILINE);

    private volatile String[] sourcePaths = new String[0];

    @Override
    public void initialize(IDebugAdapterContext context, Map<String, Object> options) {
        if (options == null) {
            return;
        }
        Object sp = options.get("sourcePaths");
        if (sp instanceof String[]) {
            this.sourcePaths = (String[]) sp;
        } else if (sp instanceof List) {
            List<?> list = (List<?>) sp;
            String[] arr = new String[list.size()];
            for (int i = 0; i < list.size(); i++) {
                arr[i] = String.valueOf(list.get(i));
            }
            this.sourcePaths = arr;
        }
    }

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
        // 1. If java-debug already handed us an absolute path, only echo it
        //    back when the file actually exists on disk. Otherwise we'd be
        //    pretending JDK / library frames have local source files.
        if (sourcePath != null && !sourcePath.isEmpty()) {
            Path p = pathFromUri(sourcePath);
            if (p != null && p.isAbsolute() && Files.isRegularFile(p)) {
                try {
                    return p.toUri().toString();
                } catch (Exception e) {
                    return sourcePath;
                }
            }
        }
        // 2. Walk configured source roots looking for `<pkg>/<Class>.java`.
        if (fullyQualifiedName != null && !fullyQualifiedName.isEmpty() && sourcePaths.length > 0) {
            String rel = fullyQualifiedName.replace('.', '/');
            // Strip inner-class suffix (Foo$Bar -> Foo).
            int dollar = rel.indexOf('$');
            if (dollar >= 0) {
                rel = rel.substring(0, dollar);
            }
            String suffix = rel + ".java";
            for (String root : sourcePaths) {
                if (root == null || root.isEmpty()) {
                    continue;
                }
                try {
                    Path cand = Paths.get(root, suffix);
                    if (Files.isRegularFile(cand)) {
                        return cand.toUri().toString();
                    }
                    // Fallback: try without package (single-flat-dir source roots).
                    int lastSlash = suffix.lastIndexOf('/');
                    if (lastSlash >= 0) {
                        Path flat = Paths.get(root, suffix.substring(lastSlash + 1));
                        if (Files.isRegularFile(flat)) {
                            return flat.toUri().toString();
                        }
                    }
                } catch (Exception ignored) {
                    // try next root
                }
            }
        }
        // 3. No real source available: return null so the client treats this
        //    frame as source-less rather than rendering an unrelated file.
        return null;
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
