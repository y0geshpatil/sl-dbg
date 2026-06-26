// Package session models one debug session: an adapter subprocess plus a DAP
// client plus mutable state (current thread, frame, breakpoints, last pause info).
//
// Sessions are owned by the daemon and looked up by short id.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	godap "github.com/google/go-dap"

	"github.com/yogeshpatil/sl-dbg/internal/adapter"
	"github.com/yogeshpatil/sl-dbg/internal/dap"
	"github.com/yogeshpatil/sl-dbg/internal/proto"
)

// State represents the high-level execution state of a session.
type State string

const (
	StateInitializing State = "initializing"
	StatePaused       State = "paused"
	StateRunning      State = "running"
	StateExited       State = "exited"
	StateTerminated   State = "terminated"
)

// Session is one debug session.
type Session struct {
	ID       string
	Lang     string
	Program  string // for launch sessions
	Attached string // "host:port" for attach sessions, empty otherwise
	ReadOnly bool

	mu              sync.Mutex
	state           State
	currentThread   int
	lastPauseReason string
	lastLocation    *proto.Loc
	lastHitBP       int
	exitCode        *int
	bpNextLocalID   int
	bpsByID         map[int]*BP // local id -> BP info

	// lastTerminal captures the most recent exited/terminated event so that
	// late waiters (`listen` issued after the program already died) get an
	// immediate, correct response instead of blocking until the timeout.
	lastTerminal *stopEvent

	cli  *dap.Client
	proc *exec.Cmd
	caps godap.Capabilities

	// For attach sessions where the debuggee is suspended on connection
	// (e.g. JVM started with suspend=y) we defer the DAP configurationDone
	// request until the first user action that should resume the program,
	// so that breakpoints set via `sl-dbg break` between attach and the
	// first `continue` are guaranteed to land before the VM starts running.
	pendingConfigDone bool
	configDoneMu      sync.Mutex

	// Event coordination
	events chan godap.Message // single subscription drain
	doneCh chan struct{}

	// "Stopped waiter" — when blocking commands run, they install a waiter here.
	waitersMu sync.Mutex
	waiters   []chan stopEvent

	// Output and event ring buffers for `sl-dbg output` / `sl-dbg events`.
	bufMu   sync.Mutex
	outputs []OutputEntry
	evlog   []LoggedEvent

	// Watch expressions (re-evaluated on snapshot or on `watch list`).
	watchMu      sync.Mutex
	watches      []*Watch
	watchNextID  int

	// Active exception breakpoint filters (replayed on restart).
	excFilters []string

	// Active function breakpoints (replayed on restart).
	funcBPs []FuncBP
}

// OutputEntry captures one DAP OutputEvent (debuggee stdout/stderr/console).
type OutputEntry struct {
	TS       time.Time
	Category string
	Output   string
}

// LoggedEvent captures notable DAP events for later inspection.
type LoggedEvent struct {
	TS   time.Time
	Type string
	Body map[string]interface{}
}

// Watch is one watch expression tracked on the session.
type Watch struct {
	ID         int
	Expression string
}

// FuncBP is a function-name breakpoint stored on the session for replay.
type FuncBP struct {
	LocalID   int
	Name      string
	Condition string
	HitCond   string
	DAPID     int
	Verified  bool
}

// BP is a stored breakpoint record on the session side.
type BP struct {
	LocalID   int
	File      string
	Line      int
	Condition string
	HitCond   string
	LogMsg    string
	Once      bool // remove after first hit
	DAPID     int  // id reported by adapter (may be 0 if not verified yet)
	Verified  bool
}

type stopEvent struct {
	reason   string
	threadID int
	location *proto.Loc
	hitBP    int
	exited   *int
	terminated bool
	message  string
}

// newID returns a short hex id.
func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Manager owns all sessions in the daemon process.
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	defID    string
}

// NewManager returns an empty manager.
func NewManager() *Manager {
	return &Manager{sessions: map[string]*Session{}}
}

// List returns a snapshot of all sessions.
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Get returns the session with id sid, or the default session if sid is empty.
func (m *Manager) Get(sid string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if sid == "" {
		sid = m.defID
	}
	s, ok := m.sessions[sid]
	if !ok {
		return nil, fmt.Errorf("session not found: %q", sid)
	}
	return s, nil
}

