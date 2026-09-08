package cli

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/y0geshpatil/sl-dbg/internal/ipc"
	"github.com/y0geshpatil/sl-dbg/internal/proto"
)

func TestSpawnedDaemonProcessIsReaped(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := startDaemonProcess(cmd); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(cmd.Process.Pid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("exited child remains unreaped while parent is alive")
}

func shutdownFixture(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sld-stop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	t.Setenv("SL_DBG_SOCKET", path)
	return path
}

func shutdownServer(t *testing.T, path string, finish func(*net.UnixListener)) *net.UnixListener {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		var req proto.Request
		if err := ipc.ReadLine(bufio.NewReader(conn), &req); err != nil {
			t.Error(err)
			return
		}
		if req.Cmd != proto.CmdShutdown {
			t.Errorf("request=%s", req.Cmd)
		}
		if err := ipc.WriteLine(conn, proto.Response{OK: true, Data: rawJSON(map[string]string{"shutdown": "scheduled"})}); err != nil {
			t.Error(err)
			return
		}
		finish(listener)
	}()
	return listener
}

func TestDaemonStopAbsentDoesNotSpawn(t *testing.T) {
	path := shutdownFixture(t)
	resp, err := stopRunningDaemon(time.Second)
	if err != nil || !resp.OK {
		t.Fatalf("absent stop: %v, %v", resp, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("stop created socket: %v", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
	resp, err = stopRunningDaemon(time.Second)
	if err != nil || !resp.OK {
		t.Fatalf("stale socket stop: %v, %v", resp, err)
	}
}

func TestDaemonStopWaitsPastAcknowledgement(t *testing.T) {
	path := shutdownFixture(t)
	ack := make(chan struct{})
	release := make(chan struct{})
	shutdownServer(t, path, func(listener *net.UnixListener) {
		close(ack)
		<-release
		listener.Close()
	})
	done := make(chan error, 1)
	go func() {
		_, err := stopRunningDaemon(time.Second)
		done <- err
	}()
	<-ack
	select {
	case err := <-done:
		close(release)
		t.Fatalf("returned before endpoint stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonStopReportsShutdownTimeout(t *testing.T) {
	path := shutdownFixture(t)
	listener := shutdownServer(t, path, func(listener *net.UnixListener) {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	})
	_, err := stopRunningDaemon(100 * time.Millisecond)
	listener.Close()
	if err == nil || !strings.Contains(err.Error(), "did not release") {
		t.Fatalf("wanted actionable timeout, got %v", err)
	}
}

func TestDaemonStopDoesNotWaitForReplacement(t *testing.T) {
	path := shutdownFixture(t)
	replacement := make(chan *net.UnixListener, 1)
	shutdownServer(t, path, func(old *net.UnixListener) {
		if err := os.Remove(path); err != nil {
			t.Error(err)
			return
		}
		next, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Error(err)
			return
		}
		replacement <- next
		old.Close()
	})
	_, err := stopRunningDaemon(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	next := <-replacement
	defer next.Close()
	if conn, err := net.Dial("unix", path); err != nil {
		t.Fatalf("replacement was removed: %v", err)
	} else {
		conn.Close()
	}
}
