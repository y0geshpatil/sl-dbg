package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/y0geshpatil/sl-dbg/internal/ipc"
	"github.com/y0geshpatil/sl-dbg/internal/proto"
)

// daemonCall sends one request, returns the decoded response. It will
// auto-spawn the daemon if it's not running.
func daemonCall(req proto.Request) (proto.Response, error) {
	cli, err := dialOrSpawn()
	if err != nil {
		return proto.Response{}, fmt.Errorf("daemon unreachable: %w", err)
	}
	defer cli.Close()

	// For blocking commands the daemon may take a while; we set a generous deadline.
	_ = cli.SetDeadline(time.Now().Add(10 * time.Minute))

	if err := cli.Send(req); err != nil {
		return proto.Response{}, err
	}
	var resp proto.Response
	if err := cli.Recv(&resp); err != nil {
		return proto.Response{}, err
	}
	return resp, nil
}

// dialOrSpawn tries to connect; if refused/missing, spawns daemon and retries.
func dialOrSpawn() (*ipc.Client, error) {
	c, err := ipc.Dial(500 * time.Millisecond)
	if err == nil {
		return c, nil
	}
	// Spawn daemon in background.
	if err := spawnDaemon(); err != nil {
		return nil, err
	}
	// Retry with backoff up to 3s.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := ipc.Dial(200 * time.Millisecond)
		if err == nil {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("daemon failed to start within 3s")
}

func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon", "serve")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

// emitResp emits a proto.Response as the user-facing JSON envelope.
func emitResp(resp proto.Response) error {
	if resp.OK {
		var data interface{}
		if len(resp.Data) > 0 {
			if err := json.Unmarshal(resp.Data, &data); err != nil {
				data = string(resp.Data)
			}
		}
		emit(data)
		return nil
	}
	if resp.Error == nil {
		emitErr("INTERNAL_ERROR", "unknown daemon error", "")
		return &daemonError{}
	}
	emitErr(resp.Error.Code, resp.Error.Message, resp.Error.Hint)
	return &daemonError{code: resp.Error.Code}
}

type daemonError struct{ code string }

func (e *daemonError) Error() string {
	if e.code == "" {
		return "daemon returned error"
	}
	return "daemon: " + e.code
}

// rawJSON converts any value to json.RawMessage.
func rawJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
