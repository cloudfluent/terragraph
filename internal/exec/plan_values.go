package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// PlanValues carries chosen raw inputs and partially known outputs from the exact saved plan, never a second evaluation.
type PlanValues struct {
	Variables map[string]Output
	Outputs   Outputs
}

// PlanValues rejects unsupported JSON rather than interpreting missing fields as verified values.
func (r *Runner) PlanValues(path string) (*PlanValues, error) {
	data, err := r.planJSON(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		FormatVersion string            `json:"format_version"`
		Variables     map[string]Output `json:"variables"`
		PlannedValues struct {
			Outputs Outputs `json:"outputs"`
		} `json:"planned_values"`
		OutputChanges map[string]struct {
			Actions   []string        `json:"actions"`
			After     json.RawMessage `json:"after"`
			Unknown   any             `json:"after_unknown"`
			Sensitive *bool           `json:"after_sensitive"`
		} `json:"output_changes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("reading plan values: invalid JSON; create a fresh plan")
	}
	if decoder.Decode(new(any)) != io.EOF || !strings.HasPrefix(doc.FormatVersion, "1.") {
		return nil, fmt.Errorf("reading plan values: unsupported plan JSON; use a runtime with format version 1")
	}
	result := &PlanValues{Variables: doc.Variables, Outputs: doc.PlannedValues.Outputs}
	if result.Outputs == nil {
		result.Outputs = Outputs{}
	}
	for name, change := range doc.OutputChanges {
		if slices.Contains(change.Actions, "delete") {
			delete(result.Outputs, name)
			continue
		}
		output := result.Outputs[name]
		output.Sensitive = change.Sensitive
		output.Unknown = change.Unknown
		if len(change.After) > 0 {
			d := json.NewDecoder(bytes.NewReader(change.After))
			d.UseNumber()
			if err := d.Decode(&output.Value); err != nil {
				return nil, fmt.Errorf("reading plan values: invalid output; create a fresh plan")
			}
		} else if !hasUnknown(change.Unknown) {
			output.Unknown = true
		}
		result.Outputs[name] = output
	}
	return result, nil
}

// CtyValue recovers exact runtime types and partial unknowns before contracts can mistake JSON null placeholders for real null.
func (o Output) CtyValue() (cty.Value, error) {
	if hasUnknown(o.Unknown) {
		value, err := maskedValue(o.Value, o.Unknown)
		if err != nil {
			return cty.NilVal, err
		}
		if len(o.Type) > 0 {
			typ, err := ctyjson.UnmarshalType(o.Type)
			if err != nil {
				return cty.NilVal, fmt.Errorf("invalid runtime type metadata")
			}
			value, err = convert.Convert(value, typ)
			if err != nil {
				return cty.NilVal, fmt.Errorf("value disagrees with runtime type metadata")
			}
		}
		return value, nil
	}
	data, err := json.Marshal(o.Value)
	if err != nil {
		return cty.NilVal, fmt.Errorf("cannot encode observed value")
	}
	if len(o.Type) > 0 {
		typ, err := ctyjson.UnmarshalType(o.Type)
		if err != nil {
			return cty.NilVal, fmt.Errorf("invalid runtime type metadata")
		}
		value, err := ctyjson.Unmarshal(data, typ)
		if err != nil {
			return cty.NilVal, fmt.Errorf("value disagrees with runtime type metadata")
		}
		return value, nil
	}
	var value ctyjson.SimpleJSONValue
	if err := json.Unmarshal(data, &value); err != nil {
		return cty.NilVal, fmt.Errorf("invalid observed value")
	}
	return value.Value, nil
}

func hasUnknown(mask any) bool {
	switch m := mask.(type) {
	case bool:
		return m
	case map[string]any:
		for _, v := range m {
			if hasUnknown(v) {
				return true
			}
		}
	case []any:
		for _, v := range m {
			if hasUnknown(v) {
				return true
			}
		}
	}
	return false
}

func maskedValue(raw, mask any) (cty.Value, error) {
	if unknown, ok := mask.(bool); ok && unknown {
		return cty.DynamicVal, nil
	}
	switch m := mask.(type) {
	case map[string]any:
		object, _ := raw.(map[string]any)
		values := map[string]cty.Value{}
		for k, v := range object {
			value, err := maskedValue(v, m[k])
			if err != nil {
				return cty.NilVal, err
			}
			values[k] = value
		}
		for k, v := range m {
			if _, ok := values[k]; !ok {
				value, err := maskedValue(nil, v)
				if err != nil {
					return cty.NilVal, err
				}
				values[k] = value
			}
		}
		return cty.ObjectVal(values), nil
	case []any:
		array, _ := raw.([]any)
		n := max(len(array), len(m))
		values := make([]cty.Value, n)
		for i := range n {
			var value, unknown any
			if i < len(array) {
				value = array[i]
			}
			if i < len(m) {
				unknown = m[i]
			}
			v, err := maskedValue(value, unknown)
			if err != nil {
				return cty.NilVal, err
			}
			values[i] = v
		}
		if n == 0 {
			return cty.EmptyTupleVal, nil
		}
		return cty.TupleVal(values), nil
	default:
		return (Output{Value: raw}).CtyValue()
	}
}
