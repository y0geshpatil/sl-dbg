// Package mcp implements a minimal stdio Model Context Protocol server that
// exposes sl-dbg commands as MCP tools. JSON-RPC 2.0 over stdin/stdout.
//
// Spec we implement (subset):
//   initialize, tools/list, tools/call, shutdown, notifications/initialized
//
// Each tool's implementation goes through the same IPC path the CLI uses,
// so MCP and CLI invocations are 100% behaviorally identical.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/yogeshpatil/sl-dbg/internal/buildinfo"
	"github.com/yogeshpatil/sl-dbg/internal/proto"
)

// DaemonCaller is the minimal surface a tool needs to talk to the daemon.
// Production wiring uses internal/cli.callDaemonRaw; tests can substitute.
type DaemonCaller interface {
	Call(cmd, sess string, args interface{}) (json.RawMessage, error)
}

// Options tune how the MCP server presents itself to clients.
type Options struct {
	// ReadOnly hides mutating tools (start, attach, set, watch add/remove,
	// run/continue/step/stop/restart, eval with side effects). Inspection-only
	// tools remain available — ideal when handing the server to an untrusted
	// agent that should observe but not perturb the target.
	ReadOnly bool
}

// Server is one MCP session over stdio.
type Server struct {
	in     *bufio.Reader
	out    io.Writer
	outMu  sync.Mutex
	caller DaemonCaller
	opts   Options
}

func NewServer(in io.Reader, out io.Writer, caller DaemonCaller) *Server {
	return NewServerWithOptions(in, out, caller, Options{})
}

// NewServerWithOptions is the option-aware constructor used by `sl-dbg mcp`
// when flags like --read-only are present.
func NewServerWithOptions(in io.Reader, out io.Writer, caller DaemonCaller, opts Options) *Server {
	return &Server{in: bufio.NewReaderSize(in, 1<<20), out: out, caller: caller, opts: opts}
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Run drives the stdio loop until EOF or context cancellation.
func (s *Server) Run(ctx context.Context) error {
	dec := json.NewDecoder(s.in)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var req rpcReq
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("decode: %w", err)
		}
		s.handle(req)
	}
}

func (s *Server) write(r rpcResp) {
	r.JSONRPC = "2.0"
	b, _ := json.Marshal(r)
	s.outMu.Lock()
	defer s.outMu.Unlock()
	_, _ = s.out.Write(b)
	_, _ = s.out.Write([]byte("\n"))
}

func (s *Server) handle(req rpcReq) {
	notification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		s.write(rpcResp{ID: req.ID, Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "sl-dbg",
				"version": buildinfo.Version,
			},
		}})
	case "notifications/initialized", "initialized":
		// no-op
	case "tools/list":
		if notification {
			return
		}
		s.write(rpcResp{ID: req.ID, Result: map[string]interface{}{"tools": s.visibleTools()}})
	case "tools/call":
		if notification {
			return
		}
		s.handleToolCall(req)
	case "shutdown":
		if !notification {
			s.write(rpcResp{ID: req.ID, Result: map[string]interface{}{}})
		}
		os.Exit(0)
	case "exit":
		os.Exit(0)
	default:
		if !notification {
			s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32601, Message: "method not found: " + req.Method}})
		}
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) handleToolCall(req rpcReq) {
	var p callParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32602, Message: err.Error()}})
		return
	}
	tool, ok := toolByName(p.Name)
	if !ok || (s.opts.ReadOnly && tool.Mutating) {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32601, Message: "unknown tool: " + p.Name}})
		return
	}
	// Issue #26: validate args against the tool's inputSchema before
	// forwarding to the daemon. Catches the obvious agent mistakes
	// (missing required field, wrong enum value, wrong type) with a clear
	// MCP error instead of letting the daemon return a cryptic ADAPTER_FAILED.
	if verr := validateArgs(tool.InputSchema, p.Arguments); verr != nil {
		s.write(rpcResp{ID: req.ID, Result: errorContent(verr.Error())})
		return
	}
	// Composite/custom handler: tool runs its own logic, possibly making
	// several daemon calls. Bypass the standard Translate path.
	if tool.Handler != nil {
		sess, rest, err := extractSession(p.Arguments)
		if err != nil {
			s.write(rpcResp{ID: req.ID, Result: errorContent(err.Error())})
			return
		}
		sess = s.resolveSession(sess)
		result, herr := tool.Handler(s, sess, rest)
		if herr != nil {
			s.write(rpcResp{ID: req.ID, Result: errorContent(herr.Error())})
			return
		}
		b, _ := json.Marshal(result)
		s.write(rpcResp{ID: req.ID, Result: textContent(string(b))})
		return
	}
	cmd, sess, daemonArgs, err := tool.Translate(p.Arguments)
	if err != nil {
		s.write(rpcResp{ID: req.ID, Result: errorContent(err.Error())})
		return
	}
	sess = s.resolveSession(sess)
	raw, callErr := s.caller.Call(cmd, sess, daemonArgs)
	if callErr != nil {
		s.write(rpcResp{ID: req.ID, Result: errorContent(callErr.Error())})
		return
	}
	s.write(rpcResp{ID: req.ID, Result: textContent(string(raw))})
}

