// Composite MCP tools. Each one bundles several daemon round-trips into a
// single tool call, lowering token cost and round-trip count for AI agents.
//
// Composites set Tool.Handler instead of Tool.Translate. The handler owns the
// full request flow and may issue multiple Server.caller.Call() invocations.
package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/yogeshpatil/sl-dbg/internal/proto"
)

func init() {
	toolRegistry = append(toolRegistry, compositeTools()...)
}

func compositeTools() []Tool {
	return []Tool{
		{
			Name:        "debug_run_until_break",
			Mutating:    true,
			Description: "Set a line breakpoint and resume execution in one call. Returns the resulting pause state.",
			InputSchema: objectSchema([]string{"location"}, map[string]interface{}{
				"location":   stringProp("file:line"),
				"condition":  stringProp("optional condition expression"),
				"timeoutSec": map[string]interface{}{"type": "number", "description": "max seconds to wait for the pause"},
				"once":       boolProp("auto-remove the breakpoint after it fires (default true)"),
			}),
			Handler: handleRunUntilBreak,
		},
		{
			Name:        "debug_inspect_at",
			Mutating:    true,
			Description: "Stop at a location, snapshot locals + evaluate a list of expressions, then optionally remove the breakpoint and continue. Bundles 4-6 normal calls into one.",
			InputSchema: objectSchema([]string{"location"}, map[string]interface{}{
				"location":    stringProp("file:line"),
				"condition":   stringProp("optional condition expression"),
				"expressions": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "expressions to evaluate at the pause"},
				"continue":    boolProp("resume after capturing (default false)"),
				"timeoutSec":  map[string]interface{}{"type": "number", "description": "max seconds to wait for the pause"},
			}),
			Handler: handleInspectAt,
		},
		{
			Name:        "debug_explain_pause",
			Description: "Return an English summary of the current pause: where we are, top frames, key locals, and active watch values. Designed to fit in a small LLM context window.",
			InputSchema: objectSchema(nil, map[string]interface{}{
				"maxVars":   intProp("cap on local variables to include (default 12)"),
				"maxFrames": intProp("cap on stack frames to include (default 5)"),
			}),
			Handler: handleExplainPause,
		},
		{
			Name:        "debug_snapshot_compact",
			Description: "Like debug_snapshot but trims variable lists and skips deep object references. Use this when context budget matters.",
			InputSchema: objectSchema(nil, map[string]interface{}{
				"maxVars":   intProp("cap on variables per scope (default 20)"),
				"maxFrames": intProp("cap on stack frames (default 8)"),
			}),
			Handler: handleSnapshotCompact,
		},
	}
}

// ----- run_until_break -----

type runUntilBreakArgs struct {
	Location   string  `json:"location"`
	Condition  string  `json:"condition,omitempty"`
	TimeoutSec float64 `json:"timeoutSec,omitempty"`
	Once       *bool   `json:"once,omitempty"`
}

func handleRunUntilBreak(s *Server, sess string, raw json.RawMessage) (interface{}, error) {
	var a runUntilBreakArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Location == "" {
		return nil, fmt.Errorf("location is required")
	}
	once := true
	if a.Once != nil {
		once = *a.Once
	}
	bpArgs := proto.BreakArgs{Location: a.Location, Condition: a.Condition, Once: once}
	bpRaw, err := s.caller.Call(proto.CmdBreak, sess, bpArgs)
	if err != nil {
		return nil, fmt.Errorf("break: %w", err)
	}
	contArgs := proto.ContinueArgs{TimeoutSec: a.TimeoutSec}
	contRaw, err := s.caller.Call(proto.CmdContinue, sess, contArgs)
	if err != nil {
		return nil, fmt.Errorf("continue: %w", err)
	}
	return map[string]json.RawMessage{
		"breakpoint": bpRaw,
		"stop":       contRaw,
	}, nil
}

// ----- inspect_at -----

type inspectAtArgs struct {
	Location    string   `json:"location"`
	Condition   string   `json:"condition,omitempty"`
	Expressions []string `json:"expressions,omitempty"`
	Continue    bool     `json:"continue,omitempty"`
	TimeoutSec  float64  `json:"timeoutSec,omitempty"`
}

