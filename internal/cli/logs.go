package cli

import (
	"bufio"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/yogeshpatil/sl-dbg/internal/ipc"
)

func newLogsCmd() *cobra.Command {
	var tail int
	var path bool
	c := &cobra.Command{
		Use:   "logs",
		Short: "Print the daemon log (or its path)",
		Example: `  sl-dbg logs            # last 200 lines
  sl-dbg logs --tail 50  # last 50 lines
  sl-dbg logs --path     # just print the file path`,
		RunE: func(*cobra.Command, []string) error {
			p := ipc.LogFilePath()
			if path {
				emit(map[string]string{"path": p})
				return nil
			}
			lines, err := tailFile(p, tail)
			if err != nil {
				emitErr("INTERNAL_ERROR", err.Error(), "")
				return nil
			}
			emit(map[string]interface{}{"path": p, "lines": lines})
			return nil
		},
	}
	c.Flags().IntVar(&tail, "tail", 200, "number of lines to return (0 = all)")
	c.Flags().BoolVar(&path, "path", false, "print only the log file path")
	return c
}

func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return lines, err
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}
