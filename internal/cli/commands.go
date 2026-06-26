package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yogeshpatil/sl-dbg/internal/mcp"
	"github.com/yogeshpatil/sl-dbg/internal/proto"
)

// mcpDaemonCaller adapts daemonCall to the mcp.DaemonCaller interface.
type mcpDaemonCaller struct{}

func (mcpDaemonCaller) Call(cmd, sess string, args interface{}) (json.RawMessage, error) {
	var raw json.RawMessage
	if args != nil {
		raw = rawJSON(args)
	}
	resp, err := daemonCall(proto.Request{Cmd: cmd, Sess: sess, Args: raw})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
		}
		return nil, fmt.Errorf("daemon error")
	}
	if len(resp.Data) == 0 {
		return json.RawMessage(`null`), nil
	}
	return resp.Data, nil
}

// abs resolves p against the CLI's CWD. Empty returns "".
func abs(p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return a
}

// absLocation rewrites "file:line" or "Class:line" so that file paths become absolute.
// Class names (no path separator, no extension match) are passed through unchanged.
func absLocation(loc string) string {
	i := strings.LastIndex(loc, ":")
	if i <= 0 {
		return loc
	}
	file, rest := loc[:i], loc[i:]
	// Heuristic: treat as file path if it contains a path separator or ends in a known source ext.
	if strings.ContainsAny(file, "/\\") || hasSourceExt(file) {
		return abs(file) + rest
	}
	return loc
}

func hasSourceExt(p string) bool {
	for _, ext := range []string{".py", ".java", ".go", ".js", ".ts", ".rb", ".rs", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// --- Session lifecycle ---

func newStartCmd() *cobra.Command {
	var lang, program, cwd, mainClass, classpath string
	var args, env []string
	var stopOnEntry, readOnly bool
	c := &cobra.Command{
		Use:   "start",
		Short: "Launch a new debug session by starting a program",
		Example: `  sl-dbg start --lang python --program ./app.py
  sl-dbg start --lang java --main com.example.App --classpath ./out`,
		RunE: func(*cobra.Command, []string) error {
			if lang == "" {
				return Usage("--lang is required")
			}
			if lang == "java" && mainClass == "" {
				return Usage("--main is required for java")
			}
			if lang != "java" && program == "" {
				return Usage("--program is required")
			}
			return callRaw(proto.CmdStart, "", proto.StartArgs{
				Lang: lang, Program: abs(program), Args: args, Cwd: abs(cwd),
				Env: env, StopOnEntry: stopOnEntry, ReadOnly: readOnly,
				MainClass: mainClass, Classpath: abs(classpath),
			})
		},
	}
	c.Flags().StringVar(&lang, "lang", "", "language (python|java|...)")
	c.Flags().StringVar(&program, "program", "", "path to entrypoint")
	c.Flags().StringVar(&cwd, "cwd", "", "working directory")
	c.Flags().StringSliceVar(&args, "args", nil, "program arguments")
	c.Flags().StringSliceVar(&env, "env", nil, "KEY=val env vars (repeatable)")
	c.Flags().BoolVar(&stopOnEntry, "stop-on-entry", false, "pause at program start")
	c.Flags().BoolVar(&readOnly, "read-only", false, "forbid state mutation")
	c.Flags().StringVar(&mainClass, "main", "", "main class (java)")
	c.Flags().StringVar(&classpath, "classpath", "", "classpath (java)")
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
		RunE: func(*cobra.Command, []string) error {
			if lang == "" {
				return Usage("--lang is required")
			}
			if port == 0 && pid == 0 {
				return Usage("either --port or --pid is required")
			}
			return callRaw(proto.CmdAttach, "", proto.AttachArgs{
				Lang: lang, Host: host, Port: port, PID: pid,
				ReadOnly: readOnly, SourceRoots: sourceRoots,
			})
		},
	}
	c.Flags().StringVar(&lang, "lang", "", "language")
	c.Flags().StringVar(&host, "host", "localhost", "remote host")
	c.Flags().IntVar(&port, "port", 0, "remote debug port")
	c.Flags().IntVar(&pid, "pid", 0, "process id")
	c.Flags().StringSliceVar(&sourceRoots, "source-root", nil, "source root (repeatable)")
	c.Flags().BoolVar(&readOnly, "read-only", false, "forbid state mutation")
	return c
}

func newListenCmd() *cobra.Command {
	var timeoutSec float64
	c := &cobra.Command{
		Use:   "listen",
		Short: "Block until the program stops at a breakpoint, exits, or terminates",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdListen, gFlags.Session, proto.ListenArgs{TimeoutSec: timeoutSec})
		},
	}
	c.Flags().Float64Var(&timeoutSec, "timeout", 60, "max seconds to wait")
	return c
}

func newSessionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sessions",
		Short: "List active debug sessions",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdSessions, "", nil)
		},
	}
}

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <session-id>",
		Short: "Set the default session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return callRaw(proto.CmdUse, "", map[string]string{"id": a[0]})
		},
	}
}

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [session-id]",
		Short: "Terminate a session",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			sid := gFlags.Session
			if len(a) > 0 {
				sid = a[0]
			}
			return callRaw(proto.CmdStop, sid, nil)
		},
	}
}

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart [session-id]",
		Short: "Restart the target (if the adapter supports it)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			sid := gFlags.Session
			if len(a) > 0 {
				sid = a[0]
			}
			return callRaw(proto.CmdRestart, sid, proto.RestartArgs{})
		},
	}
}

func newStateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "state [session-id]",
		Short: "Current session state (non-blocking)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			sid := gFlags.Session
			if len(a) > 0 {
				sid = a[0]
			}
			return callRaw(proto.CmdState, sid, nil)
		},
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
  sl-dbg break app.py:42 --if "user.id < 0"`,
		RunE: func(_ *cobra.Command, a []string) error {
			return callRaw(proto.CmdBreak, gFlags.Session, proto.BreakArgs{
				Location: absLocation(a[0]), Condition: cond, Hit: hit, LogMsg: logMsg, Once: once,
			})
		},
	}
	c.Flags().StringVar(&cond, "if", "", "conditional expression")
	c.Flags().IntVar(&hit, "hit", 0, "break on Nth hit only")
	c.Flags().StringVar(&logMsg, "log", "", "logpoint message (no pause)")
	c.Flags().BoolVar(&once, "once", false, "remove after first hit")
	return c
}

func newBreakFnCmd() *cobra.Command {
	var cond string
	var hit int
	c := &cobra.Command{
		Use:   "break-fn <function>",
		Short: "Function-entry breakpoint (e.g., Buggy.compute or my_module.fn)",
		Args:  cobra.ExactArgs(1),
		Example: `  sl-dbg break-fn Buggy.compute
  sl-dbg break-fn my_module.fn --if "x > 100"`,
		RunE: func(_ *cobra.Command, a []string) error {
			return callRaw(proto.CmdBreakFn, gFlags.Session, proto.BreakFnArgs{
				Function: a[0], Condition: cond, Hit: hit,
			})
		},
	}
	c.Flags().StringVar(&cond, "if", "", "conditional expression")
	c.Flags().IntVar(&hit, "hit", 0, "break on Nth hit only")
	return c
}

func newBreakExCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "break-ex <filter>...",
		Short: "Exception breakpoint (filters: uncaught, raised, or adapter-specific)",
		Args:  cobra.MinimumNArgs(1),
		Example: `  sl-dbg break-ex uncaught
  sl-dbg break-ex raised uncaught`,
		RunE: func(_ *cobra.Command, a []string) error {
			return callRaw(proto.CmdBreakEx, gFlags.Session, proto.BreakExArgs{Filters: a})
		},
	}
	return c
}

func newWatchCmd() *cobra.Command {
	var addExpr, action string
	var rmID, frame int
	var rmAll bool
	c := &cobra.Command{
		Use:   "watch [--add <expr> | --remove <id> | --remove-all]",
		Short: "Manage watch expressions (re-evaluated on every pause)",
		Example: `  sl-dbg watch --add "items.length"
  sl-dbg watch              # list current watches with values
  sl-dbg watch --remove 2
  sl-dbg watch --remove-all`,
		RunE: func(_ *cobra.Command, _ []string) error {
			args := proto.WatchArgs{Frame: frame}
			switch {
			case addExpr != "":
				args.Action = "add"
				args.Expression = addExpr
			case rmAll:
				args.Action = "remove"
				args.All = true
			case rmID > 0:
				args.Action = "remove"
				args.ID = rmID
			default:
				args.Action = "list"
			}
			if action != "" {
				args.Action = action
			}
			return callRaw(proto.CmdWatch, gFlags.Session, args)
		},
	}
	c.Flags().StringVar(&addExpr, "add", "", "add an expression")
	c.Flags().IntVar(&rmID, "remove", 0, "remove watch by id")
	c.Flags().BoolVar(&rmAll, "remove-all", false, "remove all watches")
	c.Flags().IntVar(&frame, "frame", 0, "stack frame to evaluate in")
	return c
}

func newBreaksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "breaks",
		Short: "List all breakpoints",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdBreaks, gFlags.Session, nil)
		},
	}
}

func newUnbreakCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "unbreak <id>...",
		Short: "Remove breakpoint(s) by id",
		RunE: func(_ *cobra.Command, a []string) error {
			ids := make([]int, 0, len(a))
			for _, s := range a {
				n, err := atoi(s)
				if err != nil {
					return Usage("invalid id: %s", s)
				}
				ids = append(ids, n)
			}
			return callRaw(proto.CmdUnbreak, gFlags.Session, proto.UnbreakArgs{IDs: ids, All: all})
		},
	}
	c.Flags().BoolVar(&all, "all", false, "remove all")
	return c
}

// --- Execution ---

func newRunCmd() *cobra.Command {
	// "run" maps to continue from the stopped-on-entry initial pause.
	return &cobra.Command{
		Use:   "run",
		Short: "Resume from entry pause (alias for continue)",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdContinue, gFlags.Session, proto.ContinueArgs{})
		},
	}
}

func newContinueCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "continue",
		Aliases: []string{"c"},
		Short:   "Resume until next pause",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdContinue, gFlags.Session, proto.ContinueArgs{
				TimeoutSec: timeoutSec(),
			})
		},
	}
}

func newStepCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "step",
		Aliases: []string{"si"},
		Short:   "Step into",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdStep, gFlags.Session, proto.StepArgs{TimeoutSec: timeoutSec()})
		},
	}
}

func newNextCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "next",
		Aliases: []string{"n"},
		Short:   "Step over",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdNext, gFlags.Session, proto.StepArgs{TimeoutSec: timeoutSec()})
		},
	}
}

func newFinishCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "finish",
		Aliases: []string{"out"},
		Short:   "Step out",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdFinish, gFlags.Session, proto.StepArgs{TimeoutSec: timeoutSec()})
		},
	}
}

func newPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause a running target",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdPause, gFlags.Session, nil)
		},
	}
}

func newUntilCmd() *cobra.Command {
	var thread int
	var timeoutSec float64
	c := &cobra.Command{
		Use:   "until <line>",
		Short: "Continue execution until a given line in the current file",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			n, err := atoi(a[0])
			if err != nil || n <= 0 {
				return Usage("invalid line: %s", a[0])
			}
			return callRaw(proto.CmdUntil, gFlags.Session, proto.UntilArgs{
				Line: n, Thread: thread, TimeoutSec: timeoutSec,
			})
		},
	}
	c.Flags().IntVar(&thread, "thread", 0, "thread id (default: current)")
	c.Flags().Float64Var(&timeoutSec, "timeout", 30, "max seconds to wait")
	return c
}

func newGotoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "goto <line>",
		Short: "Jump to line [planned]",
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
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdStack, gFlags.Session, proto.StackArgs{Thread: thread, Limit: limit})
		},
	}
	c.Flags().IntVar(&thread, "thread", 0, "thread id (default: current)")
	c.Flags().IntVar(&limit, "limit", 20, "max frames")
	return c
}

func newThreadsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "threads",
		Short: "List threads",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdThreads, gFlags.Session, nil)
		},
	}
}

func newLocalsCmd() *cobra.Command {
	var frame int
	c := &cobra.Command{
		Use:   "locals",
		Short: "Print local variables",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdLocals, gFlags.Session, proto.LocalsArgs{Frame: frame})
		},
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index (0 = top)")
	return c
}

func newGlobalsCmd() *cobra.Command {
	var frame int
	c := &cobra.Command{
		Use:   "globals",
		Short: "Print global / module-level variables",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdGlobals, gFlags.Session, proto.GlobalsArgs{Frame: frame})
		},
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index")
	return c
}

func newFieldsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "fields <ref>",
		Short: "Expand an object/collection by its variables-reference",
		Args:  cobra.ExactArgs(1),
		Example: `  # First get a ref from locals or eval:
  sl-dbg locals
  sl-dbg fields 7`,
		RunE: func(_ *cobra.Command, a []string) error {
			ref, err := atoi(a[0])
			if err != nil || ref <= 0 {
				return Usage("invalid ref: %s", a[0])
			}
			return callRaw(proto.CmdFields, gFlags.Session, proto.FieldsArgs{Ref: ref})
		},
	}
	return c
}

func newEvalCmd() *cobra.Command {
	var frame int
	var timeoutSec float64
	c := &cobra.Command{
		Use:   "eval <expression>",
		Short: "Evaluate an expression",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			return callRaw(proto.CmdEval, gFlags.Session, proto.EvalArgs{
				Expression: a[0], Frame: frame, TimeoutSec: timeoutSec,
			})
		},
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index")
	c.Flags().Float64Var(&timeoutSec, "timeout", 0, "max seconds for the evaluation (0 = adapter default)")
	return c
}

func newSetVarCmd() *cobra.Command {
	var frame int
	c := &cobra.Command{
		Use:   "set <name> <value>",
		Short: "Modify a variable",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, a []string) error {
			return callRaw(proto.CmdSet, gFlags.Session, proto.SetVarArgs{Name: a[0], Value: a[1], Frame: frame})
		},
	}
	c.Flags().IntVar(&frame, "frame", 0, "frame index")
	return c
}

func newSnapshotCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "snapshot",
		Short: "Dump full state (stack + locals + globals) as JSON",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdSnapshot, gFlags.Session, nil)
		},
	}
}

func newSourceCmd() *cobra.Command {
	var file string
	var line, around int
	c := &cobra.Command{
		Use:   "source",
		Short: "Show source code (defaults to current pause location)",
		Example: `  sl-dbg source                 # source at current line, all lines
  sl-dbg source --around 5      # 5 lines on each side of current
  sl-dbg source --file Buggy.java --line 24 --around 3`,
		RunE: func(*cobra.Command, []string) error {
			af := file
			if af != "" && !strings.HasPrefix(af, "/") {
				af = abs(af)
			}
			return callRaw(proto.CmdSource, gFlags.Session, proto.SourceArgs{
				File: af, Line: line, Around: around,
			})
		},
	}
	c.Flags().StringVar(&file, "file", "", "source file (default: current)")
	c.Flags().IntVar(&line, "line", 0, "centerline (default: current)")
	c.Flags().IntVar(&around, "around", 0, "lines of context on each side (0 = whole file)")
	return c
}

// --- I/O ---

func newOutputCmd() *cobra.Command {
	var since string
	var tail int
	c := &cobra.Command{
		Use:   "output",
		Short: "Drain captured target stdout/stderr",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdOutput, gFlags.Session, proto.OutputArgs{Since: since, Tail: tail})
		},
	}
	c.Flags().StringVar(&since, "since", "", "RFC3339 timestamp; only return newer entries")
	c.Flags().IntVar(&tail, "tail", 0, "return only the last N entries (0 = all)")
	return c
}

func newEventsCmd() *cobra.Command {
	var since string
	var tail int
	c := &cobra.Command{
		Use:   "events",
		Short: "Get the session DAP event log",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdEvents, gFlags.Session, proto.EventsArgs{Since: since, Tail: tail})
		},
	}
	c.Flags().StringVar(&since, "since", "", "RFC3339 timestamp; only return newer events")
	c.Flags().IntVar(&tail, "tail", 0, "return only the last N events (0 = all)")
	return c
}

// --- Meta ---

func newAdaptersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "adapters",
		Short: "List registered language adapters and detection status",
		RunE: func(*cobra.Command, []string) error {
			return callRaw(proto.CmdAdapters, "", nil)
		},
	}
}

func newInstallAdapterCmd() *cobra.Command {
	return newInstallAdapterCmdImpl()
}

// newDaemonCmd is satisfied by daemon_cmd.go's newDaemonCmd2. We use it here
// to keep root.go's command-registration list unchanged.
func newDaemonCmd() *cobra.Command { return newDaemonCmd2() }

func newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run as an MCP server over stdio",
		Long: `Speak the Model Context Protocol over stdin/stdout, exposing every
