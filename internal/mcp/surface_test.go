package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func runWithOpts(t *testing.T, caller DaemonCaller, opts Options, lines ...string) []string {
	t.Helper()
	in := bytes.NewBufferString(strings.Join(lines, "\n") + "\n")
	var out bytes.Buffer
	s := NewServerWithOptions(in, &out, caller, opts)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	return strings.Split(strings.TrimSpace(out.String()), "\n")
}

func TestResourcesListAndRead(t *testing.T) {
	c := &fakeCaller{reply: json.RawMessage(`{"sessions":[]}`)}
	outs := runOnce(t, c,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"sl-dbg://sessions"}}`,
	)
	if len(outs) != 2 {
		t.Fatalf("want 2 responses, got %d", len(outs))
	}
	if !strings.Contains(outs[0], `"sl-dbg://sessions"`) {
		t.Errorf("resources/list missing sessions uri: %s", outs[0])
	}
	if !strings.Contains(outs[1], `\"sessions\":[]`) {
		t.Errorf("resources/read did not return daemon payload: %s", outs[1])
	}
	if c.lastCmd != "sessions" {
		t.Errorf("expected sessions call, got %q", c.lastCmd)
	}
}

func TestPromptsListAndGet(t *testing.T) {
	c := &fakeCaller{}
	outs := runOnce(t, c,
		`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"diagnose_loop_bug","arguments":{"file":"a.py","line":"9"}}}`,
	)
	if !strings.Contains(outs[0], `"diagnose_loop_bug"`) {
		t.Errorf("prompts/list missing diagnose_loop_bug: %s", outs[0])
	}
	if !strings.Contains(outs[1], `a.py:9`) {
		t.Errorf("prompts/get did not interpolate args: %s", outs[1])
	}
}

func TestAllowlistDeniesProgram(t *testing.T) {
	c := &fakeCaller{reply: json.RawMessage(`{}`)}
	outs := runWithOpts(t, c, Options{DenyProgram: []string{"rm"}},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_start","arguments":{"lang":"go","program":"/bin/rm"}}}`,
	)
	if !strings.Contains(outs[0], "POLICY_DENIED") {
		t.Errorf("expected POLICY_DENIED, got: %s", outs[0])
	}
	if c.lastCmd == "start" {
		t.Errorf("daemon should not have been called when denied")
	}
}

func TestAllowlistAllowsWithinCwd(t *testing.T) {
	c := &fakeCaller{reply: json.RawMessage(`{"id":"abc","state":"running"}`)}
	outs := runWithOpts(t, c, Options{AllowCwd: []string{"/tmp"}},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_start","arguments":{"lang":"go","program":"/tmp/proj/main.go"}}}`,
	)
	if strings.Contains(outs[0], "POLICY_DENIED") {
		t.Errorf("expected allow, got denial: %s", outs[0])
	}
	if c.lastCmd != "start" {
		t.Errorf("expected start to reach daemon, got %q", c.lastCmd)
	}
}

func TestAllowlistDeniesOutsideCwd(t *testing.T) {
	c := &fakeCaller{}
	outs := runWithOpts(t, c, Options{AllowCwd: []string{"/tmp"}},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"debug_start","arguments":{"lang":"go","program":"/etc/passwd"}}}`,
	)
	if !strings.Contains(outs[0], "POLICY_DENIED") {
		t.Errorf("expected POLICY_DENIED, got: %s", outs[0])
	}
}
