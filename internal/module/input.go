package module

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// EffectiveValue follows variable default/null and conversion rules without rewriting the payload sent to Terraform.
func (v Variable) EffectiveValue(value cty.Value) (cty.Value, error) {
	target := cty.DynamicPseudoType
	var defaults *typeexpr.Defaults
	if v.Type != "" {
		expr, diags := hclsyntax.ParseExpression([]byte(v.Type), "<variable type>", hcl.InitialPos)
		if diags.HasErrors() {
			return cty.NilVal, fmt.Errorf("invalid variable type; fix the module declaration")
		}
		target, defaults, diags = typeexpr.TypeConstraintWithDefaults(expr)
		if diags.HasErrors() {
			return cty.NilVal, fmt.Errorf("invalid variable type; fix the module declaration")
		}
	}
	if value == cty.NilVal {
		value = v.Default
	}
	if value == cty.NilVal {
		return cty.NilVal, fmt.Errorf("input value is unavailable; supply the variable or a literal default")
	}
	if value.IsKnown() && value.IsNull() && v.Nullable != nil && !*v.Nullable {
		if v.Default == cty.NilVal || v.Default.IsNull() {
			return cty.NilVal, fmt.Errorf("null is not allowed; supply a non-null value or a non-null module default")
		}
		value = v.Default
	}
	if defaults != nil && !value.IsNull() {
		value = defaults.Apply(value)
	}
	result, err := convert.Convert(value, target)
	if err != nil {
		return cty.NilVal, fmt.Errorf("does not match declared type %s: %w", v.Type, err)
	}
	return result, nil
}
