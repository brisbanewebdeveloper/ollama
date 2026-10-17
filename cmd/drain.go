package cmd

import (
	"fmt"

	"github.com/ollama/ollama/server"
	"github.com/spf13/cobra"
)

func newDrainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:       "drain {begin|status|resume|recover}",
		Short:     "Control admission through the private local drain socket",
		Long:      "Begin draining, inspect outstanding work, or resume admission. recover forcibly reopens admission after a controller crash; do not use during a restart.",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"begin", "status", "resume", "recover"},
		RunE: func(cmd *cobra.Command, args []string) error {
			token, _ := cmd.Flags().GetString("token")
			result, err := server.DrainControl(cmd.Context(), args[0], token)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), result)
			return nil
		},
	}
	cmd.Flags().String("token", "", "Ownership token returned by begin (required for status/resume)")
	return cmd
}
