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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	godap "github.com/google/go-dap"

	"github.com/y0geshpatil/sl-dbg/internal/adapter"
	"github.com/y0geshpatil/sl-dbg/internal/ipc"
	"github.com/y0geshpatil/sl-dbg/internal/proto"
	"github.com/y0geshpatil/sl-dbg/internal/session"
)

// Server is the daemon.
type Server struct {
	mgr      *session.Manager
	listener net.Listener
	logger   *log.Logger
	mu       sync.Mutex
	closing  bool

	// startedAt is set when Run() begins listening. Used to enrich
	// SESSION_NOT_FOUND errors with a "daemon recently respawned" hint
	// when the caller's session id can't be found. Issue #28.
	startedAt time.Time

	// Policy + audit are loaded from SL_DBG_* env vars at daemon startup.
	// Zero values = legacy permissive behavior (issues #18–#23).
	policy Policy
	audit  *AuditLogger
}

// sessionNotFound builds an error response that explains the most-likely
// cause: either the id is genuinely unknown, or the daemon respawned after
// a crash and all in-memory sessions were lost. Issue #28.
func (s *Server) sessionNotFound(sid string, cause string) proto.Response {
	uptime := time.Since(s.startedAt)
	live := len(s.mgr.List())
	msg := cause
	if msg == "" {
		msg = fmt.Sprintf("no session %q", sid)
	}
	if live == 0 && uptime < 5*time.Minute {
		return errResp("DAEMON_RESPAWNED",
			fmt.Sprintf("session %q not found; daemon was started %s ago and has no sessions — it likely respawned after a crash/restart", sid, uptime.Round(time.Second)),
			"re-issue `debug_start` / `debug_attach` to recreate the session; any pre-respawn session ids are gone")
	}
	return errResp("SESSION_NOT_FOUND", msg, fmt.Sprintf("daemon uptime %s, %d live session(s) — call `debug_sessions` for current ids", uptime.Round(time.Second), live))
}

// runInspect dispatches an inspection-class handler under the session's
// per-session inspectMu so concurrent inspection chains (stack→scope→
// variables, eval, print) serialize and don't race the variablesReference
// lifecycle (STALE_FRAME). Issue #25.
//
// When the session id is unknown, we skip the lock and let the handler
// itself return SESSION_NOT_FOUND so the error shape stays consistent.
// Returns the handler's response unmodified.
func (s *Server) runInspect(ctx context.Context, req proto.Request, h func(context.Context, proto.Request) proto.Response) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return h(ctx, req)
	}
	var resp proto.Response
	_ = sess.Inspect(func() error {
		resp = h(ctx, req)
		return nil
	})
	return resp
}

