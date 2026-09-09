package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// PlanReview separates observed actions from a future graph apply that will resolve new upstream values.
type PlanReview struct {
	Evidence       bool
	HasChanges     *bool
	Resources      []exec.ResourceChange
	Outputs        []exec.OutputChange
	Policy         blueprint.Approve
	PolicyDecision string
	Inputs         []InputBasis
	Limitations    []string
	Diagnostic     *Diagnostic
}

func newPlanReview(policy blueprint.Approve) *PlanReview {
	return &PlanReview{Resources: []exec.ResourceChange{}, Outputs: []exec.OutputChange{}, Policy: policy, PolicyDecision: "unknown", Inputs: []InputBasis{}, Limitations: []string{"node-local evidence only; apply replans and does not consume this inspection artifact"}}
}

func (review *PlanReview) failure(name, code, phase string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "cancelled"
	}
	if errors.Is(err, errUpstreamOutputMissing) {
		code = "upstream_output_unavailable"
	}
	if errors.Is(err, errPlanBlocked) {
		code = "dependency_not_reached"
	}
	category := "runtime"
	if code == "cancelled" {
		category = "cancelled"
	}
	review.Diagnostic = &Diagnostic{Category: category, Severity: "error", Code: code, Phase: phase, Subject: "node." + name, Message: err.Error(), Remedy: "resolve this diagnostic and rerun plan; this result is not apply approval"}
}

func (review *PlanReview) normalize(changed bool) {
	for _, output := range review.Outputs {
		for _, action := range output.Actions {
			if action != "no-op" {
				changed = true
			}
		}
	}
	for _, resource := range review.Resources {
		for _, action := range resource.Actions {
			if action != "no-op" {
				changed = true
			}
		}
	}
	sort.Slice(review.Resources, func(i, j int) bool { return review.Resources[i].Address < review.Resources[j].Address })
	review.Evidence = true
	review.HasChanges = &changed
	review.PolicyDecision = "pass"
	if len(notPermitted(review.Resources, review.Policy)) != 0 {
		review.PolicyDecision = "block"
	}
	for _, input := range review.Inputs {
		if input.Source == "snapshot" {
			review.Limitations = append(review.Limitations, fmt.Sprintf("input %s uses an opt-in output snapshot, not live state", input.Input))
		} else {
			review.Limitations = append(review.Limitations, fmt.Sprintf("input %s uses existing upstream output, not the upstream's planned value", input.Input))
		}
	}
}
