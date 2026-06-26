package com.sldbg.java;

import java.util.Collections;
import java.util.List;

import com.microsoft.java.debug.core.adapter.ICompletionsProvider;
import com.microsoft.java.debug.core.protocol.Types.CompletionItem;
import com.sun.jdi.StackFrame;

public final class NoOpCompletionsProvider implements ICompletionsProvider {
    @Override
    public List<CompletionItem> codeComplete(StackFrame frame, String snippet, int line, int column) {
        return Collections.emptyList();
    }
}
