// Handlers for the v1 feature-parity additions: watch, globals, fields, source,
// output, events, listen, restart, break-fn, break-ex, until.
package daemon

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	godap "github.com/google/go-dap"

	"github.com/yogeshpatil/sl-dbg/internal/proto"
	"github.com/yogeshpatil/sl-dbg/internal/session"
)

// ----- watch -----

func (s *Server) handleWatch(ctx context.Context, req proto.Request) proto.Response {
	var args proto.WatchArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	switch args.Action {
	case "", "list":
		return ok(proto.WatchResult{Watches: s.evaluateWatches(ctx, sess, args.Frame)})
	case "add":
		if args.Expression == "" {
			return errResp("USAGE_ERROR", "watch add requires an expression", "")
		}
		sess.AddWatch(args.Expression)
		return ok(proto.WatchResult{Watches: s.evaluateWatches(ctx, sess, args.Frame)})
	case "remove":
		if args.All {
			sess.ClearWatches()
		} else if args.ID > 0 {
			if !sess.RemoveWatch(args.ID) {
				return errResp("USAGE_ERROR", fmt.Sprintf("no watch with id %d", args.ID), "")
			}
		} else {
			return errResp("USAGE_ERROR", "watch remove needs --id N or --all", "")
		}
		return ok(proto.WatchResult{Watches: s.evaluateWatches(ctx, sess, args.Frame)})
	default:
		return errResp("USAGE_ERROR", "unknown watch action: "+args.Action, `expected "add"|"remove"|"list"`)
	}
}

func (s *Server) evaluateWatches(ctx context.Context, sess *session.Session, frame int) []proto.WatchEntry {
	ws := sess.Watches()
	out := make([]proto.WatchEntry, 0, len(ws))
	if sess.State() != session.StatePaused {
		// Can't evaluate while running; just return expressions.
		for _, w := range ws {
			out = append(out, proto.WatchEntry{ID: w.ID, Expression: w.Expression, Error: "not paused"})
		}
		return out
	}
	frameID, ferr := resolveFrameID(ctx, sess, frame)
	for _, w := range ws {
		entry := proto.WatchEntry{ID: w.ID, Expression: w.Expression}
		if ferr != nil {
			entry.Error = ferr.Error()
			out = append(out, entry)
			continue
		}
		r, err := sess.Client().Evaluate(ctx, w.Expression, frameID, "watch")
		if err != nil {
			entry.Error = err.Error()
		} else {
			entry.Result = r.Body.Result
			entry.Type = r.Body.Type
		}
		out = append(out, entry)
	}
	return out
}

// ----- globals -----

func (s *Server) handleGlobals(ctx context.Context, req proto.Request) proto.Response {
	var args proto.GlobalsArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	frameID, err := resolveFrameID(ctx, sess, args.Frame)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	scopes, err := sess.Client().Scopes(ctx, frameID)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	var ref int
	scopeName := "Globals"
	for _, sc := range scopes.Body.Scopes {
		n := strings.ToLower(sc.Name)
		if strings.Contains(n, "global") || strings.Contains(n, "module") || strings.Contains(n, "static") {
			ref = sc.VariablesReference
			scopeName = sc.Name
			break
		}
	}
	if ref == 0 {
		out := proto.LocalsResult{Scope: "Globals"}
		// Java doesn't expose globals as a scope. Derive the declaring class
		// from the current frame name (e.g. "com.foo.Bar.method(int)") and
		// give the user/agent a copy-pasteable hint.
		if sess.Lang == "java" {
			if cls := javaClassFromFrame(ctx, sess, args.Frame); cls != "" {
				out.Hint = "Java doesn't expose globals as a scope. Inspect statics with: eval " + cls + ".<fieldName>  (or: eval " + cls + ".class.getDeclaredFields())"
			} else {
				out.Hint = "Java doesn't expose globals as a scope. Use 'eval <ClassName>.<fieldName>' to read static fields."
			}
		}
		return ok(out)
	}
	vars, err := sess.Client().Variables(ctx, ref)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	out := proto.LocalsResult{Scope: scopeName}
	for _, v := range vars.Body.Variables {
		out.Vars = append(out.Vars, proto.Var{
			Name: v.Name, Value: v.Value, Type: v.Type,
			Ref: v.VariablesReference, Expandable: v.VariablesReference > 0,
		})
	}
	return ok(out)
}

