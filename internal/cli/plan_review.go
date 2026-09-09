package cli

import (
	"fmt"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/spf13/cobra"
)

type planActionDTO struct {
	Address  string   `json:"address"`
	Actions  []string `json:"actions"`
	Category string   `json:"category"`
}
type outputChangeDTO struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}
type planCountsDTO struct {
	Create  int `json:"create"`
	Update  int `json:"update"`
	Delete  int `json:"delete"`
	Replace int `json:"replace"`
	Read    int `json:"read"`
	NoOp    int `json:"no_op"`
	Other   int `json:"other"`
}
type inputBasisDTO struct {
	Input  string `json:"input"`
	Node   string `json:"node"`
	Output string `json:"output"`
	Source string `json:"source"`
}
type planReviewDTO struct {
	Evidence    bool              `json:"evidence_available"`
	HasChanges  *bool             `json:"has_changes"`
	Resources   []planActionDTO   `json:"resource_changes"`
	Counts      *planCountsDTO    `json:"counts"`
	Outputs     []outputChangeDTO `json:"output_changes"`
	Policy      string            `json:"approve"`
	Decision    string            `json:"policy_decision"`
	Inputs      []inputBasisDTO   `json:"input_basis"`
	Limitations []string          `json:"limitations"`
	Diagnostics []diagnosticDTO   `json:"diagnostics"`
}
type planResultDTO struct {
	Selection     *selectionDTO   `json:"selection,omitempty"`
	SchemaVersion int             `json:"schema_version"`
	Nodes         []nodeRunDTO    `json:"nodes"`
	Diagnostics   []diagnosticDTO `json:"diagnostics"`
}

func reviewToDTO(review *engine.PlanReview) *planReviewDTO {
	if review == nil {
		return nil
	}
	dto := &planReviewDTO{Evidence: review.Evidence, HasChanges: review.HasChanges, Resources: []planActionDTO{}, Outputs: []outputChangeDTO{}, Policy: string(review.Policy), Decision: review.PolicyDecision, Inputs: []inputBasisDTO{}, Limitations: review.Limitations, Diagnostics: []diagnosticDTO{}}
	counts := &planCountsDTO{}
	for _, resource := range review.Resources {
		category := "other"
		if resource.IsReplace() {
			category = "replace"
			counts.Replace++
		} else if len(resource.Actions) == 1 {
			category = resource.Actions[0]
			switch category {
			case "create":
				counts.Create++
			case "update":
				counts.Update++
			case "delete":
				counts.Delete++
			case "read":
				counts.Read++
			case "no-op":
				counts.NoOp++
			default:
				counts.Other++
			}
		} else {
			counts.Other++
		}
		dto.Resources = append(dto.Resources, planActionDTO{Address: resource.Address, Actions: resource.Actions, Category: category})
	}
	if review.Evidence {
		dto.Counts = counts
	}
	for _, output := range review.Outputs {
		dto.Outputs = append(dto.Outputs, outputChangeDTO{Name: output.Name, Actions: output.Actions})
	}
	for _, basis := range review.Inputs {
		dto.Inputs = append(dto.Inputs, inputBasisDTO{Input: basis.Input, Node: basis.Node, Output: basis.Output, Source: basis.Source})
	}
	if review.Diagnostic != nil {
		dto.Diagnostics = append(dto.Diagnostics, diagnosticToDTO(*review.Diagnostic))
	}
	return dto
}

// finishPlan emits one additive, versioned result even when preparation fails before any node can start.
func finishPlan(cmd *cobra.Command, format string, runs []engine.NodeRun, phase string, err error, selection ...*selectionDTO) error {
	dto := planResultDTO{SchemaVersion: 1, Nodes: nodeRunsToDTO(runs), Diagnostics: []diagnosticDTO{}}
	if len(selection) > 0 {
		dto.Selection = selection[0]
	}
	if err != nil && len(runs) == 0 {
		dto.Diagnostics = append(dto.Diagnostics, diagnosticDTO{Source: errorLocation(err), Code: "plan_" + phase + "_failed", Phase: phase, Subject: "plan", Message: err.Error(), Remedy: "resolve the diagnostic and rerun plan"})
	}
	if format == "json" {
		if writeErr := writeJSON(cmd.OutOrStdout(), dto); writeErr != nil {
			return writeErr
		}
	} else {
		for _, node := range dto.Nodes {
			review := node.Review
			if review == nil || !review.Evidence {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s; plan evidence unavailable\n", node.Node, node.Status)
			} else {
				c := review.Counts
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: changes=%t; %d create, %d update, %d delete, %d replace; %d output entries; approve=%s (%s, assessment only)\n", node.Node, *review.HasChanges, c.Create, c.Update, c.Delete, c.Replace, len(review.Outputs), review.Policy, review.Decision)
			}
			if review != nil {
				for _, d := range review.Diagnostics {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s: %s; %s\n", node.Node, d.Code, d.Message, d.Remedy)
				}
				for _, limitation := range review.Limitations {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", limitation)
				}
			}
		}
	}
	return err
}
