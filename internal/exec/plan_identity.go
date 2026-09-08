package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// PlanRuntimeIdentity exposes compatibility facts without persisting environment credentials or the full version response.
type PlanRuntimeIdentity struct {
	Version   string            `json:"version"`
	Platform  string            `json:"platform"`
	Providers map[string]string `json:"providers"`
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
	return PlanRuntimeIdentity{Version: doc.Version, Platform: doc.Platform, Providers: doc.Providers}, nil
}