// ----- fields -----

func (s *Server) handleFields(ctx context.Context, req proto.Request) proto.Response {
	var args proto.FieldsArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	if args.Ref <= 0 {
		return errResp("USAGE_ERROR", "fields requires a positive ref", "use the Ref from a prior locals/eval response")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	vars, err := sess.Client().Variables(ctx, args.Ref)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	out := proto.LocalsResult{Scope: "fields"}
	for _, v := range vars.Body.Variables {
		out.Vars = append(out.Vars, proto.Var{
			Name: v.Name, Value: v.Value, Type: v.Type,
			Ref: v.VariablesReference, Expandable: v.VariablesReference > 0,
		})
	}
	return ok(out)
}

// ----- source -----

func (s *Server) handleSource(ctx context.Context, req proto.Request) proto.Response {
	var args proto.SourceArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}

	file := args.File
	line := args.Line
	current := 0

	// Default: use the current pause location.
	if file == "" {
		if _, _, loc := sess.LastPause(); loc != nil {
			file = loc.File
			current = loc.Line
			if line == 0 {
				line = loc.Line
			}
		}
	}
	if file == "" {
		return errResp("USAGE_ERROR", "no file specified and no current pause location", "pass --file <path>")
	}

	// Try filesystem first (works for attach-style debugging where source is local).
	if lines, err := readSourceFile(file); err == nil {
		start, end := windowOf(line, args.Around, len(lines))
		return ok(proto.SourceResult{
			File:    file,
			Start:   start + 1,
			Lines:   lines[start:end],
			Current: current,
		})
	}

	// Fall back to DAP Source request if adapter supports it.
	if args.Ref > 0 || sess.Caps().SupportsLoadedSourcesRequest {
		r, err := sess.Client().Source(ctx, file, args.Ref)
		if err == nil && r != nil {
			lines := strings.Split(r.Body.Content, "\n")
			start, end := windowOf(line, args.Around, len(lines))
			return ok(proto.SourceResult{
				File:    file,
				Start:   start + 1,
				Lines:   lines[start:end],
				Current: current,
			})
		}
	}
	return errResp("ADAPTER_FAILED", "could not read source: "+file, "")
}

func readSourceFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

func windowOf(center, around, n int) (start, end int) {
	if around <= 0 || center <= 0 {
		return 0, n
	}
	start = center - around - 1
	if start < 0 {
		start = 0
	}
	end = center + around
	if end > n {
		end = n
	}
	return
}

// ----- output -----

func (s *Server) handleOutput(req proto.Request) proto.Response {
	var args proto.OutputArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	var since time.Time
	if args.Since != "" {
		if t, err := time.Parse(time.RFC3339Nano, args.Since); err == nil {
			since = t
		}
	}
	es := sess.Outputs(since, args.Tail)
	out := proto.OutputResult{Entries: make([]proto.OutputEntry, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, proto.OutputEntry{
			TS: e.TS.Format(time.RFC3339Nano), Category: e.Category, Output: e.Output,
		})
	}
	return ok(out)
}

// ----- events -----

func (s *Server) handleEvents(req proto.Request) proto.Response {
	var args proto.EventsArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	var since time.Time
	if args.Since != "" {
		if t, err := time.Parse(time.RFC3339Nano, args.Since); err == nil {
			since = t
		}
	}
	es := sess.Events(since, args.Tail)
	out := proto.EventsResult{Events: make([]proto.EventEntry, 0, len(es))}
	for _, e := range es {
		out.Events = append(out.Events, proto.EventEntry{
			TS: e.TS.Format(time.RFC3339Nano), Type: e.Type, Body: e.Body,
		})
	}
	return ok(out)
}

// ----- listen -----

