// Package dap is a thin client wrapper over github.com/google/go-dap.
//
// One Client wraps one DAP adapter subprocess (e.g., `python -m debugpy.adapter`)
// reading & writing DAP messages over its stdio. Requests are correlated to
// responses via DAP's "seq" field; events are dispatched to subscribers.
package dap

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	godap "github.com/google/go-dap"
)

// Client speaks DAP to a single adapter.
type Client struct {
	w      io.Writer
	rw     io.ReadWriteCloser // for stdio adapters; or net.Conn
	closer io.Closer
	r      *bufio.Reader

	seq atomic.Int64

	mu        sync.Mutex
	pending   map[int]chan godap.Message // seq -> waiter
	subs      []chan godap.Message       // event subscribers (all events)
	closed    bool

	Caps godap.Capabilities

	readerDone chan struct{}
	readerErr  error
}

// New creates a Client bound to a duplex stream that speaks DAP (Content-Length framed JSON).
// rwc is typically a wrapper around an adapter subprocess's stdin/stdout, or a TCP conn.
func New(rwc io.ReadWriteCloser) *Client {
	c := &Client{
		w:          rwc,
		rw:         rwc,
		closer:     rwc,
		r:          bufio.NewReader(rwc),
		pending:    make(map[int]chan godap.Message),
		readerDone: make(chan struct{}),
	}
	go c.readLoop()
	return c
}

// NewFromConn creates a Client from a net.Conn (e.g., dlv dap on TCP).
func NewFromConn(conn net.Conn) *Client {
	return New(conn)
}

// Close terminates the client. The underlying stream is closed; pending waiters are released.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	err := c.closer.Close()
	for _, ch := range c.pending {
		close(ch)
	}
	c.pending = nil
	for _, s := range c.subs {
		close(s)
	}
	c.subs = nil
	c.mu.Unlock()
	return err
}

func (c *Client) nextSeq() int {
	return int(c.seq.Add(1))
}

// Subscribe returns a channel that receives every DAP event from the adapter.
// The channel is buffered (capacity 64). Caller must drain or the client will
// drop events for the slow subscriber.
func (c *Client) Subscribe() <-chan godap.Message {
	ch := make(chan godap.Message, 64)
	c.mu.Lock()
	c.subs = append(c.subs, ch)
	c.mu.Unlock()
	return ch
}

// readLoop reads DAP messages forever, routing responses to pending waiters
// and events to subscribers.
func (c *Client) readLoop() {
	defer close(c.readerDone)
	for {
		msg, err := godap.ReadProtocolMessage(c.r)
		if err != nil {
			// Tolerate unknown/custom DAP events (e.g. debugpy's "debugpySockets")
			// by skipping them and continuing to read.
			var fe *godap.DecodeProtocolMessageFieldError
			if errors.As(err, &fe) {
				continue
			}
			c.mu.Lock()
			c.readerErr = err
			for seq, ch := range c.pending {
				close(ch)
				delete(c.pending, seq)
			}
			c.mu.Unlock()
			return
		}
		c.dispatch(msg)
	}
}

func (c *Client) dispatch(msg godap.Message) {
	// Responses correlate by RequestSeq.
	if resp, ok := msg.(godap.ResponseMessage); ok {
		baseSeq := resp.GetResponse().RequestSeq
		c.mu.Lock()
		if ch, ok := c.pending[baseSeq]; ok {
			delete(c.pending, baseSeq)
			c.mu.Unlock()
			ch <- msg
			close(ch)
			return
		}
		c.mu.Unlock()
	}
	// Events go to all subscribers (non-blocking; drop if full).
	c.mu.Lock()
	subs := append([]chan godap.Message(nil), c.subs...)
	c.mu.Unlock()
	for _, s := range subs {
		select {
		case s <- msg:
		default:
		}
	}
}

