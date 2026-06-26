// Package proto defines the wire types exchanged between the sl-dbg CLI and
// the sl-dbg daemon over the IPC socket. Wire format: line-delimited JSON.
package proto

import "encoding/json"

type Request struct {
	ID   int             `json:"id"`
	Cmd  string          `json:"cmd"`
	Sess string          `json:"sess,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

type Response struct {
	ID    int             `json:"id"`
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *RespError      `json:"error,omitempty"`
}

type RespError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

const (
	CmdPing     = "ping"
	CmdShutdown = "shutdown"
	CmdStart    = "start"
	CmdAttach   = "attach"
	CmdSessions = "sessions"
	CmdUse      = "use"
	CmdStop     = "stop"
	CmdState    = "state"
	CmdBreak    = "break"
	CmdBreakFn  = "break-fn"
	CmdBreakEx  = "break-ex"
	CmdBreaks   = "breaks"
	CmdUnbreak  = "unbreak"
	CmdContinue = "continue"
	CmdStep     = "step"
	CmdNext     = "next"
	CmdFinish   = "finish"
	CmdPause    = "pause"
	CmdStack    = "stack"
	CmdThreads  = "threads"
	CmdLocals   = "locals"
	CmdEval     = "eval"
	CmdSet      = "set"
	CmdSnapshot = "snapshot"
	CmdAdapters = "adapters"

	// New in v1: feature-parity commands.
	CmdWatch     = "watch"
	CmdGlobals   = "globals"
	CmdFields    = "fields"
	CmdSource    = "source"
	CmdOutput    = "output"
	CmdEvents    = "events"
	CmdListen    = "listen"
	CmdRestart   = "restart"
	CmdUntil     = "until"
	CmdRun       = "run"
	CmdPrint     = "print"
)

// PrintArgs requests a deep, recursive variable dump.
// Provide either Expression (eval first, then walk) or Ref (walk an existing
// variablesReference from a prior locals/eval/fields call). Depth limits the
// recursion; 0 means a single level (like a plain eval/fields).
type PrintArgs struct {
	Expression string `json:"expression,omitempty"`
	Ref        int    `json:"ref,omitempty"`
	Frame      int    `json:"frame,omitempty"`
	Depth      int    `json:"depth,omitempty"`     // recursion levels; default 3
	MaxItems   int    `json:"maxItems,omitempty"`  // per-container cap; default 50
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

// PrintNode is one node in the rendered variable tree.
type PrintNode struct {
	Name     string      `json:"name,omitempty"`
	Value    string      `json:"value"`
	Type     string      `json:"type,omitempty"`
	Ref      int         `json:"ref,omitempty"`
	Truncated bool       `json:"truncated,omitempty"`
	Children []PrintNode `json:"children,omitempty"`
}

type PrintResult struct {
	Root PrintNode `json:"root"`
}

// WatchAction selects whether to add, remove, or list watch expressions.
type WatchArgs struct {
	Action     string `json:"action"`               // "add" | "remove" | "list"
	Expression string `json:"expression,omitempty"` // for add
	ID         int    `json:"id,omitempty"`         // for remove
	Frame      int    `json:"frame,omitempty"`
	All        bool   `json:"all,omitempty"`
}

type WatchEntry struct {
	ID         int    `json:"id"`
	Expression string `json:"expression"`
	Result     string `json:"result,omitempty"`
	Type       string `json:"type,omitempty"`
	Error      string `json:"error,omitempty"`
}

type WatchResult struct {
	Watches []WatchEntry `json:"watches"`
}

type BreakFnArgs struct {
	Function  string `json:"function"`
	Condition string `json:"condition,omitempty"`
	Hit       int    `json:"hit,omitempty"`
}

type BreakExArgs struct {
	Filters []string `json:"filters"` // e.g., ["uncaught"] or adapter-specific names
}

type FieldsArgs struct {
	Ref int `json:"ref"`
}

type GlobalsArgs struct {
	Frame int `json:"frame,omitempty"`
}

type SourceArgs struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Around  int    `json:"around,omitempty"` // num lines of context around line; 0 = whole file
	Ref     int    `json:"sourceRef,omitempty"`
}

type SourceResult struct {
	File    string   `json:"file"`
	Start   int      `json:"start"`
	Lines   []string `json:"lines"`
	Current int      `json:"current,omitempty"`
}

type OutputArgs struct {
	Since string `json:"since,omitempty"` // RFC3339Nano timestamp; only return entries after this
	Tail  int    `json:"tail,omitempty"`  // last N entries (0 = all)
}

type OutputEntry struct {
	TS       string `json:"ts"`
	Category string `json:"category"` // "stdout" | "stderr" | "console" | "telemetry"
	Output   string `json:"output"`
}

type OutputResult struct {
	Entries []OutputEntry `json:"entries"`
}

type EventsArgs struct {
	Since string `json:"since,omitempty"`
	Tail  int    `json:"tail,omitempty"`
}

type EventEntry struct {
	TS     string                 `json:"ts"`
	Type   string                 `json:"type"`
	Body   map[string]interface{} `json:"body,omitempty"`
}

type EventsResult struct {
	Events []EventEntry `json:"events"`
}

type ListenArgs struct {
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

type RestartArgs struct{}

type UntilArgs struct {
	Line       int     `json:"line"`
	Thread     int     `json:"thread,omitempty"`
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

type StartArgs struct {
	Lang        string   `json:"lang"`
	Name        string   `json:"name,omitempty"`
	Program     string   `json:"program"`
	Args        []string `json:"args,omitempty"`
	Cwd         string   `json:"cwd,omitempty"`
	Env         []string `json:"env,omitempty"`
	StopOnEntry bool     `json:"stopOnEntry,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	MainClass   string   `json:"mainClass,omitempty"`
	Classpath   string   `json:"classpath,omitempty"`
}

