package com.sldbg.java;

import java.util.concurrent.CompletableFuture;

import com.microsoft.java.debug.core.IEvaluatableBreakpoint;
import com.microsoft.java.debug.core.adapter.IEvaluationProvider;
import com.sun.jdi.ObjectReference;
import com.sun.jdi.ThreadReference;
import com.sun.jdi.Value;

/**
 * Stub evaluation provider: expression evaluation, watches, and conditional
 * breakpoints all fail fast with a clear message. Variable inspection and
 * stepping work without it.
 */
public final class NoOpEvaluationProvider implements IEvaluationProvider {

    private static CompletableFuture<Value> unsupported() {
        CompletableFuture<Value> f = new CompletableFuture<>();
        f.completeExceptionally(new UnsupportedOperationException(
                "expression evaluation is not yet supported by the sl-dbg Java adapter"));
        return f;
    }

    @Override
    public boolean isInEvaluation(ThreadReference thread) {
        return false;
    }

    @Override
    public CompletableFuture<Value> evaluate(String expression, ThreadReference thread, int depth) {
        return unsupported();
    }

    @Override
    public CompletableFuture<Value> evaluate(String expression, ObjectReference thisContext, ThreadReference thread) {
        return unsupported();
    }

    @Override
    public CompletableFuture<Value> evaluateForBreakpoint(IEvaluatableBreakpoint breakpoint, ThreadReference thread) {
        return unsupported();
    }

    @Override
    public CompletableFuture<Value> invokeMethod(ObjectReference thisContext, String methodName, String methodSignature,
                                                 Value[] args, ThreadReference thread, boolean invokeSuper) {
        return unsupported();
    }

    @Override
    public void clearState(ThreadReference thread) {
        // no-op
    }
}
