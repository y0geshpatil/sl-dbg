package com.sldbg.java;

import java.util.Collections;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.function.Consumer;

import com.microsoft.java.debug.core.adapter.HotCodeReplaceEvent;
import com.microsoft.java.debug.core.adapter.IHotCodeReplaceProvider;

import io.reactivex.Observable;
import io.reactivex.subjects.PublishSubject;

/** Hot-code-replace is not supported by sl-dbg; we just keep the bus quiet. */
public final class NoOpHotCodeReplaceProvider implements IHotCodeReplaceProvider {

    private final PublishSubject<HotCodeReplaceEvent> bus = PublishSubject.create();

    @Override
    public void onClassRedefined(Consumer<List<String>> consumer) {
        // no-op
    }

    @Override
    public CompletableFuture<List<String>> redefineClasses() {
        return CompletableFuture.completedFuture(Collections.emptyList());
    }

    @Override
    public Observable<HotCodeReplaceEvent> getEventHub() {
        return bus;
    }
}
