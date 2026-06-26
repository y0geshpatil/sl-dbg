package cli

import (
	"github.com/spf13/cobra"

	"github.com/yogeshpatil/sl-dbg/internal/buildinfo"
	"github.com/yogeshpatil/sl-dbg/pkg/api"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print sl-dbg version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			emit(api.VersionInfo{
				Version: buildinfo.Version,
				Commit:  buildinfo.Commit,
				Date:    buildinfo.Date,
			})
			return nil
		},
	}
}