// Run starts the daemon and blocks until shutdown or fatal error.
// If the socket is already in use by a live daemon, returns an error.
func Run() error {
	// Set up logging to file with crude size-based rotation. We don't want
	// secrets/PII to accumulate forever; once the log crosses ~10MiB we
	// rename it to <log>.1 (discarding any older .1) and start fresh.
	if err := ipc.EnsureDir(); err != nil {
		return err
	}
	logPath := ipc.LogFilePath()
	rotateIfTooLarge(logPath, 10*1024*1024)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	logger := log.New(lf, "sl-dbgd ", log.LstdFlags|log.Lmicroseconds)

	srv := &Server{
		mgr:       session.NewManager(),
		logger:    logger,
		policy:    LoadPolicyFromEnv(),
		startedAt: time.Now(),
	}
	audit, aerr := srv.policy.OpenAudit()
	if aerr != nil {
		logger.Printf("WARN: cannot open SL_DBG_AUDIT_LOG=%q: %v (continuing without audit)", srv.policy.AuditLogPath, aerr)
	}
	srv.audit = audit
	if srv.policy.MaxSessions > 0 {
		srv.mgr.SetMaxSessions(srv.policy.MaxSessions)
	}
	logger.Printf("policy: allow_program=%d allow_source_root=%d max_sessions=%d audit=%q allow_eval=%t deny_eval_patterns=%d",
		len(srv.policy.AllowProgram), len(srv.policy.AllowSourceRoot), srv.policy.MaxSessions,
		srv.policy.AuditLogPath, srv.policy.AllowEval, len(srv.policy.DenyEvalPatterns))

	// Refuse to start if another daemon is alive.
	stalePid := 0
	if pid := ipc.ReadPidFile(); pid > 0 {
		if isPidAlive(pid) {
			return fmt.Errorf("another daemon is running (pid %d)", pid)
		}
		stalePid = pid
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
	if stalePid > 0 {
		// Surface the fact that we respawned. The previous daemon either
		// crashed or was killed; subsequent sessions started against the
		// old daemon are now gone.
		logger.Printf("WARN: previous daemon (pid %d) was not running; respawned cleanly. Any prior sessions are lost.", stalePid)
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

// rotateIfTooLarge renames `path` to `path.1` when it exceeds maxBytes, so
// the new daemon process starts with a fresh log. Errors are best-effort.
func rotateIfTooLarge(path string, maxBytes int64) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < maxBytes {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
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
	s.logger.Printf("→ %s sess=%q args=%s", req.Cmd, req.Sess, redactArgs(req.Args))
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
		return s.runInspect(ctx, req, s.handleStack)
	case proto.CmdThreads:
		return s.runInspect(ctx, req, s.handleThreads)
	case proto.CmdLocals:
		return s.runInspect(ctx, req, s.handleLocals)
	case proto.CmdEval:
		return s.runInspect(ctx, req, s.handleEval)
	case proto.CmdSet:
		return s.runInspect(ctx, req, s.handleSet)
	case proto.CmdSnapshot:
		return s.runInspect(ctx, req, s.handleSnapshot)
	case proto.CmdWatch:
		return s.handleWatch(ctx, req)
	case proto.CmdGlobals:
		return s.runInspect(ctx, req, s.handleGlobals)
	case proto.CmdFields:
		return s.runInspect(ctx, req, s.handleFields)
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
		return s.runInspect(ctx, req, s.handlePrint)
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
	// Policy: program allowlist (#21).
	if err := s.policy.ProgramAllowed(args.Program); err != nil {
		s.audit.Log("start.denied", "", map[string]interface{}{"program": args.Program, "reason": err.Error()})
		return errResp("PROGRAM_NOT_ALLOWED", err.Error(), "set SL_DBG_ALLOW_PROGRAM to include this program, or restart the daemon without the env var")
	}
	// Policy: per-daemon session cap (#22).
	if err := s.mgr.Reserve(); err != nil {
		s.audit.Log("start.denied", "", map[string]interface{}{"program": args.Program, "reason": err.Error()})
		return errResp("RESOURCE_EXHAUSTED", err.Error(), fmt.Sprintf("the daemon is configured with SL_DBG_MAX_SESSIONS=%d. Stop an existing session or raise the limit.", s.mgr.MaxSessions()))
	}
	sess, err := s.mgr.CreateLaunch(ctx, args)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	s.audit.Log("start", sess.ID, map[string]interface{}{"lang": args.Lang, "program": args.Program, "cwd": args.Cwd})
	// Initial state. If stopOnEntry was requested, wait briefly for the entry pause.
	if args.StopOnEntry {
		pi, _ := sess.WaitForStop(ctx, 5*time.Second)
		if pi.State == "exited" || pi.State == "terminated" {
			return s.earlyTerminationResp(sess, pi)
		}
		return ok(proto.SessionResult{
			SessionID: sess.ID, Lang: sess.Lang, State: pi.State, Reason: pi.Reason,
			Location: pi.Location,
		})
	}
	// Briefly poll for early termination so a doomed launch (bad mainClass,
	// missing program, immediate crash) surfaces as an error instead of
	// returning ok=true / state=initializing and forcing the caller to wait
	// for the next `listen` to time out.
	pi, _ := sess.WaitForStop(ctx, 1500*time.Millisecond)
	if pi.State == "exited" || pi.State == "terminated" {
		return s.earlyTerminationResp(sess, pi)
	}
	return ok(proto.SessionResult{
		SessionID: sess.ID, Lang: sess.Lang, State: string(sess.State()), Reason: "launched",
	})
}

// earlyTerminationResp handles a session that terminated before the caller
// could issue any commands. Two outcomes:
//   - exit code 0 → success (state=exited): the program ran cleanly to
//     completion in under our 1.5s settle window. Not a failure. Issue #2.
//   - non-zero exit / no exit code (terminated abnormally) → LAUNCH_FAILED
//     with the captured stdout/stderr so the caller knows why.
//
// In both cases the session is removed because nothing more can be done with it.
func (s *Server) earlyTerminationResp(sess *session.Session, pi proto.PauseInfo) proto.Response {
	stdoutTail := sess.RecentStdoutTail(2048)
	stderrTail := sess.RecentStderrTail(2048)
	defer s.mgr.Remove(sess.ID)

	if pi.ExitCode != nil && *pi.ExitCode == 0 {
		// Clean exit: report as success so callers don't have to special-case
		// short-running programs. They can still grab the captured output.
		return ok(proto.SessionResult{
			SessionID: sess.ID,
			Lang:      sess.Lang,
			State:     string(session.StateExited),
			Reason:    "exited",
			ExitCode:  pi.ExitCode,
			Stdout:    stdoutTail,
			Stderr:    stderrTail,
		})
	}

	msg := "program terminated before any user command could run"
	if pi.ExitCode != nil {
		msg = fmt.Sprintf("program exited with code %d before any user command could run", *pi.ExitCode)
	}
	hint := "check program path, main class, classpath, and required env vars"
	// Build a labeled tail so we never call stdout "stderr" again (issue #2).
	var tailParts []string
	if stderrTail != "" {
		tailParts = append(tailParts, "stderr tail:\n"+strings.TrimRight(stderrTail, "\n"))
	}
	if stdoutTail != "" {
		tailParts = append(tailParts, "stdout tail:\n"+strings.TrimRight(stdoutTail, "\n"))
	}
	if len(tailParts) > 0 {
		hint = strings.Join(tailParts, "\n\n")
	}
	return errResp("LAUNCH_FAILED", msg, hint)
}

func (s *Server) handleAttach(ctx context.Context, req proto.Request) proto.Response {
	var args proto.AttachArgs
	if err := unmarshalArgs(req.Args, &args); err != nil {
		return errResp("USAGE_ERROR", err.Error(), "")
	}
	if err := s.mgr.Reserve(); err != nil {
		s.audit.Log("attach.denied", "", map[string]interface{}{"reason": err.Error()})
		return errResp("RESOURCE_EXHAUSTED", err.Error(), fmt.Sprintf("the daemon is configured with SL_DBG_MAX_SESSIONS=%d. Stop an existing session or raise the limit.", s.mgr.MaxSessions()))
	}
	sess, err := s.mgr.CreateAttach(ctx, args)
	if err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	s.audit.Log("attach", sess.ID, map[string]interface{}{"lang": args.Lang, "host": args.Host, "port": args.Port})
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
		return s.sessionNotFound(req.Sess, err.Error())
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
	// Issue #48: don't silently report success for a session id the daemon
	// has never seen. Idempotency over an unknown id hid bugs in callers.
	if _, err := s.mgr.Get(sid); err != nil {
		return s.sessionNotFound(sid, err.Error())
	}
	s.mgr.Remove(sid)
	return ok(proto.SessionResult{SessionID: sid, State: string(session.StateTerminated)})
}

func (s *Server) handleState(req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
	}
	st := string(sess.State())
	// For terminal states, the previously-cached pause reason/location/thread
	// are stale and misleading — clients use Location.Line to drive UI and
	// would loop forever thinking the program is still paused. Surface only
	// the terminal facts (state, reason, exitCode).
	if st == string(session.StateExited) || st == string(session.StateTerminated) {
		pi := proto.PauseInfo{State: st, Reason: st}
		if ec := sess.ExitCode(); ec != nil {
			pi.ExitCode = ec
		}
		return ok(pi)
	}
	reason, hitBP, loc := sess.LastPause()
	return ok(proto.PauseInfo{
		State:    st,
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if rerr := refuseIfReadOnly(sess); rerr != nil {
		return *rerr
	}
	// Policy: a breakpoint condition is an expression the adapter will
	// evaluate every time the line is hit. Issue #54.
	if args.Condition != "" {
		if err := s.policy.EvalEnabled(); err != nil {
			s.audit.Log("break.disabled", sess.ID, map[string]interface{}{"location": args.Location, "condition": args.Condition})
			return errResp("EVAL_DISABLED", err.Error(),
				"conditional breakpoints evaluate code at the target; start the daemon with SL_DBG_ALLOW_EVAL=1, or omit --condition")
		}
	}
	file, line, err := parseLocation(args.Location)
	if err != nil {
		return errResp("USAGE_ERROR", err.Error(), `expected "file:line" or "Class:line"`)
	}
	// Issue: break-class-line — for Java, "ClassName:line" or
	// "com.pkg.ClassName:line" is the natural reference (no source file
	// path needed). Resolve it to a .java file under sourceRoots so the
	// adapter gets a real Source.Path.
	if sess.Lang == "java" && !strings.ContainsAny(file, "/\\") && !strings.HasSuffix(file, ".java") {
		if resolved, ok := resolveJavaClassToFile(file, sess.SourceRoots); ok {
			file = resolved
		}
	}
	// Validate file + line before bothering the adapter. Class-name "files"
	// (no path separator) are passed through — the adapter resolves those.
	if strings.ContainsAny(file, "/\\") {
		fi, statErr := os.Stat(file)
		if statErr != nil {
			return errResp("USAGE_ERROR", "file not found: "+file,
				"pass an absolute path that exists; use Class:line for source we can't see")
		}
		if line <= 0 {
			return errResp("USAGE_ERROR", fmt.Sprintf("line %d is not a positive integer", line), "")
		}
		if eof := countLines(file); eof > 0 && line > eof {
			return errResp("USAGE_ERROR",
				fmt.Sprintf("line %d is past end of file (%d lines): %s", line, eof, file),
				"check the file or remove the breakpoint")
		}
		_ = fi
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
	var newBP *session.BP
	for i, b := range existing {
		if b.LocalID == bp.LocalID {
			newBP = existing[i]
			break
		}
	}
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
	if newBP != nil && !newBP.Verified {
		// Look up the adapter's reason text and any line-shift hint from
		// the response. java-debug returns the actual line it placed the
		// bp on if it shifted (e.g., '}' line → next executable line).
		var shiftedTo int
		for _, rbp := range resp.Body.Breakpoints {
			if rbp.Message != "" {
				out.Reason = rbp.Message
				break
			}
			if rbp.Line != 0 && rbp.Line != bp.Line {
				shiftedTo = rbp.Line
			}
		}
		if out.Reason == "" {
			switch {
			case shiftedTo > 0:
				out.Reason = fmt.Sprintf("line %d is not executable; nearest executable line is %d — try break ...:%d",
					bp.Line, shiftedTo, shiftedTo)
			case args.Condition != "":
				out.Reason = "unverified: line has no executable code, conditional cannot bind, or class not yet loaded — pick a body line (not a loop/closing-brace header)"
			default:
				out.Reason = "unverified: class not yet loaded or line has no executable code"
			}
		}
	}
	return ok(out)
}

// refuseIfReadOnly returns a non-nil response when the session was started
// with --read-only, blocking any mutating handler. Use a pointer so the
// caller can do `if r := refuseIfReadOnly(...); r != nil { return *r }`.
func refuseIfReadOnly(sess *session.Session) *proto.Response {
	if sess == nil || !sess.ReadOnly {
		return nil
	}
	r := errResp("READ_ONLY_MODE", "session is read-only",
		"remove --read-only on start/attach, or start a new mutating session")
	return &r
}

// countLines returns the number of newline-terminated lines in a file, or 0
// on error. Used for breakpoint range validation only.
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	buf := make([]byte, 32*1024)
	for {
		c, err := f.Read(buf)
		for i := 0; i < c; i++ {
			if buf[i] == '\n' {
				n++
			}
		}
		if err != nil {
			break
		}
	}
	return n + 1 // count last line even if unterminated
}

func (s *Server) handleBreaks(req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
	}
	// Initialize as empty slice — never return null. Issue #12: agents iterate
	// the array, and `null` forces them to special-case the empty-list path.
	out := proto.BreaksResult{Breakpoints: []proto.BreakResult{}}
	for _, b := range sess.AllBPs() {
		reason := ""
		if !b.Verified {
			reason = "pending (class not yet loaded, or unsupported line)"
		}
		out.Breakpoints = append(out.Breakpoints, proto.BreakResult{
			ID: b.LocalID, Verified: b.Verified, File: b.File, Line: b.Line,
			Condition: b.Condition, Reason: reason, Hits: b.Hits,
		})
	}
	for _, b := range sess.FuncBPs() {
		reason := ""
		if !b.Verified {
			reason = "pending (function/class not yet loaded)"
		}
		out.Breakpoints = append(out.Breakpoints, proto.BreakResult{
			ID: b.LocalID, Verified: b.Verified, Function: b.Name,
			Condition: b.Condition, Reason: reason, Hits: b.Hits,
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if rerr := refuseIfReadOnly(sess); rerr != nil {
		return *rerr
	}
	// Track which files were touched so we can re-sync the DAP-side set for
	// each. We must include files that became empty (so the adapter clears
	// its breakpoints there too).
	touchedFiles := map[string]bool{}
	beforeRemove := sess.AllBPs()
	for _, b := range beforeRemove {
		touchedFiles[b.File] = true
	}

	// Issue #14: always return an array, even when nothing matched.
	removed := []int{}
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if rerr := refuseIfReadOnly(sess); rerr != nil {
		return *rerr
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
		execErr = sess.Client().Continue(ctx, tid, args.SingleThread)
	case execStep:
		execErr = sess.Client().StepIn(ctx, tid, args.SingleThread)
	case execNext:
		execErr = sess.Client().Next(ctx, tid, args.SingleThread)
	case execFinish:
		execErr = sess.Client().StepOut(ctx, tid, args.SingleThread)
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	// Issue #10: if the session has already exited/terminated, don't bother
	// the adapter and don't wait 10s. Surface the terminal state immediately.
	switch sess.State() {
	case session.StateExited, session.StateTerminated:
		pi := proto.PauseInfo{State: string(sess.State()), Reason: "already " + string(sess.State())}
		if ec := sess.ExitCode(); ec != nil {
			pi.ExitCode = ec
		}
		return ok(pi)
	case session.StatePaused:
		// Already paused — nothing to do; return current state instead of
		// trying to pause a non-running target.
		reason, hitBP, loc := sess.LastPause()
		return ok(proto.PauseInfo{
			State: string(sess.State()), Reason: reason, Thread: sess.CurrentThread(),
			Location: loc, HitBP: hitBP,
		})
	}
	if err := sess.EnsureConfigurationDone(ctx); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	// Pick a thread to pause. CurrentThread() returns 0 right after a
	// continue (we haven't received another stop yet) — fall back to the
	// adapter's thread list so we don't send pause(thread=0).
	tid := sess.CurrentThread()
	if tid == 0 {
		if tr, terr := sess.Client().Threads(ctx); terr == nil && len(tr.Body.Threads) > 0 {
			tid = tr.Body.Threads[0].Id
		}
	}
	waiter := sess.InstallWaiter()
	if err := sess.Client().Pause(ctx, tid); err != nil {
		return errResp("ADAPTER_FAILED", err.Error(), "")
	}
	pi, _ := waiter.Wait(ctx, 10*time.Second)
	// Some adapters (notably JDWP for Java when the target is in a tight
	// native or sleeping section) acknowledge the pause request but never
	// fire StoppedEvent quickly. Surface this as a typed error instead of a
	// silent "still running" so callers can retry or break-fn instead.
	if pi.State != string(session.StatePaused) {
		return errResp("PAUSE_TIMEOUT",
			"adapter accepted pause but program did not stop within 10s",
			"the thread may be in a sleep/wait/native frame; set a function or line breakpoint and continue")
	}
	return ok(pi)
}

func (s *Server) handleStack(ctx context.Context, req proto.Request) proto.Response {
	var args proto.StackArgs
	_ = unmarshalArgs(req.Args, &args)
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
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
		return s.sessionNotFound(req.Sess, err.Error())
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
		return adapterErr(err, "locals")
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
	// Issue #16: reject empty/whitespace expressions up front instead of
	// passing them to the adapter (which returns the misleading "no frames
	// in current stack").
	if strings.TrimSpace(args.Expression) == "" {
		return errResp("USAGE_ERROR", "expression is empty",
			"pass a non-empty Java/Python/Go expression, e.g. `eval x + 1`")
	}
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
	}
	// Policy: eval must be explicitly enabled on the daemon (#54). The
	// substring deny-list (#19) is a secondary CLI typo-guard, NOT a
	// security boundary — it is trivially bypassable via reflection.
	if err := s.policy.EvalEnabled(); err != nil {
		s.audit.Log("eval.disabled", sess.ID, map[string]interface{}{"expr": args.Expression})
		return errResp("EVAL_DISABLED", err.Error(),
			"start the daemon with SL_DBG_ALLOW_EVAL=1 to permit eval (audit log strongly recommended via SL_DBG_AUDIT_LOG)")
	}
	if err := s.policy.EvalAllowed(args.Expression); err != nil {
		s.audit.Log("eval.denied", sess.ID, map[string]interface{}{"expr": args.Expression, "reason": err.Error()})
		return errResp("EVAL_DENIED", err.Error(),
			"the daemon blocks this expression via SL_DBG_DENY_EVAL_PATTERNS. Set the env var to '-' to disable, or remove the deny-listed token.")
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
	s.audit.Log("eval", sess.ID, map[string]interface{}{"expr": args.Expression, "context": context_, "ok": err == nil})
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
		return adapterErr(err, "eval")
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
		return s.sessionNotFound(req.Sess, err.Error())
	}
	if rerr := refuseIfReadOnly(sess); rerr != nil {
		return *rerr
	}
	// Policy: `set` mutates target state via the adapter's expression
	// evaluator and is treated as eval for SL_DBG_ALLOW_EVAL purposes (#54).
	if err := s.policy.EvalEnabled(); err != nil {
		s.audit.Log("set.disabled", sess.ID, map[string]interface{}{"name": args.Name, "value": args.Value})
		return errResp("EVAL_DISABLED", err.Error(),
			"start the daemon with SL_DBG_ALLOW_EVAL=1 to permit set (audit log strongly recommended via SL_DBG_AUDIT_LOG)")
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
	s.audit.Log("set", sess.ID, map[string]interface{}{"name": args.Name, "value": args.Value, "ok": err == nil})
	if err != nil {
		return adapterErr(err, "set")
	}
	return ok(map[string]interface{}{"name": args.Name, "value": r.Body.Value, "type": r.Body.Type})
}

func (s *Server) handleSnapshot(ctx context.Context, req proto.Request) proto.Response {
	sess, err := s.mgr.Get(req.Sess)
	if err != nil {
		return s.sessionNotFound(req.Sess, err.Error())
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
	// Resolve relative paths to absolute, but only if the "file" really
	// looks like a filesystem path. Class names like "ComplexLoopDebug" or
	// "com.example.Foo" must be passed through unchanged so the adapter can
	// resolve them by name.
	if looksLikeFilePath(file) && !strings.HasPrefix(file, "/") {
		if abs, err := absPath(file); err == nil {
			file = abs
		}
	}
	return file, line, nil
}

// resolveJavaClassToFile maps a Java class reference (either "Outer" or
// "com.pkg.Outer", with optional "$Inner" suffix) to an existing .java file
// under one of the sourceRoots. Inner-class qualifiers are stripped because
// they share the outer class's source file. Returns ("", false) when no
// matching file exists in any root.
//
// Used by handleBreak so `sl-dbg break ComplexLoopDebug:42 --lang java`
// works without requiring the caller to know the on-disk path.
func resolveJavaClassToFile(class string, roots []string) (string, bool) {
	if class == "" {
		return "", false
	}
	if i := strings.Index(class, "$"); i >= 0 {
		class = class[:i]
	}
	rel := strings.ReplaceAll(class, ".", "/") + ".java"
	bare := class
	if i := strings.LastIndex(class, "."); i >= 0 {
		bare = class[i+1:]
	}
	bareRel := bare + ".java"
	for _, root := range roots {
		for _, cand := range []string{
			filepath.Join(root, rel),
			filepath.Join(root, bareRel),
		} {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand, true
			}
		}
	}
	return "", false
}

func looksLikeFilePath(p string) bool {
	if strings.ContainsAny(p, "/\\") {
		return true
	}
	for _, ext := range []string{".py", ".java", ".go", ".js", ".ts", ".rb", ".rs", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
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

// adapterErr maps known underlying DAP/JDI error strings into a stable
// error code + actionable hint so agents and humans don't have to grep
// raw Java exception text. Falls back to ADAPTER_FAILED for unknown
// patterns, preserving the original message for debugging.
func adapterErr(err error, op string) proto.Response {
	if err == nil {
		return errResp("ADAPTER_FAILED", op+" failed", "")
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	// Issue #7: strip noisy java-debug wrappers so we don't leak the DAP/JDI
	// pipeline into user-facing errors. Keep the original text in `data` for
	// debuggability.
	cleaned := cleanAdapterMessage(msg)
	switch {
	case strings.Contains(msg, "AbsentInformationException"):
		return errResp("MISSING_DEBUG_INFO", op+": class compiled without debug info",
			"Recompile with `javac -g` (or build with -g:vars) so locals and parameter names are available.")
	case strings.Contains(msg, "No 'this'"), strings.Contains(low, "in native or static method"):
		return errResp("EVAL_NO_THIS", op+": cannot evaluate `this` in a static or native frame",
			"Qualify static fields with ClassName.field, or step into a non-static frame first.")
	case strings.Contains(msg, "Name unknown"):
		return errResp("EVAL_NAME_UNKNOWN", op+": "+cleaned,
			"Variable not in scope. For static fields use ClassName.field; for outer-class fields use Outer.this.field.")
	case strings.Contains(msg, "ClassNotLoadedException"), strings.Contains(low, "class not prepared"):
		return errResp("CLASS_NOT_LOADED", op+": target class not yet loaded by the JVM",
			"Set the breakpoint earlier, or step until the class is referenced.")
	case strings.Contains(msg, "InvalidStackFrameException"):
		return errResp("STALE_FRAME", op+": stack frame is no longer valid",
			"The program resumed; re-fetch the stack and retry.")
	case strings.Contains(msg, "VMDisconnectedException"), strings.Contains(low, "debuggee vm has terminated"):
		return errResp("VM_DISCONNECTED", op+": debuggee VM has terminated", "Start a new session.")
	case strings.Contains(low, "/ by zero"), strings.Contains(low, "arithmeticexception"):
		return errResp("EVAL_RUNTIME_EXCEPTION", op+": ArithmeticException: "+cleaned,
			"Guard against zero divisors before evaluating.")
	case strings.Contains(low, "nullpointerexception"), strings.Contains(low, "cannot access field of primitive type: null"):
		return errResp("EVAL_RUNTIME_EXCEPTION", op+": NullPointerException: "+cleaned,
			"Check that the receiver is non-null before dereferencing.")
	case strings.Contains(low, "classcastexception"):
		return errResp("EVAL_RUNTIME_EXCEPTION", op+": ClassCastException: "+cleaned,
			"Verify the runtime type before casting (use instanceof first).")
	case strings.Contains(low, "syntax error"), strings.Contains(low, "cannot find symbol"),
		strings.Contains(low, "parse error"):
		return errResp("EVAL_SYNTAX_ERROR", op+": "+cleaned, "Check the expression syntax.")
	case strings.Contains(low, "timeout"):
		return errResp("TIMEOUT", op+": "+cleaned, "Increase --timeout or check that the program is making progress.")
	case strings.Contains(low, "runtimeexception"), strings.Contains(low, "cannot evaluate because of"):
		return errResp("EVAL_RUNTIME_EXCEPTION", op+": "+cleaned,
			"The expression evaluated to a runtime exception; see message for details.")
	}
	return errResp("ADAPTER_FAILED", op+": "+cleaned, "")
}

// cleanAdapterMessage strips the noisy java-debug / DAP wrappers from an
// adapter error string so it reads as a normal sentence. Examples:
//
//   dap error: Cannot evaluate because of java.lang.RuntimeException: / by zero.
//   → / by zero
//   dap error: Cannot evaluate because of java.lang.NullPointerException: Cannot access field of primitive type: null.
//   → Cannot access field of primitive type: null
func cleanAdapterMessage(s string) string {
	const dapPrefix = "dap error: "
	const cantEval = "Cannot evaluate because of "
	s = strings.TrimSpace(s)
	if i := strings.Index(s, dapPrefix); i >= 0 {
		s = s[i+len(dapPrefix):]
	}
	if i := strings.Index(s, cantEval); i >= 0 {
		s = s[i+len(cantEval):]
	}
	// Strip a leading Java exception class name + colon (e.g.
	// "java.lang.RuntimeException: / by zero" → "/ by zero").
	if i := strings.Index(s, ": "); i > 0 {
		head := s[:i]
		if strings.Count(head, ".") >= 2 && !strings.ContainsAny(head, " \t\n\"',()") {
			s = s[i+2:]
		}
	}
	return strings.TrimRight(s, ". \n\t")
}
