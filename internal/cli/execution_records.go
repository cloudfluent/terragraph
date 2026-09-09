package cli

import (
	"errors"
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
	PluginCalls []pluginCallDTO    `json:"plugin_calls,omitempty"`
	Selection   *selectionDTO      `json:"selection,omitempty"`
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
	Selection     *selectionDTO   `json:"selection,omitempty"`
	SchemaVersion int             `json:"schema_version"`
	Executions    []executionDTO  `json:"executions"`
	Diagnostics   []diagnosticDTO `json:"diagnostics"`
}

func executionToDTO(record engine.ExecutionRecord) executionDTO {
	dto := executionDTO{Selection: selectionToDTO(record.Selection), ID: record.ID, Preparation: record.Preparation, Backup: record.Backup, Operation: record.Operation, Status: record.Status, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, FinishedAt: record.FinishedAt, RecoveryAt: record.RecoveryAt, Nodes: []executionNodeDTO{}}
	for _, node := range record.Nodes {
		dto.Nodes = append(dto.Nodes, executionNodeDTO{Node: node.Name, Review: reviewToDTO(node.Review), Phase: node.Phase, PlanID: node.PlanID, Code: node.Code})
	}
	for _, call := range record.PluginCalls {
		dto.PluginCalls = append(dto.PluginCalls, pluginCallDTO{ID: call.ID, Plugin: call.Alias, Feature: call.Feature, Phase: call.Event.Phase, Node: call.Event.Node, Status: call.Status, Code: call.Code, Decision: call.Decision, ReportAvailable: len(call.Report) > 0})
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
		phase := "arguments"
		result := executionHistoryDTO{SchemaVersion: 1, Executions: []executionDTO{}, Diagnostics: []diagnosticDTO{}}
		defer func() {
			if resultErr != nil {
				result.Diagnostics = errorDiagnostics(resultErr, engine.Diagnostic{Code: "execution_read_failed", Category: categoryForPhase(phase), Phase: phase, Subject: "execution", Remedy: "check the selected execution store and restore missing records; do not infer that infrastructure was unchanged"})
			}
			if output == "json" {
				for _, record := range result.Executions {
					if len(record.PluginCalls) > 0 {
						result.SchemaVersion = 2
					}
				}
				if err := writeJSON(cmd, result); err != nil {
					resultErr = errors.Join(resultErr, err)
				}
				return
			}
			if output == "text" {
				for _, record := range result.Executions {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", record.ID, record.Operation, record.Status)
					if kind == "show" {
						printSelection(cmd.OutOrStdout(), record.Selection, nil)
						for _, call := range record.PluginCalls {
							_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  plugin %s.%s %s %s: %s\n", call.Plugin, call.Feature, call.Phase, call.ID, call.Status)
						}
						if record.Backup {
							_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  backup: available (export with plan show --backup)")
						}
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
		phase = "history"
		diagnosticPhase(cmd, "record")
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

type pluginCallDTO struct {
	ID              string `json:"id"`
	Plugin          string `json:"plugin"`
	Feature         string `json:"feature"`
	Phase           string `json:"phase"`
	Node            string `json:"node,omitempty"`
	Status          string `json:"status"`
	Code            string `json:"code,omitempty"`
	Decision        string `json:"decision,omitempty"`
	ReportAvailable bool   `json:"report_available"`
}
