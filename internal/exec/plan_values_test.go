package exec

import (
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestOutputCtyValue_PreservesSetAndExactNumber(t *testing.T) {
	o := Output{Value: []any{json.Number("9007199254740993")}, Type: json.RawMessage(`["set","number"]`)}
	v, err := o.CtyValue()
	if err != nil {
		t.Fatal(err)
	}
	if !v.Type().Equals(cty.Set(cty.Number)) {
		t.Fatalf("got = %s, want set(number)", v.Type().FriendlyName())
	}
	for it := v.ElementIterator(); it.Next(); {
		_, value := it.Element()
		if value.AsBigFloat().Text('f', 0) != "9007199254740993" {
			t.Fatalf("got = %s", value.AsBigFloat().Text('f', 0))
		}
	}
}

func TestOutputCtyValue_PartialUnknownIsNotNull(t *testing.T) {
	o := Output{Value: map[string]any{"known": "yes"}, Unknown: map[string]any{"computed": true}}
	v, err := o.CtyValue()
	if err != nil {
		t.Fatal(err)
	}
	if v.GetAttr("computed").IsKnown() || v.GetAttr("known").AsString() != "yes" {
		t.Fatalf("got = %#v, want known sibling and unknown computed", v)
	}
}
