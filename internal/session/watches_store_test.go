package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatchPersistRoundTrip(t *testing.T) {
	// Redirect XDG_STATE_HOME to a temp dir so we don't pollute real state.
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp) // belt+suspenders

	s := &Session{
		ID:      "a",
		Lang:    "java",
		Program: "/path/to/App.jar",
		Cwd:     "/path/to",
		bpsByID: map[int]*BP{},
	}
	_ = s.AddWatch("x + 1")
	_ = s.AddWatch("self.name")

	if err := s.SaveWatches(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Fresh session, same identity tuple — must reload.
	s2 := &Session{
		ID:      "b",
		Lang:    "java",
		Program: "/path/to/App.jar",
		Cwd:     "/path/to",
		bpsByID: map[int]*BP{},
	}
	if err := s2.LoadWatches(); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := s2.Watches()
	if len(got) != 2 {
		t.Fatalf("want 2 watches, got %d", len(got))
	}
	if got[0].Expression != "x + 1" || got[1].Expression != "self.name" {
		t.Errorf("wrong exprs: %+v", got)
	}

	// Different program → no reload.
	s3 := &Session{Lang: "java", Program: "/other.jar", Cwd: "/path/to", bpsByID: map[int]*BP{}}
	_ = s3.LoadWatches()
	if len(s3.Watches()) != 0 {
		t.Errorf("different program must not reload watches: %v", s3.Watches())
	}
}

func TestWatchPersistEmptyClears(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	s := &Session{Lang: "go", Program: "/x", Cwd: "/", bpsByID: map[int]*BP{}}
	_ = s.AddWatch("y")
	_ = s.SaveWatches()

	// Now clear and re-save — file should be removed so next session starts clean.
	s.ClearWatches()
	_ = s.SaveWatches()

	path := watchFilePath(watchKey(s.Lang, s.Program, s.Cwd))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected cache file removed, got err=%v", err)
	}
}

func TestWatchPersistNoProgram(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	// Attach-only sessions have no Program → must no-op silently.
	s := &Session{Lang: "java", Cwd: "/x", bpsByID: map[int]*BP{}}
	_ = s.AddWatch("z")
	if err := s.SaveWatches(); err != nil {
		t.Errorf("save on no-program session must be a no-op, got %v", err)
	}
	// No file should have been written.
	files, _ := filepath.Glob(filepath.Join(tmp, "sl-dbg", "watches-*.json"))
	if len(files) != 0 {
		t.Errorf("expected no watch files written, got %v", files)
	}
}