// resolveSession defaults an empty session to the daemon's current default
// (the most recently started session). This removes a class of friction
// for agents that don't track session ids. Callers can override by passing
// "session" in their tool args.
func (s *Server) resolveSession(sess string) string {
	if sess != "" {
		return sess
	}
	raw, err := s.caller.Call("sessions", "", nil)
	if err != nil {
		return ""
	}
	var sr struct {
		Sessions []struct {
			ID      string `json:"id"`
			Default bool   `json:"default"`
			State   string `json:"state"`
		} `json:"sessions"`
	}
	if json.Unmarshal(raw, &sr) != nil {
		return ""
	}
	// Prefer the daemon-declared default (matches CLI behaviour).
	for _, ss := range sr.Sessions {
		if ss.Default {
			return ss.ID
		}
	}
	// Fall back to the only live (not-exited) session if there is exactly one.
	var live []string
	for _, ss := range sr.Sessions {
		if ss.State != "exited" && ss.State != "terminated" {
			live = append(live, ss.ID)
		}
	}
	if len(live) == 1 {
		return live[0]
	}
	if len(sr.Sessions) == 1 {
		return sr.Sessions[0].ID
	}
	return ""
}

// visibleTools returns the tool list visible to the current client. In
// read-only mode, tools tagged Mutating are filtered out.
func (s *Server) visibleTools() []Tool {
	if !s.opts.ReadOnly {
		return toolRegistry
	}
	out := make([]Tool, 0, len(toolRegistry))
	for _, t := range toolRegistry {
		if t.Mutating {
			continue
		}
		out = append(out, t)
	}
	return out
}

func textContent(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": false,
	}
}

func errorContent(msg string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": msg}},
		"isError": true,
	}
}

// Tool describes one MCP tool and how to translate its arguments to a daemon
// command + args.
//
// Tools come in two flavours:
//
//   - Simple tools set Translate. The server unmarshals arguments, invokes a
//     single daemon command, and returns the daemon's reply verbatim.
//   - Composite tools set Handler. They own the entire request: they may issue
//     several daemon calls (via Server.caller) and return a custom payload.
//
// Mutating marks tools that can change debugger or program state. The MCP
// server hides them from clients when launched with --read-only, so an
// untrusted agent can observe but never perturb the target.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Mutating    bool                   `json:"-"`
	Translate   func(raw json.RawMessage) (string, string, interface{}, error) `json:"-"`
	Handler     func(s *Server, sess string, args json.RawMessage) (interface{}, error) `json:"-"`
}

// Tools returns the static list of tools exposed to MCP clients.
func Tools() []Tool { return toolRegistry }

