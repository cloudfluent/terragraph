package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PlanRuntimeIdentity exposes compatibility facts without persisting environment credentials or the full version response.
type PlanRuntimeIdentity struct {
	Version   string            `json:"version"`
	Platform  string            `json:"platform"`
	Providers map[string]string `json:"providers"`
	Inputs    map[string]string `json:"inputs,omitempty"`
}

// PlanIdentity refuses unverifiable runtimes because native plan formats are tied to the producing runtime and provider selections.
func (r *Runner) PlanIdentity() (PlanRuntimeIdentity, error) {
	var out bytes.Buffer
	probe := *r
	probe.Stdout, probe.Stderr, probe.Stdin = &out, io.Discard, nil
	if err := probe.run("version", "-json"); err != nil {
		return PlanRuntimeIdentity{}, fmt.Errorf("verifying saved-plan runtime: %w", err)
	}
	var doc struct {
		Version   string            `json:"terraform_version"`
		Platform  string            `json:"platform"`
		Providers map[string]string `json:"provider_selections"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.Version == "" || doc.Platform == "" {
		return PlanRuntimeIdentity{}, fmt.Errorf("runtime version -json is incomplete; use a runtime exposing version and platform before retaining plans")
	}
	env, err := r.env()
	if err != nil {
		return PlanRuntimeIdentity{}, err
	}
	inputs := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "TF_VAR_") || key == "TF_WORKSPACE" {
			inputs[key] = value
		}
		if strings.HasPrefix(strings.ToUpper(key), "TF_CLI_ARGS") && value != "" {
			return PlanRuntimeIdentity{}, fmt.Errorf("retained plans require explicit runtime arguments; unset TF_CLI_ARGS overrides before saving or applying")
		}
	}
	return PlanRuntimeIdentity{Version: doc.Version, Platform: doc.Platform, Providers: doc.Providers, Inputs: inputs}, nil
}
