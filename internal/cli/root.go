// Package cli wires together the cobra command tree and shared CLI utilities.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Global flags applied to every subcommand.
type globalFlags struct {
	JSON    bool   // default true; pretty when --pretty
	Pretty  bool
	Quiet   bool
	Session string
	Timeout string
}

var gFlags globalFlags

// NewRootCommand builds the full sl-dbg command tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "sl-dbg",
		Short: "Stateless debugger CLI for AI agents and humans",
		Long: `sl-dbg is a stateless, command-per-invocation debugger CLI built on the
Debug Adapter Protocol (DAP). Each call is atomic; output is structured JSON.

See https://github.com/yogeshpatil/sl-dbg for full documentation.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.BoolVar(&gFlags.JSON, "json", true, "output JSON (default)")
	pf.BoolVar(&gFlags.Pretty, "pretty", false, "human-readable output")
	pf.BoolVarP(&gFlags.Quiet, "quiet", "q", false, "suppress non-error output")
	pf.StringVar(&gFlags.Session, "session", "", "session id (uses default if empty)")
	pf.StringVar(&gFlags.Timeout, "timeout", "30s", "timeout for blocking commands")

	// Session lifecycle
	root.AddCommand(newStartCmd())
	root.AddCommand(newAttachCmd())
	root.AddCommand(newListenCmd())
	root.AddCommand(newSessionsCmd())
	root.AddCommand(newUseCmd())
	root.AddCommand(newStopCmd())
	root.AddCommand(newRestartCmd())
	root.AddCommand(newStateCmd())

	// Breakpoints
	root.AddCommand(newBreakCmd())
	root.AddCommand(newBreakFnCmd())
	root.AddCommand(newBreakExCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newBreaksCmd())
	root.AddCommand(newUnbreakCmd())

	// Execution
	root.AddCommand(newRunCmd())
	root.AddCommand(newContinueCmd())
	root.AddCommand(newStepCmd())
	root.AddCommand(newNextCmd())
	root.AddCommand(newFinishCmd())
	root.AddCommand(newPauseCmd())
	root.AddCommand(newUntilCmd())
	root.AddCommand(newGotoCmd())

	// Inspection
	root.AddCommand(newStackCmd())
	root.AddCommand(newThreadsCmd())
	root.AddCommand(newLocalsCmd())
	root.AddCommand(newGlobalsCmd())
	root.AddCommand(newFieldsCmd())
	root.AddCommand(newEvalCmd())
	root.AddCommand(newSetVarCmd())
	root.AddCommand(newSnapshotCmd())
	root.AddCommand(newSourceCmd())

	// I/O
	root.AddCommand(newOutputCmd())
	root.AddCommand(newEventsCmd())

	// Meta
	root.AddCommand(newVersionCmd())
	root.AddCommand(newAdaptersCmd())
	root.AddCommand(newInstallAdapterCmd())
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newMCPCmd())

	return root
}

// usageError is returned for malformed flags / args. Maps to exit code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// Usage returns a usageError.
func Usage(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// notImplementedError marks commands that are scaffolded but not yet wired up.
type notImplementedError struct{ cmd string }

func (e *notImplementedError) Error() string {
	return fmt.Sprintf("%q: not yet implemented (see docs/ROADMAP.md)", e.cmd)
}

// NotImplemented returns a notImplementedError. Returned by stub commands.
func NotImplemented(cmd string) error { return &notImplementedError{cmd: cmd} }

// ExitCodeFor maps a returned error to the process exit code.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	var ni *notImplementedError
	if errors.As(err, &ni) {
		return 1
	}
	return 1
}
