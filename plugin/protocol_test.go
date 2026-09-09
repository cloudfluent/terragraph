package plugin

import (
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestValue_PreservesLargeInteger(t *testing.T) {
	number, err := cty.ParseNumberVal("9007199254740993123456789")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := EncodeValue(number, false)
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := wire.Cty()
	if err != nil || !roundtrip.RawEquals(number) {
		t.Fatalf("got = %v, %v, want exact number", roundtrip, err)
	}
	decoded, err := wire.Decode()
	if err != nil || decoded != json.Number("9007199254740993123456789") {
		t.Fatalf("got = %v, %v, want exact JSON number", decoded, err)
	}
}

func TestValue_PreservesTypedNull(t *testing.T) {
	original := cty.NullVal(cty.List(cty.String))
	wire, err := EncodeValue(original, true)
	if err != nil {
		t.Fatal(err)
	}
	value, err := wire.Cty()
	if err != nil || !value.RawEquals(original) || !wire.Sensitive {
		t.Fatalf("got = %v, %v, want sensitive typed null", value, err)
	}
}

func TestDescriptor_RejectsSensitiveFunction(t *testing.T) {
	d := Descriptor{Name: "test", Version: "1.0.0", Protocol: ProtocolVersion, Executable: "test", Features: []Feature{{Name: "value", Kind: "function", Effect: "pure", Sensitive: true, ResultType: json.RawMessage(`"string"`)}}}
	if err := d.Validate(); err == nil {
		t.Fatal("sensitive function accepted")
	}
}