sl-dbg command as an MCP tool. Designed to be wired into Claude Desktop,
Cursor, Continue, or any MCP-aware client.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv := mcp.NewServer(os.Stdin, os.Stdout, mcpDaemonCaller{})
			return srv.Run(context.Background())
		},
	}
}

// --- helpers ---

// callRaw is the universal CLI->daemon bridge: send a request, emit the response.
func callRaw(cmd, sess string, args interface{}) error {
	var raw []byte
	if args != nil {
		raw = rawJSON(args)
	}
	resp, err := daemonCall(proto.Request{Cmd: cmd, Sess: sess, Args: raw})
	if err != nil {
		emitErr("DAEMON_UNREACHABLE", err.Error(), "")
		return err
	}
	return emitResp(resp)
}

func atoi(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errZeroLen
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadDigit
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

var (
	errZeroLen  = &simpleErr{"empty number"}
	errBadDigit = &simpleErr{"non-digit"}
)

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

func timeoutSec() float64 {
	// gFlags.Timeout is a duration string ("30s"). Trim "s" if present.
	t := gFlags.Timeout
	if t == "" {
		return 30
	}
	// Quick parse: accept "30s", "1m", or numeric seconds.
	var n float64
	var unit byte = 's'
	end := len(t)
	if t[end-1] < '0' || t[end-1] > '9' {
		unit = t[end-1]
		end--
	}
	for i := 0; i < end; i++ {
		if t[i] >= '0' && t[i] <= '9' {
			n = n*10 + float64(t[i]-'0')
		}
	}
	switch unit {
	case 'm':
		return n * 60
	default:
		return n
	}
}