func (s *Server) handleListen(ctx context.Context, req proto.Request) proto.Response {
	var args proto.ListenArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	timeout := 60 * time.Second
	if args.TimeoutSec > 0 {
		timeout = time.Duration(args.TimeoutSec * float64(time.Second))
	}
	info, err := sess.WaitForStop(ctx, timeout)
	if err != nil {
		return errResp("TIMEOUT", err.Error(), "")
	}
	return ok(info)
}

// ----- restart -----

func (s *Server) handleRestart(ctx context.Context, req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	if !sess.Caps().SupportsRestartRequest {
		return errResp("UNSUPPORTED_FEATURE",
			"adapter does not support restart",
			"stop the session and start/attach again")
	}
	if err := sess.Client().Restart(ctx); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	return ok(map[string]interface{}{"session": sess.ID, "state": "running", "reason": "restarted"})
}

// ----- break-fn -----

func (s *Server) handleBreakFn(ctx context.Context, req proto.Request) proto.Response {
	var args proto.BreakFnArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	if args.Function == "" {
		return errResp("USAGE_ERROR", "function name required", "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	if !sess.Caps().SupportsFunctionBreakpoints {
		return errResp("UNSUPPORTED_FEATURE",
			"adapter does not support function breakpoints",
			"use `break <file:line>` instead")
	}
	bp := session.FuncBP{
		LocalID:   sess.AllocBP(),
		Name:      args.Function,
		Condition: args.Condition,
	}
	if args.Hit > 0 {
		bp.HitCond = strconv.Itoa(args.Hit)
	}
	sess.PutFuncBP(bp)
	all := sess.FuncBPs()
	dapBPs := make([]godap.FunctionBreakpoint, 0, len(all))
	for _, b := range all {
		fbp := godap.FunctionBreakpoint{Name: b.Name}
		if b.Condition != "" {
			fbp.Condition = b.Condition
		}
		if b.HitCond != "" {
			fbp.HitCondition = b.HitCond
		}
		dapBPs = append(dapBPs, fbp)
	}
	resp, err := sess.Client().SetFunctionBreakpoints(ctx, dapBPs)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	for i, rbp := range resp.Body.Breakpoints {
		if i < len(all) {
			all[i].DAPID = rbp.Id
			all[i].Verified = rbp.Verified
		}
	}
	return ok(proto.BreakResult{
		ID:        bp.LocalID,
		Verified:  bp.Verified,
		Function:  bp.Name,
		Condition: bp.Condition,
	})
}

// ----- break-ex -----

func (s *Server) handleBreakEx(ctx context.Context, req proto.Request) proto.Response {
	var args proto.BreakExArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	supported := sess.Caps().ExceptionBreakpointFilters
	if len(supported) == 0 {
		return errResp("UNSUPPORTED_FEATURE",
			"adapter does not support exception breakpoints", "")
	}
	// Validate filters; allow "all", "uncaught", "raised" or any adapter filter id.
	validIDs := map[string]bool{}
	for _, f := range supported {
		validIDs[strings.ToLower(f.Filter)] = true
		validIDs[strings.ToLower(f.Label)] = true
	}
	chosen := []string{}
	for _, f := range args.Filters {
		lf := strings.ToLower(f)
		// Map common aliases to adapter-specific filter IDs.
		matched := ""
		for _, sf := range supported {
			id := strings.ToLower(sf.Filter)
			lbl := strings.ToLower(sf.Label)
			if id == lf || lbl == lf {
				matched = sf.Filter
				break
			}
			if (lf == "uncaught" || lf == "unhandled") &&
				(strings.Contains(id, "uncaught") || strings.Contains(id, "unhandled") || strings.Contains(lbl, "uncaught") || strings.Contains(lbl, "unhandled")) {
				matched = sf.Filter
				break
			}
			if (lf == "all" || lf == "raised" || lf == "thrown") &&
				(strings.Contains(id, "all") || strings.Contains(id, "raised") || strings.Contains(id, "thrown") || strings.Contains(lbl, "all") || strings.Contains(lbl, "raised") || strings.Contains(lbl, "thrown")) {
				matched = sf.Filter
				break
			}
		}
		if matched == "" {
			return errResp("USAGE_ERROR",
				"unknown filter: "+f,
				"see `sl-dbg adapters` for supported filters")
		}
		chosen = append(chosen, matched)
	}
	if _, err := sess.Client().SetExceptionBreakpoints(ctx, chosen); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	sess.SetExcFilters(chosen)
	return ok(map[string]interface{}{
		"filters":   chosen,
		"available": filterNames(supported),
	})
}

func filterNames(fs []godap.ExceptionBreakpointsFilter) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Filter
	}
	return out
}

