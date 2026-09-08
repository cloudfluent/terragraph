package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// A missing value must not disguise a credential or provider failure as bootstrap.
var errUpstreamOutputMissing = errors.New("upstream output is unavailable")

// InputBasis records the source of a value without duplicating the value in a review report.
type InputBasis struct{ Input, Node, Output, Source string }

// resolveInputs gathers the values for name's inputs (every data edge pointing at it, plus its own literal Vars), checking each one against the target variable's declared type. Sources are tried in a fixed order — outputs already captured earlier in the current run (keyed by node name), then that upstream node's own existing state read live — and only when the graph opted into snapshots and the live read failed, the node's published output snapshot as a last resort (see snapshot.go): never ahead of the live read, so stale snapshots cannot displace a successful live read.
func (e *Engine) resolveInputs(name string, applied map[string]exec.Outputs) (map[string]any, error) {
	return e.resolveInputsWithBasis(name, applied, nil)
}

func (e *Engine) resolveInputsWithBasis(name string, applied map[string]exec.Outputs, basis *[]InputBasis) (map[string]any, error) {
	vars := map[string]any{}
	// A successful upstream read is shared across edges so one node never mixes state revisions.
	liveOutputs := map[string]exec.Outputs{}

	for _, edge := range e.Graph.Edges {
		if !edge.IsDataEdge() || edge.To.Node != name {
			continue
		}

		source := "same_run"
		outputs, ok := applied[edge.From.Node]
		if !ok {
			source = "live"
			outputs, ok = liveOutputs[edge.From.Node]
		}
		if !ok {
			source = "live"
			live, err := e.runner(edge.From.Node).Outputs()
			outputs = live
			if err == nil {
				liveOutputs[edge.From.Node] = live
			}
			if err != nil {
				// Cancellation must not fall back to disk, where stale values can obscure the cause or block a run that is already stopping.
				if cancelled := e.context().Err(); cancelled != nil {
					return nil, fmt.Errorf("resolving %s: %w", edge.To, cancelled)
				}
				if basis != nil && !e.Graph.Snapshots {
					if path, known := graph.LocalStatePath(e.Graph.Nodes[edge.From.Node]); known {
						if _, err := os.Stat(path); os.IsNotExist(err) {
							*basis = append(*basis, InputBasis{Input: edge.To.Name, Node: edge.From.Node, Output: edge.From.Name, Source: "unavailable"})
							return nil, fmt.Errorf("node.%s.input.%s: %w from node %s; recover existing local state or apply the upstream first", name, edge.To.Name, errUpstreamOutputMissing, edge.From.Node)
						}
					}
				}
				// The snapshot is a last resort, never a preference: consulted only after the live read has failed, and only when the graph opted in (Graph.Snapshots). Reading it any earlier resurrects the removed incremental-apply cache under a new name — worst on destroy, where these values feed a resource's count or for_each and a stale value changes what gets torn down.
				found := false
				if e.Graph.Snapshots {
					var snapshot snapshotFile
					snapshot, found = e.readSnapshot(edge.From.Node)
					if found {
						if !e.snapshotOutputAllowed(edge.From.Node, edge.From.Name) || slices.Contains(snapshot.Withheld, edge.From.Name) {
							return nil, fmt.Errorf("resolving %s: %s was withheld from output snapshots; sensitive outputs and outputs without verified sensitivity metadata cannot be reused; restore live upstream outputs or apply the upstream in this run: %w", edge.To, edge.From, err)
						}
						outputs = make(exec.Outputs, len(snapshot.Outputs))
						// Snapshot loading has already withheld values without verified public metadata.
						public := false
						for key, value := range snapshot.Outputs {
							outputs[key] = exec.Output{Value: value, Sensitive: &public}
						}
						source = "snapshot"
					}
				}
				if !found {
					return nil, fmt.Errorf(
						"resolving %s: reading existing outputs from upstream node %q failed: %w; check backend initialization and credentials",
						edge.To, edge.From.Node, err,
					)
				}
			}
		}

		if basis != nil {
			*basis = append(*basis, InputBasis{Input: edge.To.Name, Node: edge.From.Node, Output: edge.From.Name, Source: source})
		}
		val, ok := outputs[edge.From.Name]
		if !ok {
			return nil, fmt.Errorf(
				"resolving %s: node %q has no output value %q: %w; restore live upstream outputs or apply the upstream first",
				edge.To, edge.From.Node, edge.From.Name, errUpstreamOutputMissing,
			)
		}

		if err := e.checkType(edge, val); err != nil {
			return nil, err
		}

		vars[edge.To.Name] = val.Value
	}

	// graph.Validate already rejects a variable set by both an edge and Vars as a structural error, but resolveInputs runs on whatever graph it's handed (e.g. in a future --node-scoped run that skips Validate), so it re-checks rather than trusting that pass ran first.
	for varName, val := range e.Graph.Nodes[name].Vars {
		if _, conflict := vars[varName]; conflict {
			return nil, fmt.Errorf("node.%s.input.%s: set by both a data edge and vars; remove one", name, varName)
		}
		if err := e.checkVarType(name, varName, val, false); err != nil {
			return nil, err
		}
		vars[varName] = val
	}

	return vars, nil
}