// SetDefault changes the default session id.
func (m *Manager) SetDefault(sid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[sid]; !ok {
		return fmt.Errorf("session not found: %q", sid)
	}
	m.defID = sid
	return nil
}

// DefaultID returns the default session id, or "".
func (m *Manager) DefaultID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defID
}

// Remove disposes a session.
func (m *Manager) Remove(sid string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sid]; ok {
		s.Close()
		delete(m.sessions, sid)
		if m.defID == sid {
			m.defID = ""
			// promote any remaining session
			for k := range m.sessions {
				m.defID = k
				break
			}
		}
	}
}

// CreateLaunch spawns the adapter, sends initialize+launch, and waits for the
// "initialized" event before sending configurationDone.
func (m *Manager) CreateLaunch(ctx context.Context, args proto.StartArgs) (*Session, error) {
	spec, err := adapter.Get(args.Lang)
	if err != nil {
		return nil, err
	}
	if _, err := spec.Detect(); err != nil {
		return nil, fmt.Errorf("adapter %q not installed: %w  (hint: %s)", args.Lang, err, spec.InstallHint)
	}
	launchArgs, err := spec.BuildLaunchArgs(adapter.LaunchCfg{
		Program:     args.Program,
		Args:        args.Args,
		Cwd:         args.Cwd,
		Env:         args.Env,
		StopOnEntry: args.StopOnEntry,
		MainClass:   args.MainClass,
		Classpath:   args.Classpath,
		SourceRoots: args.SourceRoots,
	})
	if err != nil {
		return nil, err
	}

	s, err := m.startAdapter(ctx, spec, args.Lang)
	if err != nil {
		return nil, err
	}
	if args.Name != "" {
		if err := m.validateName(args.Name); err != nil {
			s.Close()
			return nil, err
		}
		s.ID = args.Name
	}
	s.Program = args.Program
	s.ReadOnly = args.ReadOnly

	if _, err := s.cli.Initialize(ctx, spec.AdapterID); err != nil {
		s.Close()
		return nil, fmt.Errorf("dap initialize: %w", err)
	}
	s.caps = s.cli.Caps

	// Subscribe BEFORE launch so we receive "initialized" event.
	initialized := make(chan struct{}, 1)
	s.startEventPump(initialized)

	// Send launch asynchronously. debugpy holds the launch response until
	// configurationDone, so we must not block on it here.
	launchCh, err := s.cli.LaunchAsync(launchArgs)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("dap launch send: %w", err)
	}

	// Wait for "initialized" event (up to 15s).
	select {
	case <-initialized:
	case <-time.After(15 * time.Second):
		s.Close()
		return nil, fmt.Errorf("timeout waiting for adapter 'initialized' event")
	}

	if err := s.cli.ConfigurationDone(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("dap configurationDone: %w", err)
	}

	// Now collect the launch response.
	select {
	case msg, ok := <-launchCh:
		if !ok {
			s.Close()
			return nil, fmt.Errorf("dap launch: adapter disconnected")
		}
		if resp, ok := msg.(godap.ResponseMessage); ok && !resp.GetResponse().Success {
			s.Close()
			return nil, fmt.Errorf("dap launch failed: %s", resp.GetResponse().Message)
		}
	case <-time.After(15 * time.Second):
		s.Close()
		return nil, fmt.Errorf("dap launch: timeout waiting for response after configurationDone")
	}

	m.register(s)
	return s, nil
}

