package cli

// This file contains stub implementations for every command referenced in
// root.go. Each stub:
//   - Declares the correct flag set
//   - Returns NotImplemented(<cmd>) so `sl-dbg <cmd>` shows the right message
//
// As phases land (see docs/ROADMAP.md), the stubs are replaced with real
// implementations that:
//   1. Validate args
//   2. Send an IPC request to the daemon
//   3. Format the response with emit() / emitErr()

import (
	"github.com/spf13/cobra"
)

// --- Session lifecycle ---

func newStartCmd() *cobra.Command {
	var lang, program, cwd string
	var args []string
	var env []string
	var sourceRoots []string
	var stopOnEntry, readOnly, makeDefault bool
	c := &cobra.Command{
		Use:   "start",
		Short: "Launch a new debug session by starting a program",
		Example: `  sl-dbg start --lang python --program ./app.py
  sl-dbg start --lang java --main com.example.App --classpath './build/libs/*'`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if lang == "" || program == "" {
				return Usage("--lang and --program are required")
			}
			return NotImplemented("start")
		},
	}
	c.Flags().StringVar(&lang, "lang", "", "language (python|java|go|node|cpp|dotnet|rust)")
	c.Flags().StringVar(&program, "program", "", "path to entrypoint")
	c.Flags().StringVar(&cwd, "cwd", "", "working directory")
	c.Flags().StringSliceVar(&args, "args", nil, "arguments to the program")
	c.Flags().StringSliceVar(&env, "env", nil, "KEY=val environment (repeatable)")
	c.Flags().StringSliceVar(&sourceRoots, "source-root", nil, "source root for path mapping (repeatable)")
	c.Flags().BoolVar(&stopOnEntry, "stop-on-entry", false, "pause at program start")
	c.Flags().BoolVar(&readOnly, "read-only", false, "forbid state mutation (no setVariable/eval-side-effects)")
	c.Flags().BoolVar(&makeDefault, "make-default", true, "set as default session for subsequent commands")
	return c
}

func newAttachCmd() *cobra.Command {
	var lang, host string
	var port, pid int
	var sourceRoots []string
	var readOnly bool
	c := &cobra.Command{
		Use:   "attach",
		Short: "Attach to a running process by host:port or PID",
		Example: `  sl-dbg attach --lang java --host localhost --port 5005
  sl-dbg attach --lang python --pid 12345`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if lang == "" {
				return Usage("--lang is required")
			}
			if port == 0 && pid == 0 {
				return Usage("either --port or --pid is required")
			}
			return NotImplemented("attach")
		},
	}
	c.Flags().StringVar(&lang, "lang", "", "language")
	c.Flags().StringVar(&host, "host", "localhost", "remote host")
	c.Flags().IntVar(&port, "port", 0, "remote debug port")
	c.Flags().IntVar(&pid, "pid", 0, "process id (for adapters supporting PID attach)")
	c.Flags().StringSliceVar(&sourceRoots, "source-root", nil, "source root for path mapping")
	c.Flags().BoolVar(&readOnly, "read-only", false, "forbid state mutation")
	return c
}

func newListenCmd() *cobra.Command {
	var lang string
	var port int
	c := &cobra.Command{
		Use:   "listen",
		Short: "Listen for a target to connect (reverse attach)",
		RunE: func(*cobra.Command, []string) error {
			if lang == "" || port == 0 {
				return Usage("--lang and --port are required")
			}
			return NotImplemented("listen")
		},
	}
	c.Flags().StringVar(&lang, "lang", "", "language")
	c.Flags().IntVar(&port, "port", 0, "port to listen on")
	return c
}

func newSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List active debug sessions",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("sessions") },
	}
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <session-id>",
		Short: "Set the default session for subsequent commands",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("use") },
	}
}

func newStopCmd() *cobra.Command {
	var detach bool
	c := &cobra.Command{
		Use:   "stop [session-id]",
		Short: "Disconnect and terminate the session",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("stop") },
	}
	c.Flags().BoolVar(&detach, "detach", false, "leave target running (attach mode only)")
	return c
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart [session-id]",
		Short: "Restart the target",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("restart") },
	}
}

func newStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "state [session-id]",
		Short: "Print current session state (non-blocking)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("state") },
	}
}

// --- Breakpoints ---

func newBreakCmd() *cobra.Command {
	var cond, logMsg string
	var hit int
	var once bool
	c := &cobra.Command{
		Use:   "break <file:line | Class:line>",
		Short: "Add a line breakpoint",
		Args:  cobra.ExactArgs(1),
		Example: `  sl-dbg break app.py:42
  sl-dbg break com.example.UserService:42 --if "user.id < 0"
  sl-dbg break app.py:42 --log "x={x}"`,
		RunE: func(*cobra.Command, []string) error { return NotImplemented("break") },
	}
	c.Flags().StringVar(&cond, "if", "", "conditional expression")
	c.Flags().IntVar(&hit, "hit", 0, "break on Nth hit only")
	c.Flags().StringVar(&logMsg, "log", "", "logpoint message (no pause)")
	c.Flags().BoolVar(&once, "once", false, "auto-remove after first hit")
	return c
}

func newBreakFnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "break-fn <function>",
		Short: "Add a function-entry breakpoint",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("break-fn") },
	}
}

