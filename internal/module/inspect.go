// Package module reads the declared variables and outputs of a Terraform/OpenTofu root module directly from its selected configuration files, without running `terraform init`. It is the single source of truth for what ports a blueprint node exposes, used both to validate edges and, eventually, to populate the Web UI's port lists.
package module

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
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
	// RequiresTofuFiles marks selected OpenTofu-only files so execution can verify the installed runtime understands the statically inspected declarations.
	RequiresTofuFiles bool
	Variables         map[string]Variable
	Outputs           map[string]bool
	OutputDetails     map[string]Output
	// Backend is the type label of terraform { backend "TYPE" {} }, or "cloud" if the module declared a cloud block and no backend block. Empty means neither was declared (Terraform's implicit local backend).
	Backend string
	// BackendConfig contains known scalar backend attributes; explicit blueprint backend_config entries override them.
	BackendConfig map[string]string
	// BackendConfigKnown prevents an unevaluable address from being mistaken for an omitted default.
	BackendConfigKnown bool
	// comparison retains exact defaults and complete backend declarations only to reject ambiguous runtime selection.
	comparison map[string]map[string]string
}

// Inspect reads Terraform files by default; callers selecting a runtime pass its FileMode so port validation and sensitivity use the executed declarations.
func Inspect(dir string, modes ...FileMode) (*Schema, error) {
	mode := TerraformFiles
	if len(modes) > 0 {
		mode = modes[0]
	}
	if mode == UnknownFiles {
		terraform, err := Inspect(dir, TerraformFiles)
		if err != nil {
			return nil, err
		}
		tofu, err := Inspect(dir, OpenTofuFiles)
		if err != nil {
			return nil, err
		}
		tofu.RequiresTofuFiles = false
		if !reflect.DeepEqual(terraform, tofu) {
			return nil, fmt.Errorf("inspecting module at %s: runtime binary is ambiguous and Terraform/OpenTofu declarations differ; use the canonical binary \"terraform\" or \"tofu\" on PATH, or make both declarations agree", dir)
		}
		return terraform, nil
	}
	if mode != TerraformFiles && mode != OpenTofuFiles {
		return nil, fmt.Errorf("inspecting module at %s: unsupported file mode %d", dir, mode)
	}
	files, err := selectedFiles(dir, mode)
	if err != nil {
		return nil, err
	}
	mod, diags := tfconfig.LoadModuleFromFilesystem(inspectionFS{files: files}, dir)
	if diags.HasErrors() {
		return nil, fmt.Errorf("inspecting module at %s: %s", dir, diags.Error())
	}

	schema := &Schema{
		Variables:     make(map[string]Variable, len(mod.Variables)),
		Outputs:       make(map[string]bool, len(mod.Outputs)),
		OutputDetails: make(map[string]Output, len(mod.Outputs)),
	}
	for _, file := range files {
		if strings.HasSuffix(file.physical, ".tofu") || strings.HasSuffix(file.physical, ".tofu.json") {
			schema.RequiresTofuFiles = true
			break
		}
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
	inspectDeclarations(schema, files)
	return schema, nil
}

var terraformBackendSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "backend", LabelNames: []string{"type"}},
		{Type: "cloud"},
	},
}