type AttachArgs struct {
	Lang        string   `json:"lang"`
	Name        string   `json:"name,omitempty"`
	Host        string   `json:"host,omitempty"`
	Port        int      `json:"port,omitempty"`
	PID         int      `json:"pid,omitempty"`
	ReadOnly    bool     `json:"readOnly,omitempty"`
	SourceRoots []string `json:"sourceRoots,omitempty"`
}

type SessionResult struct {
	SessionID string `json:"session"`
	Lang      string `json:"lang"`
	State     string `json:"state"`
	Reason    string `json:"reason,omitempty"`
	Location  *Loc   `json:"location,omitempty"`
}

type BreakArgs struct {
	Location  string `json:"location"`
	Condition string `json:"condition,omitempty"`
	Hit       int    `json:"hit,omitempty"`
	LogMsg    string `json:"logMsg,omitempty"`
	Once      bool   `json:"once,omitempty"`
}

type BreakResult struct {
	ID        int    `json:"id"`
	Verified  bool   `json:"verified"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Function  string `json:"function,omitempty"`
	Condition string `json:"condition,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type UnbreakArgs struct {
	IDs []int `json:"ids,omitempty"`
	All bool  `json:"all,omitempty"`
}

type BreaksResult struct {
	Breakpoints []BreakResult `json:"breakpoints"`
}

type ContinueArgs struct {
	Thread     int     `json:"thread,omitempty"`
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

type StepArgs struct {
	Thread     int     `json:"thread,omitempty"`
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

type StackArgs struct {
	Thread int `json:"thread,omitempty"`
	Limit  int `json:"limit,omitempty"`
}

type Frame struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Function string `json:"function,omitempty"`
}

type StackResult struct {
	Frames []Frame `json:"frames"`
}

type LocalsArgs struct {
	Frame int `json:"frame,omitempty"`
}

type Var struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	Type       string `json:"type,omitempty"`
	Ref        int    `json:"ref,omitempty"`
	Expandable bool   `json:"expandable,omitempty"`
}

type LocalsResult struct {
	Scope string `json:"scope"`
	Vars  []Var  `json:"vars"`
	Hint  string `json:"hint,omitempty"`
}

type EvalArgs struct {
	Expression string  `json:"expression"`
	Frame      int     `json:"frame,omitempty"`
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
}

type EvalResult struct {
	Result string `json:"result"`
	Type   string `json:"type,omitempty"`
	Ref    int    `json:"ref,omitempty"`
}

type SetVarArgs struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Frame int    `json:"frame,omitempty"`
}

type Loc struct {
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Function string `json:"function,omitempty"`
}

type PauseInfo struct {
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Thread   int    `json:"thread,omitempty"`
	Location *Loc   `json:"location,omitempty"`
	HitBP    int    `json:"hitBreakpoint,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Message  string `json:"message,omitempty"`
}

type SessionInfo struct {
	ID       string `json:"id"`
	Lang     string `json:"lang"`
	State    string `json:"state"`
	Program  string `json:"program,omitempty"`
	Attached string `json:"attached,omitempty"`
	Default  bool   `json:"default,omitempty"`
}

type SessionsResult struct {
	Sessions []SessionInfo `json:"sessions"`
}

type SnapshotResult struct {
	State     string  `json:"state"`
	Location  *Loc    `json:"location,omitempty"`
	Thread    int     `json:"thread,omitempty"`
	Frames    []Frame `json:"frames"`
	Locals    []Var   `json:"locals"`
	Globals   []Var   `json:"globals,omitempty"`
	Exception string  `json:"exception,omitempty"`
}

type AdapterInfo struct {
	Lang      string `json:"lang"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

type AdaptersResult struct {
	Adapters []AdapterInfo `json:"adapters"`
}
