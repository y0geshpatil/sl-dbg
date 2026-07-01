// Package main is the entry point for the sl-dbg command-line tool.
//
// sl-dbg is a stateless debugger CLI. Every invocation is a single,
// atomic action with structured JSON output. See README.md and docs/
// for full design and command reference.
package main

import (
	"fmt"
	"os"

	"github.com/y0geshpatil/sl-dbg/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(cli.ExitCodeFor(err))
	}
}
