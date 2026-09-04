package graph

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// edgeContractProblems checks the reviewed boundary against both endpoint modules so a contract cannot be internally compatible while contradicting the Terraform configuration that will execute it.
func edgeContractProblems(g *Graph) []Problem {
	var problems []Problem
	for _, edge := range g.Edges {
		if edge.Contract == nil {
			continue
		}
		if !edge.IsDataEdge() {
			problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s -> %s: contract is only valid on a data edge; remove it or name output and input ports", edge.From, edge.To)})
			continue
		}

		producer := edge.Contract.Producer
		consumer := edge.Contract.Consumer
		compatible, err := safelyConvertible(producer.Type, consumer.Type)
		if err != nil {
			problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s -> %s: invalid contract type: %v", edge.From, edge.To, err)})
		} else if !compatible {
			problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s (%s) -> %s (%s): contract types are not safely convertible; change the producer guarantee or consumer requirement", edge.From, producer.Type, edge.To, consumer.Type)})
		}
		if producer.Nullable && !consumer.Nullable {
			problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s -> %s: producer may be null but consumer requires non-null; change one side of the contract", edge.From, edge.To)})
		}
		if producer.Sensitive && !consumer.Sensitive {
			problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s -> %s: producer is sensitive but consumer does not accept sensitive values; mark the consumer sensitive or remove the binding", edge.From, edge.To)})
		}

		from := g.Nodes[edge.From.Node]
		if output, ok := from.Schema.OutputDetails[edge.From.Name]; ok {
			if output.Type != "" {
				proven, typeErr := safelyConvertible(output.Type, producer.Type)
				if typeErr != nil || !proven {
					problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s: producer contract type %s does not match module output type %s; update the contract or output", edge.From, producer.Type, output.Type)})
				}
			}
			if output.Sensitive && !producer.Sensitive {
				problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s: module output is sensitive but producer contract is not; set sensitive = true", edge.From)})
			}
		}

		to := g.Nodes[edge.To.Node]
		if input, ok := to.Schema.Variables[edge.To.Name]; ok {
			if input.Type != "" {
				accepted, typeErr := safelyConvertible(consumer.Type, input.Type)
				if typeErr != nil || !accepted {
					problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s: consumer contract type %s does not match module input type %s; update the contract or variable", edge.To, consumer.Type, input.Type)})
				}
			}
			if consumer.Sensitive && !input.Sensitive {
				problems = append(problems, Problem{Severity: SeverityError, Message: fmt.Sprintf("%s: consumer accepts sensitive values but module input is not declared sensitive; set sensitive = true on the variable", edge.To)})
			}
		}
	}
	return problems
}

func safelyConvertible(from, to string) (bool, error) {
	fromType, err := contractType(from)
	if err != nil {
		return false, err
	}
	toType, err := contractType(to)
	if err != nil {
		return false, err
	}
	return fromType.Equals(toType) || convert.GetConversion(fromType, toType) != nil, nil
}

func contractType(raw string) (cty.Type, error) {
	expr, diags := hclsyntax.ParseExpression([]byte(raw), "<contract type>", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("type %q is invalid: %s", raw, diags.Error())
	}
	resolved, diags := typeexpr.TypeConstraint(expr)
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("type %q is invalid: %s", raw, diags.Error())
	}
	return resolved, nil
}