func handleInspectAt(s *Server, sess string, raw json.RawMessage) (interface{}, error) {
	var a inspectAtArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.Location == "" {
		return nil, fmt.Errorf("location is required")
	}
	bpRaw, err := s.caller.Call(proto.CmdBreak, sess, proto.BreakArgs{Location: a.Location, Condition: a.Condition, Once: true})
	if err != nil {
		return nil, fmt.Errorf("break: %w", err)
	}
	// Refuse to continue with an unverified bp — otherwise the program runs
	// to completion and every eval comes back "no frames in current stack",
	// which is useless and looks like a tool bug. Issue #5.
	var bpRes proto.BreakResult
	_ = json.Unmarshal(bpRaw, &bpRes)
	if !bpRes.Verified {
		reason := bpRes.Reason
		if reason == "" {
			reason = "breakpoint did not verify at requested location"
		}
		return map[string]interface{}{
			"breakpoint": bpRaw,
			"error": map[string]interface{}{
				"code":    "BREAKPOINT_UNVERIFIED",
				"message": reason,
				"hint":    "pick an executable line in the same method (not loop headers or '}' lines); ensure the class is loaded with --stop-on-entry before calling inspect_at",
			},
		}, nil
	}
	stopRaw, err := s.caller.Call(proto.CmdContinue, sess, proto.ContinueArgs{TimeoutSec: a.TimeoutSec})
	if err != nil {
		return nil, fmt.Errorf("continue: %w", err)
	}
	// If the program didn't actually stop at our breakpoint (timeout, exit,
	// terminate), bail out before issuing locals/eval — those would just
	// return "no frames in current stack" and obscure the real reason.
	var stopPI proto.PauseInfo
	_ = json.Unmarshal(stopRaw, &stopPI)
	if stopPI.State != "paused" {
		return map[string]interface{}{
			"breakpoint": bpRaw,
			"stop":       stopRaw,
			"error": map[string]interface{}{
				"code":    "INSPECT_NOT_PAUSED",
				"message": fmt.Sprintf("program did not pause at %s (state=%s, reason=%s)", a.Location, stopPI.State, stopPI.Reason),
				"hint":    "the breakpoint never fired — confirm the line is reachable from the current execution point, or raise timeoutSec",
			},
		}, nil
	}
	localsRaw, _ := s.caller.Call(proto.CmdLocals, sess, proto.LocalsArgs{})
	evals := make(map[string]json.RawMessage, len(a.Expressions))
	for _, expr := range a.Expressions {
		r, eerr := s.caller.Call(proto.CmdEval, sess, proto.EvalArgs{Expression: expr})
		if eerr != nil {
			evals[expr] = json.RawMessage(fmt.Sprintf(`{"error":%q}`, eerr.Error()))
			continue
		}
		evals[expr] = r
	}
	out := map[string]interface{}{
		"breakpoint":  bpRaw,
		"stop":        stopRaw,
		"locals":      localsRaw,
		"evaluations": evals,
	}
	if a.Continue {
		if r, cerr := s.caller.Call(proto.CmdContinue, sess, proto.ContinueArgs{TimeoutSec: a.TimeoutSec}); cerr == nil {
			out["resumed"] = r
		}
	}
	return out, nil
}

// ----- explain_pause -----

type explainArgs struct {
	MaxVars   int `json:"maxVars,omitempty"`
	MaxFrames int `json:"maxFrames,omitempty"`
}

