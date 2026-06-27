package session

import (
	"testing"
)

func TestBPLifecycle(t *testing.T) {
	s := &Session{ID: "test", bpsByID: map[int]*BP{}}
	id1 := s.AllocBP()
	id2 := s.AllocBP()
	if id1 == id2 {
		t.Fatalf("AllocBP returned duplicate ids")
	}
	s.PutBP(&BP{LocalID: id1, File: "a.go", Line: 10})
	s.PutBP(&BP{LocalID: id2, File: "a.go", Line: 20, Once: true})

	if len(s.AllBPs()) != 2 {
		t.Errorf("expected 2 BPs, got %d", len(s.AllBPs()))
	}
	bs := s.BPsForFile("a.go")
	if len(bs) != 2 {
		t.Errorf("BPsForFile: got %d", len(bs))
	}
	if got, ok := s.RemoveBP(id1); !ok || got.LocalID != id1 {
		t.Errorf("RemoveBP failed: %+v ok=%v", got, ok)
	}
	if len(s.AllBPs()) != 1 {
		t.Errorf("after remove: got %d", len(s.AllBPs()))
	}
}

func TestWatchListCRUD(t *testing.T) {
	s := &Session{ID: "test"}
	w1 := s.AddWatch("x + 1")
	w2 := s.AddWatch("y.z")
	if w1.ID == w2.ID {
		t.Errorf("watch ids collide")
	}
	all := s.Watches()
	if len(all) != 2 {
		t.Fatalf("watches: %d", len(all))
	}
	if !s.RemoveWatch(w1.ID) {
		t.Errorf("RemoveWatch returned false")
	}
	if s.RemoveWatch(9999) {
		t.Errorf("RemoveWatch on missing id should be false")
	}
	if got := s.Watches(); len(got) != 1 || got[0].ID != w2.ID {
		t.Errorf("after remove: %+v", got)
	}
	s.ClearWatches()
	if got := s.Watches(); len(got) != 0 {
		t.Errorf("ClearWatches didn't clear: %+v", got)
	}
}

func TestFuncBPManagement(t *testing.T) {
	s := &Session{ID: "test"}
	s.PutFuncBP(FuncBP{LocalID: 1, Name: "main.compute"})
	s.PutFuncBP(FuncBP{LocalID: 2, Name: "other.run"})
	if len(s.FuncBPs()) != 2 {
		t.Errorf("funcBPs: %d", len(s.FuncBPs()))
	}
	if !s.RemoveFuncBPByID(1) {
		t.Errorf("remove by id returned false")
	}
	if s.RemoveFuncBPByID(999) {
		t.Errorf("remove unknown id should return false")
	}
	if got := s.FuncBPs(); len(got) != 1 || got[0].LocalID != 2 {
		t.Errorf("after remove: %+v", got)
	}
}

func TestExcFiltersRoundTrip(t *testing.T) {
	s := &Session{ID: "test"}
	if got := s.ExcFilters(); got != nil && len(got) != 0 {
		t.Errorf("initial filters not empty: %v", got)
	}
	s.SetExcFilters([]string{"uncaught", "raised"})
	got := s.ExcFilters()
	if len(got) != 2 || got[0] != "uncaught" || got[1] != "raised" {
		t.Errorf("filters: %v", got)
	}
}

// Issue #6: after the session exits, the ExitCode accessor must return the
// recorded code so handleState can suppress the stale pause location and
// surface the real exit reason instead.
func TestExitCodeAccessor(t *testing.T) {
	s := &Session{ID: "test"}
	if s.ExitCode() != nil {
		t.Errorf("ExitCode on fresh session: want nil, got %v", *s.ExitCode())
	}
	ec := 42
	s.exitCode = &ec
	got := s.ExitCode()
	if got == nil || *got != 42 {
		t.Errorf("ExitCode: want 42, got %v", got)
	}
	// Returned pointer must be a copy — mutating the caller's int must not
	// corrupt session state.
	*got = 99
	if *s.exitCode != 42 {
		t.Errorf("ExitCode aliased internal state; session exitCode now %d", *s.exitCode)
	}
}

// Issue #22: Reserve must enforce the configured cap and return ErrTooManySessions.
func TestManagerReserveCap(t *testing.T) {
	m := NewManager()
	// Unlimited by default.
	if err := m.Reserve(); err != nil {
		t.Errorf("default cap must be unlimited: %v", err)
	}
	m.SetMaxSessions(2)
	m.register(&Session{ID: "a"})
	if err := m.Reserve(); err != nil {
		t.Errorf("Reserve at 1/2 must succeed: %v", err)
	}
	m.register(&Session{ID: "b"})
	if err := m.Reserve(); err == nil {
		t.Errorf("Reserve at cap must fail")
	}
	if m.MaxSessions() != 2 {
		t.Errorf("MaxSessions accessor wrong: %d", m.MaxSessions())
	}
}

// TestLastTerminalAfterPause verifies that once a session has terminated,
// LastTerminal() reflects the terminal reason/exitCode/signal — not the
// stale last-pause reason. Regression test for issue #59 ("listen after
// SIGKILL returns stale reason:entry").
func TestLastTerminalAfterPause(t *testing.T) {
	s := &Session{ID: "t", state: StatePaused, lastPauseReason: "entry"}
	ec := 247
	s.exitCode = &ec
	s.state = StateExited
	s.lastTerminal = &stopEvent{reason: "signal", exited: &ec, terminated: true, signal: "killed"}

	reason, gotEC, sig, ok := s.LastTerminal()
	if !ok {
		t.Fatalf("LastTerminal ok=false")
	}
	if reason != "signal" {
		t.Errorf("reason=%q want signal", reason)
	}
	if sig != "killed" {
		t.Errorf("signal=%q want killed", sig)
	}
	if gotEC == nil || *gotEC != 247 {
		t.Errorf("exitCode=%v want 247", gotEC)
	}
	// Mutating returned exit code must not change the cached value.
	*gotEC = 0
	if *s.exitCode != 247 {
		t.Errorf("LastTerminal leaked internal exitCode pointer")
	}

	// LastPause still returns the (now stale) entry reason — that's
	// expected; callers in terminal state must prefer LastTerminal.
	if r, _, _ := s.LastPause(); r != "entry" {
		t.Errorf("LastPause reason=%q want entry", r)
	}
}

// TestInstallWaiterTerminalShortCircuit verifies a late waiter on an
// already-terminated session receives the terminal stopEvent immediately
// (carrying signal info), not a stale pause event.
func TestInstallWaiterTerminalShortCircuit(t *testing.T) {
	s := &Session{ID: "t", state: StateExited, lastPauseReason: "entry"}
	ec := 137
	s.lastTerminal = &stopEvent{reason: "signal", exited: &ec, terminated: true, signal: "killed"}

	ch := s.installWaiter()
	select {
	case ev := <-ch:
		if ev.reason != "signal" || ev.signal != "killed" {
			t.Errorf("waiter got reason=%q signal=%q; want signal/killed", ev.reason, ev.signal)
		}
		if ev.exited == nil || *ev.exited != 137 {
			t.Errorf("waiter exited=%v want 137", ev.exited)
		}
	default:
		t.Fatalf("waiter did not fire immediately for terminated session")
	}
}