// send writes a DAP request and returns a channel that will receive the response.
func (c *Client) send(req godap.RequestMessage) (chan godap.Message, int, error) {
	seq := c.nextSeq()
	req.GetRequest().Seq = seq
	req.GetRequest().Type = "request"

	ch := make(chan godap.Message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, 0, errors.New("dap client closed")
	}
	c.pending[seq] = ch
	c.mu.Unlock()

	if err := godap.WriteProtocolMessage(c.w, req); err != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, 0, err
	}
	return ch, seq, nil
}

// Call sends a request and waits up to timeout for the response.
func (c *Client) Call(ctx context.Context, req godap.RequestMessage, timeout time.Duration) (godap.Message, error) {
	ch, seq, err := c.send(req)
	if err != nil {
		return nil, err
	}
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("adapter disconnected (seq=%d)", seq)
		}
		if resp, ok := msg.(godap.ResponseMessage); ok && !resp.GetResponse().Success {
			return msg, fmt.Errorf("dap error: %s", resp.GetResponse().Message)
		}
		return msg, nil
	case <-timer:
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response to seq=%d", seq)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// --- Convenience wrappers for common DAP requests ---

func (c *Client) Initialize(ctx context.Context, adapterID string) (*godap.InitializeResponse, error) {
	req := &godap.InitializeRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "initialize"},
		Arguments: godap.InitializeRequestArguments{
			ClientID:                     "sl-dbg",
			ClientName:                   "sl-dbg",
			AdapterID:                    adapterID,
			LinesStartAt1:                true,
			ColumnsStartAt1:              true,
			PathFormat:                   "path",
			SupportsVariableType:         true,
			SupportsRunInTerminalRequest: false,
		},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	resp, ok := m.(*godap.InitializeResponse)
	if !ok {
		return nil, fmt.Errorf("unexpected response %T", m)
	}
	c.Caps = resp.Body
	return resp, nil
}

// LaunchAsync sends the launch request and returns a channel that will receive
// the eventual response. Useful when the adapter delays the launch response
// until after configurationDone (e.g. debugpy).
func (c *Client) LaunchAsync(args map[string]interface{}) (<-chan godap.Message, error) {
	raw, err := jsonMarshal(args)
	if err != nil {
		return nil, err
	}
	req := &godap.LaunchRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "launch"},
		Arguments: raw,
	}
	ch, _, err := c.send(req)
	return ch, err
}

// AttachAsync is the attach equivalent of LaunchAsync.
func (c *Client) AttachAsync(args map[string]interface{}) (<-chan godap.Message, error) {
	raw, err := jsonMarshal(args)
	if err != nil {
		return nil, err
	}
	req := &godap.AttachRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "attach"},
		Arguments: raw,
	}
	ch, _, err := c.send(req)
	return ch, err
}

func (c *Client) Launch(ctx context.Context, args map[string]interface{}) error {
	raw, err := jsonMarshal(args)
	if err != nil {
		return err
	}
	req := &godap.LaunchRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "launch"},
		Arguments: raw,
	}
	_, err = c.Call(ctx, req, 60*time.Second)
	return err
}

func (c *Client) Attach(ctx context.Context, args map[string]interface{}) error {
	raw, err := jsonMarshal(args)
	if err != nil {
		return err
	}
	req := &godap.AttachRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "attach"},
		Arguments: raw,
	}
	_, err = c.Call(ctx, req, 30*time.Second)
	return err
}

func (c *Client) ConfigurationDone(ctx context.Context) error {
	req := &godap.ConfigurationDoneRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "configurationDone"},
	}
	_, err := c.Call(ctx, req, 10*time.Second)
	return err
}

func (c *Client) SetBreakpoints(ctx context.Context, source godap.Source, bps []godap.SourceBreakpoint) (*godap.SetBreakpointsResponse, error) {
	req := &godap.SetBreakpointsRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "setBreakpoints"},
		Arguments: godap.SetBreakpointsArguments{
			Source:      source,
			Breakpoints: bps,
		},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.SetBreakpointsResponse), nil
}

