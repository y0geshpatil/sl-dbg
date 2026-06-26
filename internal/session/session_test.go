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