// checkType verifies a concrete value resolved from a data edge against the target variable's declared type constraint. See checkVarType, which does the actual check and is shared with a node's own literal Vars.
func (e *Engine) checkType(edge blueprint.Edge, output exec.Output) error {
	sourceSensitive := e.Graph.Nodes[edge.From.Node].Schema.OutputDetails[edge.From.Name].Sensitive
	// Runtime metadata survives live reads and level boundaries so conversion failures cannot expose protected payloads.
	sourceSensitive = sourceSensitive || output.Sensitive == nil || *output.Sensitive
	if err := e.checkVarType(edge.To.Node, edge.To.Name, output.Value, sourceSensitive); err != nil {
		return fmt.Errorf("value from %s: %w", edge.From, err)
	}
	return nil
}

// checkVarType checks concrete input convertibility with cty and optional attribute defaults; the original value is still passed to Terraform so its variable handling owns the final conversion.
func (e *Engine) checkVarType(nodeName, varName string, val any, sourceSensitive bool) (err error) {
	v, ok := e.Graph.Nodes[nodeName].Schema.Variables[varName]
	if !ok || v.Type == "" {
		return nil
	}

	typeExpr, diags := hclsyntax.ParseExpression([]byte(v.Type), "<type constraint>", hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("node.%s.input.%s: internal error parsing declared type %q: %s", nodeName, varName, v.Type, diags.Error())
	}
	ctyType, defaults, diags := typeexpr.TypeConstraintWithDefaults(typeExpr)
	if diags.HasErrors() {
		return fmt.Errorf("node.%s.input.%s: internal error resolving declared type %q: %s", nodeName, varName, v.Type, diags.Error())
	}

	// Encoding and conversion errors can contain payload keys; replace the error rather than wrapping it so callers cannot recover sensitive details from the chain.
	if v.Sensitive || sourceSensitive {
		defer func() {
			if err != nil {
				err = fmt.Errorf("node.%s.input.%s: cannot validate sensitive value against declared type %s; value details withheld; check the input value against the module variable declaration", nodeName, varName, v.Type)
			}
		}()
	}

	data, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("node.%s.input.%s: encoding value for type check: %w", nodeName, varName, err)
	}
	// Decode the JSON's actual shape first: decoding against the constraint mistakes any for cty's typed JSON wrapper and rejects extra object attributes before conversion can discard them.
	var concrete ctyjson.SimpleJSONValue
	if err := json.Unmarshal(data, &concrete); err != nil {
		return fmt.Errorf("node.%s.input.%s: decoding value for type check: %w", nodeName, varName, err)
	}
	value := concrete.Value
	if defaults != nil {
		value = defaults.Apply(value)
	}
	if _, err := convert.Convert(value, ctyType); err != nil {
		return fmt.Errorf("node.%s.input.%s: does not match declared type %s: %w", nodeName, varName, v.Type, err)
	}
	return nil
}
