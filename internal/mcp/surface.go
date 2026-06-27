// Surface extensions for the MCP server: allowlist enforcement, resources
// list/read, and prompt templates. Kept in a separate file from server.go
// to keep the core JSON-RPC loop short and easy to audit.
package mcp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/y0geshpatil/sl-dbg/internal/proto"
)

// enforceAllowlist returns a non-nil error when the configured AllowCwd /
// DenyProgram lists reject the start/attach about to be issued. Issue:
// mcp-allowlist.
func (s *Server) enforceAllowlist(cmd string, daemonArgs interface{}) error {
	if len(s.opts.AllowCwd) == 0 && len(s.opts.DenyProgram) == 0 {
		return nil
	}
	// daemonArgs is either StartArgs or AttachArgs; round-trip through JSON
	// so we don't import every concrete type and stay tolerant of additions.
	b, err := json.Marshal(daemonArgs)
	if err != nil {
		return nil // shouldn't happen; let downstream surface the issue
	}
	var probe struct {
		Program string `json:"program,omitempty"`
		Cwd     string `json:"cwd,omitempty"`
	}
	_ = json.Unmarshal(b, &probe)

	for _, deny := range s.opts.DenyProgram {
		if deny != "" && strings.Contains(strings.ToLower(probe.Program), strings.ToLower(deny)) {
			return fmt.Errorf("POLICY_DENIED: program %q matches deny rule %q", probe.Program, deny)
		}
	}
	if len(s.opts.AllowCwd) > 0 {
		target := probe.Cwd
		if target == "" {
			target = probe.Program
		}
		if target == "" {
			// attach by pid/port — nothing to allowlist against.
			return nil
		}
		abs, err := filepath.Abs(target)
		if err != nil {
			abs = target
		}
		abs = filepath.Clean(abs)
		ok := false
		for _, root := range s.opts.AllowCwd {
			rootAbs, err := filepath.Abs(root)
			if err != nil {
				continue
			}
			rootAbs = filepath.Clean(rootAbs)
			if abs == rootAbs || strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("POLICY_DENIED: %s outside allowed cwd roots %v", abs, s.opts.AllowCwd)
		}
	}
	_ = cmd
	return nil
}

// --- resources ------------------------------------------------------------

type resourceDesc struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// listResources advertises three first-class resources clients can read
// without going through tools/call. Issue: mcp-resources.
func (s *Server) listResources() []resourceDesc {
	return []resourceDesc{
		{
			URI:         "sl-dbg://sessions",
			Name:        "sessions",
			Description: "Live debug sessions managed by the daemon (JSON list).",
			MimeType:    "application/json",
		},
		{
			URI:         "sl-dbg://events",
			Name:        "events",
			Description: "Recent DAP events from the current session (JSON list).",
			MimeType:    "application/json",
		},
		{
			URI:         "sl-dbg://adapters",
			Name:        "adapters",
			Description: "Installed-status of language adapters (JSON list).",
			MimeType:    "application/json",
		},
	}
}

func (s *Server) handleResourceRead(req rpcReq) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32602, Message: err.Error()}})
		return
	}
	var cmd, sess string
	switch p.URI {
	case "sl-dbg://sessions":
		cmd = proto.CmdSessions
	case "sl-dbg://events":
		cmd = proto.CmdEvents
		sess = s.resolveSession("")
	case "sl-dbg://adapters":
		cmd = proto.CmdAdapters
	default:
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32602, Message: "unknown resource uri: " + p.URI}})
		return
	}
	raw, err := s.caller.Call(cmd, sess, nil)
	if err != nil {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32000, Message: err.Error()}})
		return
	}
	s.write(rpcResp{ID: req.ID, Result: map[string]interface{}{
		"contents": []map[string]interface{}{
			{"uri": p.URI, "mimeType": "application/json", "text": string(raw)},
		},
	}})
}

// --- prompts --------------------------------------------------------------

type promptArg struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type promptDesc struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Arguments   []promptArg `json:"arguments,omitempty"`
}

// listPrompts returns the prompt templates this server ships. Each describes
// a known debugging workflow an agent can follow. Issue: mcp-prompts.
func listPrompts() []promptDesc {
	return []promptDesc{
		{
			Name:        "diagnose_loop_bug",
			Description: "Pause the suspected loop, inspect loop vars across iterations, and report what diverges.",
			Arguments: []promptArg{
				{Name: "file", Required: true, Description: "Source file containing the loop"},
				{Name: "line", Required: true, Description: "1-based line number of a stoppable instruction inside the loop"},
			},
		},
		{
			Name:        "trace_call_path",
			Description: "Set a function breakpoint, run, then walk the stack from the hit frame outward.",
			Arguments: []promptArg{
				{Name: "function", Required: true, Description: "Function/method name (and ClassName.method for Java)"},
			},
		},
		{
			Name:        "watch_then_continue",
			Description: "Add a watch expression and continue until it evaluates to a target value.",
			Arguments: []promptArg{
				{Name: "expr", Required: true, Description: "Expression to watch"},
				{Name: "until", Required: false, Description: "Optional value to wait for"},
			},
		},
	}
}

func (s *Server) handlePromptGet(req rpcReq) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32602, Message: err.Error()}})
		return
	}
	body, ok := promptBody(p.Name, p.Arguments)
	if !ok {
		s.write(rpcResp{ID: req.ID, Error: &rpcErr{Code: -32602, Message: "unknown prompt: " + p.Name}})
		return
	}
	s.write(rpcResp{ID: req.ID, Result: map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "user", "content": map[string]interface{}{"type": "text", "text": body}},
		},
	}})
}

func promptBody(name string, args map[string]string) (string, bool) {
	switch name {
	case "diagnose_loop_bug":
		file := args["file"]
		line := args["line"]
		return fmt.Sprintf(`Use the sl-dbg tools to diagnose a loop bug at %s:%s.
1. debug_break %s:%s
2. debug_continue and capture the stop reason.
3. debug_locals — record loop counter + accumulator values.
4. debug_continue, repeat steps 3–4 for ~5 iterations.
5. Report which variable diverges from the expected progression.`, file, line, file, line), true
	case "trace_call_path":
		fn := args["function"]
		return fmt.Sprintf(`Trace the call path into %s:
1. debug_break_fn %s
2. debug_run (or debug_continue if already paused).
3. On stop, debug_stack with depth=20.
4. Summarize the call chain and what arguments led here.`, fn, fn), true
	case "watch_then_continue":
		expr := args["expr"]
		until := args["until"]
		text := fmt.Sprintf("Watch %q and continue until interesting.\n1. debug_watch action=add expr=%q\n2. debug_continue\n3. debug_watch action=eval — record value.\n", expr, expr)
		if until != "" {
			text += fmt.Sprintf("4. Repeat continue+eval until value == %s.", until)
		}
		return text, true
	}
	return "", false
}
