// Package daemon implements the long-running background server that holds
// debug sessions and serves CLI requests over the IPC socket.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	godap "github.com/google/go-dap"

	"github.com/yogeshpatil/sl-dbg/internal/adapter"
	"github.com/yogeshpatil/sl-dbg/internal/ipc"
	"github.com/yogeshpatil/sl-dbg/internal/proto"
	"github.com/yogeshpatil/sl-dbg/internal/session"
)

// Server is the daemon.
type Server struct {
	mgr      *session.Manager
	listener net.Listener
	logger   *log.Logger
	mu       sync.Mutex
	closing  bool
}

// Run starts the daemon and blocks until shutdown or fatal error.
// If the socket is already in use by a live daemon, returns an error.
func Run() error {
	// Set up logging to file.
	if err := ipc.EnsureDir(); err != nil {
		return err
	}
	lf, err := os.OpenFile(ipc.LogFilePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	logger := log.New(lf, "sl-dbgd ", log.LstdFlags|log.Lmicroseconds)

	srv := &Server{
		mgr:    session.NewManager(),
		logger: logger,
	}

	// Refuse to start if another daemon is alive.
	if pid := ipc.ReadPidFile(); pid > 0 {
		if isPidAlive(pid) {
			return fmt.Errorf("another daemon is running (pid %d)", pid)
		}
	}

	l, err := ipc.Listen(srv.handleConn)
	if err != nil {
		return err
	}
	srv.listener = l

	if err := ipc.WritePidFile(); err != nil {
		l.Close()
		return err
	}
	logger.Printf("daemon listening on %s (pid %d)", ipc.SocketPath(), os.Getpid())

	// Wait forever (handler goroutines do the work).
	select {}
}

func isPidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// handleConn serves one accepted connection. Reads one or more requests in a
// loop until the client disconnects. Long-running commands (continue, step)
// hold the connection open until they produce a response.
func (s *Server) handleConn(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		var req proto.Request
		if err := ipc.ReadLine(r, &req); err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Printf("read: %v", err)
			}
			return
		}
		resp := s.handle(req)
		resp.ID = req.ID
		if err := ipc.WriteLine(c, resp); err != nil {
			s.logger.Printf("write: %v", err)
			return
		}
	}
}

func (s *Server) handle(req proto.Request) proto.Response {
	s.logger.Printf("→ %s sess=%q args=%s", req.Cmd, req.Sess, string(req.Args))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	switch req.Cmd {
	case proto.CmdPing:
		return ok(map[string]string{"pong": "1"})
	case proto.CmdShutdown:
		go func() { time.Sleep(50 * time.Millisecond); os.Exit(0) }()
		return ok(map[string]string{"shutdown": "scheduled"})
	case proto.CmdAdapters:
		return s.handleAdapters()
	case proto.CmdStart:
		return s.handleStart(ctx, req)
	case proto.CmdAttach:
		return s.handleAttach(ctx, req)
	case proto.CmdSessions:
		return s.handleSessions()
	case proto.CmdUse:
		return s.handleUse(req)
	case proto.CmdStop:
		return s.handleStop(req)
	case proto.CmdState:
		return s.handleState(req)
	case proto.CmdBreak:
		return s.handleBreak(ctx, req)
	case proto.CmdBreaks:
		return s.handleBreaks(req)
	case proto.CmdUnbreak:
		return s.handleUnbreak(ctx, req)
	case proto.CmdContinue:
		return s.handleExec(ctx, req, execContinue)
	case proto.CmdStep:
		return s.handleExec(ctx, req, execStep)
	case proto.CmdNext:
		return s.handleExec(ctx, req, execNext)
	case proto.CmdFinish:
		return s.handleExec(ctx, req, execFinish)
	case proto.CmdPause:
		return s.handlePause(ctx, req)
	case proto.CmdStack:
		return s.handleStack(ctx, req)
	case proto.CmdThreads:
		return s.handleThreads(ctx, req)
	case proto.CmdLocals:
		return s.handleLocals(ctx, req)
	case proto.CmdEval:
		return s.handleEval(ctx, req)
	case proto.CmdSet:
		return s.handleSet(ctx, req)
	case proto.CmdSnapshot:
		return s.handleSnapshot(ctx, req)
	case proto.CmdWatch:
		return s.handleWatch(ctx, req)
	case proto.CmdGlobals:
		return s.handleGlobals(ctx, req)
	case proto.CmdFields:
		return s.handleFields(ctx, req)
	case proto.CmdSource:
		return s.handleSource(ctx, req)
	case proto.CmdOutput:
		return s.handleOutput(req)
	case proto.CmdEvents:
		return s.handleEvents(req)
	case proto.CmdListen:
		return s.handleListen(ctx, req)
	case proto.CmdRestart:
		return s.handleRestart(ctx, req)
	case proto.CmdBreakFn:
		return s.handleBreakFn(ctx, req)
	case proto.CmdBreakEx:
		return s.handleBreakEx(ctx, req)
	case proto.CmdUntil:
		return s.handleUntil(ctx, req)
	case proto.CmdPrint:
		return s.handlePrint(ctx, req)
	default:
		return errResp("UNKNOWN_COMMAND", fmt.Sprintf("unknown command: %q", req.Cmd), "")
	}
}

