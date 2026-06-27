package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/y0geshpatil/sl-dbg/internal/proto"
	"github.com/y0geshpatil/sl-dbg/internal/session"
)

// Issue #56 (security): CLI --read-only must refuse `eval`, matching the
// MCP transport which hides debug_eval. The DAP "watch" context hint does
// not sandbox the adapter — expression-form eval in every supported
// language can spawn processes, read/write files, etc.
func TestEvalRefusedOnReadOnlySession(t *testing.T) {
	s := &Server{mgr: session.NewManager(), audit: &AuditLogger{}}
	sess := &session.Session{ID: "ro1", ReadOnly: true}
	s.mgr.RegisterForTest(sess)

	args, err := json.Marshal(proto.EvalArgs{Expression: "__import__('os').system('touch /tmp/PWN')"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := s.handleEval(context.Background(), proto.Request{
		Cmd:  proto.CmdEval,
		Sess: "ro1",
		Args: args,
	})
	if resp.OK {
		t.Fatalf("read-only eval must fail, got ok response: %+v", resp)
	}
	if resp.Error == nil || resp.Error.Code != "READ_ONLY_MODE" {
		t.Fatalf("expected READ_ONLY_MODE, got error=%+v", resp.Error)
	}
}

// Mirror: a writable session must NOT trip the read-only refusal at the
// handler entry. We can't easily run a real adapter here, so only assert
// that the empty-expression USAGE_ERROR fires (which we hit before any
// session lookup) — this confirms the read-only short-circuit was not
// the reason for any failure on a writable session.
func TestEvalNotRefusedOnWritableSession(t *testing.T) {
	s := &Server{mgr: session.NewManager(), audit: &AuditLogger{}}
	sess := &session.Session{ID: "rw1", ReadOnly: false}
	s.mgr.RegisterForTest(sess)

	args, err := json.Marshal(proto.EvalArgs{Expression: "   "}) // whitespace -> USAGE_ERROR
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp := s.handleEval(context.Background(), proto.Request{
		Cmd:  proto.CmdEval,
		Sess: "rw1",
		Args: args,
	})
	if resp.Error == nil || resp.Error.Code != "USAGE_ERROR" {
		t.Fatalf("expected USAGE_ERROR for empty expr, got %+v", resp.Error)
	}
}
