package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/yogeshpatil/sl-dbg/internal/proto"
)

func TestRequestRoundTrip(t *testing.T) {
	args := proto.BreakArgs{
		Location:  "app.py:42",
		Condition: "x > 0",
		Hit:       3,
		LogMsg:    "hit x={x}",
		Once:      true,
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	req := proto.Request{ID: 1, Cmd: proto.CmdBreak, Sess: "abc", Args: raw}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got proto.Request
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Cmd != proto.CmdBreak || got.Sess != "abc" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	var gotArgs proto.BreakArgs
	if err := json.Unmarshal(got.Args, &gotArgs); err != nil {
		t.Fatal(err)
	}
	if gotArgs != args {
		t.Errorf("args round-trip mismatch:\nwant=%+v\n got=%+v", args, gotArgs)
	}
}

// All command constants must be lower-kebab. Daemon dispatch is exact-string;
// clients hard-code these. Renaming any breaks the wire.
func TestCommandConstantsAreStable(t *testing.T) {
	cases := map[string]string{
		"ping":      proto.CmdPing,
		"start":     proto.CmdStart,
		"attach":    proto.CmdAttach,
		"break":     proto.CmdBreak,
		"break-fn":  proto.CmdBreakFn,
		"break-ex":  proto.CmdBreakEx,
		"breaks":    proto.CmdBreaks,
		"unbreak":   proto.CmdUnbreak,
		"continue":  proto.CmdContinue,
		"step":      proto.CmdStep,
		"next":      proto.CmdNext,
		"finish":    proto.CmdFinish,
		"pause":     proto.CmdPause,
		"stack":     proto.CmdStack,
		"threads":   proto.CmdThreads,
		"locals":    proto.CmdLocals,
		"globals":   proto.CmdGlobals,
		"fields":    proto.CmdFields,
		"eval":      proto.CmdEval,
		"set":       proto.CmdSet,
		"watch":     proto.CmdWatch,
		"snapshot":  proto.CmdSnapshot,
		"source":    proto.CmdSource,
		"output":    proto.CmdOutput,
		"events":    proto.CmdEvents,
		"listen":    proto.CmdListen,
		"restart":   proto.CmdRestart,
		"until":     proto.CmdUntil,
		"sessions":  proto.CmdSessions,
		"use":       proto.CmdUse,
		"stop":      proto.CmdStop,
		"state":     proto.CmdState,
		"adapters":  proto.CmdAdapters,
		"shutdown":  proto.CmdShutdown,
	}
	for literal, constant := range cases {
		if literal != constant {
			t.Errorf("command drift: literal=%q != constant=%q", literal, constant)
		}
	}
}

func TestWatchArgsActionsAreKnown(t *testing.T) {
	for _, action := range []string{"", "list", "add", "remove"} {
		w := proto.WatchArgs{Action: action, Expression: "x+1"}
		b, _ := json.Marshal(w)
		var got proto.WatchArgs
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", action, err)
		}
		if got.Action != action {
			t.Errorf("action mismatch: got=%q want=%q", got.Action, action)
		}
	}
}
