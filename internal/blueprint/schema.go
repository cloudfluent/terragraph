package blueprint

import "github.com/hashicorp/hcl/v2"

// BlockAttributes reports the attributes each block type accepts, keyed by block name, taken straight from the schemas the parser itself uses.
//
// It exists so that anything presenting the blueprint language to a user — editor completion in internal/language, most obviously — can be checked against what the parser actually accepts, rather than against a second hand-maintained list that drifts. It drifted once already: `approve` was added to node and use blocks without the editor learning about it, so the attribute parsed fine and was never offered.
func BlockAttributes() map[string][]string {
	schemas := map[string]*hcl.BodySchema{
		"node":         nodeSchema,
		"edge":         edgeSchema,
		"relationship": relationshipSchema,
		"use":          useSchema,
		"runtime":      runtimeSchema,
		"vendor":       vendorSchema,
		"tfvars":       tfvarsSchema,
		"lock":         lockSchema,
		"s3":           lockS3Schema,
	}

	out := make(map[string][]string, len(schemas))
	for block, schema := range schemas {
		names := make([]string, 0, len(schema.Attributes))
		for _, attr := range schema.Attributes {
			names = append(names, attr.Name)
		}
		out[block] = names
	}
	return out
}
