package language

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/cloudfluent/terragraph/internal/module"
)

func contractPortPath(blocks []string) bool {
	for i := 1; i < len(blocks); i++ {
		if blocks[i-1] == "producer" && blocks[i] == "output" || blocks[i-1] == "consumer" && blocks[i] == "input" {
			return true
		}
	}
	return false
}

func contractTypeCompletions(text []byte, offset int) []Completion {
	start := traversalStart(text, offset)
	prefix := string(text[start:offset])
	return stringCompletions([]string{"string", "number", "bool", "any", "list", "set", "map", "object", "tuple", "optional"}, prefix, func(name string) Completion {
		insert := name
		switch name {
		case "list", "set", "map", "optional":
			insert += "(string)"
		case "object":
			insert += "({})"
		case "tuple":
			insert += "([])"
		}
		return Completion{Label: name, Insert: insert, Detail: "Terraform type constraint", Documentation: "Contract types check structure without primitive coercion. Put defaults in the module variable declaration.", Start: start, End: offset}
	})
}

// contractDiagnostics uses HCL's type parser at the original expression range instead of evaluating type constructors as runtime functions.
func contractDiagnostics(body *hclsyntax.Body) []Diagnostic {
	var result []Diagnostic
	for _, block := range body.Blocks {
		if block.Type == "producer" || block.Type == "consumer" {
			for _, port := range block.Body.Blocks {
				attr := port.Body.Attributes["type"]
				if attr == nil {
					continue
				}
				expr := hcl.Expression(attr.Expr)
				if value, diags := expr.Value(nil); !diags.HasErrors() && value.IsKnown() && !value.IsNull() && value.Type() == cty.String {
					parsed, pd := hclsyntax.ParseExpression([]byte(value.AsString()), expr.Range().Filename, expr.Range().Start)
					if pd.HasErrors() {
						rng := expr.Range()
						result = append(result, Diagnostic{rng.Start.Byte, rng.End.Byte, "Invalid contract type; use a Terraform type constraint"})
						continue
					}
					expr = parsed
				}
				_, diags := typeexpr.TypeConstraint(expr)
				for _, diag := range diags {
					if diag.Severity != hcl.DiagError {
						continue
					}
					rng := attr.Expr.Range()
					result = append(result, Diagnostic{rng.Start.Byte, rng.End.Byte, diag.Summary + ": " + diag.Detail})
				}
			}
		}
		if block.Type == "group" {
			result = append(result, contractDiagnostics(block.Body)...)
		}
	}
	return result
}

// Hover explains the source declaration and additional contract separately so omission never appears to remove the module's type.
func (w *Workspace) Hover(_ context.Context, path string, offset int) string {
	path = absolute(path)
	text := w.document(path)
	blocks := openBlocksAt(text, offset)
	for i := 1; i < len(blocks); i++ {
		parent, port := blocks[i-1], blocks[i]
		if parent.name != "producer" && parent.name != "consumer" || port.name != "input" && port.name != "output" {
			continue
		}
		schema, err := module.Inspect(filepath.Join(filepath.Dir(path), parent.label), module.UnknownFiles)
		if err != nil {
			return "Module declaration unavailable; vendor the source or select unambiguous runtime declarations. Contract checks are deferred until actual values are available."
		}
		typ := ""
		if parent.name == "consumer" {
			typ = schema.Variables[port.label].Type
		} else {
			typ = schema.OutputDetails[port.label].Type
		}
		if typ == "" {
			typ = "not declared"
		}
		return fmt.Sprintf("Module %s type: `%s`.\n\nContracts add requirements to this declaration. Type omission keeps the module input check. Actual value checks occur during plan/apply; static information alone is not confirmation.", port.name, strings.ReplaceAll(typ, "`", ""))
	}
	return ""
}
