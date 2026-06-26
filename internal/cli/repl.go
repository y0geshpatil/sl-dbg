package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

func newReplCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "repl",
		Short: "Interactive shell — keeps one daemon connection, no per-command spawn",
		Long: `Drop into a sl-dbg shell. Every input line is dispatched as if it were
'sl-dbg <line>', but the daemon connection is reused so commands are
much snappier than calling sl-dbg from a shell loop.

Built-ins:
  help            list commands
  exit | quit     leave (Ctrl-D also works)
  !<line>         run any sl-dbg command (same as typing the line directly)
  #               comment, ignored`,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRepl(context.Background())
		},
	}
	return c
}

func runRepl(ctx context.Context) error {
	in := bufio.NewReader(os.Stdin)
	stdoutIsTTY := isTerminal(os.Stdout.Fd())
	prompt := ""
	if stdoutIsTTY {
		prompt = "sl-dbg> "
		fmt.Fprintln(os.Stdout, "sl-dbg repl — type 'help' for commands, 'exit' or Ctrl-D to leave")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	for {
		if prompt != "" {
			fmt.Fprint(os.Stdout, prompt)
		}
		line, err := in.ReadString('\n')
		if err == io.EOF {
			if strings.TrimSpace(line) == "" {
				if prompt != "" {
					fmt.Fprintln(os.Stdout)
				}
				return nil
			}
			// Fall through to process last line without newline.
			err = nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "exit" || line == "quit" {
			return nil
		}
		if strings.HasPrefix(line, "!") {
			line = strings.TrimSpace(line[1:])
		}
		argv, err := splitShell(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse: %v\n", err)
			continue
		}
		if len(argv) == 0 {
			continue
		}
		// Don't let users recursively launch repl inside itself.
		if argv[0] == "repl" {
			fmt.Fprintln(os.Stderr, "already in repl")
			continue
		}
		root := NewRootCommand()
		root.SetArgs(argv)
		root.SetOut(os.Stdout)
		root.SetErr(os.Stderr)

		cmdCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- root.ExecuteContext(cmdCtx) }()
		select {
		case err := <-done:
			cancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
		case <-sigCh:
			cancel()
			<-done
			fmt.Fprintln(os.Stderr, "^C")
		}
	}
}

// splitShell is a tiny POSIX-like splitter supporting "..." and '...'.
// Unterminated quotes return an error. Backslash escapes the next byte.
func splitShell(s string) ([]string, error) {
	var (
		out  []string
		cur  strings.Builder
		inS  bool // single quote
		inD  bool // double quote
		any  bool
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !inS && !inD && (c == ' ' || c == '\t') {
			if any {
				out = append(out, cur.String())
				cur.Reset()
				any = false
			}
			continue
		}
		if !inS && c == '\\' && i+1 < len(s) {
			cur.WriteByte(s[i+1])
			i++
			any = true
			continue
		}
		if !inD && c == '\'' {
			inS = !inS
			any = true
			continue
		}
		if !inS && c == '"' {
			inD = !inD
			any = true
			continue
		}
		cur.WriteByte(c)
		any = true
	}
	if inS || inD {
		return nil, fmt.Errorf("unterminated quote")
	}
	if any {
		out = append(out, cur.String())
	}
	return out, nil
}

// isTerminal reports whether the given fd refers to a terminal.
// Uses Stat() + ModeCharDevice — portable across macOS and Linux.
func isTerminal(fd uintptr) bool {
	f := os.NewFile(fd, "")
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
