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

// Server is one MCP session over stdio.
type Server struct {
	in     *bufio.Reader
	out    io.Writer
	outMu  sync.Mutex
	caller DaemonCaller
}

func NewServer(in io.Reader, out io.Writer, caller DaemonCaller) *Server {
	return &Server{in: bufio.NewReaderSize(in, 1<<20), out: out, caller: caller}
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
		s.write(rpcResp{ID: req.ID, Result: map[string]interface{}{"tools": Tools()}})
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
	if !ok {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32601, Message: "unknown tool: " + p.Name}})
		return
	}
	cmd, sess, daemonArgs, err := tool.Translate(p.Arguments)
	if err != nil {
		s.write(rpcResp{ID: req.ID, Result: errorContent(err.Error())})
		return
	}
	raw, callErr := s.caller.Call(cmd, sess, daemonArgs)
	if callErr != nil {
		s.write(rpcResp{ID: req.ID, Result: errorContent(callErr.Error())})
		return
	}
	s.write(rpcResp{ID: req.ID, Result: textContent(string(raw))})
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
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	Translate   func(raw json.RawMessage) (string, string, interface{}, error) `json:"-"`
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
		InputSchema: objectSchema([]string{"lang", "program"}, map[string]interface{}{
			"lang":        stringProp("python | go | java | node | cpp | dotnet | rust"),
			"program":     stringProp("absolute path to program or entrypoint"),
			"args":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "program args"},
			"stopOnEntry": boolProp("pause at program entry"),
			"mainClass":   stringProp("Java main class (when lang=java)"),
			"classpath":   stringProp("Java classpath (when lang=java)"),
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
			return proto.CmdStart, sess, a, nil
		},
	},
	{
		Name:        "debug_attach",
		Description: "Attach to a running process over DAP/JDWP.",
		InputSchema: objectSchema([]string{"lang", "host", "port"}, map[string]interface{}{
			"lang":        stringProp("python | go | java"),
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
			return proto.CmdAttach, sess, a, nil
		},
	},
	{
		Name:        "debug_break",
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
		Description: "Step into the next call.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdStep, sess, proto.StepArgs{}, nil
		},
	},
	{
		Name:        "debug_next",
		Description: "Step over (next line, same frame).",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdNext, sess, proto.StepArgs{}, nil
		},
	},
	{
		Name:        "debug_finish",
		Description: "Step out of the current function.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdFinish, sess, proto.StepArgs{}, nil
		},
	},
	{
		Name:        "debug_pause",
		Description: "Suspend the running target.",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdPause, sess, nil, nil
		},
	},
	{
		Name:        "debug_until",
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
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdStack, sess, proto.StackArgs{}, nil
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
		Name:        "debug_eval",
		Description: "Evaluate an expression in the current frame.",
		InputSchema: objectSchema([]string{"expression"}, map[string]interface{}{
			"expression": stringProp("expression"),
			"frame":      intProp("frame index"),
			"timeoutSec": map[string]interface{}{"type": "number", "description": "max seconds for evaluation"},
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
		Name:        "debug_source",
		Description: "Return source code around current location (or a specific file:line).",
		InputSchema: objectSchema(nil, map[string]interface{}{
			"file":   stringProp("path"),
			"line":   intProp("center line"),
			"around": intProp("lines of context on each side; 0 = whole file"),
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
		Description: "Restart the debug session (if adapter supports it).",
		InputSchema: objectSchema(nil, map[string]interface{}{}),
		Translate: func(raw json.RawMessage) (string, string, interface{}, error) {
			sess, _, _ := extractSession(raw)
			return proto.CmdRestart, sess, proto.RestartArgs{}, nil
		},
	},
	{
		Name:        "debug_stop",
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
}