func toolByName(name string) (Tool, bool) {
	for _, t := range toolRegistry {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

func objectSchema(required []string, props map[string]interface{}) map[string]interface{} {
	// Every tool implicitly accepts an optional `session` arg. Agents that
	// juggle multiple concurrent sessions can pin a call to a specific id;
	// when omitted, the daemon's default session is used.
	if _, taken := props["session"]; !taken {
		props["session"] = map[string]interface{}{
			"type":        "string",
			"description": "optional session id; defaults to the daemon's current default (newest started)",
		}
	}
	return map[string]interface{}{
		"type":       "object",
		"required":   required,
		"properties": props,
	}
}

func stringProp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}
func intProp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "integer", "description": desc}
}
func boolProp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "boolean", "description": desc}
}

// langEnumProp produces a JSON Schema property typed as the closed enum of
// languages sl-dbg currently understands. This lets IDE-style hosts surface a
// dropdown rather than a freeform text input.
func langEnumProp(desc string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": desc,
		"enum":        []string{"python", "go", "java", "node", "cpp", "dotnet", "rust"},
	}
}

// extractSession peels off "session" and returns rest object + session id.
func extractSession(raw json.RawMessage) (string, json.RawMessage, error) {
	if len(raw) == 0 {
		return "", raw, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", raw, err
	}
	sess := ""
	if v, ok := m["session"]; ok {
		_ = json.Unmarshal(v, &sess)
		delete(m, "session")
	}
	rest, _ := json.Marshal(m)
	return sess, rest, nil
}