// CreateAttach starts the adapter and issues attach.
func (m *Manager) CreateAttach(ctx context.Context, args proto.AttachArgs) (*Session, error) {
	spec, err := adapter.Get(args.Lang)
	if err != nil {
		return nil, err
	}
	if _, err := spec.Detect(); err != nil {
		return nil, fmt.Errorf("adapter %q not installed: %w  (hint: %s)", args.Lang, err, spec.InstallHint)
	}
	attachArgs, err := spec.BuildAttachArgs(adapter.AttachCfg{
		Host:        args.Host,
		Port:        args.Port,
		PID:         args.PID,
		SourceRoots: args.SourceRoots,
	})
	if err != nil {
		return nil, err
	}

	s, err := m.startAdapter(ctx, spec, args.Lang)
	if err != nil {
		return nil, err
	}
	if args.Name != "" {
		if err := m.validateName(args.Name); err != nil {
			s.Close()
			return nil, err
		}
		s.ID = args.Name
	}
	s.ReadOnly = args.ReadOnly
	if args.Port != 0 {
		s.Attached = fmt.Sprintf("%s:%d", args.Host, args.Port)
	} else if args.PID != 0 {
		s.Attached = fmt.Sprintf("pid:%d", args.PID)
	}

	if _, err := s.cli.Initialize(ctx, spec.AdapterID); err != nil {
		s.Close()
		return nil, err
	}
	s.caps = s.cli.Caps

	initialized := make(chan struct{}, 1)
	s.startEventPump(initialized)

	attachCh, err := s.cli.AttachAsync(attachArgs)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("dap attach send: %w", err)
	}

	select {
	case <-initialized:
	case <-time.After(15 * time.Second):
		s.Close()
		return nil, fmt.Errorf("timeout waiting for adapter 'initialized' event")
	}

	// Defer configurationDone until the user issues their first resuming
	// command (continue / next / step / pause). This lets `sl-dbg break ...`
	// install breakpoints before the debuggee VM is released, which is the
	// only way to catch a JVM that was started with suspend=y.
	s.pendingConfigDone = true

	// Wait briefly for the attach response (debuggers usually reply quickly
	// once the JDI connection is established, without needing configDone).
	select {
	case msg, ok := <-attachCh:
		if !ok {
			s.Close()
			return nil, fmt.Errorf("dap attach: adapter disconnected")
		}
		if resp, ok := msg.(godap.ResponseMessage); ok && !resp.GetResponse().Success {
			s.Close()
			return nil, fmt.Errorf("dap attach failed: %s", resp.GetResponse().Message)
		}
	case <-time.After(15 * time.Second):
		s.Close()
		return nil, fmt.Errorf("dap attach: timeout waiting for attach response")
	}

	m.register(s)
	return s, nil
}

// EnsureConfigurationDone sends DAP configurationDone exactly once, just before
// the first user-issued command that resumes execution. Safe to call repeatedly.
func (s *Session) EnsureConfigurationDone(ctx context.Context) error {
	s.configDoneMu.Lock()
	defer s.configDoneMu.Unlock()
	if !s.pendingConfigDone {
		return nil
	}
	if err := s.cli.ConfigurationDone(ctx); err != nil {
		return fmt.Errorf("dap configurationDone: %w", err)
	}
	s.pendingConfigDone = false
	return nil
}

// startAdapter spawns the adapter subprocess and returns a Session shell.
func (m *Manager) startAdapter(ctx context.Context, spec adapter.Spec, lang string) (*Session, error) {
	argv, transport, err := spec.LaunchAdapter()
	if err != nil {
		return nil, err
	}

	switch transport {
	case adapter.TransportStdio:
		return m.startAdapterStdio(spec, argv, lang)
	case adapter.TransportTCPListen:
		return m.startAdapterTCP(spec, argv, lang)
	default:
		return nil, fmt.Errorf("unknown transport %v", transport)
	}
}

// startAdapterStdio spawns argv and bridges its stdin/stdout to the DAP client.
func (m *Manager) startAdapterStdio(_ adapter.Spec, argv []string, lang string) (*Session, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	go drainStderr(stderr)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start adapter %q: %w", strings.Join(argv, " "), err)
	}

	cli := dap.New(&stdioRWC{r: stdout, w: stdin, proc: cmd})
	return newSession(cli, cmd, lang), nil
}

