package com.sldbg.java;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.logging.Level;
import java.util.logging.Logger;

import com.microsoft.java.debug.core.Configuration;
import com.microsoft.java.debug.core.IEvaluatableBreakpoint;
import com.microsoft.java.debug.core.adapter.IDebugAdapterContext;
import com.microsoft.java.debug.core.adapter.IEvaluationProvider;
import com.sun.jdi.ObjectReference;
import com.sun.jdi.StackFrame;
import com.sun.jdi.ThreadReference;
import com.sun.jdi.Value;
import com.sun.tools.example.debug.expr.ExpressionParser;

/**
 * Real expression evaluator backed by the OpenJDK-bundled
 * {@code com.sun.tools.example.debug.expr.ExpressionParser} — the same engine
 * that {@code jdb} uses. Supports full Java expression syntax (field/method
 * access, arithmetic, comparisons, casts, instanceof, new, etc.).
 *
 * <p>Requires {@code --add-exports=jdk.jdi/com.sun.tools.example.debug.expr=ALL-UNNAMED}
 * on the JVM running this launcher. The launcher's MANIFEST.MF declares this
 * via {@code Add-Exports:} so {@code java -jar} picks it up automatically on
 * JDK 11+ when {@code --enable-native-access=ALL-UNNAMED} style flags are
 * honoured; we also pass the flag explicitly from the Go side as a belt-and-
 * suspenders measure.
 */
public final class JdiEvaluationProvider implements IEvaluationProvider {

    private static final Logger LOG = Logger.getLogger(Configuration.LOGGER_NAME);

    private IDebugAdapterContext context;
    private final ConcurrentHashMap<Long, Boolean> threadsInEval = new ConcurrentHashMap<>();

    @Override
    public void initialize(IDebugAdapterContext ctx, java.util.Map<String, Object> opts) {
        this.context = ctx;
    }

    @Override
    public boolean isInEvaluation(ThreadReference thread) {
        return thread != null && Boolean.TRUE.equals(threadsInEval.get(thread.uniqueID()));
    }

    @Override
    public CompletableFuture<Value> evaluate(String expression, ThreadReference thread, int depth) {
        return runEval(thread, () -> {
            StackFrame frame = thread.frame(depth);
            ExpressionParser.GetFrame getFrame = () -> frame;
            return ExpressionParser.evaluate(expression, thread.virtualMachine(), getFrame);
        });
    }

    @Override
    public CompletableFuture<Value> evaluate(String expression, ObjectReference thisContext, ThreadReference thread) {
        // ExpressionParser is frame-driven; without a stack frame we can't
        // resolve identifiers. Fall back to top-frame evaluation.
        return evaluate(expression, thread, 0);
    }

    @Override
    public CompletableFuture<Value> evaluateForBreakpoint(IEvaluatableBreakpoint bp, ThreadReference thread) {
        String cond = bp.getCondition();
        if (cond == null || cond.isEmpty()) {
            return CompletableFuture.completedFuture(null);
        }
        return evaluate(cond, thread, 0);
    }

    @Override
    public CompletableFuture<Value> invokeMethod(ObjectReference thisContext, String methodName, String methodSignature,
                                                 Value[] args, ThreadReference thread, boolean invokeSuper) {
        // Used for toString() rendering of variables. Defer to JDI directly.
        return runEval(thread, () -> {
            java.util.List<com.sun.jdi.Method> methods = thisContext.referenceType().methodsByName(methodName, methodSignature);
            if (methods.isEmpty()) {
                throw new NoSuchMethodException(methodName + methodSignature);
            }
            return thisContext.invokeMethod(thread, methods.get(0),
                args == null ? java.util.Collections.emptyList() : java.util.Arrays.asList(args),
                ObjectReference.INVOKE_SINGLE_THREADED);
        });
    }

    @Override
    public void clearState(ThreadReference thread) {
        if (thread != null) {
            threadsInEval.remove(thread.uniqueID());
        }
    }

    // ----- internals -------------------------------------------------------

    @FunctionalInterface private interface EvalTask { Value run() throws Exception; }

    private CompletableFuture<Value> runEval(ThreadReference thread, EvalTask task) {
        CompletableFuture<Value> f = new CompletableFuture<>();
        long tid = thread == null ? -1 : thread.uniqueID();
        if (tid > 0) threadsInEval.put(tid, Boolean.TRUE);
        try {
            f.complete(task.run());
        } catch (Throwable t) {
            // Surface a clean error message to the DAP client.
            LOG.log(Level.FINE, "evaluation failed", t);
            f.completeExceptionally(new RuntimeException(humanize(t), t));
        } finally {
            if (tid > 0) threadsInEval.remove(tid);
        }
        return f;
    }

    private static String humanize(Throwable t) {
        Throwable root = t;
        while (root.getCause() != null && root.getCause() != root) root = root.getCause();
        String msg = root.getMessage();
        if (msg == null || msg.isEmpty()) {
            return root.getClass().getSimpleName();
        }
        return msg;
    }
}
