package engine

import (
	"fmt"
	"sort"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/zclconf/go-cty/cty"
)

// ContractCheck reports only port-level facts; payload keys may themselves contain secrets.
type ContractCheck struct {
	Port      string
	Condition string
	Result    blueprint.ContractResult
}

func (e *Engine) nodeContracts(name string) *blueprint.DirContracts {
	node := e.Graph.Nodes[name]
	key := node.Dir
	if blueprint.IsRemote(node.Source) {
		key = node.Source
	}
	return e.Graph.Contracts.Lookup(key)
}

func (e *Engine) hasContracts(name string) bool {
	dc := e.nodeContracts(name)
	return dc != nil && (len(dc.Producer) > 0 || len(dc.Consumer) > 0)
}

func contractPorts(ports map[string]blueprint.PortContract) []string {
	names := make([]string, 0, len(ports))
	for name := range ports {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// checkPort keeps null, type, and sensitivity evidence separate so an any claim never hides a non-null requirement.
func checkPort(path string, p blueprint.PortContract, value cty.Value, sensitive *bool) []ContractCheck {
	var checks []ContractCheck
	add := func(condition string, result blueprint.ContractResult) {
		checks = append(checks, ContractCheck{path, condition, result})
	}
	if p.Type != "" {
		typ, err := blueprint.ContractType(p.Type)
		if err != nil {
			add("type", blueprint.ContractViolation)
		} else {
			add("type", blueprint.CheckContractType(value, typ))
		}
	}
	if p.Nullable != nil && !*p.Nullable {
		result := blueprint.ContractConfirmed
		if value == cty.NilVal || !value.IsKnown() {
			result = blueprint.ContractDeferred
		} else if value.IsNull() {
			result = blueprint.ContractViolation
		}
		add("nullable", result)
	}
	if p.Sensitive != nil {
		result := blueprint.ContractConfirmed
		if sensitive == nil {
			result = blueprint.ContractDeferred
		} else if *sensitive != *p.Sensitive {
			result = blueprint.ContractViolation
		}
		add("sensitive", result)
	}
	if len(checks) == 0 {
		add("value", blueprint.ContractUnconstrained)
	}
	return checks
}

func (e *Engine) contractPolicy(checks []ContractCheck, requireKnown bool) error {
	for _, check := range checks {
		if check.Result != blueprint.ContractViolation && check.Result != blueprint.ContractDeferred {
			continue
		}
		code := "C010"
		remedy := "correct the contract or the module value"
		if check.Result == blueprint.ContractDeferred {
			code = "C011"
			remedy = "restore current typed outputs or use a supported saved plan; apply the upstream in this run for null outputs"
		}
		err := fmt.Errorf("%s: contract.[%s] %s %s; %s", check.Port, code, check.Condition, check.Result, remedy)
		if e.Graph.ContractMode == "enforce" && (check.Result == blueprint.ContractViolation || requireKnown) {
			return err
		}
		e.logger().Warn(err.Error())
	}
	return nil
}

func (e *Engine) outputContractChecks(name string, outputs exec.Outputs, complete bool) []ContractCheck {
	dc := e.nodeContracts(name)
	if dc == nil {
		return nil
	}
	var checks []ContractCheck
	for _, port := range contractPorts(dc.Producer) {
		p := dc.Producer[port]
		output, ok := outputs[port]
		if !ok && !complete {
			continue
		}
		value := cty.NilVal
		if ok {
			v, err := output.CtyValue()
			if err == nil {
				value = v
				if len(output.Type) == 0 {
					value = withoutCollectionEvidence(value)
				}
			}
		}
		sensitive := output.Sensitive
		if e.Graph.Nodes[name].Schema.OutputDetails[port].Sensitive {
			sensitive = new(true)
		}
		checks = append(checks, checkPort("node."+name+".output."+port, p, value, sensitive)...)
	}
	return checks
}

func (e *Engine) validateOutputContracts(name string, outputs exec.Outputs) error {
	return e.contractPolicy(e.outputContractChecks(name, outputs, true), true)
}

// inspectContractPlan checks selected external inputs too, since managed vars alone cannot establish what the module receives.
func (e *Engine) inspectContractPlan(name string, r *exec.Runner, path string, requireInputs bool) (*exec.PlanValues, []ContractCheck, error) {
	if !e.hasContracts(name) {
		return nil, nil, nil
	}
	values, err := r.PlanValues(path)
	if err != nil {
		checks := []ContractCheck{{"node." + name, "plan evidence", blueprint.ContractDeferred}}
		if policyErr := e.contractPolicy(checks, requireInputs); policyErr != nil {
			return nil, checks, fmt.Errorf("%w: %w", policyErr, err)
		}
		return nil, checks, nil
	}
	inputs := e.inputContractChecks(name, values)
	outputs := e.outputContractChecks(name, values.Outputs, true)
	checks := append(append([]ContractCheck{}, inputs...), outputs...)
	if err := e.contractPolicy(inputs, requireInputs); err != nil {
		return values, checks, err
	}
	// Computed outputs may require this producer's apply, but known contradictions must stop it before mutation.
	if err := e.contractPolicy(outputs, false); err != nil {
		return values, checks, err
	}
	return values, checks, nil
}

// completePlanOutputs restores null only from this successful plan; missing live output alone is never null evidence.
func (e *Engine) completePlanOutputs(name string, outputs exec.Outputs, plan *exec.PlanValues) exec.Outputs {
	if plan == nil {
		return outputs
	}
	if outputs == nil {
		outputs = exec.Outputs{}
	}
	for port, output := range plan.Outputs {
		if _, exists := outputs[port]; exists || !e.Graph.Nodes[name].Schema.HasOutput(port) {
			continue
		}
		value, err := output.CtyValue()
		if err == nil && value.IsKnown() && value.IsNull() {
			outputs[port] = output
		}
	}
	return outputs
}

// inputContractChecks also serves destroy plans, whose deleted outputs make no future producer claim.
func (e *Engine) inputContractChecks(name string, values *exec.PlanValues) []ContractCheck {
	var inputs []ContractCheck
	dc := e.nodeContracts(name)
	if dc != nil {
		for _, port := range contractPorts(dc.Consumer) {
			p := dc.Consumer[port]
			variable, exists := e.Graph.Nodes[name].Schema.Variables[port]
			value := cty.NilVal
			if raw, present := values.Variables[port]; exists && present && !variable.Ephemeral {
				if chosen, err := raw.CtyValue(); err == nil {
					if effective, err := variable.EffectiveValue(chosen); err == nil {
						value = effective
					}
				}
			}

			sensitive := new(variable.Sensitive)
			inputs = append(inputs, checkPort("node."+name+".input."+port, p, value, sensitive)...)
		}
	}
	return inputs
}

// withoutCollectionEvidence refuses to infer list/set identity from an old snapshot's JSON array while retaining known sibling facts.
func withoutCollectionEvidence(value cty.Value) cty.Value {
	if !value.IsKnown() || value.IsNull() {
		return value
	}
	typ := value.Type()
	if typ.IsListType() || typ.IsSetType() || typ.IsTupleType() {
		return cty.DynamicVal
	}
	if typ.IsObjectType() || typ.IsMapType() {
		attrs := map[string]cty.Value{}
		for it := value.ElementIterator(); it.Next(); {
			key, v := it.Element()
			attrs[key.AsString()] = withoutCollectionEvidence(v)
		}
		return cty.ObjectVal(attrs)
	}
	return value
}