// startAdapterTCP picks a free local port, substitutes {PORT} in argv, spawns
// the adapter, waits for the port to accept, then dials it.
func (m *Manager) startAdapterTCP(_ adapter.Spec, argvTpl []string, lang string) (*Session, error) {
	port, err := pickFreePort()
	if err != nil {
		return nil, fmt.Errorf("pick free port: %w", err)
	}
	argv := make([]string, len(argvTpl))
	for i, a := range argvTpl {
		argv[i] = strings.ReplaceAll(a, "{PORT}", fmt.Sprintf("%d", port))
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	go drainStderr(stderr)
	// dlv writes to stdout (its banner); drain it.
	cmd.Stdout = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start adapter %q: %w", strings.Join(argv, " "), err)
	}

	// Wait up to 5s for the adapter to start accepting connections.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	var conn net.Conn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if conn == nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("adapter %s did not accept TCP on %s within 5s: %v", argv[0], addr, err)
	}

	cli := dap.NewFromConn(conn)
	return newSession(cli, cmd, lang), nil
}

func newSession(cli *dap.Client, cmd *exec.Cmd, lang string) *Session {
	return &Session{
		ID:            newID(),
		Lang:          lang,
		state:         StateInitializing,
		bpsByID:       map[int]*BP{},
		bpNextLocalID: 1,
		cli:           cli,
		proc:          cmd,
		doneCh:        make(chan struct{}),
	}
}

func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func drainStderr(r io.Reader) {
	logPath := os.Getenv("SL_DBG_ADAPTER_LOG")
	if logPath == "" {
		logPath = "/tmp/sl-dbg-adapter.log"
	}
	f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if f == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	defer f.Close()
	_, _ = io.Copy(f, r)
}

func (m *Manager) register(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.ID] = s
	// Promote every new session to default. The most-recent session is
	// almost always the one the user wants their next command to target;
	// the old behavior (first-wins) silently sent commands to a stale
	// session when the user started a second one.
	m.defID = s.ID
}

// validateName ensures a user-supplied session name is unique and safe.
func (m *Manager) validateName(name string) error {
	for _, c := range name {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			return fmt.Errorf("session name must be [A-Za-z0-9_-]; got %q", name)
		}
	}
	if len(name) == 0 || len(name) > 64 {
		return fmt.Errorf("session name must be 1..64 chars")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, exists := m.sessions[name]; exists {
		return fmt.Errorf("session name %q already in use", name)
	}
	return nil
}

// startEventPump forwards DAP events into the session's internal handlers.
// Signals 'initialized' channel exactly once on the first InitializedEvent.
func (s *Session) startEventPump(initialized chan<- struct{}) {
	sub := s.cli.Subscribe()
	go func() {
		signaled := false
		for msg := range sub {
			s.handleEvent(msg)
			if !signaled {
				if _, ok := msg.(*godap.InitializedEvent); ok {
					signaled = true
					initialized <- struct{}{}
				}
			}
		}
		close(s.doneCh)
	}()
}

func (s *Session) handleEvent(msg godap.Message) {
	// Log every event into a bounded ring buffer for `sl-dbg events`.
	s.recordEvent(msg)
	switch ev := msg.(type) {
	case *godap.OutputEvent:
		s.recordOutput(ev.Body.Category, ev.Body.Output)
	case *godap.StoppedEvent:
		s.mu.Lock()
		s.state = StatePaused
		s.currentThread = ev.Body.ThreadId
		s.lastPauseReason = ev.Body.Reason
		s.lastHitBP = 0
		if len(ev.Body.HitBreakpointIds) > 0 {
			s.lastHitBP = ev.Body.HitBreakpointIds[0]
		}
		// location filled later via stack request on demand
		s.lastLocation = nil
		// If any "once" breakpoints were hit, mark them for removal.
		var toRemove []int
		if len(ev.Body.HitBreakpointIds) > 0 {
			for _, hid := range ev.Body.HitBreakpointIds {
				for _, b := range s.bpsByID {
					if b.Once && b.DAPID == hid {
						toRemove = append(toRemove, b.LocalID)
					}
				}
			}
		}
		s.mu.Unlock()
		if len(toRemove) > 0 {
			go s.clearOnceBPs(toRemove)
		}
		s.notifyWaiters(stopEvent{
			reason:   ev.Body.Reason,
			threadID: ev.Body.ThreadId,
			hitBP:    s.lastHitBP,
		})

	case *godap.ContinuedEvent:
		s.mu.Lock()
		s.state = StateRunning
		s.mu.Unlock()

	case *godap.ExitedEvent:
		ec := ev.Body.ExitCode
		s.mu.Lock()
		s.state = StateExited
		s.exitCode = &ec
		ecCopy := ec
		s.lastTerminal = &stopEvent{reason: "exited", exited: &ecCopy}
		s.mu.Unlock()
		s.notifyWaiters(stopEvent{reason: "exited", exited: &ec})

	case *godap.TerminatedEvent:
		s.mu.Lock()
		if s.state != StateExited {
			s.state = StateTerminated
		}
		// Don't overwrite an earlier ExitedEvent (which carries the code).
		if s.lastTerminal == nil {
			s.lastTerminal = &stopEvent{reason: "terminated", terminated: true}
		}
		s.mu.Unlock()
		s.notifyWaiters(stopEvent{reason: "terminated", terminated: true})
	}
}

