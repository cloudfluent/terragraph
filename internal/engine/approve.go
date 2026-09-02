package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
)

// approveFor resolves how much of a node's plan may be applied, following the same layering `runtime` and `env` already use (see runtimeFor): the node's own declaration wins, then whatever an enclosing `use` cascaded to it, then the run-wide default from --approve, then ApproveSafe.
//
// The CLI can therefore only fill a gap nothing else spoke to. --approve=all does not override a node that declared `approve = "safe"`; that is the safe direction, and it is the same rule the blueprint already relies on for runtime.
func (e *Engine) approveFor(name string, runDefault blueprint.Approve) blueprint.Approve {
	if a := e.Graph.Nodes[name].Approve; a != "" {
		return a
	}
	if runDefault != "" {
		return runDefault
	}
	return blueprint.ApproveSafe
}

// summarizeChanges renders a plan the way Terraform counts one, so a forty-node run can be read at a glance instead of as forty walls of subcommand output. A replacement counts once on each side, as Terraform's own summary line does.
func summarizeChanges(changes []exec.ResourceChange) string {
	var add, change, destroy int
	for _, c := range changes {
		if c.IsReplace() {
			add++
			destroy++
			continue
		}
		for _, a := range c.Actions {
			switch a {
			case "create":
				add++
			case "update":
				change++
			case "delete":
				destroy++
			}
		}
	}
	return fmt.Sprintf("%d to add, %d to change, %d to destroy", add, change, destroy)
}

// describeAction names what a change does in the vocabulary the approve levels are written in.
func describeAction(c exec.ResourceChange) string {
	if c.IsReplace() {
		return "replace"
	}
	for _, a := range c.Actions {
		if a != "no-op" && a != "read" {
			return a
		}
	}
	return "no-op"
}

// notPermitted returns the changes a plan makes that its approve level does not allow, sorted by address so the same plan always reports them in the same order.
func notPermitted(changes []exec.ResourceChange, level blueprint.Approve) []exec.ResourceChange {
	var blocked []exec.ResourceChange
	for _, c := range changes {
		for _, a := range c.Actions {
			if !level.Permits(a) {
				blocked = append(blocked, c)
				break
			}
		}
	}
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Address < blocked[j].Address })
	return blocked
}

// gateError explains what a node's plan wanted to do that it was not permitted to, and how to permit it. It names both routes deliberately: the blueprint declaration is the durable answer for a node whose plan is destructive by design, and the flag is the one-off.
func gateError(name string, level blueprint.Approve, blocked []exec.ResourceChange) error {
	var b strings.Builder
	fmt.Fprintf(&b, "node %s plans %d change(s) its approve level (%s) does not permit:", name, len(blocked), level)
	for _, c := range blocked {
		fmt.Fprintf(&b, "\n  %s  %s", c.Address, describeAction(c))
	}
	b.WriteString("\n\nStopped before applying, so no later level ran.")
	fmt.Fprintf(&b, "\nIf this is intended, declare approve = %q on that node, or re-run with --approve=all.", blueprint.ApproveAll)
	return fmt.Errorf("%s", b.String())
}
