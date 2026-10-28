package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// versionCommand reports build metadata without contacting an installation.
// Missing ldflags fall back to development placeholders rather than empty fields.
func (app *application) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use: "version", Short: "Print build information", Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := app.options.Version
			if info.Version == "" {
				info.Version = "dev"
			}
			if info.Commit == "" {
				info.Commit = "none"
			}
			if info.Date == "" {
				info.Date = "unknown"
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
		},
	}
}
