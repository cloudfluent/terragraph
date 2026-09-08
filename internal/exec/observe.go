package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// InitRead requires a fresh caller-owned data directory and a backend whose initialization cannot create state.
func (r *Runner) InitRead(backendConfig map[string]string) error {
	args := []string{"init", "-input=false", "-lockfile=readonly", "-reconfigure"}
	keys := make([]string, 0, len(backendConfig))
	for key := range backendConfig {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, fmt.Sprintf("-backend-config=%s=%s", key, backendConfig[key]))
	}
	return r.run(args...)
}

// StateObservation retains only facts from the state document, never resource values or provider credentials.
type StateObservation struct {
	Present     bool
	Determinate bool
	Empty       bool
	Resources   int
	Outputs     int
}

// ObserveState reads raw state through the runtime so encrypted or remote state does not need a second engine parser.
func (r *Runner) ObserveState() (StateObservation, error) {
	var out bytes.Buffer
	reader := *r
	reader.Stdout = &out
	if err := reader.run("state", "pull"); err != nil {
		return StateObservation{}, fmt.Errorf("reading state: %w", err)
	}
	if len(bytes.TrimSpace(out.Bytes())) == 0 {
		return StateObservation{}, nil
	}
	var doc struct {
		Serial    int `json:"serial"`
		Version   int `json:"version"`
		Resources []struct {
			Instances []json.RawMessage `json:"instances"`
		} `json:"resources"`
		Outputs map[string]json.RawMessage `json:"outputs"`
	}
	decoder := json.NewDecoder(&out)
	if err := decoder.Decode(&doc); err != nil {
		return StateObservation{}, fmt.Errorf("decoding state: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF || doc.Version != 4 {
		return StateObservation{}, fmt.Errorf("unsupported state document; use a runtime with state format version 4")
	}
	result := StateObservation{Present: true, Determinate: doc.Serial > 0, Outputs: len(doc.Outputs)}
	for _, resource := range doc.Resources {
		result.Resources += len(resource.Instances)
	}
	result.Empty = result.Resources == 0 && result.Outputs == 0
	return result, nil
}