// ---- per-command handlers ----

func (s *Server) handleAdapters() proto.Response {
	var out proto.AdaptersResult
	for _, lang := range adapter.List() {
		spec, _ := adapter.Get(lang)
		path, err := spec.Detect()
		ai := proto.AdapterInfo{Lang: lang, Installed: err == nil, Path: path}
		if err != nil {
			ai.Hint = spec.InstallHint
		}
		out.Adapters = append(out.Adapters, ai)
	}
	return ok(out)
}

func (s *Server) handleStart(ctx context.Context, req proto.Request) proto.Response {
	var args proto.StartArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.CreateLaunch(ctx, args)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	// Initial state. If stopOnEntry was requested, wait briefly for the entry pause.
	if args.StopOnEntry {
		pi, _ := sess.WaitForStop(ctx, 5*time.Second)
		return ok(proto.SessionResult{
			SessionID: sess.ID, Lang: sess.Lang, State: pi.State, Reason: pi.Reason,
			Location: pi.Location,
		})
	}
	return ok(proto.SessionResult{
		SessionID: sess.ID, Lang: sess.Lang, State: string(sess.State()), Reason: "launched",
	})
}

func (s *Server) handleAttach(ctx context.Context, req proto.Request) proto.Response {
	var args proto.AttachArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.CreateAttach(ctx, args)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	return ok(proto.SessionResult{
		SessionID: sess.ID, Lang: sess.Lang, State: string(sess.State()), Reason: "attached",
	})
}

func (s *Server) handleSessions() proto.Response {
	defID := s.mgr.DefaultID()
	var out proto.SessionsResult
	for _, sess := range s.mgr.List() {
		out.Sessions = append(out.Sessions, proto.SessionInfo{
			ID:       sess.ID,
			Lang:     sess.Lang,
			State:    string(sess.State()),
			Program:  sess.Program,
			Attached: sess.Attached,
			Default:  sess.ID == defID,
		})
	}
	return ok(out)
}

func (s *Server) handleUse(req proto.Request) proto.Response {
	var v struct{ ID string `json:"id"` }
	if err := unmarshalArgs(req.Args, &v); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	if err := s.mgr.SetDefault(v.ID); err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	return ok(map[string]string{"default": v.ID})
}

func (s *Server) handleStop(req proto.Request) proto.Response {
	sid := req.Sess
	if sid == "" {
		sid = s.mgr.DefaultID()
	}
	if sid == "" {
		return errResp("SESSION_NOT_FOUND", "no active session", "")
	}
	s.mgr.Remove(sid)
	return ok(proto.SessionResult{SessionID: sid, State: string(session.StateTerminated)})
}

