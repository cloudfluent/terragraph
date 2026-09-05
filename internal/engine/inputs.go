package engine

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// resolveInputs gathers the values for name's inputs (every data edge pointing at it, plus its own literal Vars), checking each one against the target variable's declared type. Sources are tried in a fixed order — outputs already captured earlier in the current run (keyed by node name), then that upstream node's own existing state read live — and only when the graph opted into snapshots and the live read failed, the node's published output snapshot as a last resort (see snapshot.go): never ahead of the live read, so it can never serve a value newer infrastructure has moved past.
func (e *Engine) resolveInputs(name string, applied map[string]map[string]any) (map[string]any, error) {
	vars := map[string]any{}

	for _, edge := range e.Graph.Edges {
		if !edge.IsDataEdge() || edge.To.Node != name {
			continue
		}

		outputs, ok := applied[edge.From.Node]
		if !ok {
			var err error
			outputs, err = e.runner(edge.From.Node).Outputs()
			if err != nil {
				// The snapshot is a last resort, never a preference: consulted
				// only after the live read has failed, and only when the graph
				// opted in (Graph.Snapshots). Reading it any earlier resurrects
				// the removed incremental-apply cache under a new name — worst
				// on destroy, where these values feed a resource's count or
				// for_each and a stale value changes what gets torn down.
				found := false
				if e.Graph.Snapshots {
					outputs, found = e.readSnapshot(edge.From.Node)
				}
				if !found {
					return nil, fmt.Errorf(
						"resolving %s: upstream node %q has not been applied yet (%w)",
						edge.To, edge.From.Node, err,
					)
				}
			}
		}

		val, ok := outputs[edge.From.Name]
		if !ok {
			return nil, fmt.Errorf(
				"resolving %s: node %q has no output value %q; apply it first",
				edge.To, edge.From.Node, edge.From.Name,
			)
		}

		if err := e.checkType(edge, val); err != nil {
			return nil, err
		}

		vars[edge.To.Name] = val
	}

	// graph.Validate already rejects a variable set by both an edge and Vars as a structural error, but resolveInputs runs on whatever graph it's handed (e.g. in a future --node-scoped run that skips Validate), so it re-checks rather than trusting that pass ran first.
	for varName, val := range e.Graph.Nodes[name].Vars {
		if _, conflict := vars[varName]; conflict {
			return nil, fmt.Errorf("node.%s.input.%s: set by both a data edge and vars; remove one", name, varName)
		}
		if err := e.checkVarType(name, varName, val); err != nil {
			return nil, err
		}
		vars[varName] = val
	}

	return vars, nil
}

// checkType verifies a concrete value resolved from a data edge against the target variable's declared type constraint. See checkVarType, which does the actual check and is shared with a node's own literal Vars.
func (e *Engine) checkType(edge blueprint.Edge, val any) error {
	if err := e.checkVarType(edge.To.Node, edge.To.Name, val); err != nil {
		return fmt.Errorf("value from %s: %w", edge.From, err)
	}
	return nil
}

// checkVarType verifies val against the type constraint node.varName declares, if any. This is an exact runtime check, not static inference: by the time either a data edge or a literal Vars entry supplies a value it's already concrete, so it's decoded straight against the target's cty.Type using the same mechanism Terraform itself uses to load *.tfvars.json. A variable with no declared type constraint is skipped (nothing to check against).
func (e *Engine) checkVarType(nodeName, varName string, val any) error {
	v, ok := e.Graph.Nodes[nodeName].Schema.Variables[varName]
	if !ok || v.Type == "" {
		return nil
	}

	typeExpr, diags := hclsyntax.ParseExpression([]byte(v.Type), "<type constraint>", hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("node.%s.input.%s: internal error parsing declared type %q: %s", nodeName, varName, v.Type, diags.Error())
	}
	ctyType, diags := typeexpr.TypeConstraint(typeExpr)
	if diags.HasErrors() {
		return fmt.Errorf("node.%s.input.%s: internal error resolving declared type %q: %s", nodeName, varName, v.Type, diags.Error())
	}

	data, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("node.%s.input.%s: encoding value for type check: %w", nodeName, varName, err)
	}
	if _, err := ctyjson.Unmarshal(data, ctyType); err != nil {
		return fmt.Errorf("node.%s.input.%s: does not match declared type %s: %w", nodeName, varName, v.Type, err)
	}
	return nil
}