func handleExplainPause(s *Server, sess string, raw json.RawMessage) (interface{}, error) {
	var a explainArgs
	_ = json.Unmarshal(raw, &a)
	if a.MaxVars <= 0 {
		a.MaxVars = 12
	}
	if a.MaxFrames <= 0 {
		a.MaxFrames = 5
	}
	stateRaw, _ := s.caller.Call(proto.CmdState, sess, nil)
	stackRaw, _ := s.caller.Call(proto.CmdStack, sess, proto.StackArgs{})
	localsRaw, _ := s.caller.Call(proto.CmdLocals, sess, proto.LocalsArgs{})
	watchRaw, _ := s.caller.Call(proto.CmdWatch, sess, proto.WatchArgs{Action: "list"})

	var st struct {
		State    string `json:"state"`
		Reason   string `json:"reason"`
		Location struct {
			File     string `json:"file"`
			Line     int    `json:"line"`
			Function string `json:"function"`
		} `json:"location"`
	}
	_ = json.Unmarshal(stateRaw, &st)

	var sk struct {
		Frames []struct {
			File     string `json:"file"`
			Line     int    `json:"line"`
			Function string `json:"function"`
		} `json:"frames"`
	}
	_ = json.Unmarshal(stackRaw, &sk)
	frames := sk.Frames
	if len(frames) > a.MaxFrames {
		frames = frames[:a.MaxFrames]
	}

	var lc struct {
		Scope string      `json:"scope"`
		Vars  []proto.Var `json:"vars"`
		Hint  string      `json:"hint"`
	}
	_ = json.Unmarshal(localsRaw, &lc)
	vars := lc.Vars
	if len(vars) > a.MaxVars {
		vars = vars[:a.MaxVars]
	}

	// Issue #8: when the session isn't paused, the prose summary used to
	// claim "Paused (...)" anyway. Branch up front so LLM agents don't try
	// follow-up debug ops against a dead session.
	switch st.State {
	case "exited":
		var ecOnly struct {
			ExitCode *int `json:"exitCode"`
		}
		_ = json.Unmarshal(stateRaw, &ecOnly)
		ec := ""
		if ecOnly.ExitCode != nil {
			ec = fmt.Sprintf(" with code %d", *ecOnly.ExitCode)
		}
		return map[string]interface{}{
			"summary": "Program exited" + ec + "; no active stack frame.",
			"state":   st,
		}, nil
	case "terminated":
		return map[string]interface{}{
			"summary": "Session terminated; no active stack frame.",
			"state":   st,
		}, nil
	case "running":
		return map[string]interface{}{
			"summary": "Program is running; call debug_pause or wait for a breakpoint to inspect state.",
			"state":   st,
		}, nil
	}

	// Build prose summary.
	summary := fmt.Sprintf("Paused (%s) at %s:%d in %s.", st.Reason, st.Location.File, st.Location.Line, st.Location.Function)
	if len(frames) > 1 {
		summary += fmt.Sprintf(" Top callers: %s", framesToString(frames[1:]))
	}
	if len(vars) > 0 {
		summary += " Locals: " + varsToString(vars) + "."
	}
	if lc.Hint != "" {
		summary += " Hint: " + lc.Hint
	}

	return map[string]interface{}{
		"summary":      summary,
		"state":        st,
		"topFrames":    frames,
		"localsSample": vars,
		"watches":      json.RawMessage(watchRaw),
	}, nil
}

func framesToString(frames []struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Function string `json:"function"`
}) string {
	out := ""
	for i, f := range frames {
		if i > 0 {
			out += " → "
		}
		out += fmt.Sprintf("%s (%s:%d)", f.Function, f.File, f.Line)
	}
	return out
}

func varsToString(vars []proto.Var) string {
	out := ""
	for i, v := range vars {
		if i > 0 {
			out += ", "
		}
		val := v.Value
		if len(val) > 60 {
			val = val[:57] + "..."
		}
		out += fmt.Sprintf("%s=%s", v.Name, val)
	}
	return out
}

// ----- snapshot_compact -----

func handleSnapshotCompact(s *Server, sess string, raw json.RawMessage) (interface{}, error) {
	var a explainArgs
	_ = json.Unmarshal(raw, &a)
	if a.MaxVars <= 0 {
		a.MaxVars = 20
	}
	if a.MaxFrames <= 0 {
		a.MaxFrames = 8
	}
	snapRaw, err := s.caller.Call(proto.CmdSnapshot, sess, nil)
	if err != nil {
		return nil, err
	}
	var snap map[string]interface{}
	if err := json.Unmarshal(snapRaw, &snap); err != nil {
		return json.RawMessage(snapRaw), nil
	}
	if frames, ok := snap["frames"].([]interface{}); ok && len(frames) > a.MaxFrames {
		snap["frames"] = frames[:a.MaxFrames]
		snap["framesTruncated"] = len(frames) - a.MaxFrames
	}
	trimVars := func(key string) {
		if vs, ok := snap[key].([]interface{}); ok && len(vs) > a.MaxVars {
			snap[key] = vs[:a.MaxVars]
			snap[key+"Truncated"] = len(vs) - a.MaxVars
		}
	}
	trimVars("locals")
	trimVars("globals")
	return snap, nil
}