// inspectDeclarations shares the runtime-selected file list so defaults, sensitivity and backend checks cannot disagree with the inspected ports.
func inspectDeclarations(schema *Schema, files []moduleFile) {
	schema.BackendConfigKnown = true
	schema.comparison = make(map[string]map[string]string)
	parser := hclparse.NewParser()
	cloud := false
	ports := map[string]hcl.Attributes{}
	for _, selected := range files {
		src, err := os.ReadFile(selected.physical)
		if err != nil {
			continue
		}
		var file *hcl.File
		var diags hcl.Diagnostics
		if strings.HasSuffix(selected.physical, ".json") {
			file, diags = parser.ParseJSON(src, selected.physical)
		} else {
			file, diags = parser.ParseHCL(src, selected.physical)
		}
		if diags.HasErrors() || file == nil {
			continue
		}
		content, _, _ := file.Body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "variable", LabelNames: []string{"name"}}, {Type: "output", LabelNames: []string{"name"}}, {Type: "terraform"}}})
		for _, block := range content.Blocks {
			if block.Type == "variable" || block.Type == "output" {
				content, _, _ := block.Body.PartialContent(portMetadataSchema)
				key := block.Type + "." + block.Labels[0]
				if !selected.override || ports[key] == nil {
					ports[key] = hcl.Attributes{}
				}
				for name, attr := range content.Attributes {
					ports[key][name] = attr
				}
				continue
			}
			inner, _, _ := block.Body.PartialContent(terraformBackendSchema)
			for _, b := range inner.Blocks {
				// A backend or cloud override replaces either predecessor, including its default-path projection and ambiguity evidence.
				if selected.override {
					schema.Backend = ""
					schema.BackendConfig = nil
					schema.BackendConfigKnown = true
					cloud = false
					delete(schema.comparison, "backend")
					delete(schema.comparison, "cloud")
				}
				if b.Type == "cloud" {
					cloud = true
					schema.comparison["cloud"] = bodyIdentity(b.Body, src)
					continue
				}
				schema.Backend = b.Labels[0]
				schema.BackendConfig = make(map[string]string)
				schema.BackendConfigKnown = true
				attrs, attrDiags := b.Body.JustAttributes()
				schema.comparison["backend"] = bodyIdentity(b.Body, src)
				if attrDiags.HasErrors() {
					schema.BackendConfigKnown = false
				}
				for name, attr := range attrs {
					value, vd := attr.Expr.Value(nil)
					if vd.HasErrors() || !value.IsKnown() || value.IsNull() || (value.Type() != cty.String && value.Type() != cty.Number && value.Type() != cty.Bool) {
						schema.BackendConfigKnown = false
						continue
					}
					str, err := convert.Convert(value, cty.String)
					if err != nil {
						schema.BackendConfigKnown = false
						continue
					}
					schema.BackendConfig[name] = str.AsString()
				}
			}
		}
	}
	applyPortMetadata(schema, ports, parser.Sources())
	if schema.Backend == "" && cloud {
		schema.Backend = "cloud"
	}
}

var portMetadataSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "type"}, {Name: "description"}, {Name: "sensitive"}, {Name: "deprecated"}, {Name: "default"}}}

// applyPortMetadata retains omitted attributes in sparse override files, which tfconfig otherwise replaces with zero values, including sensitive=false.
func applyPortMetadata(schema *Schema, ports map[string]hcl.Attributes, sources map[string][]byte) {
	for key, attrs := range ports {
		text := func(name string) string {
			attr := attrs[name]
			if attr == nil {
				return ""
			}
			var value string
			diags := gohcl.DecodeExpression(attr.Expr, nil, &value)
			if name == "type" && diags.HasErrors() {
				return string(attr.Expr.Range().SliceBytes(sources[attr.Expr.Range().Filename]))
			}
			return value
		}
		sensitive := false
		if attr := attrs["sensitive"]; attr != nil {
			_ = gohcl.DecodeExpression(attr.Expr, nil, &sensitive)
		}
		kind, name, _ := strings.Cut(key, ".")
		if kind == "variable" {
			schema.Variables[name] = Variable{Name: name, Type: text("type"), Description: text("description"), Sensitive: sensitive, Deprecated: text("deprecated"), Required: attrs["default"] == nil}
			if attr := attrs["default"]; attr != nil {
				schema.comparison[key] = map[string]string{"default": expressionIdentity(attr.Expr, sources[attr.Expr.Range().Filename])}
			}
		} else {
			schema.OutputDetails[name] = Output{Name: name, Type: text("type"), Description: text("description"), Sensitive: sensitive, Deprecated: text("deprecated")}
		}
	}
}

// bodyIdentity includes unknown expressions and complex attributes, which the backend address projection deliberately omits.
func bodyIdentity(body hcl.Body, src []byte) map[string]string {
	attrs, diags := body.JustAttributes()
	result := make(map[string]string, len(attrs))
	for name, attr := range attrs {
		result[name] = expressionIdentity(attr.Expr, src)
	}
	// Nested blocks have no backend-independent schema; identical source is the conservative proof available without running a tool.
	if diags.HasErrors() {
		result["#source"] = string(src)
	}
	return result
}

// expressionIdentity avoids float64 rounding when an ambiguous wrapper differs only in a large numeric default.
func expressionIdentity(expr hcl.Expression, src []byte) string {
	value, diags := expr.Value(nil)
	if !diags.HasErrors() && value.IsWhollyKnown() {
		encoded, err := ctyjson.Marshal(value, value.Type())
		typ, typeErr := ctyjson.MarshalType(value.Type())
		if err == nil && typeErr == nil {
			return string(typ) + ":" + string(encoded)
		}
	}
	return "expression:" + string(expr.Range().SliceBytes(src))
}

// HasOutput reports whether the module declares an output with this name.
func (s *Schema) HasOutput(name string) bool { return s.Outputs[name] }

// HasVariable reports whether the module declares a variable with this name.
func (s *Schema) HasVariable(name string) bool {
	_, ok := s.Variables[name]
	return ok
}
