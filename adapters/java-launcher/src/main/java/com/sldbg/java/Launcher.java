/*
 * sl-dbg Java launcher: a minimal standalone DAP server that embeds
 * Microsoft's java-debug-core without requiring Eclipse JDT-LS.
 *
 * Usage:  java -jar sl-dbg-java-adapter.jar --port <N>
 *         (the launcher listens on 127.0.0.1:N, accepts one client, runs DAP)
 */
package com.sldbg.java;

import java.io.IOException;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.util.logging.Level;
import java.util.logging.Logger;

import com.microsoft.java.debug.core.Configuration;
import com.microsoft.java.debug.core.adapter.ICompletionsProvider;
import com.microsoft.java.debug.core.adapter.IEvaluationProvider;
import com.microsoft.java.debug.core.adapter.IHotCodeReplaceProvider;
import com.microsoft.java.debug.core.adapter.ISourceLookUpProvider;
import com.microsoft.java.debug.core.adapter.IVirtualMachineManagerProvider;
import com.microsoft.java.debug.core.adapter.ProtocolServer;
import com.microsoft.java.debug.core.adapter.ProviderContext;

public final class Launcher {

    private static final Logger LOG = Logger.getLogger(Configuration.LOGGER_NAME);

    public static void main(String[] args) throws IOException {
        int port = -1;
        boolean stdio = false;
        for (int i = 0; i < args.length; i++) {
            String a = args[i];
            if (a.equals("--stdio")) {
                stdio = true;
            } else if (a.equals("--port") && i + 1 < args.length) {
                port = Integer.parseInt(args[++i]);
            } else if (a.startsWith("--port=")) {
                port = Integer.parseInt(a.substring("--port=".length()));
            }
        }

        Logger root = Logger.getLogger("");
        for (java.util.logging.Handler h : root.getHandlers()) {
            h.setLevel(Level.WARNING);
        }
        root.setLevel(Level.WARNING);

        ProviderContext ctx = buildProviderContext();

        if (stdio) {
            ProtocolServer server = new ProtocolServer(System.in, System.out, ctx);
            server.run();
            return;
        }

        if (port < 0) {
            System.err.println("sl-dbg-java-adapter: must specify --port <N> or --stdio");
            System.exit(2);
        }

        try (ServerSocket ss = new ServerSocket(port, 1, InetAddress.getByName("127.0.0.1"))) {
            // Print our chosen port (handy if caller passed 0)
            System.err.println("sl-dbg-java-adapter listening on 127.0.0.1:" + ss.getLocalPort());
            Socket conn = ss.accept();
            conn.setTcpNoDelay(true);
            ProtocolServer server = new ProtocolServer(conn.getInputStream(), conn.getOutputStream(), ctx);
            server.run();
        }
    }

    private static ProviderContext buildProviderContext() {
        ProviderContext pc = new ProviderContext();
        pc.registerProvider(ISourceLookUpProvider.class, new SimpleSourceLookUpProvider());
        pc.registerProvider(IVirtualMachineManagerProvider.class, new JdiVirtualMachineManagerProvider());
        pc.registerProvider(IHotCodeReplaceProvider.class, new NoOpHotCodeReplaceProvider());
        pc.registerProvider(IEvaluationProvider.class, new JdiEvaluationProvider());
        pc.registerProvider(ICompletionsProvider.class, new NoOpCompletionsProvider());
        return pc;
    }
}
