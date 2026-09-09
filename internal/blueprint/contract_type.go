package blueprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
)

// ContractType keeps optional attributes intact when graph identity and runtime validation share a constraint.
func ContractType(text string) (cty.Type, error) {
	expr, diags := hclsyntax.ParseExpression([]byte(text), "<contract type>", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("invalid contract type: %s", diags.Error())
	}
	typ, diags := typeexpr.TypeConstraint(expr)
	if diags.HasErrors() {
		return cty.NilType, fmt.Errorf("invalid contract type: %s", diags.Error())
	}
	return typ, nil
}

// CanonicalContractType preserves omission and optionality instead of erasing them with TypeString.
func CanonicalContractType(text string) (string, error) {
	if text == "" {
		return "", nil
	}
	typ, err := ContractType(text)
	if err != nil {
		return "", err
	}
	return constraintString(typ), nil
}

func constraintString(t cty.Type) string {
	switch {
	case t.IsObjectType():
		keys := make([]string, 0, len(t.AttributeTypes()))
		for k := range t.AttributeTypes() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrs := make([]string, 0, len(keys))
		for _, k := range keys {
			v := constraintString(t.AttributeType(k))
			if t.AttributeOptional(k) {
				v = "optional(" + v + ")"
			}
			attrs = append(attrs, k+" = "+v)
		}
		return "object({" + strings.Join(attrs, ", ") + "})"
	case t.IsTupleType():
		elems := make([]string, 0, len(t.TupleElementTypes()))
		for _, el := range t.TupleElementTypes() {
			elems = append(elems, constraintString(el))
		}
		return "tuple([" + strings.Join(elems, ", ") + "])"
	case t.IsListType():
		return "list(" + constraintString(t.ElementType()) + ")"
	case t.IsSetType():
		return "set(" + constraintString(t.ElementType()) + ")"
	case t.IsMapType():
		return "map(" + constraintString(t.ElementType()) + ")"
	default:
		return typeexpr.TypeString(t)
	}
}

// ContractResult distinguishes insufficient evidence from both success and the absence of a claim.
type ContractResult string

const (
	ContractConfirmed     ContractResult = "confirmed"
	ContractViolation     ContractResult = "violation"
	ContractDeferred      ContractResult = "deferred"
	ContractUnconstrained ContractResult = "unconstrained"
)

// CheckContractType checks structure without changing primitives, projecting objects, or materializing optional attributes.
func CheckContractType(value cty.Value, constraint cty.Type) ContractResult {
	if value == cty.NilVal {
		return ContractDeferred
	}
	if constraint == cty.DynamicPseudoType {
		return ContractConfirmed
	}
	typ := value.Type()
	if value.IsNull() {
		return ContractConfirmed
	}
	if !value.IsKnown() {
		return ContractDeferred
	}
	if constraint.IsPrimitiveType() {
		if typ.Equals(constraint) {
			return ContractConfirmed
		}
		return ContractViolation
	}
	result := ContractConfirmed
	combine := func(r ContractResult) {
		if r == ContractViolation || result != ContractViolation && r == ContractDeferred {
			result = r
		}
	}
	switch {
	case constraint.IsObjectType():
		if !typ.IsObjectType() && !typ.IsMapType() {
			return ContractViolation
		}
		for key, el := range constraint.AttributeTypes() {
			var v cty.Value
			if typ.IsObjectType() {
				if !typ.HasAttribute(key) {
					if !constraint.AttributeOptional(key) {
						combine(ContractViolation)
					}
					continue
				}
				v = value.GetAttr(key)
			} else {
				if !value.HasIndex(cty.StringVal(key)).True() {
					if !constraint.AttributeOptional(key) {
						combine(ContractViolation)
					}
					continue
				}
				v = value.Index(cty.StringVal(key))
			}
			combine(CheckContractType(v, el))
		}
	case constraint.IsTupleType():
		if !typ.IsTupleType() && !typ.IsListType() {
			return ContractViolation
		}
		if value.LengthInt() != len(constraint.TupleElementTypes()) {
			return ContractViolation
		}
		for i, el := range constraint.TupleElementTypes() {
			combine(CheckContractType(value.Index(cty.NumberIntVal(int64(i))), el))
		}
	case constraint.IsListType(), constraint.IsSetType(), constraint.IsMapType():
		if constraint.IsListType() && !typ.IsListType() && !typ.IsTupleType() || constraint.IsSetType() && !typ.IsSetType() || constraint.IsMapType() && !typ.IsMapType() && !typ.IsObjectType() {
			return ContractViolation
		}

		for it := value.ElementIterator(); it.Next(); {
			_, v := it.Element()
			combine(CheckContractType(v, constraint.ElementType()))
		}

		if constraint.HasDynamicTypes() {
			if !value.IsWhollyKnown() {
				combine(ContractDeferred)
			}
			// Conversion chooses a common any type; structural rechecking prevents primitive coercion from satisfying the promise.
			converted, err := convert.Convert(value, constraint)
			if err != nil {
				combine(ContractViolation)
			} else {
				precise := preserveOptional(converted.Type().ElementType(), constraint.ElementType())
				for it := value.ElementIterator(); it.Next(); {
					_, v := it.Element()
					combine(CheckContractType(v, precise))
				}
			}
		}

	default:
		return ContractViolation
	}
	return result
}

// preserveOptional keeps a resolved any element type from turning an absent optional attribute into a required one.
func preserveOptional(actual, constraint cty.Type) cty.Type {
	if actual.IsObjectType() && constraint.IsObjectType() {
		attrs := maps.Clone(actual.AttributeTypes())
		optional := []string{}
		for key, typ := range attrs {
			if constraint.HasAttribute(key) {
				attrs[key] = preserveOptional(typ, constraint.AttributeType(key))
				if constraint.AttributeOptional(key) {
					optional = append(optional, key)
				}
			}
		}
		return cty.ObjectWithOptionalAttrs(attrs, optional)
	}
	if actual.IsCollectionType() && constraint.IsCollectionType() {
		element := preserveOptional(actual.ElementType(), constraint.ElementType())
		switch {
		case actual.IsListType():
			return cty.List(element)
		case actual.IsSetType():
			return cty.Set(element)
		default:
			return cty.Map(element)
		}
	}
	if actual.IsTupleType() && constraint.IsTupleType() && len(actual.TupleElementTypes()) == len(constraint.TupleElementTypes()) {
		elements := slices.Clone(actual.TupleElementTypes())
		for i, t := range elements {
			elements[i] = preserveOptional(t, constraint.TupleElementType(i))
		}
		return cty.Tuple(elements)
	}
	return actual
}
