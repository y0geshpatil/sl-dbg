package cli

import (
	"github.com/spf13/cobra"

	"github.com/yogeshpatil/sl-dbg/internal/daemon"
	"github.com/yogeshpatil/sl-dbg/internal/ipc"
	"github.com/yogeshpatil/sl-dbg/internal/proto"
)

// daemonCmd has two subcommands relevant to users:
//   sl-dbg daemon serve   -- run the daemon in the foreground (used by auto-spawn)
//   sl-dbg daemon stop    -- shut down the daemon
//   sl-dbg daemon status  -- check if daemon is running
//   sl-dbg daemon logs    -- print log file path
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
		Short: "Stop the running daemon",
		RunE: func(*cobra.Command, []string) error {
			resp, err := daemonCall(proto.Request{Cmd: proto.CmdShutdown})
			if err != nil {
				emitErr("DAEMON_UNREACHABLE", err.Error(), "")
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
