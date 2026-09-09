package language

import (
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// backendAddressDiagnostics reuses execution's parser so incomplete editor documents cannot acquire a different address-rule language.
func backendAddressDiagnostics(body *hclsyntax.Body) []Diagnostic {
	var result []Diagnostic
	for _, block := range body.Blocks {
		if block.Type == "group" {
			result = append(result, backendAddressDiagnostics(block.Body)...)
		}
		if block.Type != "node" && block.Type != "use" {
			continue
		}
		attr := block.Body.Attributes["backend_address"]
		if attr == nil {
			continue
		}
		if _, err := blueprint.ParseBackendAddress(attr.AsHCLAttribute()); err != nil {
			rng := attr.Expr.Range()
			result = append(result, Diagnostic{Start: rng.Start.Byte, End: rng.End.Byte, Message: err.Error()})
		}
	}
	return result
}
