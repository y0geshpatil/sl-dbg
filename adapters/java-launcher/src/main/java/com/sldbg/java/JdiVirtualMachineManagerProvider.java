package com.sldbg.java;

import com.microsoft.java.debug.core.adapter.IVirtualMachineManagerProvider;
import com.sun.jdi.Bootstrap;
import com.sun.jdi.VirtualMachineManager;

/** Trivial provider: hand back the platform JDI VirtualMachineManager. */
public final class JdiVirtualMachineManagerProvider implements IVirtualMachineManagerProvider {
    @Override
    public VirtualMachineManager getVirtualMachineManager() {
        return Bootstrap.virtualMachineManager();
    }
}
