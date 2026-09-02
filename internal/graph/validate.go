package graph

import (
	"fmt"
	"sort"
	"strings"
)

// Severity distinguishes a hard failure from an advisory finding.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

// Problem is one validation issue found in a Graph. Validate collects all of them in one pass so a user fixing typos in a large blueprint sees every mistake at once instead of one-at-a-time.
type Problem struct {
	Severity Severity
	Message  string
}

// IsError reports whether this problem should block graph/plan/apply, as opposed to a Warning, which is only surfaced.
func (p Problem) IsError() bool { return p.Severity == SeverityError }

// Validate checks a built Graph for issues that Build itself does not catch: edges referencing ports that don't actually exist on the target module (Error), two data edges targeting the same input, whether their from sides differ or match (Error; checked here rather than at parse so group expansion can rewrite a use export onto leaf ports first), a node's own vars targeting a variable the module doesn't declare or one a data edge already targets (Error), cycles in the dependency graph (Error), and required variables that neither an edge nor vars ever supplies a value for (Warning: a module may legitimately get such a value from its own terraform.tfvars or the environment, outside the blueprint entirely).
func Validate(g *Graph) []Problem {
	var problems []Problem

	wired := make(map[string]bool)
	reportedMulti := make(map[string]bool)
	for _, e := range g.Edges {
		if !e.IsDataEdge() {
			continue
		}
		from := g.Nodes[e.From.Node]
		if !from.Schema.HasOutput(e.From.Name) {
			problems = append(problems, Problem{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: node %q has no output named %q", e.From, e.From.Node, e.From.Name),
			})
		}
		to := g.Nodes[e.To.Node]
		if !to.Schema.HasVariable(e.To.Name) {
			problems = append(problems, Problem{
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s: node %q has no input variable named %q", e.To, e.To.Node, e.To.Name),
			})
		}
		key := e.To.Node + "." + e.To.Name
		if wired[key] {
			if !reportedMulti[key] {
				problems = append(problems, Problem{
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s.input.%s: set by more than one data edge; remove extras", e.To.Node, e.To.Name),
				})
				reportedMulti[key] = true
			}
			continue
		}
		wired[key] = true
	}

	names := make([]string, 0, len(g.Nodes))
	for name := range g.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		node := g.Nodes[name]
		keys := make([]string, 0, len(node.Vars))
		for varName := range node.Vars {
			keys = append(keys, varName)
		}
		sort.Strings(keys)
		for _, varName := range keys {
			if !node.Schema.HasVariable(varName) {
				problems = append(problems, Problem{
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s.input.%s: vars sets it, but node %q has no such input variable", name, varName, name),
				})
				continue
			}
			key := name + "." + varName
			if wired[key] {
				problems = append(problems, Problem{
					Severity: SeverityError,
					Message:  fmt.Sprintf("node.%s.input.%s: set by both a data edge and vars; remove one", name, varName),
				})
				continue
			}
			wired[key] = true
		}
	}

	for _, name := range names {
		node := g.Nodes[name]
		varNames := make([]string, 0, len(node.Schema.Variables))
		for varName := range node.Schema.Variables {
			varNames = append(varNames, varName)
		}
		sort.Strings(varNames)
		for _, varName := range varNames {
			v := node.Schema.Variables[varName]
			if v.Required && !wired[name+"."+varName] {
				problems = append(problems, Problem{
					Severity: SeverityWarning,
					Message:  fmt.Sprintf("node.%s.input.%s: required variable %q has no edge feeding it (must be supplied some other way, e.g. terraform.tfvars)", name, varName, varName),
				})
			}
		}
	}

	for _, cycle := range FindCycles(g) {
		problems = append(problems, Problem{
			Severity: SeverityError,
			Message:  fmt.Sprintf("cycle detected: %s", strings.Join(cycle, " -> ")),
		})
	}

	return problems
}

// FindCycle returns the node names in one cyclic strongly-connected component, or nil if the graph is acyclic. Kept for callers that just want a short, single-cycle error message (topo.go); Validate uses FindCycles to report every cyclic cluster in the graph, not just one.
func FindCycle(g *Graph) []string {
	cycles := FindCycles(g)
	if len(cycles) == 0 {
		return nil
	}
	return cycles[0]
}
