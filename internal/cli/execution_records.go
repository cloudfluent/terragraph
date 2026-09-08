package cli

import (
	"fmt"
	"time"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/spf13/cobra"
)

type executionNodeDTO struct {
	Node   string         `json:"node"`
	Review *planReviewDTO `json:"review,omitempty"`
	Phase  string         `json:"phase"`
	PlanID string         `json:"plan_id,omitempty"`
	Code   string         `json:"code,omitempty"`
}

type executionDTO struct {
	ID          string             `json:"id"`
	Preparation string             `json:"preparation,omitempty"`
	Backup      bool               `json:"backup_available"`
	Operation   string             `json:"operation"`
	Status      string             `json:"status"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	FinishedAt  *time.Time         `json:"finished_at,omitempty"`
	RecoveryAt  *time.Time         `json:"recovery_at,omitempty"`
	Nodes       []executionNodeDTO `json:"nodes"`
}

type executionHistoryDTO struct {
	SchemaVersion int             `json:"schema_version"`
	Executions    []executionDTO  `json:"executions"`
	Diagnostics   []diagnosticDTO `json:"diagnostics"`
}

func executionToDTO(record engine.ExecutionRecord) executionDTO {
	dto := executionDTO{ID: record.ID, Preparation: record.Preparation, Backup: record.Backup, Operation: record.Operation, Status: record.Status, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, FinishedAt: record.FinishedAt, RecoveryAt: record.RecoveryAt, Nodes: []executionNodeDTO{}}
	for _, node := range record.Nodes {
		dto.Nodes = append(dto.Nodes, executionNodeDTO{Node: node.Name, Review: reviewToDTO(node.Review), Phase: node.Phase, PlanID: node.PlanID, Code: node.Code})
	}
	return dto
}

func newExecutionHistoryCmd(kind string, path *string) *cobra.Command {
	var output string
	var backup bool
	usage, short := kind, "List execution records without reading infrastructure state"
	argsCheck := cobra.NoArgs
	if kind == "show" {
		usage = "show <execution-id>"
		short = "Show an execution's recorded phases without rerunning it"
		argsCheck = cobra.ExactArgs(1)
	}
	cmd := &cobra.Command{Use: usage, Short: short, RunE: func(cmd *cobra.Command, args []string) (resultErr error) {
		if backup && output != "text" {
			return fmt.Errorf("--backup emits raw native state; omit --output json")
		}
		result := executionHistoryDTO{SchemaVersion: 1, Executions: []executionDTO{}, Diagnostics: []diagnosticDTO{}}
		defer func() {
			if resultErr != nil {
				diagnostic := diagnosticToDTO(engine.Diagnostic{Code: "execution_read_failed", Phase: "history", Subject: "execution", Message: resultErr.Error(), Remedy: "check the selected execution store and restore missing records; do not infer that infrastructure was unchanged"})
				result.Diagnostics = append(result.Diagnostics, diagnostic)
			}
			if output == "json" {
				if err := writeJSON(cmd.OutOrStdout(), result); resultErr == nil {
					resultErr = err
				}
				return
			}
			if output == "text" {
				for _, record := range result.Executions {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", record.ID, record.Operation, record.Status)
					if kind == "show" {
						if record.Preparation != "" {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  backend preparation: %s\n", record.Preparation)
						}
						for _, node := range record.Nodes {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s: %s\n", node.Node, node.Phase)
						}
					}
				}
			}
		}()
		if err := argsCheck(cmd, args); err != nil {
			return err
		}
		if output != "text" && output != "json" {
			return fmt.Errorf("unknown output %q (want text or json)", output)
		}
		e, close, err := engine.OpenExecutionHistory(cmd.Context(), *path, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer close()
		if backup {
			return e.ReadExecutionBackup(args[0], cmd.OutOrStdout())
		}
		if kind == "show" {
			record, err := e.GetExecution(args[0])
			if record.ID != "" {
				result.Executions = append(result.Executions, executionToDTO(record))
			}
			return err
		}
		records, err := e.ListExecutions()
		for _, record := range records {
			result.Executions = append(result.Executions, executionToDTO(record))
		}
		return err
	}}
	if kind == "show" {
		cmd.Flags().BoolVar(&backup, "backup", false, "write the native state backup to stdout; may contain secrets")
	}
	cmd.Flags().StringVar(&output, "output", "text", "output format: text or json")
	return cmd
}