func (s *Server) handleState(req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	reason, hitBP, loc := sess.LastPause()
	return ok(proto.PauseInfo{
		State:    string(sess.State()),
		Reason:   reason,
		Thread:   sess.CurrentThread(),
		Location: loc,
		HitBP:    hitBP,
	})
}

func (s *Server) handleBreak(ctx context.Context, req proto.Request) proto.Response {
	var args proto.BreakArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	file, line, err := parseLocation(args.Location)
	if err != nil {
		return errResp("USAGE_ERROR", err.Error(), `expected "file:line" or "Class:line"`)
	}

	// Build the *full* breakpoint set for this file (DAP setBreakpoints REPLACES).
	bp := &session.BP{
		LocalID:   sess.AllocBP(),
		File:      file,
		Line:      line,
		Condition: args.Condition,
		LogMsg:    args.LogMsg,
		Once:      args.Once,
	}
	if args.Hit > 0 {
		bp.HitCond = strconv.Itoa(args.Hit)
	}
	sess.PutBP(bp)

	existing := sess.BPsForFile(file)
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

	resp, err := sess.Client().SetBreakpoints(ctx, godap.Source{Path: file}, dapBPs)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}

	// Match the response BPs back to our locals by line number.
	for i, rbp := range resp.Body.Breakpoints {
		if i >= len(existing) {
			break
		}
		existing[i].DAPID = rbp.Id
		existing[i].Verified = rbp.Verified
	}

	out := proto.BreakResult{
		ID:        bp.LocalID,
		Verified:  bp.Verified,
		File:      bp.File,
		Line:      bp.Line,
		Condition: bp.Condition,
	}
	return ok(out)
}

func (s *Server) handleBreaks(req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	var out proto.BreaksResult
	for _, b := range sess.AllBPs() {
		reason := ""
		if !b.Verified {
			reason = "pending (class not yet loaded, or unsupported line)"
		}
		out.Breakpoints = append(out.Breakpoints, proto.BreakResult{
			ID: b.LocalID, Verified: b.Verified, File: b.File, Line: b.Line,
			Condition: b.Condition, Reason: reason,
		})
	}
	for _, b := range sess.FuncBPs() {
		reason := ""
		if !b.Verified {
			reason = "pending (function/class not yet loaded)"
		}
		out.Breakpoints = append(out.Breakpoints, proto.BreakResult{
			ID: b.LocalID, Verified: b.Verified, Function: b.Name,
			Condition: b.Condition, Reason: reason,
		})
	}
	if filters := sess.ExcFilters(); len(filters) > 0 {
		out.Breakpoints = append(out.Breakpoints, proto.BreakResult{
			Verified: true, Reason: "exception: " + strings.Join(filters, ","),
		})
	}
	return ok(out)
}

func (s *Server) handleUnbreak(ctx context.Context, req proto.Request) proto.Response {
	var args proto.UnbreakArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	// Track which files were touched so we can re-sync the DAP-side set for
	// each. We must include files that became empty (so the adapter clears
	// its breakpoints there too).
	touchedFiles := map[string]bool{}
	beforeRemove := sess.AllBPs()
	for _, b := range beforeRemove {
		touchedFiles[b.File] = true
	}

	var removed []int
	if args.All {
		for _, b := range beforeRemove {
			sess.RemoveBP(b.LocalID)
			removed = append(removed, b.LocalID)
		}
	} else {
		for _, id := range args.IDs {
			if b, ok := sess.RemoveBP(id); ok {
				removed = append(removed, id)
				touchedFiles[b.File] = true
			}
		}
	}

	for file := range touchedFiles {
		bps := sess.BPsForFile(file)
		dapBPs := make([]godap.SourceBreakpoint, 0, len(bps))
		for _, b := range bps {
			sb := godap.SourceBreakpoint{Line: b.Line}
			if b.Condition != "" {
				sb.Condition = b.Condition
			}
			dapBPs = append(dapBPs, sb)
		}
		_, _ = sess.Client().SetBreakpoints(ctx, godap.Source{Path: file}, dapBPs)
	}

	return ok(map[string]interface{}{"removed": removed})
}

