// Package ipc implements line-delimited JSON over a Unix domain socket
// between the sl-dbg CLI and the sl-dbg daemon.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

// SocketPath returns the per-user daemon socket path.
// Honors $SL_DBG_SOCKET if set (useful for tests).
func SocketPath() string {
	if p := os.Getenv("SL_DBG_SOCKET"); p != "" {
		return p
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		return filepath.Join(rt, "sl-dbg", "daemon.sock")
	}
	u, err := user.Current()
	uid := "0"
	if err == nil {
		uid = u.Uid
	}
	return filepath.Join(os.TempDir(), "sl-dbg-"+uid, "daemon.sock")
}

// PidFilePath returns the daemon pid file path (next to the socket).
func PidFilePath() string {
	return SocketPath() + ".pid"
}

// LogFilePath returns the daemon log file path.
func LogFilePath() string {
	return SocketPath() + ".log"
}

// EnsureDir creates the socket parent directory with 0700 permissions.
func EnsureDir() error {
	return os.MkdirAll(filepath.Dir(SocketPath()), 0o700)
}

// --- Client ---

// Client is a single CLI-side connection to the daemon.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
}

// Dial connects to the daemon socket. Returns os.ErrNotExist style errors when
// the daemon is not running, so callers can decide to auto-spawn.
func Dial(timeout time.Duration) (*Client, error) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.Dial("unix", SocketPath())
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, r: bufio.NewReader(conn)}, nil
}

// Send writes a JSON object followed by a newline.
func (c *Client) Send(v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = c.conn.Write(b)
	return err
}

// Recv reads one JSON object (one line) into v.
// Honors SetReadDeadline if you set it on the underlying conn.
func (c *Client) Recv(v interface{}) error {
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && len(line) == 0 {
			return io.EOF
		}
		if len(line) == 0 {
			return err
		}
	}
	return json.Unmarshal(line, v)
}

// SetDeadline sets the deadline for both reads and writes.
func (c *Client) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// --- Server ---

// HandlerFunc serves one accepted connection.
type HandlerFunc func(conn net.Conn)

// Listen creates the socket (removing any stale file) and starts accepting
// connections. The provided handler is invoked for each accepted connection
// in its own goroutine.
func Listen(handler HandlerFunc) (net.Listener, error) {
	if err := EnsureDir(); err != nil {
		return nil, fmt.Errorf("ensure dir: %w", err)
	}
	sock := SocketPath()
	_ = os.Remove(sock) // remove stale
	l, err := net.Listen("unix", sock)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(sock, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return // listener closed
			}
			go handler(c)
		}
	}()
	return l, nil
}

// --- Pid file ---

// WritePidFile writes the current process pid.
func WritePidFile() error {
	if err := EnsureDir(); err != nil {
		return err
	}
	return os.WriteFile(PidFilePath(), []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// ReadPidFile returns the pid stored in the daemon pid file, or 0.
func ReadPidFile() int {
	b, err := os.ReadFile(PidFilePath())
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(string(b))
	return n
}

// RemovePidFile removes the pid file.
func RemovePidFile() { _ = os.Remove(PidFilePath()) }

// --- Helpers for the server-side bufio framing ---

// NewServerReader returns a bufio reader for incoming JSON lines.
func NewServerReader(r io.Reader) *bufio.Reader { return bufio.NewReader(r) }

// WriteLine encodes v as JSON to w followed by a newline.
func WriteLine(w io.Writer, v interface{}) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ReadLine reads one JSON object from r into v.
func ReadLine(r *bufio.Reader, v interface{}) error {
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return err
	}
	return json.Unmarshal(line, v)
}