// installWaiter registers a one-shot waiter for the next stop/exit/terminate event.
func (s *Session) installWaiter() chan stopEvent {
	ch := make(chan stopEvent, 1)
	// If the session has already terminated, satisfy this waiter immediately
	// so listeners that arrived late still get the correct answer.
	s.mu.Lock()
	if s.lastTerminal != nil {
		ev := *s.lastTerminal
		s.mu.Unlock()
		ch <- ev
		return ch
	}
	s.mu.Unlock()
	s.waitersMu.Lock()
	s.waiters = append(s.waiters, ch)
	s.waitersMu.Unlock()
	return ch
}

func (s *Session) notifyWaiters(ev stopEvent) {
	s.waitersMu.Lock()
	w := s.waiters
	s.waiters = nil
	s.waitersMu.Unlock()
	for _, ch := range w {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Close releases all resources.
func (s *Session) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if s.cli != nil {
		_ = s.cli.Disconnect(ctx, true)
		_ = s.cli.Close()
	}
	if s.proc != nil && s.proc.Process != nil {
		_ = s.proc.Process.Kill()
		_, _ = s.proc.Process.Wait()
	}
}

// State returns the current state.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// CurrentThread returns the last-known stopped thread, or 1.
func (s *Session) CurrentThread() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentThread == 0 {
		return 1
	}
	return s.currentThread
}

// Caps returns the adapter capabilities reported by initialize.
func (s *Session) Caps() godap.Capabilities { return s.caps }

// Client returns the DAP client.
func (s *Session) Client() *dap.Client { return s.cli }

// LastPause returns the last pause metadata.
func (s *Session) LastPause() (reason string, hitBP int, loc *proto.Loc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPauseReason, s.lastHitBP, s.lastLocation
}

// SetLastLocation updates the cached pause location.
func (s *Session) SetLastLocation(loc *proto.Loc) {
	s.mu.Lock()
	s.lastLocation = loc
	s.mu.Unlock()
}

// StopWaiter is an opaque handle to a single-shot waiter for the next stop /
// exit / terminate event on this session. Install one BEFORE issuing the DAP
// request that triggers the event, then call Wait to block on the result.
type StopWaiter struct {
	ch <-chan stopEvent
	s  *Session
}

// InstallWaiter registers a one-shot waiter for the next stop/exit/terminate
// event, so callers can install the waiter before issuing the triggering DAP
// request (avoiding the race where the event arrives between request-send and
// waiter-install).
func (s *Session) InstallWaiter() StopWaiter {
	return StopWaiter{ch: s.installWaiter(), s: s}
}

// Wait blocks on the waiter, applying the usual timeout / ctx semantics, and
// translates the result into proto.PauseInfo.
func (w StopWaiter) Wait(ctx context.Context, timeout time.Duration) (proto.PauseInfo, error) {
	var t <-chan time.Time
	if timeout > 0 {
		tm := time.NewTimer(timeout)
		defer tm.Stop()
		t = tm.C
	}
	select {
	case ev := <-w.ch:
		pi := proto.PauseInfo{Reason: ev.reason, Thread: ev.threadID, HitBP: ev.hitBP}
		if ev.exited != nil {
			pi.State = string(StateExited)
			pi.ExitCode = ev.exited
		} else if ev.terminated {
			pi.State = string(StateTerminated)
		} else {
			pi.State = string(StatePaused)
		}
		return pi, nil
	case <-t:
		return proto.PauseInfo{State: string(StateRunning), Reason: "timeout"}, nil
	case <-ctx.Done():
		return proto.PauseInfo{}, ctx.Err()
	}
}