type execKind int

const (
	execContinue execKind = iota
	execStep
	execNext
	execFinish
)

func (s *Server) handleExec(ctx context.Context, req proto.Request, kind execKind) proto.Response {
	var args proto.ContinueArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	tid := args.Thread
	if tid == 0 {
		tid = sess.CurrentThread()
	}
	timeout := 30 * time.Second
	if args.TimeoutSec > 0 {
		timeout = time.Duration(args.TimeoutSec * float64(time.Second))
	}

	// Install waiter BEFORE issuing the resume request to avoid losing the stop.
	waiter := sess.InstallWaiter()

	if err := sess.EnsureConfigurationDone(ctx); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}

	var execErr error
	switch kind {
	case execContinue:
		execErr = sess.Client().Continue(ctx, tid)
	case execStep:
		execErr = sess.Client().StepIn(ctx, tid)
	case execNext:
		execErr = sess.Client().Next(ctx, tid)
	case execFinish:
		execErr = sess.Client().StepOut(ctx, tid)
	}
	if execErr != nil {
		return errResp("ADAPTER_FAILED", execErr.Error(), "")
	}

	pi, err := waiter.Wait(ctx, timeout)
	if err != nil {
		return errResp("INTERNAL_ERROR", err.Error(), "")
	}

	// On paused: enrich with location from stack[0].
	if pi.State == string(session.StatePaused) {
		if loc := fetchTopLocation(ctx, sess); loc != nil {
			pi.Location = loc
			sess.SetLastLocation(loc)
			// Auto-remove any matching --once breakpoint at this location.
			// (Fallback for adapters that don't fill HitBreakpointIds.)
			if pi.Reason == "breakpoint" {
				clearOnceAt(ctx, sess, loc.File, loc.Line)
			}
		}
	}
	return ok(pi)
}

func clearOnceAt(ctx context.Context, sess *session.Session, file string, line int) {
	for _, b := range sess.BPsForFile(file) {
		if b.Once && b.Line == line {
			_, _ = sess.RemoveBP(b.LocalID)
		}
	}
	// Resync the file's BPs.
	remaining := sess.BPsForFile(file)
	dapBPs := make([]godap.SourceBreakpoint, 0, len(remaining))
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
	_, _ = sess.Client().SetBreakpoints(ctx, godap.Source{Path: file}, dapBPs)
}

func (s *Server) handlePause(ctx context.Context, req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	if err := sess.EnsureConfigurationDone(ctx); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	// Install waiter BEFORE issuing pause so we don't miss the
	// StoppedEvent the adapter fires synchronously from its handler.
	waiter := sess.InstallWaiter()
	if err := sess.Client().Pause(ctx, sess.CurrentThread()); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	pi, _ := waiter.Wait(ctx, 10*time.Second)
	return ok(pi)
}

func (s *Server) handleStack(ctx context.Context, req proto.Request) proto.Response {
	var args proto.StackArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	tid := args.Thread
	if tid == 0 {
		tid = sess.CurrentThread()
	}
	limit := args.Limit
	if limit == 0 {
		limit = 20
	}
	r, err := sess.Client().StackTrace(ctx, tid, limit)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	var out proto.StackResult
	for _, f := range r.Body.StackFrames {
		out.Frames = append(out.Frames, proto.Frame{
			ID:       f.Id,
			Name:     f.Name,
			File:     f.Source.Path,
			Line:     f.Line,
			Column:   f.Column,
			Function: f.Name,
		})
	}
	return ok(out)
}

func (s *Server) handleThreads(ctx context.Context, req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	r, err := sess.Client().Threads(ctx)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	threads := make([]map[string]interface{}, 0, len(r.Body.Threads))
	for _, t := range r.Body.Threads {
		threads = append(threads, map[string]interface{}{"id": t.Id, "name": t.Name})
	}
	return ok(map[string]interface{}{"threads": threads})
}

