package blueprint

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
	"testing"
)

func TestCheckContractType_StructuralValues(t *testing.T) {
	for _, tc := range []struct {
		value, typ string
		want       ContractResult
	}{
		{`123`, `string`, ContractViolation},
		{`["a","b"]`, `list(string)`, ContractConfirmed},
		{`["a",123]`, `list(any)`, ContractViolation},
		{`{id="a",extra=true}`, `object({id=string})`, ContractConfirmed},
		{`{}`, `object({id=optional(string)})`, ContractConfirmed},
		{`{}`, `object({id=string})`, ContractViolation},
		{`[{id="a",extra=true},{id="b"}]`, `list(object({id=string}))`, ContractConfirmed},
		{`{a={id="a"},b={id="b",extra=true}}`, `map(object({id=string}))`, ContractConfirmed},
		{`{a=1,b="2"}`, `map(any)`, ContractViolation},
		{`[{value=1},{value="2"}]`, `list(object({value=any}))`, ContractViolation},
		{`[]`, `list(string)`, ContractConfirmed},
		{`[]`, `tuple([string])`, ContractViolation},
		{`["a"]`, `set(string)`, ContractViolation},
		{`toset(["a"])`, `set(string)`, ContractConfirmed},
		{`toset(["a"])`, `list(string)`, ContractViolation},
		{`{value=null}`, `object({value=string})`, ContractConfirmed},
	} {
		t.Run(tc.value+"_"+tc.typ, func(t *testing.T) {
			expr, d := hclsyntax.ParseExpression([]byte(tc.value), "test", hcl.InitialPos)
			if d.HasErrors() {
				t.Fatal(d)
			}
			value, d := expr.Value(&hcl.EvalContext{Functions: map[string]function.Function{"toset": stdlib.MakeToFunc(cty.Set(cty.DynamicPseudoType))}})
			if d.HasErrors() {
				t.Fatal(d)
			}
			typ, err := ContractType(tc.typ)
			if err != nil {
				t.Fatal(err)
			}
			if got := CheckContractType(value, typ); got != tc.want {
				t.Fatalf("got = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCheckContractType_PartialUnknownStillRejectsKnownSibling(t *testing.T) {
	typ, err := ContractType(`object({id=string,computed=string})`)
	if err != nil {
		t.Fatal(err)
	}
	value := cty.ObjectVal(map[string]cty.Value{"id": cty.NumberIntVal(123), "computed": cty.DynamicVal})
	if got := CheckContractType(value, typ); got != ContractViolation {
		t.Fatalf("got = %s, want violation", got)
	}
	value = cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("a"), "computed": cty.DynamicVal})
	if got := CheckContractType(value, typ); got != ContractDeferred {
		t.Fatalf("got = %s, want deferred", got)
	}
}

func TestCheckContractType_UnknownCommonElementTypeIsDeferred(t *testing.T) {
	typ, err := ContractType("list(any)")
	if err != nil {
		t.Fatal(err)
	}
	value := cty.TupleVal([]cty.Value{cty.DynamicVal, cty.DynamicVal})
	if got := CheckContractType(value, typ); got != ContractDeferred {
		t.Fatalf("got = %s, want deferred", got)
	}
}

func TestCheckContractType_OptionalAttributesSurviveAnyResolution(t *testing.T) {
	typ, err := ContractType("set(object({ id = any, extra = optional(string) }))")
	if err != nil {
		t.Fatal(err)
	}
	value := cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("a")})})
	if got := CheckContractType(value, typ); got != ContractConfirmed {
		t.Fatalf("got = %s, want confirmed", got)
	}
}