// WaitForStop blocks until the session pauses again, exits, or timeout fires.
func (s *Session) WaitForStop(ctx context.Context, timeout time.Duration) (proto.PauseInfo, error) {
	ch := s.installWaiter()
	var t <-chan time.Time
	if timeout > 0 {
		tm := time.NewTimer(timeout)
		defer tm.Stop()
		t = tm.C
	}
	select {
	case ev := <-ch:
		pi := proto.PauseInfo{
			Reason: ev.reason,
			Thread: ev.threadID,
			HitBP:  ev.hitBP,
		}
		if ev.exited != nil {
			pi.State = string(StateExited)
			pi.ExitCode = ev.exited
		} else if ev.terminated {
			pi.State = string(StateTerminated)
		} else {
			pi.State = string(StatePaused)
		}
		return pi, nil
	case <-t:
		return proto.PauseInfo{State: string(StateRunning), Reason: "timeout"}, nil
	case <-ctx.Done():
		return proto.PauseInfo{}, ctx.Err()
	}
}

// AllocBP reserves the next local breakpoint id.
func (s *Session) AllocBP() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.bpNextLocalID
	s.bpNextLocalID++
	return id
}

// PutBP stores breakpoint metadata.
func (s *Session) PutBP(b *BP) {
	s.mu.Lock()
	s.bpsByID[b.LocalID] = b
	s.mu.Unlock()
}

// AllBPs returns a snapshot of all breakpoints.
func (s *Session) AllBPs() []*BP {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*BP, 0, len(s.bpsByID))
	for _, b := range s.bpsByID {
		out = append(out, b)
	}
	return out
}

// RemoveBP removes by local id and returns whether it was present.
func (s *Session) RemoveBP(id int) (*BP, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bpsByID[id]
	if ok {
		delete(s.bpsByID, id)
	}
	return b, ok
}

// BPsForFile returns the breakpoints registered for a given source file.
func (s *Session) BPsForFile(file string) []*BP {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*BP
	for _, b := range s.bpsByID {
		if b.File == file {
			out = append(out, b)
		}
	}
	return out
}

// ----- output / event ring buffers -----

const maxOutputEntries = 4096
const maxEventEntries = 1024

func (s *Session) recordOutput(category, output string) {
	if category == "" {
		category = "console"
	}
	s.bufMu.Lock()
	s.outputs = append(s.outputs, OutputEntry{TS: time.Now().UTC(), Category: category, Output: output})
	if n := len(s.outputs); n > maxOutputEntries {
		s.outputs = append([]OutputEntry(nil), s.outputs[n-maxOutputEntries:]...)
	}
	s.bufMu.Unlock()
}

func (s *Session) recordEvent(msg godap.Message) {
	ev, ok := msg.(godap.EventMessage)
	if !ok {
		return
	}
	body := map[string]interface{}{}
	switch e := msg.(type) {
	case *godap.StoppedEvent:
		body["reason"] = e.Body.Reason
		body["threadId"] = e.Body.ThreadId
		body["hitBreakpointIds"] = e.Body.HitBreakpointIds
	case *godap.ContinuedEvent:
		body["threadId"] = e.Body.ThreadId
	case *godap.ExitedEvent:
		body["exitCode"] = e.Body.ExitCode
	case *godap.OutputEvent:
		body["category"] = e.Body.Category
		body["output"] = e.Body.Output
	case *godap.ThreadEvent:
		body["reason"] = e.Body.Reason
		body["threadId"] = e.Body.ThreadId
	case *godap.BreakpointEvent:
		body["reason"] = e.Body.Reason
	}
	s.bufMu.Lock()
	s.evlog = append(s.evlog, LoggedEvent{TS: time.Now().UTC(), Type: ev.GetEvent().Event, Body: body})
	if n := len(s.evlog); n > maxEventEntries {
		s.evlog = append([]LoggedEvent(nil), s.evlog[n-maxEventEntries:]...)
	}
	s.bufMu.Unlock()
}