func (s *Server) handleLocals(ctx context.Context, req proto.Request) proto.Response {
	var args proto.LocalsArgs
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
	scopeName := "Locals"
	for _, sc := range scopes.Body.Scopes {
		if strings.EqualFold(sc.Name, "Locals") || strings.EqualFold(sc.Name, "Local") {
			ref = sc.VariablesReference
			scopeName = sc.Name
			break
		}
	}
	if ref == 0 && len(scopes.Body.Scopes) > 0 {
		ref = scopes.Body.Scopes[0].VariablesReference
		scopeName = scopes.Body.Scopes[0].Name
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
	// Java without debug info: javac strips local-variable names so JDWP
	// returns only synthetic argN slots. Detect and nudge the user.
	if sess.Lang == "java" && looksLikeMissingDebugInfo(out.Vars) {
		out.Hint = "Java source was compiled without debug info — locals show only argN slots. Recompile with: javac -g <file>.java"
	}
	return ok(out)
}

// looksLikeMissingDebugInfo returns true when every variable name is "this"
// or "argN" — the telltale signature of javac without -g.
func looksLikeMissingDebugInfo(vars []proto.Var) bool {
	if len(vars) == 0 {
		return false
	}
	hasArg := false
	for _, v := range vars {
		if v.Name == "this" {
			continue
		}
		if len(v.Name) >= 4 && v.Name[:3] == "arg" {
			ok := true
			for _, c := range v.Name[3:] {
				if c < '0' || c > '9' {
					ok = false
					break
				}
			}
			if ok {
				hasArg = true
				continue
			}
		}
		return false
	}
	return hasArg
}

func (s *Server) handleEval(ctx context.Context, req proto.Request) proto.Response {
	var args proto.EvalArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	if sess.ReadOnly {
		// Allow watch context but not repl (repl can mutate state).
	}
	frameID, err := resolveFrameID(ctx, sess, args.Frame)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	context_ := "repl"
	if sess.ReadOnly {
		context_ = "watch"
	}
	if args.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(args.TimeoutSec*float64(time.Second)))
		defer cancel()
	}
	r, err := sess.Client().Evaluate(ctx, args.Expression, frameID, context_)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errResp("TIMEOUT", "evaluation exceeded timeout", "increase --timeout or simplify the expression")
		}
		// Auto-qualify static fields for Java: if the error looks like an
		// unresolved identifier and we have a declaring class, retry as
		// `ClassName.<expr>` once. This makes `eval globalCounter` work
		// from inside a method without forcing the caller to know the FQN.
		if sess.Lang == "java" && looksLikeNameUnknown(err.Error()) && isSimpleIdentifier(args.Expression) {
			if cls := javaClassFromFrame(ctx, sess, args.Frame); cls != "" {
				qualified := cls + "." + args.Expression
				if r2, err2 := sess.Client().Evaluate(ctx, qualified, frameID, context_); err2 == nil {
					return ok(proto.EvalResult{
						Result: r2.Body.Result, Type: r2.Body.Type, Ref: r2.Body.VariablesReference,
					})
				}
			}
		}
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	return ok(proto.EvalResult{
		Result: r.Body.Result, Type: r.Body.Type, Ref: r.Body.VariablesReference,
	})
}

func looksLikeNameUnknown(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "name unknown") ||
		strings.Contains(s, "cannot find symbol") ||
		strings.Contains(s, "cannot resolve")
}

func isSimpleIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '_' || c == '$' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handleSet(ctx context.Context, req proto.Request) proto.Response {
	var args proto.SetVarArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	if sess.ReadOnly {
		return errResp("READ_ONLY_MODE", "session is read-only", "remove --read-only to mutate state")
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
	for _, sc := range scopes.Body.Scopes {
		if strings.EqualFold(sc.Name, "Locals") || strings.EqualFold(sc.Name, "Local") {
			ref = sc.VariablesReference
			break
		}
	}
	if ref == 0 && len(scopes.Body.Scopes) > 0 {
		ref = scopes.Body.Scopes[0].VariablesReference
	}
	r, err := sess.Client().SetVariable(ctx, ref, args.Name, args.Value)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	return ok(map[string]interface{}{"name": args.Name, "value": r.Body.Value, "type": r.Body.Type})
}

func (s *Server) handleSnapshot(ctx context.Context, req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return errResp("SESSION_NOT_FOUND", err.Error(), "")
	}
	state := string(sess.State())
	out := proto.SnapshotResult{State: state}
	if state != string(session.StatePaused) {
		return ok(out)
	}
	tid := sess.CurrentThread()
	out.Thread = tid

	stk, err := sess.Client().StackTrace(ctx, tid, 50)
	if err == nil {
		for _, f := range stk.Body.StackFrames {
			out.Frames = append(out.Frames, proto.Frame{
				ID: f.Id, Name: f.Name, File: f.Source.Path, Line: f.Line, Column: f.Column, Function: f.Name,
			})
		}
		if len(stk.Body.StackFrames) > 0 {
			top := stk.Body.StackFrames[0]
			out.Location = &proto.Loc{File: top.Source.Path, Line: top.Line, Function: top.Name}
		}
	}
	if len(out.Frames) > 0 {
		scopes, err := sess.Client().Scopes(ctx, out.Frames[0].ID)
		if err == nil {
			for _, sc := range scopes.Body.Scopes {
				vars, err := sess.Client().Variables(ctx, sc.VariablesReference)
				if err != nil {
					continue
				}
				for _, v := range vars.Body.Variables {
					vv := proto.Var{
						Name: v.Name, Value: v.Value, Type: v.Type,
						Ref: v.VariablesReference, Expandable: v.VariablesReference > 0,
					}
					if strings.EqualFold(sc.Name, "Locals") || strings.EqualFold(sc.Name, "Local") {
						out.Locals = append(out.Locals, vv)
					} else {
						out.Globals = append(out.Globals, vv)
					}
				}
			}
		}
	}
	return ok(out)
}

// ---- helpers ----

func fetchTopLocation(ctx context.Context, sess *session.Session) *proto.Loc {
	r, err := sess.Client().StackTrace(ctx, sess.CurrentThread(), 1)
	if err != nil || len(r.Body.StackFrames) == 0 {
		return nil
	}
	f := r.Body.StackFrames[0]
	return &proto.Loc{File: f.Source.Path, Line: f.Line, Function: f.Name}
}

func resolveFrameID(ctx context.Context, sess *session.Session, idx int) (int, error) {
	r, err := sess.Client().StackTrace(ctx, sess.CurrentThread(), 50)
	if err != nil {
		return 0, err
	}
	if len(r.Body.StackFrames) == 0 {
		return 0, fmt.Errorf("no frames in current stack")
	}
	if idx < 0 || idx >= len(r.Body.StackFrames) {
		idx = 0
	}
	return r.Body.StackFrames[idx].Id, nil
}

func parseLocation(loc string) (string, int, error) {
	i := strings.LastIndex(loc, ":")
	if i <= 0 || i == len(loc)-1 {
		return "", 0, fmt.Errorf("invalid location: %q", loc)
	}
	line, err := strconv.Atoi(loc[i+1:])
	if err != nil {
		return "", 0, fmt.Errorf("invalid line in %q: %w", loc, err)
	}
	file := loc[:i]
	// Resolve relative paths to absolute for adapters that need it.
	if !strings.HasPrefix(file, "/") {
		if abs, err := absPath(file); err == nil {
			file = abs
		}
	}
	return file, line, nil
}

func absPath(p string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return p, err
	}
	if strings.HasPrefix(p, "/") {
		return p, nil
	}
	return wd + "/" + p, nil
}

func unmarshalArgs(raw json.RawMessage, into interface{}) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, into)
}

func ok(data interface{}) proto.Response {
	raw, _ := json.Marshal(data)
	return proto.Response{OK: true, Data: raw}
}

func errResp(code, msg, hint string) proto.Response {
	return proto.Response{OK: false, Error: &proto.RespError{Code: code, Message: msg, Hint: hint}}
}
