package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/y0geshpatil/sl-dbg/internal/daemon"
	"github.com/y0geshpatil/sl-dbg/internal/ipc"
	"github.com/y0geshpatil/sl-dbg/internal/proto"
)

// daemonCmd has two subcommands relevant to users:
//
//	sl-dbg daemon serve   -- run the daemon in the foreground (used by auto-spawn)
//	sl-dbg daemon stop    -- shut down the daemon
//	sl-dbg daemon status  -- check if daemon is running
//	sl-dbg daemon logs    -- print log file path
func newDaemonCmd2() *cobra.Command {
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Daemon controls (serve|stop|status|logs)",
	}

	c.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the daemon in the foreground (auto-invoked by CLI; rarely run by users)",
		RunE: func(*cobra.Command, []string) error {
			return daemon.Run()
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon and wait for shutdown (no-op if absent)",
		RunE: func(*cobra.Command, []string) error {
			resp, err := stopRunningDaemon(5 * time.Second)
			if err != nil {
				code := "DAEMON_UNREACHABLE"
				if errors.Is(err, context.DeadlineExceeded) {
					code = "TIMEOUT"
				}
				emitErr(code, err.Error(), "check `sl-dbg daemon status` and `sl-dbg daemon logs` before retrying")
				return err
			}
			return emitResp(resp)
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(*cobra.Command, []string) error {
			pid := ipc.ReadPidFile()
			cl, err := ipc.Dial(500_000_000) // 500ms
			alive := err == nil
			if cl != nil {
				cl.Close()
			}
			emit(map[string]interface{}{
				"pid":    pid,
				"socket": ipc.SocketPath(),
				"alive":  alive,
				"log":    ipc.LogFilePath(),
			})
			return nil
		},
	})

	c.AddCommand(&cobra.Command{
		Use:   "logs",
		Short: "Print the daemon log file path (tail it with `tail -f`)",
		RunE: func(*cobra.Command, []string) error {
			emit(map[string]string{"log": ipc.LogFilePath()})
			return nil
		},
	})

	return c
}

func stopRunningDaemon(timeout time.Duration) (proto.Response, error) {
	deadline := time.Now().Add(timeout)
	socket := ipc.SocketPath()
	originalSocket, err := os.Stat(socket)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return proto.Response{}, fmt.Errorf("inspect daemon socket %s: %w", socket, err)
	}
	if originalSocket != nil && originalSocket.Mode()&os.ModeSocket == 0 {
		return proto.Response{}, fmt.Errorf("daemon endpoint %s is not a Unix socket", socket)
	}
	pid := ipc.ReadPidFile()
	client, err := ipc.Dial(min(timeout, 500*time.Millisecond))
	if err != nil {
		if daemonEndpointAbsent(err) {
			return proto.Response{OK: true, Data: rawJSON(map[string]string{"shutdown": "not running"})}, nil
		}
		return proto.Response{}, fmt.Errorf("connect to daemon at %s: %w", socket, err)
	}
	defer client.Close()
	if err := client.SetDeadline(deadline); err != nil {
		return proto.Response{}, fmt.Errorf("set shutdown deadline: %w", err)
	}
	if err := client.Send(proto.Request{Cmd: proto.CmdShutdown}); err != nil {
		return proto.Response{}, fmt.Errorf("send daemon shutdown: %w", err)
	}
	var resp proto.Response
	if err := client.Recv(&resp); err != nil {
		return proto.Response{}, fmt.Errorf("receive daemon shutdown acknowledgement: %w", err)
	}
	if !resp.OK {
		return resp, nil
	}
	_ = client.Close()

	// Shutdown is acknowledged before exit. Do not let the next invocation reach
	// the old listener or race its live pidfile when spawning a replacement.
	for time.Now().Before(deadline) {
		currentSocket, err := os.Stat(socket)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return proto.Response{}, fmt.Errorf("check shutdown socket %s: %w", socket, err)
		}
		if originalSocket != nil && currentSocket != nil && !os.SameFile(originalSocket, currentSocket) {
			return resp, nil
		}
		probe, err := ipc.Dial(min(time.Until(deadline), 100*time.Millisecond))
		if err == nil {
			_ = probe.Close()
		} else if daemonEndpointAbsent(err) {
			if pid <= 0 || errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				return resp, nil
			}
		} else {
			return proto.Response{}, fmt.Errorf("verify daemon shutdown at %s: %w", socket, err)
		}
		time.Sleep(min(time.Until(deadline), 10*time.Millisecond))
	}
	return proto.Response{}, fmt.Errorf("daemon acknowledged shutdown but did not release its endpoint and pid %d within %s: %w", pid, timeout, context.DeadlineExceeded)
}

func daemonEndpointAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
