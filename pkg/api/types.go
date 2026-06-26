// Package api defines the public JSON envelope types returned by sl-dbg commands.
// These types are stable across versions; backwards-incompatible changes require
// a major version bump.
package api

import "time"

// SchemaVersion identifies the JSON envelope shape. Increment on breaking changes
// so AI agents / client libraries can detect incompatibilities at runtime.
const SchemaVersion = "1"

// Response is the standard envelope for every sl-dbg command's stdout.
type Response struct {
	Schema  string      `json:"schema"`
	OK      bool        `json:"ok"`
	Data    interface{} `json:"data,omitempty"`
	Error   *Error      `json:"error,omitempty"`
	Session string      `json:"session,omitempty"`
	State   string      `json:"state,omitempty"` // paused | running | exited | terminated
	TS      time.Time   `json:"ts"`
}

// Error describes a failure with a stable code, message, and optional hint.
type Error struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
	Hint    string                 `json:"hint,omitempty"`
}

// Standard error codes. Keep these stable; clients depend on them.
const (
	ErrUsage              = "USAGE_ERROR"
	ErrIPC                = "IPC_ERROR"
	ErrDaemonUnreachable  = "DAEMON_UNREACHABLE"
	ErrAdapterNotFound    = "ADAPTER_NOT_FOUND"
	ErrAdapterFailed      = "ADAPTER_FAILED"
	ErrSessionNotFound    = "SESSION_NOT_FOUND"
	ErrBreakpointDenied   = "BREAKPOINT_DENIED"
	ErrBreakpointPending  = "BREAKPOINT_PENDING"
	ErrTargetCrashed      = "TARGET_CRASHED"
	ErrTimeout            = "TIMEOUT"
	ErrReadOnly           = "READ_ONLY_MODE"
	ErrUnsupportedFeature = "UNSUPPORTED_FEATURE"
	ErrInternal           = "INTERNAL_ERROR"
)

// VersionInfo is returned by `sl-dbg version`.
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Location identifies a position in source code.
type Location struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Function string `json:"function,omitempty"`
}

// Breakpoint represents a registered breakpoint.
type Breakpoint struct {
	ID        int      `json:"id"`
	Verified  bool     `json:"verified"`
	File      string   `json:"file,omitempty"`
	Line      int      `json:"line,omitempty"`
	Function  string   `json:"function,omitempty"`
	Condition string   `json:"condition,omitempty"`
	HitCount  int      `json:"hitCount,omitempty"`
	LogMessage string  `json:"logMessage,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

// StackFrame is one entry in a call stack.
type StackFrame struct {
	ID       int      `json:"id"`
	Name     string   `json:"name"`
	Location Location `json:"location"`
}

// Variable represents a single named value at a debug pause point.
type Variable struct {
	Name       string `json:"name"`
	Value      string `json:"value"`
	Type       string `json:"type,omitempty"`
	Ref        int    `json:"ref,omitempty"`        // for lazy expansion
	Expandable bool   `json:"expandable,omitempty"`
}

// SessionInfo summarizes a session for listing.
type SessionInfo struct {
	ID       string `json:"id"`
	Lang     string `json:"lang"`
	State    string `json:"state"`
	Program  string `json:"program,omitempty"`
	Attached string `json:"attached,omitempty"`
	Default  bool   `json:"default,omitempty"`
}

// PauseEvent is the response shape for blocking execution commands.
type PauseEvent struct {
	State          string    `json:"state"`         // paused | running | exited
	Reason         string    `json:"reason"`        // breakpoint | step | exception | pause | entry | exit | timeout
	Thread         int       `json:"thread,omitempty"`
	Location       *Location `json:"location,omitempty"`
	HitBreakpoint  int       `json:"hitBreakpoint,omitempty"`
	ExitCode       *int      `json:"exitCode,omitempty"`
	ExceptionInfo  *Exception `json:"exception,omitempty"`
}

// Exception describes a thrown exception.
type Exception struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	StackTrace  string `json:"stackTrace,omitempty"`
}
