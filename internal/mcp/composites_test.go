package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// scriptedCaller returns different responses per (cmd, sess) pair.
type scriptedCaller struct {
	t        *testing.T
	calls    []scriptCall
	replies  map[string]json.RawMessage // key = cmd
	defaultR json.RawMessage
}

type scriptCall struct {
	cmd  string
	sess string
	args interface{}
}

func (s *scriptedCaller) Call(cmd, sess string, args interface{}) (json.RawMessage, error) {
	s.calls = append(s.calls, scriptCall{cmd, sess, args})
	if r, ok := s.replies[cmd]; ok {
		return r, nil
	}
	if s.defaultR != nil {
		return s.defaultR, nil
	}
	return json.RawMessage(`{}`), nil
}

func TestReadOnlyHidesMutatingTools(t *testing.T) {
	c := &scriptedCaller{}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	s := NewServerWithOptions(in, &out, c, Options{ReadOnly: true})
	_ = s.Run(context.Background())
	body := out.String()
	for _, banned := range []string{`"debug_start"`, `"debug_continue"`, `"debug_break"`, `"debug_stop"`, `"debug_eval"`} {
		if strings.Contains(body, banned) {
			t.Errorf("read-only listing leaked mutating tool: %s", banned)
		}
	}
	for _, allowed := range []string{`"debug_state"`, `"debug_stack"`, `"debug_locals"`, `"debug_sessions"`} {
		if !strings.Contains(body, allowed) {
			t.Errorf("read-only listing missing inspection tool: %s", allowed)
		}
	}
}

func TestReadOnlyRejectsMutatingCall(t *testing.T) {
	c := &scriptedCaller{}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_continue","arguments":{}}}` + "\n")
	var out bytes.Buffer
	s := NewServerWithOptions(in, &out, c, Options{ReadOnly: true})
	_ = s.Run(context.Background())
	if !strings.Contains(out.String(), `"code":-32601`) {
		t.Errorf("read-only should reject debug_continue: %s", out.String())
	}
}

func TestDefaultSessionResolvesWhenSingle(t *testing.T) {
	c := &scriptedCaller{
		replies: map[string]json.RawMessage{
			"sessions": json.RawMessage(`{"sessions":[{"id":"only1"}]}`),
			"state":    json.RawMessage(`{"state":"paused"}`),
		},
	}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_state","arguments":{}}}` + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, c)
	_ = s.Run(context.Background())
	// the state call must have used "only1" as session
	var sawState bool
	for _, call := range c.calls {
		if call.cmd == "state" {
			sawState = true
			if call.sess != "only1" {
				t.Errorf("default-session: state called with sess=%q, want only1", call.sess)
			}
		}
	}
	if !sawState {
		t.Errorf("expected a state call; calls=%+v", c.calls)
	}
}

func TestDefaultSessionStaysEmptyWhenAmbiguous(t *testing.T) {
	c := &scriptedCaller{
		replies: map[string]json.RawMessage{
			"sessions": json.RawMessage(`{"sessions":[{"id":"a"},{"id":"b"}]}`),
		},
	}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_state","arguments":{}}}` + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, c)
	_ = s.Run(context.Background())
	for _, call := range c.calls {
		if call.cmd == "state" && call.sess != "" {
			t.Errorf("ambiguous: state should be called with empty sess, got %q", call.sess)
		}
	}
}

func TestRunUntilBreakComposite(t *testing.T) {
	c := &scriptedCaller{
		replies: map[string]json.RawMessage{
			"sessions": json.RawMessage(`{"sessions":[{"id":"s1"}]}`),
			"break":    json.RawMessage(`{"id":1,"line":42,"verified":true}`),
			"continue": json.RawMessage(`{"state":"paused","reason":"breakpoint"}`),
		},
	}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_run_until_break","arguments":{"location":"foo.py:42"}}}` + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, c)
	_ = s.Run(context.Background())
	cmds := []string{}
	for _, call := range c.calls {
		cmds = append(cmds, call.cmd)
	}
	wantSeq := []string{"sessions", "break", "continue"}
	if strings.Join(cmds, ",") != strings.Join(wantSeq, ",") {
		t.Errorf("call sequence: got %v want %v", cmds, wantSeq)
	}
	if !strings.Contains(out.String(), `breakpoint`) || !strings.Contains(out.String(), `paused`) {
		t.Errorf("composite output missing parts: %s", out.String())
	}
}

func TestLangEnumInSchema(t *testing.T) {
	c := &scriptedCaller{}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, c)
	_ = s.Run(context.Background())
	if !strings.Contains(out.String(), `"enum":["python","go","java","node","cpp","dotnet","rust"]`) {
		t.Errorf("expected lang enum in schema; got: %s", out.String()[:600])
	}
}

// Issue #5: when the bp doesn't verify, inspect_at must not resume — otherwise
// the program runs to completion and every eval comes back "no frames".
func TestInspectAtShortCircuitsOnUnverifiedBP(t *testing.T) {
	c := &scriptedCaller{
		replies: map[string]json.RawMessage{
			"sessions": json.RawMessage(`{"sessions":[{"id":"s1"}]}`),
			"break":    json.RawMessage(`{"id":1,"line":71,"verified":false,"reason":"unverified: line has no executable code"}`),
		},
	}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_inspect_at","arguments":{"location":"Foo.java:71","expressions":["i"]}}}` + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, c)
	_ = s.Run(context.Background())
	for _, call := range c.calls {
		if call.cmd == "continue" || call.cmd == "eval" || call.cmd == "locals" {
			t.Fatalf("inspect_at should not have called %s after unverified bp; calls=%+v", call.cmd, c.calls)
		}
	}
	if !strings.Contains(out.String(), "BREAKPOINT_UNVERIFIED") {
		t.Errorf("expected BREAKPOINT_UNVERIFIED in response: %s", out.String())
	}
}

// Issue #5 follow-up: if continue completes by program-exit rather than
// hitting our bp, skip locals/eval and surface a typed error.
func TestInspectAtShortCircuitsOnExit(t *testing.T) {
	c := &scriptedCaller{
		replies: map[string]json.RawMessage{
			"sessions": json.RawMessage(`{"sessions":[{"id":"s1"}]}`),
			"break":    json.RawMessage(`{"id":1,"line":71,"verified":true}`),
			"continue": json.RawMessage(`{"state":"exited","reason":"exited","exitCode":0}`),
		},
	}
	in := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_inspect_at","arguments":{"location":"Foo.java:71","expressions":["i"]}}}` + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, c)
	_ = s.Run(context.Background())
	for _, call := range c.calls {
		if call.cmd == "eval" || call.cmd == "locals" {
			t.Fatalf("inspect_at should not have called %s after non-paused stop; calls=%+v", call.cmd, c.calls)
		}
	}
	if !strings.Contains(out.String(), "INSPECT_NOT_PAUSED") {
		t.Errorf("expected INSPECT_NOT_PAUSED in response: %s", out.String())
	}
}
