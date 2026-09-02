// Package module reads the declared variables and outputs of a Terraform/OpenTofu root module directly from its .tf files, without running `terraform init`. It is the single source of truth for what ports a blueprint node exposes, used both to validate edges and, eventually, to populate the Web UI's port lists.
package module

import (
	"fmt"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// Variable is one declared input variable of a root module.
type Variable struct {
	Name string
	// Type is the raw declared type constraint text (e.g. "string", "list(string)"), or "" if the variable has no type constraint.
	Type string
	// Required is true when the variable has no default value.
	Required bool
}

// Schema is the subset of a root module's shape that terragraph cares about: its declared input variables (with type/required metadata) and the names of its output values. Standard root module outputs don't declare a type (that's an HCP Terraform Stacks-only feature), so Outputs only tracks presence.
type Schema struct {
	Variables map[string]Variable
	Outputs   map[string]bool
}

// Inspect statically parses the root module at dir and returns its variable/output schema.
func Inspect(dir string) (*Schema, error) {
	mod, diags := tfconfig.LoadModule(dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("inspecting module at %s: %s", dir, diags.Error())
	}

	schema := &Schema{
		Variables: make(map[string]Variable, len(mod.Variables)),
		Outputs:   make(map[string]bool, len(mod.Outputs)),
	}
	for name, v := range mod.Variables {
		schema.Variables[name] = Variable{
			Name:     name,
			Type:     v.Type,
			Required: v.Required,
		}
	}
	for name := range mod.Outputs {
		schema.Outputs[name] = true
	}
	return schema, nil
}

// HasOutput reports whether the module declares an output with this name.
func (s *Schema) HasOutput(name string) bool { return s.Outputs[name] }

// HasVariable reports whether the module declares a variable with this name.
func (s *Schema) HasVariable(name string) bool {
	_, ok := s.Variables[name]
	return ok
}
