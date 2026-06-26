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
)

type StartArgs struct {
	Lang        string   `json:"lang"`
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
}

type EvalArgs struct {
	Expression string `json:"expression"`
	Frame      int    `json:"frame,omitempty"`
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
