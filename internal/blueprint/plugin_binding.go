package blueprint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
)

// PluginBinding names an explicit input supplier so graph validation can reject competing sources before any provider is called.
type PluginBinding struct {
	Alias       string
	Feature     string
	Reference   map[string]any
	Environment []string
}

func parsePluginBindings(blocks []*hcl.Block) (map[string]PluginBinding, map[string]PluginBinding, error) {
	inputs := map[string]PluginBinding{}
	credentials := map[string]PluginBinding{}
	for _, block := range blocks {
		b, d := block.Body.Content(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "from", Required: true}, {Name: "ref", Required: true}, {Name: "environment"}}})
		if d.HasErrors() {
			return nil, nil, d
		}
		t, d := hcl.AbsTraversalForExpr(b.Attributes["from"].Expr)
		if d.HasErrors() || len(t) != 3 || t.RootName() != "plugin" {
			return nil, nil, fmt.Errorf("%s.%s.from: use plugin.alias.feature", block.Type, block.Labels[0])
		}
		a, ok := t[1].(hcl.TraverseAttr)
		f, ok2 := t[2].(hcl.TraverseAttr)
		if !ok || !ok2 {
			return nil, nil, fmt.Errorf("%s.%s.from: use plugin.alias.feature", block.Type, block.Labels[0])
		}
		ref, err := parseVarsAttr(b.Attributes["ref"])
		if err != nil {
			return nil, nil, err
		}
		binding := PluginBinding{Alias: a.Name, Feature: f.Name, Reference: ref}
		if attr := b.Attributes["environment"]; attr != nil {
			if block.Type != "credential" {
				return nil, nil, fmt.Errorf("input.%s: environment belongs in a credential binding", block.Labels[0])
			}
			v, d := attr.Expr.Value(nil)
			if d.HasErrors() || v.IsNull() || !v.IsWhollyKnown() || (!v.Type().IsTupleType() && !v.Type().IsListType()) {
				return nil, nil, fmt.Errorf("credential.%s.environment: use a literal list", block.Labels[0])
			}
			for _, v := range v.AsValueSlice() {
				if v.IsNull() || v.Type() != cty.String || !CredentialEnvironmentAllowed(v.AsString()) {
					return nil, nil, fmt.Errorf("credential.%s.environment: only credential environment names are allowed; remove runtime-control variables", block.Labels[0])
				}
				binding.Environment = append(binding.Environment, v.AsString())
			}
		}
		target := inputs
		if block.Type == "credential" {
			target = credentials
			if len(binding.Environment) == 0 {
				return nil, nil, fmt.Errorf("credential.%s.environment: explicitly allow the returned environment names", block.Labels[0])
			}
		}
		if _, ok := target[block.Labels[0]]; ok {
			return nil, nil, fmt.Errorf("%s.%s: duplicate binding", block.Type, block.Labels[0])
		}
		target[block.Labels[0]] = binding
	}
	return inputs, credentials, nil
}

// CredentialEnvironmentAllowed excludes runtime arguments and routing controls that could retarget an approved plan.
func CredentialEnvironmentAllowed(key string) bool {
	key = strings.ToUpper(key)
	if !regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`).MatchString(key) {
		return false
	}
	if strings.HasPrefix(key, "TF_TOKEN_") || key == "TF_HTTP_USERNAME" || key == "TF_HTTP_PASSWORD" {
		return true
	}
	if strings.HasPrefix(key, "TF_") || strings.HasPrefix(key, "TOFU_") || strings.HasPrefix(key, "TERRAGRAPH_") || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") {
		return false
	}
	switch key {
	case "PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "COMSPEC", "SHELL", "BASH_ENV", "ENV", "AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ENDPOINT_URL":
		return false
	}
	return true
}