// toolRegistry: one MCP tool per daemon command. Keep in sync with
// internal/cli/commands.go. Adding a tool here makes it visible to MCP
// clients (Claude Desktop, Cursor, Continue, etc.) immediately.
var toolRegistry = []Tool{
	{
		Name:        "debug_start",
		Description: "Launch a program under the debugger. lang one of python|go|java.",
		Mutating:    true,
		InputSchema: objectSchema([]string{"lang", "program"}, map[string]interface{}{
			"lang":        langEnumProp("language adapter to use"),
			"program":     stringProp("absolute path to program or entrypoint"),
			"args":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "program args"},
			"stopOnEntry": boolProp("pause at program entry"),
			"mainClass":   stringProp("Java main class (when lang=java)"),
			"classpath":   stringProp("Java classpath (when lang=java)"),
			"sourceRoots": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "source roots for path resolution"},
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.StartArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			// Issue #49: on `debug_start`, the optional `session` arg names
			// the NEW session (since the daemon hasn't created it yet).
			// Pass it through as StartArgs.Name and route the request with
			// empty `sess` so the daemon allocates rather than looking up.
			if sess != "" && a.Name == "" {
				a.Name = sess
			}
			return proto.CmdStart, "", a, nil
		},
	},
	{
		Name:        "debug_attach",
		Description: "Attach to a running process over DAP/JDWP.",
		Mutating:    true,
		InputSchema: objectSchema([]string{"lang", "host", "port"}, map[string]interface{}{
			"lang":        langEnumProp("language adapter to use"),
			"host":        stringProp("hostname"),
			"port":        intProp("DAP/JDWP port"),
			"pid":         intProp("alternative to host:port (where supported)"),
			"sourceRoots": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "source roots"},
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.AttachArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			// Issue #49: same fix for `debug_attach` (new session name).
			if sess != "" && a.Name == "" {
				a.Name = sess
			}
			return proto.CmdAttach, "", a, nil
		},
	},
	{
		Name:        "debug_break",
		Mutating:    true,
		Description: "Set a line breakpoint. location is 'file:line'.",
		InputSchema: objectSchema([]string{"location"}, map[string]interface{}{
			"location":  stringProp("file:line"),
			"condition": stringProp("optional condition expression"),
			"hit":       intProp("break only on Nth hit"),
			"logMsg":    stringProp("logpoint message; if set, BP prints without stopping"),
			"once":      boolProp("remove after first hit"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.BreakArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdBreak, sess, a, nil
		},
	},
	{
		Name:        "debug_break_fn",
		Mutating:    true,
		Description: "Function/method entry breakpoint.",
		InputSchema: objectSchema([]string{"function"}, map[string]interface{}{
			"function":  stringProp("fully-qualified function name"),
			"condition": stringProp("optional condition"),
			"hit":       intProp("hit count"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.BreakFnArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdBreakFn, sess, a, nil
		},
	},
	{
		Name:        "debug_break_ex",
		Mutating:    true,
		Description: "Enable exception breakpoints. filters: uncaught | raised | adapter-specific.",
		InputSchema: objectSchema([]string{"filters"}, map[string]interface{}{
			"filters": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "filter names"},
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.BreakExArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdBreakEx, sess, a, nil
		},
	},
	{
		Name:        "debug_continue",
		Mutating:    true,
		Description: "Resume execution. Blocks until next pause / exit.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"timeoutSec": map[string]interface{}{"type": "number", "description": "max seconds to wait"},
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.ContinueArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdContinue, sess, a, nil
		},
	},
	{
		Name:        "debug_step",
		Mutating:    true,
		Description: "Step into the next call.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdStep, sess, proto.StepArgs{}, nil
		},
	},
	{
		Name:        "debug_next",
		Mutating:    true,
		Description: "Step over (next line, same frame).",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdNext, sess, proto.StepArgs{}, nil
		},
	},
	{
		Name:        "debug_finish",
		Mutating:    true,
		Description: "Step out of the current function.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdFinish, sess, proto.StepArgs{}, nil
		},
	},
	{
		Name:        "debug_pause",
		Mutating:    true,
		Description: "Suspend the running target.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdPause, sess, nil, nil
		},
	},
	{
		Name:        "debug_until",
		Mutating:    true,
		Description: "Continue execution until a given line in the current source file.",
		InputSchema: objectSchema([]string{"line"}, map[string]interface{}{
			"line": intProp("target line in current file"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.UntilArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdUntil, sess, a, nil
		},
	},
	{
		Name:        "debug_stack",
		Description: "Return the current call stack.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"thread": intProp("thread id (default: current)"),
			"limit":  intProp("max frames to return (default: 20)"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.StackArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdStack, sess, a, nil
		},
	},
	{
		Name:        "debug_threads",
		Description: "List all threads in the target process.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdThreads, sess, nil, nil
		},
	},
	{
		Name:        "debug_breaks",
		Description: "List all breakpoints (line, function, and exception).",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdBreaks, sess, nil, nil
		},
	},
	{
		Name:        "debug_unbreak",
		Mutating:    true,
		Description: "Remove one or more breakpoints by id; or all of them.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"ids": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}, "description": "breakpoint ids to remove"},
			"all": boolProp("remove every breakpoint in the session"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.UnbreakArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdUnbreak, sess, a, nil
		},
	},
	{
		Name:        "debug_set",
		Mutating:    true,
		Description: "Mutate a local variable in the current (or specified) frame.",
		InputSchema: objectSchema([]string{"name", "value"}, map[string]interface{}{
			"name":  stringProp("variable name as shown in debug_locals"),
			"value": stringProp("new value, expressed in the target language's syntax (e.g. '42', '\"hi\"', 'true')"),
			"frame": intProp("frame index (0 = top)"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.SetVarArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdSet, sess, a, nil
		},
	},
	{
		Name:        "debug_locals",
		Description: "Return local variables at the current frame.",
		InputSchema: objectSchema(nil, map[string]interface{}{"frame": intProp("frame index (0 = top)")}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.LocalsArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdLocals, sess, a, nil
		},
	},
	{
		Name:        "debug_globals",
		Description: "Return module/global variables.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdGlobals, sess, proto.GlobalsArgs{}, nil
		},
	},
	{
		Name:        "debug_fields",
		Description: "Expand a variables-reference.",
		InputSchema: objectSchema([]string{"ref"}, map[string]interface{}{"ref": intProp("variables reference")}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.FieldsArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdFields, sess, a, nil
		},
	},
	{
		Name:     "debug_eval",
		Mutating: true,
		Description: "Evaluate an expression in the current frame. Use for ad-hoc inspection " +
			"(`x + 1`, `obj.method()`) and quick what-if probes; sl-dbg auto-qualifies bare " +
			"static-field names in Java. DO NOT use for repeated inspection of the same " +
			"expression — use `debug_watch add` instead. Daemon may block expressions matching " +
			"SL_DBG_DENY_EVAL_PATTERNS (default: Java side-effect classes like FileOutputStream).",
		InputSchema: objectSchema([]string{"expression"}, map[string]interface{}{
			"expression": stringProp("expression to evaluate; e.g. `x + 1`, `obj.method()`"),
			"frame":      intProp("frame index (0 = top of stack; default 0)"),
			"timeoutSec": map[string]interface{}{"type": "number", "description": "max seconds for evaluation (default 5)"},
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.EvalArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdEval, sess, a, nil
		},
	},
	{
		Name:        "debug_watch",
		Mutating:    true,
		Description: "Manage watch expressions. action: list | add | remove.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"action":     stringProp("list | add | remove"),
			"expression": stringProp("expression (for add)"),
			"id":         intProp("watch id (for remove)"),
			"all":        boolProp("remove all (for remove)"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.WatchArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdWatch, sess, a, nil
		},
	},
	{
		Name: "debug_source",
		Description: "Show source code around the current pause location, or at any file:line. " +
			"Zero-arg form (`{}`) defaults to the current paused frame. " +
			"DO NOT use to read arbitrary files outside the program — the daemon may block " +
			"paths outside SL_DBG_ALLOW_SOURCE_ROOT with SOURCE_PATH_DENIED.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"file":   stringProp("absolute path; defaults to current pause location"),
			"line":   intProp("center line; defaults to current pause line"),
			"around": intProp("lines of context on each side; 0 = whole file (default 8)"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.SourceArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdSource, sess, a, nil
		},
	},
	{
		Name:        "debug_output",
		Description: "Drain captured target stdout/stderr.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"since": stringProp("RFC3339 timestamp; only newer entries"),
			"tail":  intProp("last N entries"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.OutputArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdOutput, sess, a, nil
		},
	},
	{
		Name:        "debug_events",
		Description: "Return the captured DAP event log.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"since": stringProp("RFC3339 timestamp"),
			"tail":  intProp("last N events"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.EventsArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdEvents, sess, a, nil
		},
	},
	{
		Name:        "debug_listen",
		Description: "Block until the next stop/exit/terminate event.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"timeoutSec": map[string]interface{}{"type": "number"},
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, _ := extractSession(raw)
			var a proto.ListenArgs
			_ = json.Unmarshal(rest, &a)
			return proto.CmdListen, sess, a, nil
		},
	},
	{
		Name:        "debug_snapshot",
		Description: "Full state dump: stack + locals + globals.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdSnapshot, sess, nil, nil
		},
	},
	{
		Name:        "debug_state",
		Description: "Return current session state (paused/running/exited).",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdState, sess, nil, nil
		},
	},
	{
		Name:        "debug_restart",
		Mutating:    true,
		Description: "Restart the debug session (if adapter supports it).",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdRestart, sess, proto.RestartArgs{}, nil
		},
	},
	{
		Name:        "debug_stop",
		Mutating:    true,
		Description: "Terminate the session.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdStop, sess, nil, nil
		},
	},
	{
		Name:        "debug_sessions",
		Description: "List active debug sessions.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			return proto.CmdSessions, "", nil, nil
		},
	},
	{
		Name:        "debug_adapters",
		Description: "List installed language adapters.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			return proto.CmdAdapters, "", nil, nil
		},
	},
	{
		Name:        "debug_print",
		Mutating:    true,
		Description: "Recursively expand a value (collections, nested objects) to a given depth.",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"expression": stringProp("expression to evaluate (or use ref)"),
			"ref":        intProp("variables-reference from a prior locals/eval/fields call"),
			"frame":      intProp("frame index"),
			"depth":      intProp("recursion depth (default 3)"),
			"maxItems":   intProp("max items per container (default 50)"),
		}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, rest, err := extractSession(raw)
			if err != nil {
				return "", "", nil, err
			}
			var a proto.PrintArgs
			if err := json.Unmarshal(rest, &a); err != nil {
				return "", "", nil, err
			}
			return proto.CmdPrint, sess, a, nil
		},
	},
}