func newBreakExCmd() *cobra.Command {
	var uncaught, caught, all bool
	c := &cobra.Command{
		Use:   "break-ex <ExceptionType>",
		Short: "Break when exception is thrown",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("break-ex") },
	}
	c.Flags().BoolVar(&uncaught, "uncaught", false, "only uncaught exceptions")
	c.Flags().BoolVar(&caught, "caught", false, "only caught exceptions")
	c.Flags().BoolVar(&all, "all", false, "all exceptions")
	return c
}

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch <expression>",
		Short: "Add a data breakpoint (break when value changes)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("watch") },
	}
}

func newBreaksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "breaks",
		Short: "List all breakpoints",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("breaks") },
	}
}

func newUnbreakCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "unbreak <id>...",
		Short: "Remove breakpoints by id",
		RunE: func(_ *cobra.Command, args []string) error {
			if !all && len(args) == 0 {
				return Usage("provide breakpoint ids or --all")
			}
			return NotImplemented("unbreak")
		},
	}
	c.Flags().BoolVar(&all, "all", false, "remove all breakpoints")
	return c
}

// --- Execution control ---

func newRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run from start (after start --stop-on-entry)",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("run") },
	}
}

func newContinueCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "continue",
		Aliases: []string{"c"},
		Short:   "Resume until next pause",
		RunE:    func(*cobra.Command, []string) error { return NotImplemented("continue") },
	}
	return c
}

func newStepCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "step",
		Aliases: []string{"si"},
		Short:   "Step into",
		RunE:    func(*cobra.Command, []string) error { return NotImplemented("step") },
	}
}

func newNextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "next",
		Aliases: []string{"n"},
		Short:   "Step over",
		RunE:    func(*cobra.Command, []string) error { return NotImplemented("next") },
	}
}

func newFinishCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "finish",
		Aliases: []string{"out"},
		Short:   "Step out of current frame",
		RunE:    func(*cobra.Command, []string) error { return NotImplemented("finish") },
	}
}

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause a running target",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("pause") },
	}
}

func newUntilCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "until <line>",
		Short: "Continue until reaching the given line",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("until") },
	}
}

func newGotoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "goto <line>",
		Short: "Jump to a line without executing intervening code (if supported)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("goto") },
	}
}

// --- Inspection ---

func newStackCmd() *cobra.Command {
	var thread, limit int
	c := &cobra.Command{
		Use:   "stack",
		Short: "Print call stack",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("stack") },
	}
	c.Flags().IntVar(&thread, "thread", 0, "thread id (default: current)")
	c.Flags().IntVar(&limit, "limit", 20, "max frames")
	return c
}

func newThreadsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "threads",
		Short: "List threads",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("threads") },
	}
}

func newLocalsCmd() *cobra.Command {
	var frame int
	c := &cobra.Command{
		Use:   "locals",
		Short: "Print local variables",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("locals") },
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index (0 = top)")
	return c
}

func newGlobalsCmd() *cobra.Command {
	var frame int
	c := &cobra.Command{
		Use:   "globals",
		Short: "Print global / module-level variables",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("globals") },
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index")
	return c
}

func newFieldsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fields <ref>",
		Short: "Expand a previously returned object reference",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("fields") },
	}
}

func newEvalCmd() *cobra.Command {
	var frame int
	c := &cobra.Command{
		Use:   "eval <expression>",
		Short: "Evaluate an expression in the current frame",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("eval") },
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index")
	return c
}

func newSetVarCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> <value>",
		Short: "Modify a variable in the current frame",
		Args:  cobra.ExactArgs(2),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("set") },
	}
}

func newSnapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot",
		Short: "Dump full state (stack + locals + globals + watches) as JSON",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("snapshot") },
	}
}

func newSourceCmd() *cobra.Command {
	var frame, around int
	c := &cobra.Command{
		Use:   "source",
		Short: "Print source code around the current line",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("source") },
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index")
	c.Flags().IntVar(&around, "around", 5, "lines of context")
	return c
}

// --- I/O ---

func newOutputCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "output",
		Short: "Drain target stdout/stderr buffer",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("output") },
	}
	c.Flags().BoolVar(&follow, "follow", false, "stream incoming output")
	return c
}

func newEventsCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "events",
		Short: "Stream raw debug events as JSON Lines",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("events") },
	}
	c.Flags().BoolVar(&follow, "follow", false, "follow new events")
	return c
}

// --- Meta ---

func newAdaptersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adapters",
		Short: "List registered language adapters and detection status",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("adapters") },
	}
}

func newInstallAdapterCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install-adapter <lang>",
		Short: "Install a missing language adapter (pip/go/curl)",
		Args:  cobra.ExactArgs(1),
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("install-adapter") },
	}
}

func newDaemonCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Daemon controls (start|stop|status|logs)",
	}
	c.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon (normally implicit)",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("daemon start") },
	})
	c.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("daemon stop") },
	})
	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Daemon status",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("daemon status") },
	})
	c.AddCommand(&cobra.Command{
		Use:   "logs",
		Short: "Tail daemon log",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("daemon logs") },
	})
	return c
}

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run as an MCP server over stdio (Phase 5)",
		RunE:  func(*cobra.Command, []string) error { return NotImplemented("mcp") },
	}
}
