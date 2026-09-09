package cli

import (
	"fmt"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/spf13/cobra"
)

func finishSavedExecution(cmd *cobra.Command, output string, record engine.ExecutionRecord, resultErr error, selection ...*selectionDTO) error {
	result := executionHistoryDTO{SchemaVersion: 1, Executions: []executionDTO{}, Diagnostics: []diagnosticDTO{}}
	if len(selection) > 0 {
		result.Selection = selection[0]
	}
	if record.ID != "" {
		result.Executions = append(result.Executions, executionToDTO(record))
	}
	if resultErr != nil {
		result.Diagnostics = append(result.Diagnostics, diagnosticToDTO(engine.Diagnostic{Code: "saved_plan_failed", Phase: "plan", Subject: "execution", Message: resultErr.Error(), Remedy: "inspect plan show before retrying; never replay an uncertain mutation"}))
	}
	if output == "json" {
		if err := writeJSON(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	} else if record.ID != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", record.ID, record.Status)
	}
	return resultErr
}