func (c *Client) Continue(ctx context.Context, threadID int) error {
	req := &godap.ContinueRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "continue"},
		Arguments: godap.ContinueArguments{ThreadId: threadID},
	}
	_, err := c.Call(ctx, req, 10*time.Second)
	return err
}

func (c *Client) Next(ctx context.Context, threadID int) error {
	req := &godap.NextRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "next"},
		Arguments: godap.NextArguments{ThreadId: threadID},
	}
	_, err := c.Call(ctx, req, 10*time.Second)
	return err
}

func (c *Client) StepIn(ctx context.Context, threadID int) error {
	req := &godap.StepInRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "stepIn"},
		Arguments: godap.StepInArguments{ThreadId: threadID},
	}
	_, err := c.Call(ctx, req, 10*time.Second)
	return err
}

func (c *Client) StepOut(ctx context.Context, threadID int) error {
	req := &godap.StepOutRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "stepOut"},
		Arguments: godap.StepOutArguments{ThreadId: threadID},
	}
	_, err := c.Call(ctx, req, 10*time.Second)
	return err
}

func (c *Client) Pause(ctx context.Context, threadID int) error {
	req := &godap.PauseRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "pause"},
		Arguments: godap.PauseArguments{ThreadId: threadID},
	}
	_, err := c.Call(ctx, req, 10*time.Second)
	return err
}

func (c *Client) StackTrace(ctx context.Context, threadID int, limit int) (*godap.StackTraceResponse, error) {
	req := &godap.StackTraceRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "stackTrace"},
		Arguments: godap.StackTraceArguments{
			ThreadId: threadID,
			Levels:   limit,
		},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.StackTraceResponse), nil
}

func (c *Client) Scopes(ctx context.Context, frameID int) (*godap.ScopesResponse, error) {
	req := &godap.ScopesRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "scopes"},
		Arguments: godap.ScopesArguments{FrameId: frameID},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.ScopesResponse), nil
}

func (c *Client) Variables(ctx context.Context, ref int) (*godap.VariablesResponse, error) {
	req := &godap.VariablesRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "variables"},
		Arguments: godap.VariablesArguments{VariablesReference: ref},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.VariablesResponse), nil
}

func (c *Client) Evaluate(ctx context.Context, expr string, frameID int, context_ string) (*godap.EvaluateResponse, error) {
	req := &godap.EvaluateRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "evaluate"},
		Arguments: godap.EvaluateArguments{
			Expression: expr,
			FrameId:    frameID,
			Context:    context_,
		},
	}
	m, err := c.Call(ctx, req, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.EvaluateResponse), nil
}

func (c *Client) SetVariable(ctx context.Context, varsRef int, name, value string) (*godap.SetVariableResponse, error) {
	req := &godap.SetVariableRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "setVariable"},
		Arguments: godap.SetVariableArguments{
			VariablesReference: varsRef,
			Name:               name,
			Value:              value,
		},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.SetVariableResponse), nil
}

func (c *Client) Threads(ctx context.Context) (*godap.ThreadsResponse, error) {
	req := &godap.ThreadsRequest{
		Request: godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "threads"},
	}
	m, err := c.Call(ctx, req, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return m.(*godap.ThreadsResponse), nil
}

func (c *Client) Disconnect(ctx context.Context, terminate bool) error {
	req := &godap.DisconnectRequest{
		Request:   godap.Request{ProtocolMessage: godap.ProtocolMessage{Type: "request"}, Command: "disconnect"},
		Arguments: &godap.DisconnectArguments{TerminateDebuggee: terminate},
	}
	_, err := c.Call(ctx, req, 5*time.Second)
	return err
}

// Helpers

func jsonMarshal(v interface{}) ([]byte, error) {
	// go-dap requires LaunchRequest.Arguments to be json.RawMessage
	return marshal(v)
}

// Itoa is exposed for adapters to format IDs uniformly.
func Itoa(i int) string { return strconv.Itoa(i) }
