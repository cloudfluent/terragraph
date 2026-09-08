package cli

import (
	"fmt"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/spf13/cobra"
)

type executionPruneDTO struct {
	SchemaVersion int      `json:"schema_version"`
	Removed       []string `json:"removed"`
}

func newExecutionCleanupCmd(kind string, path *string) *cobra.Command {
	var output string
	usage, short, argsCheck := kind, "Remove eligible completed artifacts and expired terminal records", cobra.NoArgs
	if kind == "cancel" {
		usage, short, argsCheck = "cancel <execution-id>", "Cancel a paused execution without touching infrastructure", cobra.ExactArgs(1)
	}
	cmd := &cobra.Command{Use: usage, Short: short, Args: argsCheck, RunE: func(cmd *cobra.Command, args []string) error {
		if output != "text" && output != "json" {
			return fmt.Errorf("unknown output %q (want text or json)", output)
		}
		e, close, err := engine.OpenExecutionHistory(cmd.Context(), *path, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer close()
		if kind == "cancel" {
			record, err := e.CancelExecution(args[0])
			return finishSavedExecution(cmd, output, record, err)
		}
		removed, err := e.PruneExecutions()
		if output == "json" {
			if writeErr := writeJSON(cmd.OutOrStdout(), executionPruneDTO{SchemaVersion: 1, Removed: removed}); writeErr != nil {
				return writeErr
			}
		} else {
			for _, id := range removed {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), id)
			}
		}
		return err
	}}
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}
