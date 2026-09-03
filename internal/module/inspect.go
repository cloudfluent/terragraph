// Package module reads the declared variables and outputs of a Terraform/OpenTofu root module directly from its .tf files, without running `terraform init`. It is the single source of truth for what ports a blueprint node exposes, used both to validate edges and, eventually, to populate the Web UI's port lists.
package module

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// Variable is one declared input variable of a root module.
type Variable struct {
	Name string
	// Type is the raw declared type constraint text (e.g. "string", "list(string)"), or "" if the variable has no type constraint.
	Type        string
	Description string
	Sensitive   bool
	Deprecated  string
	// Required is true when the variable has no default value.
	Required bool
}

type Output struct {
	Name        string
	Type        string
	Description string
	Sensitive   bool
	Deprecated  string
}

// Schema is the subset of a root module's shape that terragraph cares about: its declared input variables (with type/required metadata), the names of its output values, and the backend type if any. Standard root module outputs don't declare a type (that's an HCP Terraform Stacks-only feature), so Outputs only tracks presence.
type Schema struct {
	Variables     map[string]Variable
	Outputs       map[string]bool
	OutputDetails map[string]Output
	// Backend is the type label of terraform { backend "TYPE" {} }, or "cloud" if the module declared a cloud block and no backend block. Empty means neither was declared (Terraform's implicit local backend). Attributes inside the block are not captured.
	Backend string
}

// Inspect statically parses the root module at dir and returns its variable/output schema.
func Inspect(dir string) (*Schema, error) {
	mod, diags := tfconfig.LoadModule(dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("inspecting module at %s: %s", dir, diags.Error())
	}

	schema := &Schema{
		Variables:     make(map[string]Variable, len(mod.Variables)),
		Outputs:       make(map[string]bool, len(mod.Outputs)),
		OutputDetails: make(map[string]Output, len(mod.Outputs)),
	}
	for name, v := range mod.Variables {
		schema.Variables[name] = Variable{
			Name:        name,
			Type:        v.Type,
			Description: v.Description,
			Sensitive:   v.Sensitive,
			Deprecated:  v.Deprecated,
			Required:    v.Required,
		}
	}
	for name, output := range mod.Outputs {
		schema.Outputs[name] = true
		schema.OutputDetails[name] = Output{Name: name, Type: output.Type, Description: output.Description, Sensitive: output.Sensitive, Deprecated: output.Deprecated}
	}
	schema.Backend = inspectBackend(dir)
	return schema, nil
}

var terraformFileSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{{Type: "terraform"}},
}

var terraformBackendSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "backend", LabelNames: []string{"type"}},
		{Type: "cloud"},
	},
}

// inspectBackend returns the module's backend type label, "cloud" if only a cloud block is present, or "" if neither was declared. terraform-config-inspect does not expose this; we scan .tf / .tf.json ourselves and ignore backend attributes.
func inspectBackend(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	parser := hclparse.NewParser()
	backend := ""
	cloud := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			continue
		}
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var file *hcl.File
		var diags hcl.Diagnostics
		switch {
		case strings.HasSuffix(name, ".tf.json"):
			file, diags = parser.ParseJSON(src, path)
		case strings.HasSuffix(name, ".tf"):
			file, diags = parser.ParseHCL(src, path)
		default:
			continue
		}
		if diags.HasErrors() || file == nil {
			continue
		}
		content, _, _ := file.Body.PartialContent(terraformFileSchema)
		for _, block := range content.Blocks {
			inner, _, _ := block.Body.PartialContent(terraformBackendSchema)
			for _, b := range inner.Blocks {
				switch b.Type {
				case "backend":
					if len(b.Labels) > 0 && b.Labels[0] != "" {
						backend = b.Labels[0]
					}
				case "cloud":
					cloud = true
				}
			}
		}
	}
	if backend != "" {
		return backend
	}
	if cloud {
		return "cloud"
	}
	return ""
}

// HasOutput reports whether the module declares an output with this name.
func (s *Schema) HasOutput(name string) bool { return s.Outputs[name] }

// HasVariable reports whether the module declares a variable with this name.
func (s *Schema) HasVariable(name string) bool {
	_, ok := s.Variables[name]
	return ok
}
