// Handlers for the v1 feature-parity additions: watch, globals, fields, source,
// output, events, listen, restart, break-fn, break-ex, until.
package daemon

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	godap "github.com/google/go-dap"

	"github.com/y0geshpatil/sl-dbg/internal/proto"
	"github.com/y0geshpatil/sl-dbg/internal/session"
)

// sessionOwnsPath returns true when path is under one of the session's
// known-good roots: configured SourceRoots, the launch Cwd, or the directory
// of the launched program. Used as the per-session escape hatch from the
// daemon-wide SL_DBG_ALLOW_SOURCE_ROOT allowlist so that existing flows that
// passed sourceRoots explicitly continue to work without operator config.
func sessionOwnsPath(sess *session.Session, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	var roots []string
	roots = append(roots, sess.SourceRoots...)
	if sess.Cwd != "" {
		roots = append(roots, sess.Cwd)
	}
	if sess.Program != "" {
		roots = append(roots, filepath.Dir(sess.Program))
	}
	for _, r := range roots {
		ra, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		ra = filepath.Clean(ra)
		if abs == ra || strings.HasPrefix(abs, ra+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ----- watch -----

func (s *Server) handleWatch(ctx context.Context, req proto.Request) proto.Response {
	var args proto.WatchArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
	}
	// Infer action when the caller passes only payload fields. Common
	// pattern: MCP agents call debug_watch with `expression` but no
	// `action`, expecting "add"; previously this silently returned a list.
	if args.Action == "" {
		switch {
		case args.Expression != "":
			args.Action = "add"
		case args.ID > 0 || args.All:
			args.Action = "remove"
		default:
			args.Action = "list"
		}
	}
	switch args.Action {
	case "list":
		return ok(proto.WatchResult{Watches: s.evaluateWatches(ctx, sess, args.Frame)})
	case "add":
		if rerr := refuseIfReadOnly(sess); rerr != nil {
			return *rerr
		}
		if args.Expression == "" {
			return errResp("USAGE_ERROR", "watch add requires an expression", "")
		}
		sess.AddWatch(args.Expression)
		return ok(proto.WatchResult{Watches: s.evaluateWatches(ctx, sess, args.Frame)})
	case "remove":
		if rerr := refuseIfReadOnly(sess); rerr != nil {
			return *rerr
		}
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
		return s.sessionNotFound(req.Sess, err.Error())
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
		// Java doesn't expose globals as a scope. Try to populate it by
		// evaluating the bare class name — java-debug returns an object
		// whose variablesReference enumerates the static fields.
		if sess.Lang == "java" {
			cls := javaClassFromFrame(ctx, sess, args.Frame)
			if cls != "" {
				if r, err := sess.Client().Evaluate(ctx, cls, frameID, "repl"); err == nil && r.Body.VariablesReference > 0 {
					vars, verr := sess.Client().Variables(ctx, r.Body.VariablesReference)
					if verr == nil {
						out.Scope = cls + " (statics)"
						for _, v := range vars.Body.Variables {
							// Java reflection includes some pseudo-entries like
							// "static" headers; keep them — they're harmless.
							out.Vars = append(out.Vars, proto.Var{
								Name: v.Name, Value: v.Value, Type: v.Type,
								Ref: v.VariablesReference, Expandable: v.VariablesReference > 0,
							})
						}
						if len(out.Vars) > 0 {
							return ok(out)
						}
					}
				}
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	vars, err := sess.Client().Variables(ctx, args.Ref)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	out := proto.LocalsResult{Scope: "fields"}
	for _, v := range vars.Body.Variables {
		if !args.ShowSpecial && isPythonSpecial(v.Name) {
			continue
		}
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
		return s.sessionNotFound(req.Sess, err.Error())
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
	// Fallback: at entry pauses the StoppedEvent sometimes arrives without a
	// resolved location. Ask the adapter for the current top stack frame so
	// `debug_source` works zero-arg even right after `--stop-on-entry`.
	if file == "" && sess.State() == session.StatePaused {
		if topFile, topLine, ok := currentTopFrame(ctx, sess); ok {
			file = topFile
			current = topLine
			if line == 0 {
				line = topLine
			}
		}
	}
	if file == "" {
		return errResp("USAGE_ERROR", "no file specified and no current pause location",
			"pass file=<path> or wait until the program is paused at a known location")
	}

	// Policy: source-root allowlist (#18). Path is validated against the
	// daemon-wide allowlist union the session's known-good roots
	// (SourceRoots, Cwd, program dir). Only the daemon allowlist is hard;
	// the per-session roots are convenience so existing flows keep working
	// when no daemon allowlist is configured.
	if err := s.policy.SourcePathAllowed(file); err != nil {
		if !sessionOwnsPath(sess, file) {
			s.audit.Log("source.denied", sess.ID, map[string]interface{}{"file": file, "reason": err.Error()})
			return errResp("SOURCE_PATH_DENIED", err.Error(),
				"set SL_DBG_ALLOW_SOURCE_ROOT to include a parent directory, or start the session with sourceRoots covering this path")
		}
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	var since time.Time
	if args.Since != "" {
		t, perr := time.Parse(time.RFC3339Nano, args.Since)
		if perr != nil {
			return errResp("USAGE_ERROR",
				fmt.Sprintf("invalid 'since' timestamp %q: %v", args.Since, perr),
				"use RFC3339Nano, e.g. 2026-06-26T19:59:26.279496Z")
		}
		since = t
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	var since time.Time
	if args.Since != "" {
		t, perr := time.Parse(time.RFC3339Nano, args.Since)
		if perr != nil {
			return errResp("USAGE_ERROR",
				fmt.Sprintf("invalid 'since' timestamp %q: %v", args.Since, perr),
				"use RFC3339Nano, e.g. 2026-06-26T19:59:26.279496Z")
		}
		since = t
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	// Issue #30: short-circuit when the session is already paused/terminal.
	// The old code installed a waiter and blocked for the full timeout even
	// though there was nothing to wait for — every subsequent inspection
	// call already had its location available via LastPause.
	if st := sess.State(); st == session.StatePaused || st == session.StateExited || st == session.StateTerminated {
		reason, hitBP, loc := sess.LastPause()
		pi := proto.PauseInfo{State: string(st), Reason: reason, HitBP: hitBP, Location: loc}
		if st == session.StatePaused && reason == "" {
			pi.Reason = "already-paused"
		}
		return ok(pi)
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
		return s.sessionNotFound(req.Sess, err.Error())
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if rerr := refuseIfReadOnly(sess); rerr != nil {
		return *rerr
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
		fbp := godap.FunctionBreakpoint{Name: normalizeFuncBPName(sess.Lang, b.Name)}
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

// normalizeFuncBPName adapts a user-supplied function-breakpoint name to the
// format expected by the language adapter.
//
// The Java adapter (com.microsoft.java.debug.core's SetFunctionBreakpointsRequestHandler)
// expects names in the form "FullyQualifiedClass#method"; it splits on '#' and
// silently produces an unverified breakpoint if exactly two non-blank segments
// are not present. Users naturally type "Class.method" (mirroring source code),
// so for Java we translate the final '.' separator to '#'. We also strip any
// "(...)" argument-signature suffix because the adapter doesn't accept it and
// would treat the parentheses as part of the method name.
//
// For non-Java sessions the name is returned unchanged: the Python (debugpy)
// and Go (dlv) adapters both accept dot-qualified names directly.
func normalizeFuncBPName(lang, name string) string {
	if name == "" {
		return name
	}
	if lang != "java" {
		return name
	}
	// Drop method-arg signature, e.g. "Foo.bar(int)" -> "Foo.bar".
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}
	// If the user already used the adapter's native separator, pass through.
	if strings.Contains(name, "#") {
		return name
	}
	// Convert the final '.' (which separates class from method) into '#'.
	// "com.example.Foo.bar" -> "com.example.Foo#bar". For nested types
	// (Outer.Inner), the user must spell the class half with '$' and the
	// adapter separator explicitly, i.e. "Outer$Inner#method", because JDI
	// uses '$' for the inner-class delimiter and we can't distinguish
	// package segments from outer-class segments by name alone.
	if i := strings.LastIndexByte(name, '.'); i > 0 && i < len(name)-1 {
		return name[:i] + "#" + name[i+1:]
	}
	return name
}

// ----- break-ex -----

func (s *Server) handleBreakEx(ctx context.Context, req proto.Request) proto.Response {
	var args proto.BreakExArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if rerr := refuseIfReadOnly(sess); rerr != nil {
		return *rerr
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
		return s.sessionNotFound(req.Sess, err.Error())
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
	if err := sess.Client().Continue(ctx, thread, false); err != nil {
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
	// Issue #15: match the shape of continue/next/step/finish — include
	// the post-pause location so callers don't need a follow-up state call.
	if info.State == string(session.StatePaused) {
		if loc := fetchTopLocation(ctx, sess); loc != nil {
			info.Location = loc
			sess.SetLastLocation(loc)
		}
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

// ----- print (recursive expansion) -----

func (s *Server) handlePrint(ctx context.Context, req proto.Request) proto.Response {
	var args proto.PrintArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	if args.Expression == "" && args.Ref <= 0 {
		return errResp("USAGE_ERROR", "print requires --expr or --ref", "")
	}
	depth := args.Depth
	if depth <= 0 {
		depth = 3
	}
	maxItems := args.MaxItems
	if maxItems <= 0 {
		maxItems = 50
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if args.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(args.TimeoutSec*float64(time.Second)))
		defer cancel()
	}
	root := proto.PrintNode{}
	if args.Expression != "" {
		frameID, err := resolveFrameID(ctx, sess, args.Frame)
		if err != nil {
			return errResp("ADAPTER_FAILED", err.Error(), "")
		}
		r, err := sess.Client().Evaluate(ctx, args.Expression, frameID, "repl")
		if err != nil {
			return errResp("ADAPTER_FAILED", err.Error(), "")
		}
		root = proto.PrintNode{
			Name:  args.Expression,
			Value: r.Body.Result,
			Type:  r.Body.Type,
			Ref:   r.Body.VariablesReference,
		}
	} else {
		root.Ref = args.Ref
	}
	if root.Ref > 0 && depth > 0 {
		kids, trunc, err := expandRefFiltered(ctx, sess, root.Ref, depth, maxItems, args.ShowSpecial)
		if err != nil {
			return errResp("ADAPTER_FAILED", err.Error(), "")
		}
		root.Children = kids
		root.Truncated = trunc
	}
	return ok(proto.PrintResult{Root: root})
}

func expandRef(ctx context.Context, sess *session.Session, ref, depth, maxItems int) ([]proto.PrintNode, bool, error) {
	return expandRefFiltered(ctx, sess, ref, depth, maxItems, false)
}

// expandRefFiltered is the workhorse: when showSpecial is false, drops the
// Python "special variables" containers and __dunder__ entries before
// recursing. Issue #31.
func expandRefFiltered(ctx context.Context, sess *session.Session, ref, depth, maxItems int, showSpecial bool) ([]proto.PrintNode, bool, error) {
	vars, err := sess.Client().Variables(ctx, ref)
	if err != nil {
		return nil, false, err
	}
	truncated := false
	src := vars.Body.Variables
	if !showSpecial {
		filtered := src[:0]
		for _, v := range src {
			if isPythonSpecial(v.Name) {
				continue
			}
			filtered = append(filtered, v)
		}
		src = filtered
	}
	if len(src) > maxItems {
		src = src[:maxItems]
		truncated = true
	}
	out := make([]proto.PrintNode, 0, len(src))
	for _, v := range src {
		n := proto.PrintNode{
			Name:  v.Name,
			Value: v.Value,
			Type:  v.Type,
			Ref:   v.VariablesReference,
		}
		// Issue #3: java-debug shows wrapper primitives nested in
		// containers as opaque object refs (e.g. "Integer@318"). Unwrap
		// them to their primitive value with a tiny eval so list/map
		// inspection isn't useless. Best-effort: on any error we fall back
		// to the original opaque value.
		if sess.Lang == "java" && v.VariablesReference > 0 && isJavaWrapperType(v.Type) {
			if prim, ok := unwrapJavaWrapper(ctx, sess, v.VariablesReference); ok {
				n.Value = prim
				n.Ref = 0 // primitive — no further expansion useful
			}
		}
		if n.Ref > 0 && depth-1 > 0 {
			kids, trunc, err := expandRefFiltered(ctx, sess, n.Ref, depth-1, maxItems, showSpecial)
			if err == nil {
				n.Children = kids
				n.Truncated = trunc
			}
		}
		out = append(out, n)
	}
	return out, truncated, nil
}

// isJavaWrapperType returns true for the eight boxed primitives plus String,
// matching the type strings java-debug uses (sometimes fully-qualified,
// sometimes not). Issue #3.
func isJavaWrapperType(t string) bool {
	switch t {
	case "Integer", "Long", "Short", "Byte", "Float", "Double", "Boolean", "Character",
		"java.lang.Integer", "java.lang.Long", "java.lang.Short", "java.lang.Byte",
		"java.lang.Float", "java.lang.Double", "java.lang.Boolean", "java.lang.Character":
		return true
	}
	return false
}

// unwrapJavaWrapper reads the wrapper's `value` field and returns it as a
// human-readable primitive string. Returns ("", false) on any adapter error.
// Issue #3.
func unwrapJavaWrapper(ctx context.Context, sess *session.Session, ref int) (string, bool) {
	r, err := sess.Client().Variables(ctx, ref)
	if err != nil {
		return "", false
	}
	for _, f := range r.Body.Variables {
		if f.Name == "value" {
			return f.Value, true
		}
	}
	return "", false
}

// isPythonSpecial matches debugpy's container labels and __dunder__ names.
// Issue #29 / #31.
func isPythonSpecial(name string) bool {
	switch name {
	case "special variables", "function variables", "class variables":
		return true
	}
	if len(name) >= 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
		return true
	}
	return false
}

// currentTopFrame fetches the top stack frame of the current thread when the
// session is paused. Used as a fallback when LastPause hasn't captured a
// location yet (common at entry pauses with some adapters).
func currentTopFrame(ctx context.Context, sess *session.Session) (string, int, bool) {
	tid := sess.CurrentThread()
	r, err := sess.Client().StackTrace(ctx, tid, 1)
	if err != nil || len(r.Body.StackFrames) == 0 {
		return "", 0, false
	}
	f := r.Body.StackFrames[0]
	return f.Source.Path, f.Line, f.Source.Path != ""
}
