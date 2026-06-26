package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeCaller struct {
	lastCmd  string
	lastSess string
	lastArgs interface{}
	reply    json.RawMessage
	err      error
}

func (f *fakeCaller) Call(cmd, sess string, args interface{}) (json.RawMessage, error) {
	f.lastCmd, f.lastSess, f.lastArgs = cmd, sess, args
	if f.err != nil {
		return nil, f.err
	}
	return f.reply, nil
}

func runOnce(t *testing.T, caller DaemonCaller, lines ...string) []string {
	t.Helper()
	in := bytes.NewBufferString(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	s := NewServer(in, &out, caller)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.Split(strings.TrimSpace(out.String()), "\n")
}

func TestInitializeAndToolsList(t *testing.T) {
	c := &fakeCaller{}
	outs := runOnce(t, c,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	if len(outs) != 2 {
		t.Fatalf("want 2 responses, got %d: %v", len(outs), outs)
	}
	if !strings.Contains(outs[0], `"protocolVersion":"2024-11-05"`) {
		t.Errorf("initialize missing protocolVersion: %s", outs[0])
	}
	if !strings.Contains(outs[1], `"debug_start"`) {
		t.Errorf("tools/list missing debug_start: %s", outs[1])
	}
}

func TestToolCallTranslates(t *testing.T) {
	c := &fakeCaller{reply: json.RawMessage(`{"ok":1}`)}
	outs := runOnce(t, c,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"debug_break","arguments":{"session":"s1","location":"foo.py:10","once":true}}}`,
	)
	if c.lastCmd != "break" {
		t.Errorf("cmd: got %q want break", c.lastCmd)
	}
	if c.lastSess != "s1" {
		t.Errorf("sess: got %q want s1", c.lastSess)
	}
	b, _ := json.Marshal(c.lastArgs)
	if !strings.Contains(string(b), `"location":"foo.py:10"`) || !strings.Contains(string(b), `"once":true`) {
		t.Errorf("args wrong: %s", b)
	}
	if !strings.Contains(outs[0], `\"ok\":1`) {
		t.Errorf("response missing data: %s", outs[0])
	}
}

func TestUnknownTool(t *testing.T) {
	outs := runOnce(t, &fakeCaller{},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
	)
	if !strings.Contains(outs[0], `"code":-32601`) {
		t.Errorf("expected method-not-found: %s", outs[0])
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	outs := runOnce(t, &fakeCaller{},
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	if len(outs) != 1 || outs[0] != "" {
		t.Errorf("notification should produce no output, got %v", outs)
	}
}
