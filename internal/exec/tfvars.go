package exec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TFVarsFileName is the ephemeral, engine-managed variable file terragraph writes into each node's directory before every plan/apply. Terraform loads *.auto.tfvars.json automatically, so no other configuration is needed to wire it up. It must be added to the node's .gitignore.
const TFVarsFileName = ".terragraph.auto.tfvars.json"

// WriteTFVars (re)writes the ephemeral tfvars file for a node from the values resolved for its input edges. If vars is empty, any stale file from a previous run is removed instead: a node with no incoming data edges should never load an outdated value.
func WriteTFVars(nodeDir string, vars map[string]any) (string, error) {
	path := filepath.Join(nodeDir, TFVarsFileName)

	if len(vars) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("removing stale tfvars file %s: %w", path, err)
		}
		return path, nil
	}

	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding tfvars for %s: %w", nodeDir, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("writing tfvars file %s: %w", path, err)
	}
	return path, nil
}
