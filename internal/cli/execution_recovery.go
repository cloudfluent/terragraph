package cli

import (
	"fmt"
	"log/slog"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/spf13/cobra"
)

func newExecutionRecoveryCmd(path *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var confirmStopped, stateReviewed, replan, initializeBackend bool
	var output string
	cmd := &cobra.Command{Use: "recover <execution-id>", Short: "Recover outputs or retire an inspected uncertain attempt without replaying it", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if output != "text" && output != "json" {
			return fmt.Errorf("unknown output %q (want text or json)", output)
		}
		var e *engine.Engine
		var close func()
		var err error
		if replan {
			diagnosticPhase(cmd, "record")
			e, close, err = engine.OpenExecutionHistory(cmd.Context(), *path, cmd.ErrOrStderr())
		} else {
			e, close, err = loadLockedEngine(cmd, path, binaryOf, loggerOf)
		}
		if err != nil {
			return err
		}
		defer close()
		record, err := e.RecoverExecution(args[0], confirmStopped, stateReviewed, replan, initializeBackend)
		if output == "json" {
			diagnostics := errorDiagnostics(err, engine.Diagnostic{Code: "recovery_required", Category: "recovery", Phase: "recovery", Subject: "execution", RelatedExecutionID: record.ID})
			if record.ID == "" {
				if writeErr := writeJSON(cmd, errorResultDTO{SchemaVersion: 1, Diagnostics: diagnostics}); writeErr != nil {
					return writeErr
				}
			} else if writeErr := writeJSON(cmd, executionRecoveryDTO{SchemaVersion: 1, executionDTO: executionToDTO(record), Diagnostics: diagnostics}); writeErr != nil {
				return writeErr
			}
			return err
		}
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s; create a fresh plan before continuing\n", record.ID, record.Status)
		return err
	}}
	cmd.Flags().BoolVar(&initializeBackend, "initialize-backend", false, "allow journaled backend initialization for recovery when read-only preparation is unsupported")
	cmd.Flags().BoolVar(&confirmStopped, "confirm-stopped", false, "confirm the previous executor has stopped; does not force-unlock anything")
	cmd.Flags().BoolVar(&stateReviewed, "state-reviewed", false, "confirm actual state and affected resources have been inspected")
	cmd.Flags().BoolVar(&replan, "replan", false, "retire the attempt while preserving its recorded outcome; requires a fresh plan")
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}

// executionRecoveryDTO preserves recover's flat record while retaining partial results on failure.
type executionRecoveryDTO struct {
	SchemaVersion int `json:"schema_version"`
	executionDTO
	Diagnostics []diagnosticDTO `json:"diagnostics"`
}