// ----- until -----

func (s *Server) handleUntil(ctx context.Context, req proto.Request) proto.Response {
	var args proto.UntilArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	if args.Line <= 0 {
		return errResp("USAGE_ERROR", "until requires a positive line number", "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	// Resolve current source file from last pause location.
	_, _, loc := sess.LastPause()
	if loc == nil || loc.File == "" {
		return errResp("USAGE_ERROR", "no current pause location", "session must be paused")
	}
	// Install a one-shot synthetic BP at file:line; continue; wait; clear.
	bp := &session.BP{
		LocalID: sess.AllocBP(),
		File:    loc.File,
		Line:    args.Line,
	}
	sess.PutBP(bp)
	existing := sess.BPsForFile(loc.File)
	dapBPs := make([]godap.SourceBreakpoint, 0, len(existing))
	for _, b := range existing {
		sb := godap.SourceBreakpoint{Line: b.Line}
		if b.Condition != "" {
			sb.Condition = b.Condition
		}
		if b.HitCond != "" {
			sb.HitCondition = b.HitCond
		}
		if b.LogMsg != "" {
			sb.LogMessage = b.LogMsg
		}
		dapBPs = append(dapBPs, sb)
	}
	if _, err := sess.Client().SetBreakpoints(ctx, godap.Source{Path: loc.File}, dapBPs); err != nil {
		_, _ = sess.RemoveBP(bp.LocalID)
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	// Continue and wait.
	timeout := 30 * time.Second
	if args.TimeoutSec > 0 {
		timeout = time.Duration(args.TimeoutSec * float64(time.Second))
	}
	waiter := sess.InstallWaiter()
	thread := sess.CurrentThread()
	if args.Thread > 0 {
		thread = args.Thread
	}
	if err := sess.Client().Continue(ctx, thread); err != nil {
		_, _ = sess.RemoveBP(bp.LocalID)
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	info, werr := waiter.Wait(ctx, timeout)
	// Clear the temp BP regardless.
	_, _ = sess.RemoveBP(bp.LocalID)
	remaining := sess.BPsForFile(loc.File)
	dapBPs = dapBPs[:0]
	for _, b := range remaining {
		sb := godap.SourceBreakpoint{Line: b.Line}
		if b.Condition != "" {
			sb.Condition = b.Condition
		}
		if b.HitCond != "" {
			sb.HitCondition = b.HitCond
		}
		if b.LogMsg != "" {
			sb.LogMessage = b.LogMsg
		}
		dapBPs = append(dapBPs, sb)
	}
	_, _ = sess.Client().SetBreakpoints(ctx, godap.Source{Path: loc.File}, dapBPs)
	if werr != nil {
		return errResp("TIMEOUT", werr.Error(), "")
	}
	return ok(info)
}

// javaClassFromFrame extracts the declaring class name from a DAP stack
// frame's display name. java-debug formats frame names as
// "pkg.Class.method(arg)" or sometimes "Class.method". Returns "" when the
// shape is unrecognized.
func javaClassFromFrame(ctx context.Context, sess *session.Session, idx int) string {
	r, err := sess.Client().StackTrace(ctx, sess.CurrentThread(), 1+idx)
	if err != nil || len(r.Body.StackFrames) == 0 {
		return ""
	}
	if idx < 0 || idx >= len(r.Body.StackFrames) {
		idx = 0
	}
	name := r.Body.StackFrames[idx].Name
	// Strip "(args)" suffix.
	if p := strings.Index(name, "("); p > 0 {
		name = name[:p]
	}
	// "pkg.Class.method" -> "pkg.Class"
	if dot := strings.LastIndex(name, "."); dot > 0 {
		return name[:dot]
	}
	return ""
}