// Outputs returns captured output entries newer than `since` (zero-time = all).
// If tail > 0, only the last `tail` entries are returned.
func (s *Session) Outputs(since time.Time, tail int) []OutputEntry {
	s.bufMu.Lock()
	defer s.bufMu.Unlock()
	out := make([]OutputEntry, 0, len(s.outputs))
	for _, e := range s.outputs {
		if !since.IsZero() && !e.TS.After(since) {
			continue
		}
		out = append(out, e)
	}
	if tail > 0 && len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out
}

// RecentStderrTail returns up to maxBytes worth of the most recent stderr +
// stdout entries concatenated, newest last. Used to enrich LAUNCH_FAILED
// errors with a snippet of what the doomed program actually said.
func (s *Session) RecentStderrTail(maxBytes int) string {
	s.bufMu.Lock()
	defer s.bufMu.Unlock()
	if len(s.outputs) == 0 {
		return ""
	}
	var b []byte
	for i := len(s.outputs) - 1; i >= 0; i-- {
		e := s.outputs[i]
		if e.Category != "stderr" && e.Category != "console" && e.Category != "stdout" {
			continue
		}
		if len(b)+len(e.Output) > maxBytes && len(b) > 0 {
			break
		}
		b = append([]byte(e.Output), b...)
		if len(b) >= maxBytes {
			break
		}
	}
	return string(b)
}

// Events returns captured event log entries with the same filtering.
func (s *Session) Events(since time.Time, tail int) []LoggedEvent {
	s.bufMu.Lock()
	defer s.bufMu.Unlock()
	out := make([]LoggedEvent, 0, len(s.evlog))
	for _, e := range s.evlog {
		if !since.IsZero() && !e.TS.After(since) {
			continue
		}
		out = append(out, e)
	}
	if tail > 0 && len(out) > tail {
		out = out[len(out)-tail:]
	}
	return out
}

// ----- watch list -----

func (s *Session) AddWatch(expr string) *Watch {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.watchNextID++
	w := &Watch{ID: s.watchNextID, Expression: expr}
	s.watches = append(s.watches, w)
	return w
}

func (s *Session) RemoveWatch(id int) bool {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	for i, w := range s.watches {
		if w.ID == id {
			s.watches = append(s.watches[:i], s.watches[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Session) ClearWatches() {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.watches = nil
}

func (s *Session) Watches() []*Watch {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	out := make([]*Watch, len(s.watches))
	copy(out, s.watches)
	return out
}

// ----- function BP / exception filter helpers -----

func (s *Session) PutFuncBP(b FuncBP) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.funcBPs = append(s.funcBPs, b)
}

func (s *Session) FuncBPs() []FuncBP {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]FuncBP, len(s.funcBPs))
	copy(out, s.funcBPs)
	return out
}

func (s *Session) RemoveFuncBPByID(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, b := range s.funcBPs {
		if b.LocalID == id {
			s.funcBPs = append(s.funcBPs[:i], s.funcBPs[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Session) SetExcFilters(f []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.excFilters = append([]string(nil), f...)
}

func (s *Session) ExcFilters() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.excFilters...)
}

// AllBPsForFiles groups all source BPs by file.
func (s *Session) AllBPsForFiles() map[string][]*BP {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]*BP{}
	for _, b := range s.bpsByID {
		out[b.File] = append(out[b.File], b)
	}
	return out
}

// clearOnceBPs removes the named breakpoints from the session and re-syncs
// each affected source file with the adapter so the BP no longer fires.
func (s *Session) clearOnceBPs(ids []int) {
	files := map[string]struct{}{}
	for _, id := range ids {
		if b, ok := s.RemoveBP(id); ok {
			files[b.File] = struct{}{}
		}
	}
	for f := range files {
		bps := s.BPsForFile(f)
		dapBPs := make([]godap.SourceBreakpoint, 0, len(bps))
		for _, b := range bps {
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = s.cli.SetBreakpoints(ctx, godap.Source{Path: f}, dapBPs)
		cancel()
	}
}

// ----- stdio bridge -----

type stdioRWC struct {
	r    io.Reader
	w    io.WriteCloser
	proc *exec.Cmd
}

func (s *stdioRWC) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *stdioRWC) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *stdioRWC) Close() error {
	_ = s.w.Close()
	return nil
}
