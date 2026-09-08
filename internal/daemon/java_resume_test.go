package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	godap "github.com/google/go-dap"
	"github.com/y0geshpatil/sl-dbg/internal/dap"
	"github.com/y0geshpatil/sl-dbg/internal/proto"
	"github.com/y0geshpatil/sl-dbg/internal/session"
)

func fakeSession(t *testing.T, lang string, pending bool, respond func(godap.RequestMessage) []godap.Message) (*Server, *session.Session) {
	t.Helper()
	clientConn, adapterConn := net.Pipe()
	client := dap.New(clientConn)
	client.Caps.SupportsFunctionBreakpoints = true
	sess := session.NewForTest(client, lang, pending)
	server := &Server{mgr: session.NewManager(), audit: &AuditLogger{}}
	server.mgr.RegisterForTest(sess)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(adapterConn)
		for {
			message, err := godap.ReadProtocolMessage(reader)
			if err != nil {
				return
			}
			request := message.(godap.RequestMessage)
			for _, response := range respond(request) {
				if err := godap.WriteProtocolMessage(adapterConn, response); err != nil {
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		// Close the peer first so event dispatch has finished before Close
		// closes subscriber channels.
		adapterConn.Close()
		<-done
		client.Close()
	})
	return server, sess
}

func responseTo(request godap.RequestMessage) godap.Response {
	return godap.Response{
		ProtocolMessage: godap.ProtocolMessage{Type: "response"},
		RequestSeq:      request.GetSeq(),
		Command:         request.GetRequest().Command,
		Success:         true,
	}
}

func TestJavaAttachContinueDoesNotResumeTwice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lang    string
		pending bool
		want    []string
	}{
		{"java-attach", "java", true, []string{"configurationDone", "stackTrace", "setBreakpoints", "continue", "stackTrace", "setBreakpoints"}},
		{"java-launch", "java", false, []string{"continue", "stackTrace", "setBreakpoints", "continue", "stackTrace", "setBreakpoints"}},
		{"python-attach", "python", true, []string{"configurationDone", "continue", "stackTrace", "setBreakpoints", "continue", "stackTrace", "setBreakpoints"}},
		{"go-attach", "go", true, []string{"configurationDone", "continue", "stackTrace", "setBreakpoints", "continue", "stackTrace", "setBreakpoints"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var commands []string
			server, sess := fakeSession(t, tc.lang, tc.pending, func(request godap.RequestMessage) []godap.Message {
				command := request.GetRequest().Command
				mu.Lock()
				commands = append(commands, command)
				mu.Unlock()
				base := responseTo(request)
				stop := &godap.StoppedEvent{
					Event: godap.Event{ProtocolMessage: godap.ProtocolMessage{Type: "event"}, Event: "stopped"},
					Body:  godap.StoppedEventBody{Reason: "breakpoint", ThreadId: 1},
				}
				switch command {
				case "configurationDone":
					response := &godap.ConfigurationDoneResponse{Response: base}
					if tc.lang == "java" {
						// Deliver the stop before the response to exercise the
						// waiter's registration as well as the double-resume bug.
						return []godap.Message{stop, response}
					}
					return []godap.Message{response}
				case "continue":
					return []godap.Message{stop, &godap.ContinueResponse{Response: base}}
				case "stackTrace":
					return []godap.Message{&godap.StackTraceResponse{Response: base, Body: godap.StackTraceResponseBody{
						StackFrames: []godap.StackFrame{{Id: 1, Name: "Buggy.process", Line: 24, Source: &godap.Source{Path: "Buggy.java"}}},
					}}}
				case "setBreakpoints":
					return []godap.Message{&godap.SetBreakpointsResponse{Response: base}}
				default:
					t.Errorf("unexpected request: %s", command)
					return nil
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for i := 0; i < 2; i++ {
				result := server.handleExec(ctx, proto.Request{Sess: sess.ID, Args: json.RawMessage(`{"timeoutSec":0.1}`)}, execContinue)
				if !result.OK {
					t.Fatalf("continue %d failed: %+v", i, result)
				}
				var pause proto.PauseInfo
				if err := json.Unmarshal(result.Data, &pause); err != nil {
					t.Fatal(err)
				}
				if pause.Reason != "breakpoint" || pause.Location == nil || pause.Location.Line != 24 {
					t.Fatalf("continue %d missed stop: %+v", i, pause)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(commands, tc.want) {
				t.Fatalf("DAP requests: %v; want %v", commands, tc.want)
			}
		})
	}
}
